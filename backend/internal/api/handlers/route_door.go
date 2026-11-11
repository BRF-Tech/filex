package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/shortlink"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── A visit to an app's public route (filex 0.60.0) ────────────────────────
//
// docs/APP-PLUGINS-API.md → Public routes. The app answers the visit; this
// door holds what a visitor meets whatever the app is:
//
//   - a client held back by the miss limiter, or over the per-minute limit,
//     is answered 429 without the app being asked;
//   - a redirect is a 302 to an http(s) address (the app's answer checked),
//     with no-store, no referrer, noindex and no cookie;
//   - a page is filex's own template with the app's words in it - no HTML
//     or script of the app's;
//   - every miss is ONE page, 404, whatever the reason (never existed,
//     deleted, ended, switched off, the app not answering the route now), and
//     a miss the app said counts against the visitor;
//   - a busy or failing app is 503 with Retry-After, never a hang.

// RouteDoor serves the visits.
type RouteDoor struct {
	Registry *wasmplugin.Registry
	Store    db.Store
	Tenants  tenanturl.Resolver
	// MultiTenant: a visit on filex's own address is in the tenant of the
	// host it arrived on.
	MultiTenant bool
	Misses      *shortlink.MissLimiter
	Hits        *shortlink.RateLimiter
	Dedupe      *shortlink.Dedupe
	// busy holds a slot per visit being answered: past routeDoorInFlight
	// at once a visit is answered 503 before any work, so a flood that the
	// machine cannot keep up with is shed at the door, not queued in it.
	busy chan struct{}
	// pages are filex's own pages (a miss, too many tries, unavailable),
	// rendered once per language and kind and kept for routePageTTL.
	pages sync.Map // lang + "\x00" + kind -> cachedPage
}

// cachedPage is one rendered page of the door's own.
type cachedPage struct {
	body []byte
	at   time.Time
}

// routePageTTL is how long a rendered page of the door's own is served
// again. ⚠ Measured 2026-11-11 (#313 H2/H4): rendering the template for
// every shed or missed visit took a quarter of the door's CPU under a
// flood - exactly the answers a flood is made of. A minute bounds how long
// a language pack's new wording waits.
const routePageTTL = time.Minute

// routeDoorInFlight is the most visits the door works on at once, every
// app's together. Far above what a pool of warm instances answers in its
// wait (16 instances, a few dozen waiting), far below what a flood offers.
const routeDoorInFlight = 256

// NewRouteDoor builds the door with the limiters' standing rules.
func NewRouteDoor(reg *wasmplugin.Registry, store db.Store, tenants tenanturl.Resolver, multiTenant bool) *RouteDoor {
	return &RouteDoor{
		Registry: reg, Store: store, Tenants: tenants, MultiTenant: multiTenant,
		Misses: &shortlink.MissLimiter{},
		Hits:   &shortlink.RateLimiter{Limit: shortlink.HitLimit, Window: shortlink.HitWindow},
		Dedupe: &shortlink.Dedupe{},
		busy:   make(chan struct{}, routeDoorInFlight),
	}
}

// maxRouteQuery is the most of a query string the app is handed.
const maxRouteQuery = 1 << 10

func routeHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Del("Set-Cookie")
}

// Serve answers one visit (shortlink.HostSplit / OwnAddress).
func (d *RouteDoor) Serve(w http.ResponseWriter, r *http.Request, v shortlink.Visit) {
	routeHeaders(w)
	if d.busy != nil {
		select {
		case d.busy <- struct{}{}:
			defer func() { <-d.busy }()
		default:
			w.Header().Set("Retry-After", "1")
			d.page(w, r, http.StatusServiceUnavailable, srvtext.Pick(""), "unavailable", nil)
			return
		}
	}
	lang := langOf(r)
	client := shortlink.ClientKey(clientip.FromRequest(r))
	if d.Misses.Locked(client) || !d.Hits.Allow(client) {
		d.page(w, r, http.StatusTooManyRequests, lang, "busy", nil)
		return
	}
	if d.Registry == nil {
		d.page(w, r, http.StatusNotFound, lang, "miss", nil)
		return
	}
	tenant := d.tenant(r.Context(), r, v.Base)
	path := v.Code
	if v.Preview {
		path += "+"
	}
	countable := shortlink.Countable(r) && d.Dedupe.First(visitKey(v.Base.ID, v.Base.App, path), client)
	q := r.URL.RawQuery
	if len(q) > maxRouteQuery {
		q = q[:maxRouteQuery]
	}
	in := wire.RouteInput{
		Base:   wire.RouteBase{ID: v.Base.ID, URL: d.baseURL(r, v.Base)},
		Path:   path,
		Query:  q,
		Method: r.Method,
		// HEAD reaches the app as GET would (a link checker), never counted.
		Countable: countable,
		Locale:    lang,
	}
	out, err := d.Registry.Route(r.Context(), v.Base.App, v.Base.Route, tenant, in)
	switch {
	case errors.Is(err, wasmplugin.ErrNoRoute):
		d.page(w, r, http.StatusNotFound, lang, "miss", nil)
		return
	case err != nil:
		w.Header().Set("Retry-After", "1")
		d.page(w, r, http.StatusServiceUnavailable, lang, "unavailable", nil)
		return
	}
	switch {
	case out.Miss:
		d.Misses.Miss(client)
		d.page(w, r, http.StatusNotFound, lang, "miss", nil)
	case out.Redirect != "":
		w.Header().Set("Location", out.Redirect)
		w.WriteHeader(http.StatusFound)
	default:
		d.page(w, r, http.StatusOK, lang, "", out.Page)
	}
}

// visitKey identifies an address for the one-click-a-minute rule.
func visitKey(base int64, app, path string) int64 {
	sum := sha256.Sum256([]byte(app + "\x00" + strconv.FormatInt(base, 10) + "\x00" + path))
	var n int64
	for _, b := range sum[:8] {
		n = n<<8 | int64(b)
	}
	return n
}

// tenant is the visited address's tenant: a host of its own belongs to one;
// filex's own address is the tenant of the host the visit arrived on.
func (d *RouteDoor) tenant(ctx context.Context, r *http.Request, b shortlink.Base) int64 {
	if !d.MultiTenant {
		return 0
	}
	if b.Host != "" {
		return b.TenantID
	}
	if d.Store == nil {
		return 0
	}
	p, err := d.Store.GetProviderByHost(ctx, tenanturl.RequestHost(r))
	if err != nil || p == nil {
		return 0
	}
	return d.Registry.TenantKey(ctx, p.ID)
}

// baseURL is the base as the visitor reached it.
func (d *RouteDoor) baseURL(r *http.Request, b shortlink.Base) string {
	if b.Host != "" {
		u := "https://" + b.Host
		if b.Prefix != "" {
			u += "/" + b.Prefix
		}
		return u
	}
	return strings.TrimRight(d.Tenants.FromRequest(r), "/") + "/" + b.Prefix
}

var routePage = template.Must(template.New("route").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow"><title>{{.Title}}</title>
<style>
:root{color-scheme:light dark;--bg:#f6f7f9;--fg:#1d2330;--muted:#5b6475;--card:#fff;--line:#dde1e8;--accent:#2f6fde}
@media (prefers-color-scheme:dark){:root{--bg:#12151b;--fg:#e7eaf0;--muted:#a3abba;--card:#1b2029;--line:#2b3240;--accent:#6b9dff}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif;display:flex;min-height:100vh;align-items:center;justify-content:center;padding:16px;box-sizing:border-box}
main{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:28px 24px;max-width:520px;width:100%;box-sizing:border-box}
h1{font-size:20px;margin:0 0 8px}p{margin:0 0 16px;color:var(--muted);overflow-wrap:anywhere}
a.go{display:inline-block;background:var(--accent);color:#fff;text-decoration:none;padding:10px 16px;border-radius:8px;font-weight:600}
</style></head><body><main><h1>{{.Title}}</h1>{{if .Text}}<p>{{.Text}}</p>{{end}}{{if .Link}}<a class="go" href="{{.Link}}" rel="noopener noreferrer nofollow">{{.LinkLabel}}</a>{{end}}</main></body></html>
`))

// page draws filex's page: one of its own (kind: miss, busy, unavailable) or
// the app's words in it (kind "").
func (d *RouteDoor) page(w http.ResponseWriter, r *http.Request, status int, lang, kind string, p *wire.RoutePage) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	data := struct{ Lang, Title, Text, Link, LinkLabel string }{Lang: srvtext.Pick(lang)}
	if p != nil {
		data.Title, data.Text = p.Title.Get(lang), p.Text.Get(lang)
		if p.Link != "" && wasmplugin.CheckOutboundURL(p.Link) == nil {
			data.Link, data.LinkLabel = p.Link, p.LinkLabel.Get(lang)
			if data.LinkLabel == "" {
				data.LinkLabel = srvtext.Text(data.Lang, "server.route.go", nil)
			}
		}
	} else {
		data.Title = srvtext.Text(data.Lang, "server.route."+kind+"_title", nil)
		data.Text = srvtext.Text(data.Lang, "server.route."+kind+"_text", nil)
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	if p != nil {
		_ = routePage.Execute(w, data)
		return
	}
	key := data.Lang + "\x00" + kind
	if c, ok := d.pages.Load(key); ok && time.Since(c.(cachedPage).at) < routePageTTL {
		_, _ = w.Write(c.(cachedPage).body)
		return
	}
	var buf bytes.Buffer
	_ = routePage.Execute(&buf, data)
	d.pages.Store(key, cachedPage{body: buf.Bytes(), at: time.Now()})
	_, _ = w.Write(buf.Bytes())
}
