package wasmplugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── A visit to a public route, and the shortener's call (filex 0.60.0) ─────
//
// docs/APP-PLUGINS-API.md → Public routes, The link-shortener role. Both run
// on the app's warm instances (pool.go). The route door (package api) holds
// the visitor-facing invariants: the host split, the miss limiter, the
// preview filter, the headers; this file holds what the answer may be.

// The budgets: a visit is a person waiting on a redirect; a shortener call is
// a person waiting on a button.
const (
	routeBudget   = time.Second
	routeWait     = 50 * time.Millisecond
	shortenBudget = 5 * time.Second
	shortenWait   = 2 * time.Second
)

// ErrNoRoute: the app is not answering that route now (not set up, stopped,
// removed, held by its license).
var ErrNoRoute = errors.New("wasmplugin: the route is not answered now")

// Route hands one visit to the app that answers the base. tenant is the
// visited address's tenant (the `tenant` scope of the app's data on this
// call).
func (r *Registry) Route(ctx context.Context, app, route string, tenant int64, in wire.RouteInput) (*wire.RouteOutput, error) {
	p, ok := r.ByName(app)
	if !ok {
		return nil, ErrNoRoute
	}
	if _, ok := p.Manifest.Route(route); !ok {
		return nil, ErrNoRoute
	}
	c, err := p.running()
	if err != nil {
		return nil, ErrNoRoute
	}
	s := &Scope{plugin: p, reg: r, route: true, files: map[string]*scopeFile{}, handles: map[uint64]*handle{}}
	s.setTenant(tenant)
	in.Route = route
	in.Settings = r.routeSettings(ctx, p)
	inb, _ := json.Marshal(in)
	outb, err := r.hotCall(ctx, c, s, "route", inb, routeBudget, routeWait)
	if err != nil {
		return nil, err
	}
	var out wire.RouteOutput
	if err := json.Unmarshal(outb, &out); err != nil {
		return nil, &CallError{Code: CodePluginError, Export: "route", Message: "route returned malformed JSON"}
	}
	if err := CheckRouteOutput(&out); err != nil {
		p.log("warn", "route: "+err.Error())
		return nil, &CallError{Code: CodePluginError, Export: "route", Message: err.Error()}
	}
	return &out, nil
}

// CheckRouteOutput holds a route's answer to the contract: exactly one of a
// redirect, a page and a miss; a redirect (and a page's link) an absolute
// http(s) address, no user name or password, at most 2048 bytes; a page's
// texts short.
func CheckRouteOutput(out *wire.RouteOutput) error {
	n := 0
	if out.Redirect != "" {
		n++
		if err := CheckOutboundURL(out.Redirect); err != nil {
			return errors.New("redirect " + err.Error())
		}
	}
	if out.Page != nil {
		n++
		if len(out.Page.Title) == 0 {
			return errors.New("a page needs a title")
		}
		if out.Page.Link != "" {
			if err := CheckOutboundURL(out.Page.Link); err != nil {
				return errors.New("page link " + err.Error())
			}
		}
		for _, t := range []wire.Text{out.Page.Title, out.Page.Text, out.Page.LinkLabel} {
			for _, v := range t {
				if len(v) > 4096 {
					return errors.New("a page's text is at most 4 KiB")
				}
			}
		}
	}
	if out.Miss {
		n++
	}
	if n != 1 {
		return errors.New("a route answers exactly one of redirect, page and miss")
	}
	return nil
}

// CheckOutboundURL is the rule for an address filex sends a visitor to on an
// app's word: absolute http or https, a host, no user name or password, at
// most 2048 bytes.
func CheckOutboundURL(raw string) error {
	if len(raw) > 2048 {
		return errors.New("is longer than 2048 bytes")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" {
		return errors.New("is not an absolute http(s) address")
	}
	if u.User != nil {
		return errors.New("carries a user name or password")
	}
	if strings.ContainsAny(raw, "\r\n\t ") {
		return errors.New("holds white space")
	}
	return nil
}

// Shortener is the app holding the link-shortener role now, "" for none:
// set up, running, with a `link_shortener` block and the grant. One app
// holds it at a time (the first by name; enabling a second is refused,
// ShortenerConflict).
func (r *Registry) Shortener() (*Installed, bool) {
	var best *Installed
	for _, p := range r.All() {
		if p.Manifest == nil || p.Manifest.LinkShortener == nil || !p.Grants.Has(PermLinkShortener) {
			continue
		}
		if state, _ := p.State(); state != StateRunning {
			continue
		}
		if best == nil || p.Row.Name < best.Row.Name {
			best = p
		}
	}
	return best, best != nil
}

// ShortenerConflict names the app that already holds the shortener role
// when m would be a second one, "" when there is none.
func (r *Registry) ShortenerConflict(name string, m *Manifest) string {
	if m == nil || m.LinkShortener == nil {
		return ""
	}
	for _, p := range r.All() {
		if p.Row.Name == name || p.Manifest == nil || p.Manifest.LinkShortener == nil {
			continue
		}
		if state, _ := p.State(); state == StateDisabled {
			continue
		}
		return p.Row.Name
	}
	return ""
}

// ShortenerRequires is the user permission key "Get short link" needs ("" for
// none) - app.<app>.<id>.
func (r *Registry) ShortenerRequires() string {
	p, ok := r.Shortener()
	if !ok || p.Manifest.LinkShortener.Requires == "" {
		return ""
	}
	return "app." + p.Row.Name + "." + p.Manifest.LinkShortener.Requires
}

// Shorten asks the shortener app for a short address of one of filex's links,
// for actor. The answer must lie on one of the bases the app was handed.
func (r *Registry) Shorten(ctx context.Context, actor *model.User, locale string, in wire.ShortenInput) (*wire.ShortenOutput, error) {
	p, ok := r.Shortener()
	if !ok {
		return nil, ErrNoRoute
	}
	c, err := p.running()
	if err != nil {
		return nil, ErrNoRoute
	}
	s := &Scope{plugin: p, reg: r, actor: actor, locale: locale, files: map[string]*scopeFile{}, handles: map[uint64]*handle{}}
	tenant := s.dataTenant(ctx)
	in.Bases = r.routeBases(ctx, p, p.Manifest.LinkShortener.Route, tenant)
	if len(in.Bases) == 0 {
		return nil, ErrNoRoute
	}
	in.Locale = locale
	in.Settings = r.publicSettings(ctx, p)
	if actor != nil {
		a := r.wireActor(ctx, p, actor, actorIPFrom(ctx))
		in.Actor = &a
	}
	inb, _ := json.Marshal(in)
	outb, err := r.hotCall(ctx, c, s, "shorten", inb, shortenBudget, shortenWait)
	if err != nil {
		return nil, err
	}
	var out wire.ShortenOutput
	if err := json.Unmarshal(outb, &out); err != nil {
		return nil, &CallError{Code: CodePluginError, Export: "shorten", Message: "shorten returned malformed JSON"}
	}
	if out.URL == "" {
		return &out, nil
	}
	if err := CheckOutboundURL(out.URL); err != nil {
		return nil, &CallError{Code: CodePluginError, Export: "shorten", Message: "the short address " + err.Error()}
	}
	onBase := false
	for _, b := range in.Bases {
		if strings.HasPrefix(out.URL, strings.TrimRight(b.URL, "/")+"/") {
			onBase = true
			break
		}
	}
	if !onBase {
		p.log("warn", "shorten answered an address on none of its bases: "+out.URL)
		return nil, &CallError{Code: CodePluginError, Export: "shorten", Message: "the short address lies on none of the app's addresses"}
	}
	return &out, nil
}

// callRoutes is the route bases a screen of the app is told (CallContext),
// for the person's tenant: what an app's own page needs to make a short
// address of its own.
func (r *Registry) callRoutes(ctx context.Context, p *Installed, actor *model.User) map[string][]wire.RouteBase {
	if p.Manifest == nil || len(p.Manifest.Routes) == 0 {
		return nil
	}
	s := &Scope{plugin: p, reg: r, actor: actor}
	tenant := s.dataTenant(ctx)
	out := map[string][]wire.RouteBase{}
	for _, rt := range p.Manifest.Routes {
		if bases := r.routeBases(ctx, p, rt.ID, tenant); len(bases) > 0 {
			out[rt.ID] = bases
		}
	}
	return out
}

// routeSettingsTTL is how long a visit reads the app's settings from memory:
// a setting is changed once in a while, a route is visited all the time, and
// a flood of visits must not become a flood of settings reads (each one a
// database row and an unsealing).
const routeSettingsTTL = 5 * time.Second

type cachedSettings struct {
	app  *Installed
	vals map[string]string
	at   time.Time
}

func (r *Registry) routeSettings(ctx context.Context, p *Installed) map[string]string {
	now := time.Now()
	r.settingsMu.Lock()
	c, ok := r.settingsCache[p.Row.Name]
	r.settingsMu.Unlock()
	if ok && c.app == p && now.Sub(c.at) < routeSettingsTTL {
		return c.vals
	}
	vals := r.publicSettings(ctx, p)
	r.settingsMu.Lock()
	if r.settingsCache == nil {
		r.settingsCache = map[string]cachedSettings{}
	}
	r.settingsCache[p.Row.Name] = cachedSettings{app: p, vals: vals, at: now}
	r.settingsMu.Unlock()
	return vals
}
