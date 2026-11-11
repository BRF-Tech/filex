package wasmplugin

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/appdata"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── An app's own data (data_*, permission `data`) and link_live ────────────
//
// docs/APP-PLUGINS-API.md → An app's own data. The store is internal/appdata;
// this file is the host functions in front of it and the one decision they
// add: WHICH tenant a call's `tenant` scope is. The app names `tenant` or
// `app`; the host maps `tenant` to the call's tenant - the person's on a call
// made for a person, the visited address's on a route visit - so an app
// cannot read or write another tenant's keys by spelling them.

// routeHostFns are the host functions a route visit may reach.
var routeHostFns = map[string]bool{
	"data_get": true, "data_list": true, "data_incr": true,
	"link_live": true, "settings_get": true,
}

// routeDenied is the one sentence a route visit is refused with.
const routeDenied = "a route visit reads and counts the app's data and nothing else"

// dataTenant is the tenant of the call's `tenant` scope.
func (s *Scope) dataTenant(ctx context.Context) int64 {
	if s.tenantSet {
		return s.tenant
	}
	if !s.reg.opts.MultiTenant {
		return 0
	}
	switch {
	case s.actor != nil:
		return s.reg.tenantKey(ctx, tenantOf(s.actor))
	case s.page != nil:
		return s.reg.tenantKey(ctx, s.page.TenantID)
	}
	return 0
}

// setTenant fixes the call's tenant (a route visit: the address's).
func (s *Scope) setTenant(t int64) { s.tenant, s.tenantSet = t, true }

// tenantKey is a provider id as a data scope knows it: the platform's own
// tenant (the supertenant, or none) is 0.
func (r *Registry) tenantKey(ctx context.Context, providerID int64) int64 {
	if providerID <= 0 || r.opts.Store == nil {
		return 0
	}
	if v, ok := r.tenantKeys.Load(providerID); ok {
		e := v.(tenantKeyEntry)
		if time.Since(e.at) < time.Minute {
			return e.key
		}
	}
	key := providerID
	if p, err := r.opts.Store.GetProvider(ctx, providerID); err == nil && p != nil && p.IsSupertenant {
		key = 0
	}
	r.tenantKeys.Store(providerID, tenantKeyEntry{key: key, at: time.Now()})
	return key
}

type tenantKeyEntry struct {
	key int64
	at  time.Time
}

// TenantKey is tenantKey for the callers outside the package (the route
// door resolves a host's tenant with it).
func (r *Registry) TenantKey(ctx context.Context, providerID int64) int64 {
	if !r.opts.MultiTenant {
		return 0
	}
	return r.tenantKey(ctx, providerID)
}

func (s *Scope) dataScope(ctx context.Context, scope string) (string, error) {
	switch scope {
	case wire.DataApp:
		return appdata.AppScope, nil
	case wire.DataTenant, "":
		return appdata.TenantScope(s.dataTenant(ctx)), nil
	}
	return "", hostErr(wire.ErrInvalid, "scope is tenant or app")
}

// dataErr is a store failure in the wire's words.
func dataErr(err error) error {
	switch {
	case errors.Is(err, appdata.ErrConflict):
		return hostErr(wire.ErrBusy, "the condition does not hold")
	case errors.Is(err, appdata.ErrQuota):
		return hostErr(wire.ErrTooLarge, "the app's data is at its size limit")
	case errors.Is(err, appdata.ErrCounter):
		return hostErr(wire.ErrInvalid, "a counter key is written by data_incr only, and data_incr adds only to a counter")
	case errors.Is(err, appdata.ErrKey):
		return hostErr(wire.ErrInvalid, "a key is 1 to 512 printable ASCII characters (no space)")
	case errors.Is(err, appdata.ErrValue):
		return hostErr(wire.ErrInvalid, "a value is valid UTF-8, at most 64 KiB")
	case errors.Is(err, appdata.ErrScope):
		return hostErr(wire.ErrInvalid, "scope is tenant or app")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return hostErr(wire.ErrTimeout, "the app's data did not answer in time")
	}
	return hostErr(wire.ErrUnavailable, "the app's data cannot be reached")
}

func (s *Scope) store() (appdata.Store, error) {
	if st := s.reg.opts.Data; st != nil {
		return st, nil
	}
	return nil, hostErr(wire.ErrUnavailable, "this filex keeps no app data")
}

func hfDataGet(ctx context.Context, s *Scope, raw json.RawMessage) (any, error) {
	var in struct {
		Scope string `json:"scope"`
		Key   string `json:"key"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad input")
	}
	st, err := s.store()
	if err != nil {
		return nil, err
	}
	sc, err := s.dataScope(ctx, in.Scope)
	if err != nil {
		return nil, err
	}
	e, found, err := st.Get(ctx, s.plugin.Row.Name, sc, in.Key)
	if err != nil {
		return nil, dataErr(err)
	}
	if found && e.Counter {
		e.Value = s.reg.counters.pendingValue(s.plugin.Row.Name, sc, in.Key, e.Value)
	}
	return map[string]any{"value": e.Value, "rev": e.Rev, "found": found}, nil
}

func hfDataPut(ctx context.Context, s *Scope, raw json.RawMessage) (any, error) {
	var in struct {
		Scope    string `json:"scope"`
		Key      string `json:"key"`
		Value    string `json:"value"`
		IfAbsent bool   `json:"if_absent"`
		IfRev    int64  `json:"if_rev"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad input")
	}
	st, err := s.store()
	if err != nil {
		return nil, err
	}
	sc, err := s.dataScope(ctx, in.Scope)
	if err != nil {
		return nil, err
	}
	rev, err := st.Put(ctx, s.plugin.Row.Name, sc, in.Key, in.Value, appdata.Cond{IfAbsent: in.IfAbsent, IfRev: in.IfRev})
	if err != nil {
		return nil, dataErr(err)
	}
	return map[string]any{"rev": rev}, nil
}

func hfDataDelete(ctx context.Context, s *Scope, raw json.RawMessage) (any, error) {
	var in struct {
		Scope string `json:"scope"`
		Key   string `json:"key"`
		IfRev int64  `json:"if_rev"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad input")
	}
	st, err := s.store()
	if err != nil {
		return nil, err
	}
	sc, err := s.dataScope(ctx, in.Scope)
	if err != nil {
		return nil, err
	}
	ok, err := st.Delete(ctx, s.plugin.Row.Name, sc, in.Key, in.IfRev)
	if err != nil {
		return nil, dataErr(err)
	}
	s.reg.counters.forget(s.plugin.Row.Name, sc, in.Key)
	return map[string]any{"deleted": ok}, nil
}

func hfDataList(ctx context.Context, s *Scope, raw json.RawMessage) (any, error) {
	var in struct {
		Scope  string `json:"scope"`
		Prefix string `json:"prefix"`
		After  string `json:"after"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad input")
	}
	st, err := s.store()
	if err != nil {
		return nil, err
	}
	sc, err := s.dataScope(ctx, in.Scope)
	if err != nil {
		return nil, err
	}
	items, next, err := st.List(ctx, s.plugin.Row.Name, sc, in.Prefix, in.After, in.Limit)
	if err != nil {
		return nil, dataErr(err)
	}
	out := make([]map[string]any, 0, len(items))
	for _, e := range items {
		v := e.Value
		if e.Counter {
			v = s.reg.counters.pendingValue(s.plugin.Row.Name, sc, e.Key, v)
		}
		out = append(out, map[string]any{"key": e.Key, "value": v, "rev": e.Rev})
	}
	return map[string]any{"items": out, "next": next}, nil
}

func hfDataIncr(ctx context.Context, s *Scope, raw json.RawMessage) (any, error) {
	var in struct {
		Scope string `json:"scope"`
		Key   string `json:"key"`
		By    int64  `json:"by"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad input")
	}
	st, err := s.store()
	if err != nil {
		return nil, err
	}
	sc, err := s.dataScope(ctx, in.Scope)
	if err != nil {
		return nil, err
	}
	if err := appdata.CheckKey(in.Key); err != nil {
		return nil, dataErr(err)
	}
	if s.route {
		// A visit's count is added up here and written about once a second
		// (counters.go): a flood of visits to one link is one write a
		// second, not one per visit.
		n, err := s.reg.counters.add(ctx, st, s.plugin.Row.Name, sc, in.Key, in.By)
		if err != nil {
			return nil, dataErr(err)
		}
		return map[string]any{"value": n}, nil
	}
	n, err := st.Incr(ctx, s.plugin.Row.Name, sc, in.Key, in.By)
	if err != nil {
		return nil, dataErr(err)
	}
	return map[string]any{"value": s.reg.counters.pendingAdd(s.plugin.Row.Name, sc, in.Key, n)}, nil
}

// hfLinkLive answers whether one of filex's public links is still open for
// the call's tenant: there, not revoked, not run out, its creator's account
// on. Anything else - including a link of another tenant - is not live,
// with no word on why.
func hfLinkLive(ctx context.Context, s *Scope, raw json.RawMessage) (any, error) {
	var in struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad input")
	}
	return map[string]any{"live": s.reg.linkLive(ctx, s, strings.TrimSpace(in.Link))}, nil
}

// linkLiveTTL is how long an answer of link_live is kept: a short link is
// opened far more often than its share changes, and a flood of visits to one
// link must not become a flood of database reads. A revoked link answers a
// miss within this long.
const linkLiveTTL = 5 * time.Second

type linkLiveKey struct {
	token  string
	tenant int64
}

type linkLiveEntry struct {
	live bool
	at   time.Time
}

func (r *Registry) linkLive(ctx context.Context, s *Scope, token string) bool {
	if token == "" || len(token) > 256 || r.opts.Store == nil {
		return false
	}
	k := linkLiveKey{strings.ToLower(token), s.dataTenant(ctx)}
	now := time.Now()
	r.linkMu.Lock()
	if e, ok := r.linkCache[k]; ok && now.Sub(e.at) < linkLiveTTL {
		r.linkMu.Unlock()
		return e.live
	}
	r.linkMu.Unlock()
	live := r.linkLiveNow(ctx, s, token)
	r.linkMu.Lock()
	if r.linkCache == nil || len(r.linkCache) >= 50000 {
		r.linkCache = map[linkLiveKey]linkLiveEntry{}
	}
	r.linkCache[k] = linkLiveEntry{live: live, at: now}
	r.linkMu.Unlock()
	return live
}

// forgetLinkLive drops the kept answers (a link was revoked from filex's own
// doors: the next visit asks again).
func (r *Registry) ForgetLinkLive() {
	r.linkMu.Lock()
	r.linkCache = nil
	r.linkMu.Unlock()
}

func (r *Registry) linkLiveNow(ctx context.Context, s *Scope, token string) bool {
	sh, err := r.opts.Store.GetShareByToken(ctx, strings.ToLower(token))
	if err != nil || sh == nil || sh.IsApp() {
		return false
	}
	now := time.Now()
	if sh.RevokedAt != nil || sh.IsExpired(now) {
		return false
	}
	if r.opts.MultiTenant && r.tenantKey(ctx, sh.TenantID) != s.dataTenant(ctx) {
		return false
	}
	if sh.CreatedBy != nil {
		u, err := r.opts.Store.GetUser(ctx, *sh.CreatedBy)
		if err != nil || u == nil || !u.Enabled {
			return false
		}
	}
	return true
}

// DataUsage is how many keys and bytes an app keeps (Reconfigure's and
// removal's count).
func (r *Registry) DataUsage(ctx context.Context, app string) (int64, int64) {
	if r.opts.Data == nil {
		return 0, 0
	}
	keys, bytes, err := r.opts.Data.Usage(ctx, app)
	if err != nil {
		return 0, 0
	}
	return keys, bytes
}

// DataInfo says where apps' data lives, for the setup wizard and the review.
func (r *Registry) DataInfo() (driver string, nodeLocal bool) {
	if r.opts.Data == nil {
		return "", false
	}
	return r.opts.Data.Driver(), r.opts.Data.NodeLocal()
}

// ── Visits' counts, added up ───────────────────────────────────────────────

// counterBuffer adds up the data_incr of route visits and writes them about
// once a second. The value a visit is answered is the stored one plus what
// is waiting.
type counterBuffer struct {
	mu      sync.Mutex
	pending map[counterKey]int64
	// base is the stored value last read for a key, so a visit's answer
	// costs no read once the key was seen.
	base    map[counterKey]int64
	started bool
	stop    chan struct{}
	store   appdata.Store
	log     func(msg string, err error)
}

type counterKey struct{ app, scope, key string }

const counterFlushEvery = time.Second

func (c *counterBuffer) add(ctx context.Context, st appdata.Store, app, scope, key string, by int64) (int64, error) {
	k := counterKey{app, scope, key}
	c.mu.Lock()
	base, seen := c.base[k]
	c.mu.Unlock()
	if !seen {
		e, found, err := st.Get(ctx, app, scope, key)
		if err != nil {
			return 0, err
		}
		if found && !e.Counter {
			return 0, appdata.ErrCounter
		}
		if found {
			base = parseInt(e.Value)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending, c.base = map[counterKey]int64{}, map[counterKey]int64{}
	}
	if _, ok := c.base[k]; !ok {
		c.base[k] = base
	}
	c.pending[k] += by
	if !c.started {
		c.started, c.store, c.stop = true, st, make(chan struct{})
		go c.loop(c.stop)
	}
	return c.base[k] + c.pending[k], nil
}

// pendingValue is a stored counter's value with what is waiting added.
func (c *counterBuffer) pendingValue(app, scope, key, stored string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := c.pending[counterKey{app, scope, key}]; d != 0 {
		return itoa64(parseInt(stored) + d)
	}
	return stored
}

// pendingAdd is a value just written by Incr with what is waiting added.
func (c *counterBuffer) pendingAdd(app, scope, key string, n int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := counterKey{app, scope, key}
	if _, ok := c.base[k]; ok {
		c.base[k] = n
	}
	return n + c.pending[k]
}

// forget drops what is waiting for a deleted key.
func (c *counterBuffer) forget(app, scope, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := counterKey{app, scope, key}
	delete(c.pending, k)
	delete(c.base, k)
}

// dropApp drops everything waiting for an app (its data is being erased).
func (c *counterBuffer) dropApp(app string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.pending {
		if k.app == app {
			delete(c.pending, k)
		}
	}
	for k := range c.base {
		if k.app == app {
			delete(c.base, k)
		}
	}
}

func (c *counterBuffer) loop(stop chan struct{}) {
	t := time.NewTicker(counterFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			c.Flush(context.Background())
		case <-stop:
			return
		}
	}
}

// close writes what is waiting and ends the loop (Registry.Close).
func (c *counterBuffer) close(ctx context.Context) {
	c.Flush(ctx)
	c.mu.Lock()
	stop := c.stop
	c.stop, c.started = nil, false
	c.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

// Flush writes what is waiting (also called at shutdown and by tests).
func (c *counterBuffer) Flush(ctx context.Context) {
	c.mu.Lock()
	batch := c.pending
	c.pending = map[counterKey]int64{}
	st := c.store
	c.mu.Unlock()
	if st == nil {
		return
	}
	for k, d := range batch {
		n, err := st.Incr(ctx, k.app, k.scope, k.key, d)
		c.mu.Lock()
		if err != nil {
			// A failed write is dropped, not retried for ever: a click count
			// is worth less than a store the flusher keeps hammering.
			delete(c.base, k)
		} else {
			c.base[k] = n
		}
		c.mu.Unlock()
		if err != nil && c.log != nil {
			c.log("app data: a visit count was not written", err)
		}
	}
}

func parseInt(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
