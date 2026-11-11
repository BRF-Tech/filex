package wasmplugin

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/appdata"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Public routes, setup, the pool, the shortener: against the real module ─
//
// The echo module answers `route`, `shorten`, `reset` and a `setup` view
// (testdata/echo/routes.go); these tests install it with a manifest widened
// by the 0.60 blocks.

// routesManifest widens echo's manifest into a link shortener.
func routesManifest(m map[string]any) {
	perms, _ := m["permissions"].([]any)
	m["permissions"] = append(perms, "data", "public_routes", "link_shortener")
	ups, _ := m["user_permissions"].([]any)
	m["user_permissions"] = append(ups, map[string]any{"id": "use", "label": map[string]any{"en": "Shorten links", "tr": "Bağlantı kısalt"}, "default": "user"})
	m["routes"] = []any{map[string]any{"id": "go", "label": map[string]any{"en": "Short links", "tr": "Kısa bağlantılar"}, "default_prefix": "l"}}
	m["setup"] = map[string]any{"view": "setup", "reset": map[string]any{"warning": map[string]any{"en": "Every short link goes.", "tr": "Bütün kısa bağlantılar gider."}}}
	m["link_shortener"] = map[string]any{"route": "go", "requires": "use"}
	views, _ := m["views"].([]any)
	m["views"] = append(views, map[string]any{"id": "setup", "placement": "setup", "label": map[string]any{"en": "Set up", "tr": "Kur"}})
}

type routesHarness struct {
	*harness
	data appdata.Store
	p    *Installed
}

func newRoutesHarness(t *testing.T, opts func(*Options)) *routesHarness {
	t.Helper()
	data, err := appdata.OpenSQLite(t.TempDir(), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = data.Close() })
	h := newHarness(t, func(o *Options) {
		o.Data = data
		o.RoutePool = 2
		if opts != nil {
			opts(o)
		}
	})
	h.reg.SetRouteHosts(RouteHosts{
		Reserved:  func(first string) bool { return first == "s" || first == "api" },
		OwnOrigin: func(context.Context, int64) string { return "https://fm.example.com" },
	})
	st, _, err := h.reg.Install(context.Background(), echoInput(t, routesManifest))
	require.NoError(t, err)
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)
	return &routesHarness{harness: h, data: data, p: p}
}

// setUp walks the wizard: the app's step, an address, finish.
func (h *routesHarness) setUp(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	s, done, err := h.reg.SetupView(ctx, h.p.Row.ID, nil, "en", wire.ViewEventInput{Event: "open"})
	require.NoError(t, err)
	require.False(t, done)
	require.NotEmpty(t, s.Actions)
	_, done, err = h.reg.SetupView(ctx, h.p.Row.ID, nil, "en", wire.ViewEventInput{Event: "action", ActionID: "finish"})
	require.NoError(t, err)
	require.True(t, done)
	_, err = h.reg.AddRouteBase(ctx, h.p.Row.ID, "go", "", "l", 0, true)
	require.NoError(t, err)
	require.NoError(t, h.reg.CompleteSetup(ctx, h.p.Row.ID, 1))
}

func (h *routesHarness) visit(t *testing.T, path string, countable bool) (*wire.RouteOutput, error) {
	t.Helper()
	return h.reg.Route(context.Background(), "echo", "go", 0, wire.RouteInput{
		Base: wire.RouteBase{ID: 1, URL: "https://fm.example.com/l"}, Path: path, Method: "GET", Countable: countable,
	})
}

// An app with routes runs nothing until its setup is finished, and nothing
// of it is served.
func TestSetup_AnAppWithRoutesWaitsForItsWizard(t *testing.T) {
	h := newRoutesHarness(t, nil)
	ctx := context.Background()
	state, _ := h.p.State()
	assert.Equal(t, StateSetup, state)
	_, err := h.visit(t, "hit-a", true)
	assert.ErrorIs(t, err, ErrNoRoute)
	_, err = h.reg.ViewEvent(ctx, "echo", "hello", h.st.ID, nil, nil, "en", wire.ViewEventInput{Event: "open"})
	assert.Error(t, err, "a screen of an app waiting for its setup")
	assert.Empty(t, h.reg.RouteBases())
	_, ok := h.reg.Shortener()
	assert.False(t, ok, "the shortener role is not held before the setup")

	// Finishing before the app's step and an address is refused.
	assert.ErrorIs(t, h.reg.CompleteSetup(ctx, h.p.Row.ID, 1), ErrSetupAppStep)
	_, _, err = h.reg.SetupView(ctx, h.p.Row.ID, nil, "en", wire.ViewEventInput{Event: "action", ActionID: "finish"})
	require.NoError(t, err)
	assert.ErrorIs(t, h.reg.CompleteSetup(ctx, h.p.Row.ID, 1), ErrSetupNoBase)

	// The app kept what its step decided in its own data.
	e, ok, err := h.data.Get(ctx, "echo", appdata.AppScope, "config")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "set up", e.Value)

	_, err = h.reg.AddRouteBase(ctx, h.p.Row.ID, "go", "", "l", 0, false)
	require.NoError(t, err)
	require.NoError(t, h.reg.CompleteSetup(ctx, h.p.Row.ID, 1))
	state, _ = h.p.State()
	assert.Equal(t, StateRunning, state)
	bases := h.reg.RouteBases()
	require.Len(t, bases, 1)
	assert.Equal(t, "echo", bases[0].App)
	assert.Equal(t, "l", bases[0].Prefix)

	// A second registry over the same database finds the app set up.
	reg2, err := New(h.reg.opts)
	require.NoError(t, err)
	t.Cleanup(func() { reg2.Close(context.Background()) })
	require.NoError(t, reg2.Load(ctx))
	p2, ok := reg2.ByName("echo")
	require.True(t, ok)
	state, _ = p2.State()
	assert.Equal(t, StateRunning, state, "the setup outlives the process")
}

func TestSetup_TheAddressesAreChecked(t *testing.T) {
	h := newRoutesHarness(t, nil)
	ctx := context.Background()
	h.reg.SetRouteHosts(RouteHosts{
		Reserved:    func(first string) bool { return first == "s" },
		IsFilexHost: func(_ context.Context, host string) bool { return host == "fm.example.com" },
		OwnOrigin:   func(context.Context, int64) string { return "https://fm.example.com" },
	})
	code := func(err error) string {
		var be *RouteBaseError
		if errors.As(err, &be) {
			return be.Field + ":" + be.Err.Error()
		}
		return "?" + err.Error()
	}
	for _, c := range []struct {
		host, prefix, want string
	}{
		{"", "", "prefix:short_link_prefix_required"},
		{"", "s", "prefix:short_link_prefix_reserved"},
		{"", "a_b", "prefix:short_link_prefix_invalid"},
		{"fm.example.com", "", "host:route_host_is_filex"},
		{"203.0.113.7", "", "host:short_link_host_invalid"},
	} {
		_, err := h.reg.AddRouteBase(ctx, h.p.Row.ID, "go", c.host, c.prefix, 0, false)
		require.Error(t, err, "%q %q", c.host, c.prefix)
		assert.Equal(t, c.want, code(err), "%q %q", c.host, c.prefix)
	}
	b, err := h.reg.AddRouteBase(ctx, h.p.Row.ID, "go", "L.Example.com", "", 0, false)
	require.NoError(t, err)
	assert.Equal(t, "l.example.com", b.Host)
	assert.Equal(t, "https://l.example.com", b.URL)
	assert.True(t, b.Default, "the first base is the default")
	_, err = h.reg.AddRouteBase(ctx, h.p.Row.ID, "go", "l.example.com", "", 0, false)
	assert.Equal(t, "prefix:route_base_taken", code(err))
	_, err = h.reg.AddRouteBase(ctx, h.p.Row.ID, "nope", "", "x", 0, false)
	assert.ErrorIs(t, err, ErrNoSuchRoute)
	// Removed while not done; after the setup only switched off.
	own, err := h.reg.AddRouteBase(ctx, h.p.Row.ID, "go", "", "l", 0, false)
	require.NoError(t, err)
	require.NoError(t, h.reg.DeleteRouteBase(ctx, h.p.Row.ID, own.ID))
	_, _, err = h.reg.SetupView(ctx, h.p.Row.ID, nil, "en", wire.ViewEventInput{Event: "action", ActionID: "finish"})
	require.NoError(t, err)
	require.NoError(t, h.reg.CompleteSetup(ctx, h.p.Row.ID, 1))
	assert.ErrorIs(t, h.reg.DeleteRouteBase(ctx, h.p.Row.ID, b.ID), ErrSetupDone)
	off := false
	_, err = h.reg.PatchRouteBase(ctx, h.p.Row.ID, b.ID, &off, nil)
	require.NoError(t, err)
	assert.Empty(t, h.reg.RouteBases()[0:0])
	for _, rb := range h.reg.RouteBases() {
		assert.False(t, rb.Enabled && rb.ID == b.ID, "a switched-off base is served")
	}
}

// What a visit may be answered, and what the app may do on one.
func TestRoute_AnswersAndTheirRules(t *testing.T) {
	h := newRoutesHarness(t, nil)
	h.setUp(t)
	ctx := context.Background()

	out, err := h.visit(t, "hit-a", true)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/a", out.Redirect)
	_, err = h.visit(t, "hit-a", false)
	require.NoError(t, err)
	h.reg.counters.Flush(ctx)
	e, ok, err := h.data.Get(ctx, "echo", appdata.TenantScope(0), "n/a")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "1", e.Value, "only the countable visit is counted")

	out, err = h.visit(t, "page", true)
	require.NoError(t, err)
	require.NotNil(t, out.Page)
	assert.Equal(t, "A page", out.Page.Title["en"])

	out, err = h.visit(t, "nothing-here", true)
	require.NoError(t, err)
	assert.True(t, out.Miss)

	_, err = h.visit(t, "bad", true)
	assert.Error(t, err, "a javascript: redirect is refused")

	out, err = h.visit(t, "write", true)
	require.NoError(t, err)
	require.NotNil(t, out.Page)
	assert.Contains(t, out.Page.Title["en"], "refused", "a visit cannot write the app's data")
	_, ok, _ = h.data.Get(ctx, "echo", appdata.TenantScope(0), "w")
	assert.False(t, ok)
}

// A call that runs past its budget, or crashes, takes its instance with it;
// the next visit gets a fresh one.
func TestPool_ABadCallTakesItsInstanceWithIt(t *testing.T) {
	h := newRoutesHarness(t, nil)
	h.setUp(t)
	start := time.Now()
	_, err := h.visit(t, "sleep", true)
	took := time.Since(start)
	require.Error(t, err)
	assert.True(t, IsCode(err, CodeTimeout), "%v", err)
	assert.Less(t, took, 3*time.Second, "the budget is a second")
	out, err := h.visit(t, "hit-b", true)
	require.NoError(t, err, "the next visit, on a fresh instance")
	assert.Equal(t, "https://example.com/b", out.Redirect)

	_, err = h.visit(t, "crash", true)
	require.Error(t, err)
	out, err = h.visit(t, "hit-c", true)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/c", out.Redirect)
}

// The pool never runs more calls at once than its size, and what does not
// fit in the queue is answered busy at once.
func TestPool_ConcurrencyIsBoundedAndOverflowIsShed(t *testing.T) {
	h := newRoutesHarness(t, nil)
	h.setUp(t)
	c, err := h.p.running()
	require.NoError(t, err)
	pool := c.hot(poolSize(h.reg.opts.RoutePool))
	require.Equal(t, 2, pool.size)

	var wg sync.WaitGroup
	var ok, busy atomic.Int32
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := h.visit(t, "hit-x", false); err == nil {
				ok.Add(1)
			} else if IsCode(err, CodeBusy) {
				busy.Add(1)
			} else {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	assert.LessOrEqual(t, pool.peak.Load(), int32(2), "never more calls at once than the pool")
	assert.Equal(t, int32(40), ok.Load()+busy.Load())
	assert.Positive(t, ok.Load())

	// Two slow calls hold both instances; four more fill the queue; the next
	// is answered busy without waiting for its wait.
	var slow sync.WaitGroup
	for i := 0; i < 2; i++ {
		slow.Add(1)
		go func() { defer slow.Done(); _, _ = h.visit(t, "sleep", false) }()
	}
	time.Sleep(200 * time.Millisecond)
	s := &Scope{plugin: h.p, reg: h.reg, route: true}
	in, _ := json.Marshal(wire.RouteInput{Path: "hit-y", Method: "GET"})
	var queued sync.WaitGroup
	for i := 0; i < 4; i++ {
		queued.Add(1)
		go func() {
			defer queued.Done()
			_, _ = pool.call(WithScope(context.Background(), s), "route", in, routeBudget, 2*time.Second)
		}()
	}
	time.Sleep(100 * time.Millisecond)
	t0 := time.Now()
	_, err = pool.call(WithScope(context.Background(), s), "route", in, routeBudget, 2*time.Second)
	assert.True(t, IsCode(err, CodeBusy), "%v", err)
	assert.Less(t, time.Since(t0), 100*time.Millisecond, "a full queue answers at once")
	slow.Wait()
	queued.Wait()
}

// Half of the last twenty calls failing opens the breaker: the app is left
// alone and every call is answered busy at once.
func TestPool_TheBreakerOpensOnFailures(t *testing.T) {
	h := newRoutesHarness(t, nil)
	h.setUp(t)
	for i := 0; i < breakerWindow; i++ {
		_, _ = h.visit(t, "crash", false)
	}
	t0 := time.Now()
	_, err := h.visit(t, "hit-z", false)
	assert.True(t, IsCode(err, CodeBusy), "%v", err)
	assert.Less(t, time.Since(t0), 50*time.Millisecond)
	c, _ := h.p.running()
	assert.True(t, c.hot(2).br.open(time.Now()))
}

// The shortener: one address per link, held to the app's bases, the same
// address again for the same link, nothing made when only asked.
func TestShorten_OneAddressPerLinkOnTheAppsBases(t *testing.T) {
	h := newRoutesHarness(t, nil)
	h.setUp(t)
	ctx := context.Background()
	in := wire.ShortenInput{Target: wire.ShortenTarget{Kind: wire.ShortenShare, URL: "https://fm.example.com/s/tok", Link: ""}}
	none, err := h.reg.Shorten(ctx, nil, "en", wire.ShortenInput{Target: in.Target, Existing: true})
	require.NoError(t, err)
	assert.Empty(t, none.URL, "asked only: nothing is made")
	out, err := h.reg.Shorten(ctx, nil, "en", in)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out.URL, "https://fm.example.com/l/"), out.URL)
	again, err := h.reg.Shorten(ctx, nil, "en", in)
	require.NoError(t, err)
	assert.Equal(t, out.URL, again.URL)
	code := strings.TrimPrefix(out.URL, "https://fm.example.com/l/")
	got, err := h.visit(t, code, true)
	require.NoError(t, err)
	assert.Equal(t, "https://fm.example.com/s/tok", got.Redirect)
}

// A link made in one tenant is not found on another tenant's address.
func TestRoute_TenantsDoNotSeeEachOthersData(t *testing.T) {
	h := newRoutesHarness(t, func(o *Options) { o.MultiTenant = true })
	h.setUp(t)
	ctx := context.Background()
	out, err := h.reg.Shorten(ctx, nil, "en", wire.ShortenInput{Target: wire.ShortenTarget{Kind: wire.ShortenShare, URL: "https://fm.example.com/s/t1"}})
	require.NoError(t, err)
	code := strings.TrimPrefix(out.URL, "https://fm.example.com/l/")
	at := func(tenant int64) *wire.RouteOutput {
		o, err := h.reg.Route(ctx, "echo", "go", tenant, wire.RouteInput{Path: code, Method: "GET"})
		require.NoError(t, err)
		return o
	}
	assert.Equal(t, "https://fm.example.com/s/t1", at(0).Redirect)
	assert.True(t, at(42).Miss, "tenant 42's address does not find the platform's link")
}

// A link to a share answers a miss once the share ends: there is no "this
// existed" to learn.
func TestShorten_AnEndedShareIsAMiss(t *testing.T) {
	h := newRoutesHarness(t, nil)
	h.setUp(t)
	ctx := context.Background()
	h.writeFile(t, "a.txt", "x")
	n, err := h.store.CreateNode(ctx, &model.Node{StorageID: h.st.ID, Name: "a.txt", Path: "/a.txt", PathHash: "h-a", Type: model.NodeTypeFile})
	require.NoError(t, err)
	sh, err := h.share.Create(ctx, shareOpts(n.ID))
	require.NoError(t, err)
	out, err := h.reg.Shorten(ctx, nil, "en", wire.ShortenInput{Target: wire.ShortenTarget{Kind: wire.ShortenShare, URL: "https://fm.example.com/s/" + sh.Token, Link: sh.Token}})
	require.NoError(t, err)
	code := strings.TrimPrefix(out.URL, "https://fm.example.com/l/")
	got, err := h.visit(t, code, true)
	require.NoError(t, err)
	assert.Equal(t, "https://fm.example.com/s/"+sh.Token, got.Redirect)
	require.NoError(t, h.store.RevokeShare(ctx, sh.ID))
	// link_live's answer is kept for linkLiveTTL; a revoke from filex's own
	// doors is seen within that, here at once.
	h.reg.ForgetLinkLive()
	got, err = h.visit(t, code, true)
	require.NoError(t, err)
	assert.True(t, got.Miss)
}

// Reconfigure erases what the app keeps and puts it back into its setup;
// the name must be typed back.
func TestReconfigure_ErasesAndStartsTheWizardAgain(t *testing.T) {
	h := newRoutesHarness(t, nil)
	h.setUp(t)
	ctx := context.Background()
	_, err := h.reg.Shorten(ctx, nil, "en", wire.ShortenInput{Target: wire.ShortenTarget{Kind: wire.ShortenShare, URL: "https://x/s/1"}})
	require.NoError(t, err)
	facts, err := h.reg.EraseFacts(ctx, h.p.Row.ID)
	require.NoError(t, err)
	assert.Positive(t, facts.Counts.DataKeys)
	assert.Equal(t, "echo", facts.Confirm)
	assert.Equal(t, "Every short link goes.", facts.Warning["en"])

	assert.ErrorIs(t, h.reg.Reconfigure(ctx, h.p.Row.ID, "Echo", 1), ErrConfirm)
	require.NoError(t, h.reg.Reconfigure(ctx, h.p.Row.ID, "echo", 1))
	keys, _ := h.reg.DataUsage(ctx, "echo")
	assert.Zero(t, keys, "every key is erased")
	state, _ := h.p.State()
	assert.Equal(t, StateSetup, state)
	assert.Empty(t, h.reg.RouteBases(), "nothing is served while it is set up again")
	_, err = h.visit(t, "hit-a", true)
	assert.ErrorIs(t, err, ErrNoRoute)
	a, err := h.reg.SetupStatus(ctx, h.p.Row.ID)
	require.NoError(t, err)
	assert.False(t, a.AppStep.Done)
	require.Len(t, a.Routes, 1)
	assert.Len(t, a.Routes[0].Bases, 1, "the addresses are kept for the wizard to start from")
}

// Removing an app that keeps data asks for its name, then erases it all.
func TestRemove_ErasesTheAppsData(t *testing.T) {
	h := newRoutesHarness(t, nil)
	h.setUp(t)
	ctx := context.Background()
	_, err := h.reg.Shorten(ctx, nil, "en", wire.ShortenInput{Target: wire.ShortenTarget{Kind: wire.ShortenShare, URL: "https://x/s/1"}})
	require.NoError(t, err)
	_, err = h.reg.ConfirmRemove(ctx, h.p.Row.ID, "")
	assert.ErrorIs(t, err, ErrConfirm)
	_, err = h.reg.ConfirmRemove(ctx, h.p.Row.ID, "echo")
	require.NoError(t, err)
	require.NoError(t, h.reg.Remove(ctx, h.p.Row.ID))
	keys, _, err := h.data.Usage(ctx, "echo")
	require.NoError(t, err)
	assert.Zero(t, keys)
	_, found, err := h.store.(interface {
		GetAppSetup(ctx context.Context, app string) (string, bool, error)
	}).GetAppSetup(ctx, "echo")
	require.NoError(t, err)
	assert.False(t, found, "the setup record goes too")
}

// One shortener at a time.
func TestShortener_OneAppHoldsTheRole(t *testing.T) {
	h := newRoutesHarness(t, nil)
	h.setUp(t)
	p, ok := h.reg.Shortener()
	require.True(t, ok)
	assert.Equal(t, "echo", p.Row.Name)
	other := &Manifest{}
	other.LinkShortener = &wire.LinkShortenerSpec{Route: "go"}
	assert.Equal(t, "echo", h.reg.ShortenerConflict("other", other))
	assert.Empty(t, h.reg.ShortenerConflict("echo", other), "the app itself is no conflict")
	assert.Empty(t, h.reg.ShortenerConflict("other", &Manifest{}))
}

// Warm instances are cheap enough for a visit: a warm call is far below a
// fresh instance (measured, not assumed).
func TestPool_AWarmVisitIsCheap(t *testing.T) {
	if testing.Short() || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("timing")
	}
	h := newRoutesHarness(t, nil)
	h.setUp(t)
	_, _ = h.visit(t, "hit-w", false)
	const n = 200
	start := time.Now()
	for i := 0; i < n; i++ {
		_, err := h.visit(t, "hit-w", false)
		require.NoError(t, err)
	}
	per := time.Since(start) / n
	t.Logf("warm visit: %v", per)
	assert.Less(t, per, 5*time.Millisecond, "a warm visit costs a fraction of a fresh instance (10 ms)")
}

func shareOpts(nodeID int64) share.CreateOpts { return share.CreateOpts{NodeID: nodeID} }
