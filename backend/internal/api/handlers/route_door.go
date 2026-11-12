package handlers

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/memcache"
	"github.com/brf-tech/filex/backend/internal/shortlink"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/templates"
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
//   - a page is a template (page.route) with the app's words in it - no HTML
//     or script of the app's;
//   - every miss is ONE page, 404, whatever the reason (never existed,
//     deleted, ended, switched off, the app not answering the route now), and
//     a miss the app said counts against the visitor;
//   - a busy or failing app is 503 with Retry-After, never a hang.
//
// ⚠⚠ The door's pages are templates an administrator can change (#324):
// its own answers are page.error (error.key route_miss / route_busy /
// route_unavailable), an app's page is page.route, both in page.layout, in
// the ADDRESS's templates (a host of its own: its tenant's; filex's own
// address: the tenant of the host the visit arrived on). They are served
// with a Content-Security-Policy of default-src 'none' and rendered
// self-contained (templates.Target.SelfContained): an app's host of its own
// serves nothing else of filex, so a page there loads nothing from anywhere -
// no logo, no stylesheet but its own inline one.
//
// ⚠⚠ And they are drawn once and served again (routePageTTL): an edited
// template is rendered in the wasm sandbox, the dearest render filex has, and
// a flood is made of exactly these answers.

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
	// Templates renders the door's pages (page.error, page.route); nil =
	// filex's own templates, with no branding (a door a test built).
	Templates *templates.Renderer
	// pages are the pages drawn - the door's own per language, kind and
	// tenant, an app's per language, tenant and words - kept for
	// routePageTTL. drawing names the pages being drawn again: while one
	// visit draws, the others are served what was drawn before.
	pages   *memcache.Cache[string, drawnPage]
	drawing sync.Map // page key -> struct{}
	// hosts remember the tenant of an address of filex's own (a visit there
	// is in the tenant of the host it arrived on): a flood answered "too many
	// tries" costs no database read a visit.
	hosts *memcache.Cache[string, hostTenant]
}

// drawnPage is one rendered page of the door's, and when it was drawn.
type drawnPage struct {
	body []byte
	// gz is body compressed once, for a visitor whose browser takes gzip.
	gz []byte
	at time.Time
	// gen is the renderer's generation it was drawn under: a template saved
	// since is shown at once, not when the minute is out.
	gen uint64
}

// hostTenant is the tenant a host of filex's own belongs to.
type hostTenant struct {
	id    int64
	super bool
	found bool
}

// routePageTTL is how long a rendered page of the door's is served again.
// ⚠ Measured 2026-11-11 (#313 H2/H4): rendering the page for every shed or
// missed visit took a quarter of the door's CPU under a flood - exactly the
// answers a flood is made of - and that was a Go html/template; a Liquid
// template, and an edited one in the sandbox above all, costs far more. A
// minute bounds how long a language pack's new wording waits; a saved
// template is shown at once (drawnPage.gen).
const routePageTTL = time.Minute

// routePagesKept bounds the pages kept: the door's own are a handful per
// tenant, an app's one per link shown; past it the least recently used go.
const routePagesKept = 1024

// routeHostsKept bounds the remembered hosts, and routeHostTTL is how long
// one is believed (a host given to another tenant counts after it).
const (
	routeHostsKept = 1024
	routeHostTTL   = 30 * time.Second
)

// routePageCSP is the door's pages' policy: nothing is loaded - no script,
// no image, no font, no stylesheet but the page's own inline one - nothing
// is posted, and nothing frames them.
const routePageCSP = "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// The door's own answers, as page.error's error.key; server.route.<kind>_title
// and _text say them.
const (
	routeMiss        = "route_miss"
	routeBusy        = "route_busy"
	routeUnavailable = "route_unavailable"
)

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
		// The hard expiry only bounds how long a page is served stale while
		// another visit draws it again (cachedPage).
		pages: memcache.New[string, drawnPage](memcache.Options{MaxEntries: routePagesKept, TTL: 10 * routePageTTL}),
		hosts: memcache.New[string, hostTenant](memcache.Options{MaxEntries: routeHostsKept, TTL: routeHostTTL}),
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
			d.ownPage(w, r, v.Base, http.StatusServiceUnavailable, langOf(r), routeUnavailable)
			return
		}
	}
	lang := langOf(r)
	client := shortlink.ClientKey(clientip.FromRequest(r))
	if d.Misses.Locked(client) || !d.Hits.Allow(client) {
		d.ownPage(w, r, v.Base, http.StatusTooManyRequests, lang, routeBusy)
		return
	}
	if d.Registry == nil {
		d.ownPage(w, r, v.Base, http.StatusNotFound, lang, routeMiss)
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
		d.ownPage(w, r, v.Base, http.StatusNotFound, lang, routeMiss)
		return
	case err != nil:
		w.Header().Set("Retry-After", "1")
		d.ownPage(w, r, v.Base, http.StatusServiceUnavailable, lang, routeUnavailable)
		return
	}
	switch {
	case out.Miss:
		d.Misses.Miss(client)
		d.ownPage(w, r, v.Base, http.StatusNotFound, lang, routeMiss)
	case out.Redirect != "":
		w.Header().Set("Location", out.Redirect)
		w.WriteHeader(http.StatusFound)
	default:
		d.appPage(w, r, v.Base, lang, out.Page)
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
	h := d.hostTenant(ctx, r)
	if !h.found {
		return 0
	}
	return d.Registry.TenantKey(ctx, h.id)
}

// hostTenant is the tenant the host a visit arrived on belongs to, from
// d.hosts when it is remembered. A store error is not remembered.
func (d *RouteDoor) hostTenant(ctx context.Context, r *http.Request) hostTenant {
	if d.Store == nil {
		return hostTenant{}
	}
	host := tenanturl.RequestHost(r)
	if h, ok := d.hosts.Get(host); ok {
		return h
	}
	p, err := d.Store.GetProviderByHost(ctx, host)
	if err != nil {
		return hostTenant{}
	}
	var h hostTenant
	if p != nil {
		h = hostTenant{id: p.ID, super: p.IsSupertenant, found: true}
	}
	d.hosts.Put(host, h)
	return h
}

// pageTarget is whose templates and branding a page of the door's wears: the
// address's - a host of its own is its tenant's, filex's own address the
// tenant of the host the visit arrived on (the platform's own tenant and an
// unknown host: the install's) - in the visitor's language, self-contained.
func (d *RouteDoor) pageTarget(ctx context.Context, r *http.Request, b shortlink.Base, lang string) templates.Target {
	t := templates.Target{Host: r.Host, Lang: srvtext.Pick(lang), SelfContained: true}
	switch {
	case !d.MultiTenant:
	case b.Host != "":
		t.TenantID = b.TenantID
	default:
		if h := d.hostTenant(ctx, r); h.found && !h.super {
			t.TenantID = h.id
		}
	}
	return t
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

// ownPage answers with one of the door's own pages: page.error, error.key
// kind (routeMiss, routeBusy, routeUnavailable), drawn once per language,
// kind and tenant.
func (d *RouteDoor) ownPage(w http.ResponseWriter, r *http.Request, b shortlink.Base, status int, lang, kind string) {
	t := d.pageTarget(r.Context(), r, b, lang)
	name := strings.TrimPrefix(kind, "route_")
	// The sentence is looked up only when the page is drawn: a page served
	// again costs no catalogue lookup.
	title := func() string { return srvtext.Text(t.Lang, "server.route."+name+"_title", nil) }
	key := "own\x00" + kind + "\x00" + t.Lang + "\x00" + strconv.FormatInt(t.TenantID, 10)
	d.write(w, r, status, title, key, func() (string, error) {
		title := title()
		return orDefaultTemplates(d.Templates).Page(r.Context(), t, "page.error", map[string]any{
			"page": map[string]any{"title": title},
			"error": map[string]any{
				"code": status, "key": kind, "title": title,
				"body": srvtext.Text(t.Lang, "server.route."+name+"_text", nil),
			},
		})
	})
}

// appPage answers with the page an app drew (page.route): its words, in the
// visitor's language, and its link when the link is a web address. A page is
// drawn once per language, tenant and words: a link's preview opened by many
// is one render.
func (d *RouteDoor) appPage(w http.ResponseWriter, r *http.Request, b shortlink.Base, lang string, p *wire.RoutePage) {
	t := d.pageTarget(r.Context(), r, b, lang)
	var title, text, link, label string
	if p != nil {
		title, text = p.Title.Get(t.Lang), p.Text.Get(t.Lang)
		if p.Link != "" && wasmplugin.CheckOutboundURL(p.Link) == nil {
			link, label = p.Link, p.LinkLabel.Get(t.Lang)
			if label == "" {
				label = srvtext.Text(t.Lang, "server.route.go", nil)
			}
		}
	}
	sum := sha256.Sum256([]byte(title + "\x00" + text + "\x00" + link + "\x00" + label))
	key := "app\x00" + t.Lang + "\x00" + strconv.FormatInt(t.TenantID, 10) + "\x00" + hex.EncodeToString(sum[:])
	d.write(w, r, http.StatusOK, func() string { return title }, key, func() (string, error) {
		return orDefaultTemplates(d.Templates).Page(r.Context(), t, "page.route", map[string]any{
			"page":  map[string]any{"title": title},
			"route": map[string]any{"title": title, "text": text, "link": link, "link_label": label},
		})
	})
}

// write sends a page of the door's: the one drawn under key when it is still
// good, else what draw renders now - compressed when the visitor's browser
// takes gzip. HEAD is answered without a page.
//
// ⚠ Measured 2026-11-12 (#324, a CPU profile under H4): a page in
// page.layout is 5.6 KB where the door's own html/template page was 1.2 KB,
// and writing it took a sixth of filex's CPU in a flood - on loopback the
// sender pays for the receiver's TCP too. Compressed once when it is drawn,
// it is 2.1 KB, written in one go.
func (d *RouteDoor) write(w http.ResponseWriter, r *http.Request, status int, title func() string, key string, draw func() (string, error)) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", routePageCSP)
	h.Set("Vary", "Accept-Encoding")
	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return
	}
	p, err := d.cachedPage(key, draw)
	if err != nil {
		// filex's own template failed (an edited one falls back to it): a
		// defect of this build. The visitor still gets the words.
		slog.Error("templates: the app route door's page did not render", slog.String("err", err.Error()))
		words := html.EscapeString(title())
		p = drawnPage{body: []byte("<!doctype html><meta charset=\"utf-8\"><title>" + words + "</title><h1>" + words + "</h1>")}
	}
	body := p.body
	if p.gz != nil && takesGzip(r) {
		h.Set("Content-Encoding", "gzip")
		body = p.gz
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// takesGzip: the request's Accept-Encoding names gzip (and does not refuse
// it with q=0).
func takesGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}

// cachedPage is the page drawn under key when it is younger than
// routePageTTL and no template was saved since; else draw's. While one visit
// draws a page again, the others are served the one drawn before - a flood
// does not render the same page once a visit when the minute turns.
func (d *RouteDoor) cachedPage(key string, draw func() (string, error)) (drawnPage, error) {
	gen := d.Templates.Generation()
	if c, ok := d.pages.Get(key); ok {
		if c.gen == gen && time.Since(c.at) < routePageTTL {
			return c, nil
		}
		if _, busy := d.drawing.LoadOrStore(key, struct{}{}); busy {
			return c, nil
		}
		defer d.drawing.Delete(key)
	}
	out, err := draw()
	if err != nil {
		return drawnPage{}, err
	}
	p := drawnPage{body: []byte(out), gz: gzipped(out), at: time.Now(), gen: gen}
	d.pages.Put(key, p)
	return p, nil
}

// gzipped is s compressed, or nil when compressing did not work.
func gzipped(s string) []byte {
	var b bytes.Buffer
	zw, err := gzip.NewWriterLevel(&b, gzip.BestCompression)
	if err != nil {
		return nil
	}
	if _, err := io.WriteString(zw, s); err != nil {
		return nil
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	return b.Bytes()
}
