package wasmplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/shortlink"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Setting an app up (filex 0.60.0) ───────────────────────────────────────
//
// docs/APP-PLUGINS-API.md → Setting an app up. An app whose manifest has a
// `setup` block, or routes (which have no address until they are given one),
// is installed into the `setup` state: loaded, running nothing - no action,
// screen, interface call, wake-up, page, thumbnail, route or role - until an
// administrator finishes its wizard. The wizard is the app's own `setup`
// view (filex draws it) followed by filex's own steps: the addresses of its
// routes, who holds its permissions. Reconfigure erases what the app keeps
// and opens the wizard again; removing the app erases it too.
//
// The record is the host's (app_plugin_setup, migration 00138): the routes'
// addresses and whether the steps are done. Nothing of the app's own data is
// in it.

// StateSetup is an app waiting to be set up. It overlays "running" only,
// like StateUnlicensed.
const StateSetup = "setup"

// SetupRecord is the host's record of an app's setup.
type SetupRecord struct {
	// Done: the wizard was finished; the app runs.
	Done bool `json:"done"`
	// AppDone: the app's own step said done (its setup view answered
	// `done: true`).
	AppDone bool `json:"app_done"`
	// Routes are the bases given to each of the app's routes, by route id.
	Routes   map[string][]RouteBaseRow `json:"routes,omitempty"`
	NextBase int64                     `json:"next_base"`
	// CompletedAt / CompletedBy: when and by whom the wizard was finished.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CompletedBy int64      `json:"completed_by,omitempty"`
}

// RouteBaseRow is one base of a route.
type RouteBaseRow struct {
	ID       int64  `json:"id"`
	Host     string `json:"host"`
	Prefix   string `json:"prefix"`
	TenantID int64  `json:"tenant_id"`
	Enabled  bool   `json:"enabled"`
	Default  bool   `json:"default"`
}

// Errors the setup doors answer with their codes.
var (
	ErrSetupAppStep   = errors.New("setup_app_step")
	ErrSetupNoBase    = errors.New("setup_no_base")
	ErrSetupDone      = errors.New("setup_done")
	ErrNoSuchRoute    = errors.New("no_such_route")
	ErrNoSuchBase     = errors.New("no_such_base")
	ErrRouteBaseTaken = errors.New("route_base_taken")
	ErrRouteHostTaken = errors.New("route_host_taken")
	ErrRouteHostFilex = errors.New("route_host_is_filex")
	ErrConfirm        = errors.New("confirm_mismatch")
	ErrNoSetup        = errors.New("setup_not_needed")
	ErrNoSuchTenant   = errors.New("no_such_tenant")
)

// RouteBaseError is a base refused, with the field it is about.
type RouteBaseError struct {
	Err   error
	Field string
}

func (e *RouteBaseError) Error() string { return e.Err.Error() }
func (e *RouteBaseError) Unwrap() error { return e.Err }

// setupPending reports whether the app is still waiting to be set up.
func (p *Installed) setupPending() bool {
	if p == nil || p.Manifest == nil || !p.Manifest.NeedsSetup() {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.setup == nil || !p.setup.Done
}

func (p *Installed) setupRecord() *SetupRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.setup == nil {
		return &SetupRecord{}
	}
	cp := *p.setup
	cp.Routes = map[string][]RouteBaseRow{}
	for k, v := range p.setup.Routes {
		cp.Routes[k] = append([]RouteBaseRow(nil), v...)
	}
	return &cp
}

// loadSetups reads every app's record (Load).
func (r *Registry) loadSetups(ctx context.Context) map[string]*SetupRecord {
	out := map[string]*SetupRecord{}
	docs, err := r.opts.Store.ListAppSetups(ctx)
	if err != nil {
		r.log.Warn("app-plugins: setup records unreadable; apps that need a setup wait for one", "err", err)
		return out
	}
	for app, doc := range docs {
		var rec SetupRecord
		if json.Unmarshal([]byte(doc), &rec) == nil {
			out[app] = &rec
		}
	}
	return out
}

func (r *Registry) saveSetup(ctx context.Context, p *Installed, rec *SetupRecord) error {
	b, _ := json.Marshal(rec)
	if err := r.opts.Store.PutAppSetup(ctx, p.Row.Name, string(b)); err != nil {
		return err
	}
	r.mu.Lock()
	if r.setups == nil {
		r.setups = map[string]*SetupRecord{}
	}
	r.setups[p.Row.Name] = rec
	r.mu.Unlock()
	p.mu.Lock()
	p.setup = rec
	p.mu.Unlock()
	r.routesChanged()
	return nil
}

// forgetSetup deletes an app's record (removal).
func (r *Registry) forgetSetup(ctx context.Context, name string) error {
	r.mu.Lock()
	delete(r.setups, name)
	r.mu.Unlock()
	r.routesChanged()
	return r.opts.Store.DeleteAppSetup(ctx, name)
}

// ── What the wizard is told ────────────────────────────────────────────────

// SetupAnswer is GET …/{id}/setup.
type SetupAnswer struct {
	State       string            `json:"state"`
	Needed      bool              `json:"needed"`
	Done        bool              `json:"done"`
	AppStep     *SetupAppStep     `json:"app_step"`
	Routes      []SetupRoute      `json:"routes"`
	Permissions []SetupPermission `json:"permissions"`
	Data        SetupData         `json:"data"`
	Warning     wire.Text         `json:"warning,omitempty"`
	CompletedAt *time.Time        `json:"completed_at,omitempty"`
	// OwnHosts is the host filex itself answers on (its public address): a
	// base on "this filex's address" lies on it, and a host of the app's
	// own is pointed at it in DNS. Empty when filex has no address.
	OwnHosts []string `json:"own_hosts"`
}

// SetupAppStep is the app's own step.
type SetupAppStep struct {
	View string `json:"view"`
	Done bool   `json:"done"`
}

// SetupRoute is one route and its bases.
type SetupRoute struct {
	ID            string          `json:"id"`
	Label         wire.Text       `json:"label"`
	DefaultPrefix string          `json:"default_prefix,omitempty"`
	Bases         []SetupBaseView `json:"bases"`
}

// SetupBaseView is a base as the wizard draws it.
type SetupBaseView struct {
	RouteBaseRow
	URL string `json:"url"`
}

// SetupPermission is one of the app's user permissions, for the "who may
// use it" step.
type SetupPermission struct {
	ID          string    `json:"id"`
	Key         string    `json:"key"`
	Label       wire.Text `json:"label"`
	Description wire.Text `json:"description,omitempty"`
	Default     string    `json:"default"`
}

// SetupData says where the app's own data lives.
type SetupData struct {
	Uses      bool   `json:"uses"`
	Driver    string `json:"driver,omitempty"`
	NodeLocal bool   `json:"node_local"`
}

// SetupStatus answers the wizard's facts for app id.
func (r *Registry) SetupStatus(ctx context.Context, id int64) (*SetupAnswer, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	m := p.Manifest
	rec := p.setupRecord()
	state, _ := p.State()
	a := &SetupAnswer{State: state, Needed: m.NeedsSetup(), Done: rec.Done, CompletedAt: rec.CompletedAt}
	if m.Setup != nil && m.Setup.View != "" {
		a.AppStep = &SetupAppStep{View: m.Setup.View, Done: rec.AppDone}
	}
	if m.Setup != nil && m.Setup.Reset != nil {
		a.Warning = m.Setup.Reset.Warning
	}
	for _, rt := range m.Routes {
		sr := SetupRoute{ID: rt.ID, Label: rt.Label, DefaultPrefix: rt.DefaultPrefix, Bases: []SetupBaseView{}}
		for _, b := range rec.Routes[rt.ID] {
			sr.Bases = append(sr.Bases, SetupBaseView{RouteBaseRow: b, URL: r.baseURL(ctx, b)})
		}
		a.Routes = append(a.Routes, sr)
	}
	for _, up := range m.UserPermissions {
		a.Permissions = append(a.Permissions, SetupPermission{ID: up.ID, Key: "app." + p.Row.Name + "." + up.ID,
			Label: up.Label, Description: up.Description, Default: up.Default})
	}
	if p.Grants.Has(PermData) {
		a.Data.Uses = true
		a.Data.Driver, a.Data.NodeLocal = r.DataInfo()
	}
	a.OwnHosts = []string{}
	if r.routeHosts.OwnOrigin != nil {
		if u, err := url.Parse(r.routeHosts.OwnOrigin(ctx, 0)); err == nil && u.Host != "" {
			a.OwnHosts = append(a.OwnHosts, u.Host)
		}
	}
	return a, nil
}

// ── The app's own step ─────────────────────────────────────────────────────

// SetupView answers one event of the app's setup view, for an administrator.
// The app may keep what it was told in its own data (`data`); a surface
// that says `done` finishes the app's step.
func (r *Registry) SetupView(ctx context.Context, id int64, actor *model.User, locale string, in wire.ViewEventInput) (*wire.Surface, bool, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, false, installErr(ErrCodeNotFound, "no such plugin")
	}
	m := p.Manifest
	if m.Setup == nil || m.Setup.View == "" {
		return nil, false, ErrNoSetup
	}
	c, err := p.compiledForSetup()
	if err != nil {
		return nil, false, err
	}
	release, err := p.enterCall(ctx)
	if err != nil {
		return nil, false, err
	}
	defer release()
	scope, err := newScope(p, r, "", 0, nil, actor, locale, false)
	if err != nil {
		return nil, false, err
	}
	defer scope.Close()
	in.ViewID = m.Setup.View
	in.Context = wire.CallContext{Locale: locale, Settings: r.publicSettings(ctx, p)}
	if actor != nil {
		a := r.wireActor(ctx, p, actor, actorIPFrom(ctx))
		in.Context.Actor = &a
	}
	inb, _ := json.Marshal(in)
	outb, err := c.Call(WithScope(ctx, scope), "view_event", inb, 0)
	if err != nil {
		return nil, false, err
	}
	var s wire.Surface
	if err := json.Unmarshal(outb, &s); err != nil {
		return nil, false, &CallError{Code: CodePluginError, Message: "view_event returned malformed JSON"}
	}
	SanitizeSurface(&s)
	// A setup screen sends nobody anywhere and queues no work: the app is
	// not running yet.
	s.Open, s.Job = nil, nil
	rec := p.setupRecord()
	if s.Done && !rec.AppDone {
		rec.AppDone = true
		if err := r.saveSetup(ctx, p, rec); err != nil {
			return nil, false, err
		}
	}
	return &s, rec.AppDone, nil
}

// compiledForSetup is the app's module while it waits to be set up: loaded,
// running nothing but its setup step.
func (p *Installed) compiledForSetup() (*Compiled, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.hold != "" {
		return nil, &CallError{Code: CodeUnsupported, Message: "plugin is not running: " + p.hold}
	}
	if p.compiled == nil || p.state != StateRunning {
		return nil, &CallError{Code: CodeUnsupported, Message: "plugin is not running"}
	}
	return p.compiled, nil
}

// ── The addresses ──────────────────────────────────────────────────────────

// RouteHosts answers whether a host is one filex itself answers on (its own
// address, a tenant's, the apps' interface origin, the S3 host...), so it can
// be no route's host of its own; and whether a path's first segment is one of
// filex's routes, so it can be no prefix on filex's own address. Set by the
// router (SetRouteHosts).
type RouteHosts struct {
	IsFilexHost  func(ctx context.Context, host string) bool
	Reserved     func(first string) bool
	OwnOrigin    func(ctx context.Context, tenantID int64) string
	TenantExists func(ctx context.Context, id int64) bool
}

// SetRouteHosts wires the router's answers (routes.go in package api).
func (r *Registry) SetRouteHosts(h RouteHosts) { r.routeHosts = h }

// SetRoutesChanged hands the router fn, called (on a goroutine of its own)
// soon after the served bases change - a setup finished, a base added or
// switched off, an app started, stopped, removed or upgraded - and every
// routesRefresh anyway, so a change no door announced is picked up too.
// fn rebuilds the router's snapshot from RouteBases.
func (r *Registry) SetRoutesChanged(fn func()) {
	r.onRoutes = fn
	r.routesSig = make(chan struct{}, 1)
	r.routesStop = make(chan struct{})
	sig, stop := r.routesSig, r.routesStop
	go func() {
		t := time.NewTicker(routesRefresh)
		defer t.Stop()
		for {
			select {
			case <-sig:
			case <-t.C:
			case <-stop:
				return
			}
			fn()
		}
	}()
	fn()
}

// stopRoutes ends the rebuilding goroutine (Close).
func (r *Registry) stopRoutes() {
	if r.routesStop != nil {
		select {
		case <-r.routesStop:
		default:
			close(r.routesStop)
		}
	}
}

// routesRefresh is how often the router's snapshot is rebuilt unasked.
const routesRefresh = 10 * time.Second

// routesChanged asks for a rebuild. ⚠ Never calls fn itself: it is called
// from places that hold the registry's or an entry's lock, and fn reads both.
func (r *Registry) routesChanged() {
	if r.routesSig == nil {
		return
	}
	select {
	case r.routesSig <- struct{}{}:
	default:
	}
}

// baseURL is a base's address, on the tenant's origin for filex's own.
func (r *Registry) baseURL(ctx context.Context, b RouteBaseRow) string {
	if b.Host != "" {
		u := "https://" + b.Host
		if b.Prefix != "" {
			u += "/" + b.Prefix
		}
		return u
	}
	origin := ""
	if r.routeHosts.OwnOrigin != nil {
		origin = r.routeHosts.OwnOrigin(ctx, b.TenantID)
	}
	return strings.TrimRight(origin, "/") + "/" + b.Prefix
}

// AddRouteBase gives one of the app's routes a base. Refusals are
// *RouteBaseError (the field, the code).
func (r *Registry) AddRouteBase(ctx context.Context, id int64, route, host, prefix string, tenantID int64, makeDefault bool) (*SetupBaseView, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	if _, ok := p.Manifest.Route(route); !ok {
		return nil, ErrNoSuchRoute
	}
	r.setupMu.Lock()
	defer r.setupMu.Unlock()
	h := ""
	if strings.TrimSpace(host) != "" {
		var err error
		if h, err = shortlink.CheckHost(host); err != nil {
			return nil, &RouteBaseError{Err: err, Field: "host"}
		}
		if r.routeHosts.IsFilexHost != nil && r.routeHosts.IsFilexHost(ctx, h) {
			return nil, &RouteBaseError{Err: ErrRouteHostFilex, Field: "host"}
		}
	}
	pre, err := shortlink.CheckPrefix(prefix, h == "", r.routeHosts.Reserved)
	if err != nil {
		return nil, &RouteBaseError{Err: err, Field: "prefix"}
	}
	if h == "" || !r.opts.MultiTenant {
		tenantID = 0
	} else if tenantID != 0 && (r.routeHosts.TenantExists == nil || !r.routeHosts.TenantExists(ctx, tenantID)) {
		return nil, &RouteBaseError{Err: ErrNoSuchTenant, Field: "tenant_id"}
	}
	// One (host, prefix) answers one route of one app; on filex's own
	// address one prefix does.
	for _, other := range r.All() {
		orec := other.setupRecord()
		for _, bases := range orec.Routes {
			for _, b := range bases {
				if b.Host == h && b.Prefix == pre {
					return nil, &RouteBaseError{Err: ErrRouteBaseTaken, Field: "prefix"}
				}
				if h != "" && b.Host == h && other.Row.Name != p.Row.Name {
					return nil, &RouteBaseError{Err: ErrRouteHostTaken, Field: "host"}
				}
			}
		}
	}
	rec := p.setupRecord()
	if rec.Routes == nil {
		rec.Routes = map[string][]RouteBaseRow{}
	}
	rec.NextBase++
	b := RouteBaseRow{ID: rec.NextBase, Host: h, Prefix: pre, TenantID: tenantID, Enabled: true}
	bases := rec.Routes[route]
	if makeDefault || len(bases) == 0 {
		for i := range bases {
			bases[i].Default = false
		}
		b.Default = true
	}
	rec.Routes[route] = append(bases, b)
	if err := r.saveSetup(ctx, p, rec); err != nil {
		return nil, err
	}
	return &SetupBaseView{RouteBaseRow: b, URL: r.baseURL(ctx, b)}, nil
}

// PatchRouteBase switches a base on or off, or makes it the route's default.
// A base is never edited otherwise: a printed address must keep meaning what
// it meant.
func (r *Registry) PatchRouteBase(ctx context.Context, id, baseID int64, enabled, makeDefault *bool) (*SetupBaseView, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	r.setupMu.Lock()
	defer r.setupMu.Unlock()
	rec := p.setupRecord()
	for route, bases := range rec.Routes {
		for i := range bases {
			if bases[i].ID != baseID {
				continue
			}
			if enabled != nil {
				bases[i].Enabled = *enabled
			}
			if makeDefault != nil && *makeDefault {
				for j := range bases {
					bases[j].Default = j == i
				}
			}
			rec.Routes[route] = bases
			if err := r.saveSetup(ctx, p, rec); err != nil {
				return nil, err
			}
			return &SetupBaseView{RouteBaseRow: bases[i], URL: r.baseURL(ctx, bases[i])}, nil
		}
	}
	return nil, ErrNoSuchBase
}

// DeleteRouteBase removes a base while the setup is not finished; after that
// a base is only switched off.
func (r *Registry) DeleteRouteBase(ctx context.Context, id, baseID int64) error {
	p, ok := r.ByID(id)
	if !ok {
		return installErr(ErrCodeNotFound, "no such plugin")
	}
	r.setupMu.Lock()
	defer r.setupMu.Unlock()
	rec := p.setupRecord()
	if rec.Done {
		return ErrSetupDone
	}
	for route, bases := range rec.Routes {
		for i := range bases {
			if bases[i].ID != baseID {
				continue
			}
			wasDefault := bases[i].Default
			bases = append(bases[:i], bases[i+1:]...)
			if wasDefault && len(bases) > 0 {
				bases[0].Default = true
			}
			rec.Routes[route] = bases
			return r.saveSetup(ctx, p, rec)
		}
	}
	return ErrNoSuchBase
}

// CompleteSetup finishes the wizard: the app's own step said done, every
// route has a base it is served on. The app runs from now.
func (r *Registry) CompleteSetup(ctx context.Context, id int64, by int64) error {
	p, ok := r.ByID(id)
	if !ok {
		return installErr(ErrCodeNotFound, "no such plugin")
	}
	if !p.Manifest.NeedsSetup() {
		return ErrNoSetup
	}
	r.setupMu.Lock()
	defer r.setupMu.Unlock()
	rec := p.setupRecord()
	if p.Manifest.Setup != nil && p.Manifest.Setup.View != "" && !rec.AppDone {
		return ErrSetupAppStep
	}
	for _, rt := range p.Manifest.Routes {
		served := false
		for _, b := range rec.Routes[rt.ID] {
			served = served || b.Enabled
		}
		if !served {
			return ErrSetupNoBase
		}
	}
	now := time.Now().UTC()
	rec.Done, rec.CompletedAt, rec.CompletedBy = true, &now, by
	if err := r.saveSetup(ctx, p, rec); err != nil {
		return err
	}
	p.log("info", "setup finished")
	return nil
}

// ── Reconfigure, and removing an app ───────────────────────────────────────

// EraseCounts is what Reconfigure (and removing the app) erases.
type EraseCounts struct {
	DataKeys  int64 `json:"data_keys"`
	DataBytes int64 `json:"data_bytes"`
	StateRows int64 `json:"state_rows"`
	Locks     int64 `json:"locks"`
	Links     int64 `json:"links"`
}

// Any reports whether there is anything to erase.
func (c EraseCounts) Any() bool {
	return c.DataKeys+c.StateRows+c.Locks+c.Links > 0
}

// EraseAnswer is GET …/{id}/erase.
type EraseAnswer struct {
	Counts  EraseCounts `json:"counts"`
	Warning wire.Text   `json:"warning,omitempty"`
	Confirm string      `json:"confirm"`
}

// EraseFacts counts what the app keeps.
func (r *Registry) EraseFacts(ctx context.Context, id int64) (*EraseAnswer, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	var c EraseCounts
	c.DataKeys, c.DataBytes = r.DataUsage(ctx, p.Row.Name)
	if st, lk, err := r.opts.Store.AppPluginKept(ctx, p.Row.ID); err == nil {
		c.StateRows, c.Locks = st, lk
	}
	if _, total, err := r.opts.Store.ListAppPluginShares(ctx, p.Row.ID, true, 1, 0); err == nil {
		c.Links = total
	}
	a := &EraseAnswer{Counts: c, Confirm: p.Row.Name}
	if p.Manifest.Setup != nil && p.Manifest.Setup.Reset != nil {
		a.Warning = p.Manifest.Setup.Reset.Warning
	}
	return a, nil
}

// eraseData tells the app (its `reset`, when it has one) and erases what it
// keeps: its own data, its per-file state, its locks, its scheduled work,
// and the links it opened (revoked).
func (r *Registry) eraseData(ctx context.Context, p *Installed, removing bool) error {
	if c, err := p.compiledForSetup(); err == nil {
		if has, _ := c.HasExport(ctx, "reset"); has {
			in, _ := json.Marshal(wire.ResetInput{Removing: removing, Settings: r.publicSettings(ctx, p)})
			s, serr := newScope(p, r, "", 0, nil, nil, "", false)
			if serr == nil {
				s.system = true
				if _, err := c.Call(WithScope(ctx, s), "reset", in, 30*time.Second); err != nil {
					p.log("warn", "reset: "+err.Error()+" (the data is erased anyway)")
				}
				s.Close()
			}
		}
	}
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	r.counters.dropApp(p.Row.Name)
	if r.opts.Data != nil {
		keep(r.opts.Data.Drop(ctx, p.Row.Name))
	}
	if !removing {
		keep(r.opts.Store.EraseAppPluginKept(ctx, p.Row.ID))
	}
	// The links it opened end (a revoke moves their expiry to now, as
	// share_revoke does), page by page until none is active.
	for {
		rows, _, err := r.opts.Store.ListAppPluginShares(ctx, p.Row.ID, true, 200, 0)
		if err != nil || len(rows) == 0 {
			keep(err)
			break
		}
		revoked := 0
		for _, row := range rows {
			if row.Share == nil {
				continue
			}
			if err := r.opts.Store.RevokeShare(ctx, row.Share.ID); err == nil {
				revoked++
			}
		}
		if revoked == 0 {
			break
		}
	}
	return first
}

// Reconfigure erases what the app keeps and puts it back into its setup.
// confirm must be the app's name. The routes' bases are kept for the wizard
// to start from, unfinished (they can be removed or added again).
func (r *Registry) Reconfigure(ctx context.Context, id int64, confirm string, by int64) error {
	p, ok := r.ByID(id)
	if !ok {
		return installErr(ErrCodeNotFound, "no such plugin")
	}
	if !p.Manifest.NeedsSetup() {
		return ErrNoSetup
	}
	if strings.TrimSpace(confirm) != p.Row.Name {
		return ErrConfirm
	}
	if r.opts.Demo {
		return installErr(ErrCodeDemo, "app plugins cannot be changed on the demo instance")
	}
	r.setupMu.Lock()
	defer r.setupMu.Unlock()
	rec := p.setupRecord()
	rec.Done, rec.AppDone, rec.CompletedAt, rec.CompletedBy = false, false, nil, 0
	// The app stops answering first, so nothing writes while it is erased.
	if err := r.saveSetup(ctx, p, rec); err != nil {
		return err
	}
	if c, err := p.compiledForSetup(); err == nil {
		c.closeHot()
	}
	err := r.eraseData(ctx, p, false)
	p.log("info", fmt.Sprintf("reconfigured: its data was erased (by account %d)", by))
	return err
}

// ConfirmRemove is what removing an app asks: when it keeps anything, the
// app's name typed back. ErrConfirm with the counts when it is missing or
// wrong.
func (r *Registry) ConfirmRemove(ctx context.Context, id int64, confirm string) (*EraseAnswer, error) {
	a, err := r.EraseFacts(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.Counts.Any() && strings.TrimSpace(confirm) != a.Confirm {
		return a, ErrConfirm
	}
	return a, nil
}

// ── The served bases ───────────────────────────────────────────────────────

// RouteBases are the bases filex serves now: every enabled base of every
// route of every app that is set up and running.
func (r *Registry) RouteBases() []shortlink.Base {
	var out []shortlink.Base
	for _, p := range r.All() {
		if state, _ := p.State(); state != StateRunning || !p.HasModule() {
			continue
		}
		rec := p.setupRecord()
		if !rec.Done {
			continue
		}
		for _, rt := range p.Manifest.Routes {
			for _, b := range rec.Routes[rt.ID] {
				out = append(out, shortlink.Base{ID: b.ID, Host: b.Host, Prefix: b.Prefix, TenantID: b.TenantID,
					Enabled: b.Enabled, App: p.Row.Name, Route: rt.ID})
			}
		}
	}
	// Longest prefix first, so `go/x` is matched before `go`.
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].Prefix) > len(out[j].Prefix) })
	return out
}

// routeBases is the app's bases of one route, for a call.
func (r *Registry) routeBases(ctx context.Context, p *Installed, route string, tenant int64) []wire.RouteBase {
	rec := p.setupRecord()
	var out []wire.RouteBase
	for _, b := range rec.Routes[route] {
		if !b.Enabled {
			continue
		}
		// A host of its own belongs to one tenant; filex's own address is
		// every tenant's, on that tenant's origin.
		if b.Host != "" && r.opts.MultiTenant && b.TenantID != tenant {
			continue
		}
		bb := b
		if b.Host == "" {
			bb.TenantID = tenant
		}
		out = append(out, wire.RouteBase{ID: b.ID, URL: r.baseURL(ctx, bb), Default: b.Default})
	}
	return out
}
