package handlers_test

// Plugin install requests (internal/pluginreq, docs/APP-PLUGINS.md → Install
// requests), over real HTTP.
//
// ⚠⚠ The rule under test (owner, 2026-09-28): an API key may not install,
// upgrade, remove or re-permission a plugin — it may only leave a request.
// Before this, an agent holding an admin-scoped key could copy the manifest's
// permission list into `permissions` and pass the review that is the app
// model's security boundary itself.
//
// ⚠ The harness registers the always-on api-token driver the way
// internal/server does (useProductionAuthChain): testutil's chain has the
// session driver only, and on that chain a token never reaches /api/admin at
// all — every "token refused" assertion would pass for the wrong reason.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/pluginreq"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/tenantstore"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// packManifest is a language pack: an app with no module, so the fixture needs
// no wasm build and every test here runs everywhere.
func packManifest(version string) string {
	return `{"manifest_version":1,"name":"lang-eo","version":"` + version + `","label":{"en":"Esperanto","tr":"Esperanto dili"},` +
		`"languages":["en","tr"],"permissions":[],"ui_locales":{"eo":{"common.cancel":"Nuligi"}}}`
}

// source serves an app's manifest (plain http on loopback, which the app
// fetcher allows with LoopbackSources) and a storage plugin's feed + binary (https, which the
// storage fetcher requires). Both answers can be changed mid-test — that is
// how a source that moves on between the request and its approval is made.
type source struct {
	mu       sync.Mutex
	manifest string
	feed     map[string]any
	binary   []byte
	app      *httptest.Server
	tls      *httptest.Server
}

func newSource(t *testing.T) *source {
	s := &source{manifest: packManifest("1.0.0"), binary: []byte("a build")}
	s.app = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path != "/filex-app.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(s.manifest))
	}))
	t.Cleanup(s.app.Close)
	s.tls = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/filex-storage.json":
			_ = json.NewEncoder(w).Encode(s.feed)
		case "/myfs":
			_, _ = w.Write(s.binary)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.tls.Close)
	return s
}

func (s *source) set(fn func(s *source)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func (s *source) manifestURL() string { return s.app.URL + "/filex-app.json" }
func (s *source) feedURL() string     { return s.tls.URL + "/filex-storage.json" }

// feedFor publishes one build for the fixture's platform.
func (s *source) feedFor(version string, build []byte) map[string]any {
	sum := sha256.Sum256(build)
	return map[string]any{"name": "myfs", "version": version, "binaries": map[string]any{
		"testos/testarch": map[string]any{"url": s.tls.URL + "/myfs", "sha256": hex.EncodeToString(sum[:])},
	}}
}

// clock is the request service's time, moved by hand.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type prFix struct {
	srv     *httptest.Server
	admin   *http.Client // a signed-in administrator's session
	store   db.Store
	reg     *wasmplugin.Registry
	mgr     *plugin.Manager
	mgrDir  string
	notif   notify.Service
	adminID int64
	token   string // an admin + mcp scoped API key on the same account
	src     *source
	clock   *clock
}

func newPRFix(t *testing.T) *prFix {
	t.Helper()
	ctx := context.Background()
	_, raw := testutil.NewTestDB(t)
	accounting := quotastore.New(raw)
	var store db.Store = identitystore.New(accounting)

	localDrv := authlocal.New(store)
	require.NoError(t, localDrv.Init(ctx, nil))
	auth.SetEnabled([]auth.Driver{localDrv})
	useProductionAuthChain(t, store)

	src := newSource(t)
	cfg := config.Default()
	cfg.PublicURL = "http://test.local"
	cfg.CORS.AllowedOrigins = []string{"*"}

	resolver := func(id int64) (storage.Driver, error) { return nil, fmt.Errorf("unknown storage %d", id) }
	reg, err := wasmplugin.New(wasmplugin.Options{
		Store: store, Dir: filepath.Join(t.TempDir(), "app-plugins"),
		SecretKey: "0123456789abcdef0123456789abcdef", StorageResolver: resolver,
		LoopbackSources: true, // the app source below is a loopback httptest server
	})
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(context.Background()) })

	mgrDir := t.TempDir()
	mgr, err := plugin.New(plugin.Options{Store: store, Dir: mgrDir, SecretKey: "test-secret-key",
		HTTP: src.tls.Client(), Platform: "testos/testarch"})
	require.NoError(t, err)
	t.Cleanup(mgr.Shutdown)

	notif := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
	t.Cleanup(notif.Stop)

	clk := &clock{t: time.Now().UTC()}
	reqs := pluginreq.New(pluginreq.Options{Store: store, Apps: reg, Plugins: mgr, Notify: notif, Now: clk.Now})
	// Default apps (0.50), as internal/server wires it: an app request's File
	// types group and the approving administrator's choices.
	assocSvc := assoc.New(store)
	assocSvc.SetSource(reg)

	srv := httptest.NewServer(api.BuildRouter(&api.Deps{
		Cfg: cfg, Store: tenantstore.New(store), Quota: accounting.Quota(),
		Worker: syncpkg.New(store), Caps: capability.New(store), Share: share.NewService(store),
		StorageResolver: resolver, AppPlugins: reg, Plugins: mgr, Notify: notif,
		PluginRequests: reqs, LocalAuth: localDrv, Assoc: assocSvc,
	}))
	t.Cleanup(srv.Close)

	client := freshClient(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	admin, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)
	tok := testutil.NewAPIToken(t, store, admin.ID, "admin,mcp,read")
	return &prFix{srv: srv, admin: client, store: store, reg: reg, mgr: mgr, mgrDir: mgrDir, notif: notif,
		adminID: admin.ID, token: tok, src: src, clock: clk}
}

// withToken sends one request carrying only the API key — no cookie jar, so
// nothing can fall through to the session.
func withToken(t *testing.T, method, url, token string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func (f *prFix) url(p string) string { return f.srv.URL + p }

// packBody is the install body for the fixture's language pack.
func (f *prFix) packBody() map[string]any {
	return map[string]any{"url": "", "manifest_url": f.src.manifestURL(), "permissions": []string{}}
}

type requestWire struct {
	ID          int64    `json:"id"`
	Kind        string   `json:"kind"`
	Op          string   `json:"op"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	SHA256      string   `json:"sha256"`
	Status      string   `json:"status"`
	Permissions []string `json:"permissions"`
	Requester   string   `json:"requester"`
	TokenLabel  string   `json:"token_label"`
	Reason      string   `json:"reason"`
	Note        string   `json:"decision_note"`
	Created     *bool    `json:"created"`
	Message     string   `json:"message"`
}

func decodeRequest(t *testing.T, raw []byte) requestWire {
	t.Helper()
	var out struct {
		Request requestWire `json:"request"`
		Created *bool       `json:"created"`
		Message string      `json:"message"`
	}
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	r := out.Request
	r.Created = out.Created
	r.Message = out.Message
	return r
}

// appRequest leaves the fixture's app request with the API key.
func (f *prFix) appRequest(t *testing.T) (int, requestWire) {
	t.Helper()
	status, raw := withToken(t, http.MethodPost, f.url("/api/admin/plugin-requests"), f.token, map[string]any{
		"kind": "app", "op": "install", "manifest_url": f.src.manifestURL(),
		"reason": "Esperanto konuşan ekip için dil paketi gerekiyor",
	})
	if status >= 300 {
		return status, requestWire{Message: string(raw)}
	}
	return status, decodeRequest(t, raw)
}

func (f *prFix) installedApps(t *testing.T) []map[string]any {
	t.Helper()
	status, raw := doReq(t, f.admin, http.MethodGet, f.url("/api/admin/app-plugins"), nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var body struct {
		Plugins []map[string]any `json:"plugins"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	return body.Plugins
}

func (f *prFix) auditActions(t *testing.T, prefix string) []*model.AuditEntry {
	t.Helper()
	rows, err := f.store.ListAuditRecent(context.Background(), 500)
	require.NoError(t, err)
	var out []*model.AuditEntry
	for _, r := range rows {
		if strings.HasPrefix(r.Action, prefix) {
			out = append(out, r)
		}
	}
	return out
}

func sessionRefused(t *testing.T, status int, raw []byte, what string) {
	t.Helper()
	assert.Equal(t, http.StatusForbidden, status, "%s with an API key must be refused; body: %s", what, raw)
	assert.Contains(t, string(raw), "session_required", what)
	assert.Contains(t, string(raw), "/api/admin/plugin-requests", "%s: the refusal names where to leave a request", what)
}

// ── The REST gate ──────────────────────────────────────────────────────

// An API key cannot install, upgrade, switch, re-permission or remove an app;
// it can still read the list, a detail, the logs, check for updates and run
// the install REVIEW (the dry run, which installs nothing). A signed-in
// administrator's session does all of it.
func TestPluginGate_AnAPIKeyCannotChangeApps(t *testing.T) {
	f := newPRFix(t)

	status, raw := withToken(t, http.MethodPost, f.url("/api/admin/app-plugins"), f.token, f.packBody())
	sessionRefused(t, status, raw, "installing an app")
	assert.Empty(t, f.installedApps(t), "nothing was installed")

	status, raw = withToken(t, http.MethodPost, f.url("/api/admin/app-plugins?dry_run=1"), f.token, f.packBody())
	assert.Equal(t, http.StatusOK, status, "the review installs nothing and stays open to a key: %s", raw)

	status, raw = doReq(t, f.admin, http.MethodPost, f.url("/api/admin/app-plugins"), f.packBody())
	require.Equal(t, http.StatusCreated, status, "an administrator's session installs: %s", raw)
	var app struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(raw, &app))
	one := fmt.Sprintf("%s/api/admin/app-plugins/%d", f.srv.URL, app.ID)

	for _, c := range []struct {
		what, method, url string
		body              any
	}{
		{"switching an app off", http.MethodPatch, one, map[string]any{"enabled": false}},
		{"upgrading an app", http.MethodPost, one + "/upgrade", f.packBody()},
		{"going back to the previous version", http.MethodPost, one + "/rollback", map[string]any{}},
		{"changing who may use its actions", http.MethodPut, one + "/overrides", map[string]any{"actions": []any{}}},
		{"removing an app", http.MethodDelete, one, nil},
	} {
		status, raw := withToken(t, c.method, c.url, f.token, c.body)
		sessionRefused(t, status, raw, c.what)
	}
	require.Len(t, f.installedApps(t), 1, "the app is still there")

	for _, c := range []struct{ method, url string }{
		{http.MethodGet, f.url("/api/admin/app-plugins")},
		{http.MethodGet, one},
		{http.MethodGet, one + "/logs"},
		{http.MethodPost, f.url("/api/admin/app-plugins/updates/check")},
	} {
		status, raw := withToken(t, c.method, c.url, f.token, map[string]any{})
		assert.Equal(t, http.StatusOK, status, "%s %s stays open to a key: %s", c.method, c.url, raw)
	}

	status, raw = doReq(t, f.admin, http.MethodDelete, one, nil)
	assert.Equal(t, http.StatusNoContent, status, "the session removes it: %s", raw)
}

// The same rule for storage plugins — a plugin whose process runs with
// filex's rights and every storage's credentials.
func TestPluginGate_AnAPIKeyCannotChangeStoragePlugins(t *testing.T) {
	f := newPRFix(t)
	ctx := context.Background()
	row, err := f.store.CreatePlugin(ctx, &model.Plugin{Name: "myfs", Kind: model.PluginKindBinary, Binary: "myfs",
		SHA256: strings.Repeat("a", 64), Version: "1.0.0", Driver: "myfs"})
	require.NoError(t, err)
	require.NoError(t, f.mgr.Load(ctx))
	one := fmt.Sprintf("%s/api/admin/plugins/%d", f.srv.URL, row.ID)

	status, raw := withToken(t, http.MethodPost, f.url("/api/admin/plugins"), f.token,
		map[string]any{"name": "otherfs", "source": f.src.feedURL()})
	sessionRefused(t, status, raw, "installing a storage plugin")
	for _, c := range []struct {
		what, method, url string
		body              any
	}{
		{"switching a storage plugin", http.MethodPatch, one, map[string]any{"enabled": true}},
		{"changing where its updates come from", http.MethodPatch, one, map[string]any{"source": f.src.feedURL()}},
		{"upgrading a storage plugin", http.MethodPost, one + "/upgrade", map[string]any{"from_source": true}},
		{"removing a storage plugin", http.MethodDelete, one, nil},
	} {
		status, raw := withToken(t, c.method, c.url, f.token, c.body)
		sessionRefused(t, status, raw, c.what)
	}
	_, err = f.store.GetPlugin(ctx, row.ID)
	require.NoError(t, err, "the plugin is still there")

	for _, c := range []struct{ method, url string }{
		{http.MethodGet, f.url("/api/admin/plugins")},
		{http.MethodGet, one},
		{http.MethodPost, f.url("/api/admin/plugins/updates/check")},
	} {
		status, raw := withToken(t, c.method, c.url, f.token, map[string]any{})
		assert.Equal(t, http.StatusOK, status, "%s %s stays open to a key: %s", c.method, c.url, raw)
	}

	status, raw = doReq(t, f.admin, http.MethodPatch, one, map[string]any{"source": f.src.feedURL()})
	assert.Equal(t, http.StatusOK, status, "the session changes it: %s", raw)
}

// ── Requests ───────────────────────────────────────────────────────────

// A key leaves a request: 201, pending, what the source answered frozen on
// it, nothing installed, the administrators told once, an audit row naming
// the key. Asking again for the same source answers the same request.
func TestPluginRequests_AnAPIKeyLeavesARequest(t *testing.T) {
	f := newPRFix(t)

	status, r := f.appRequest(t)
	require.Equal(t, http.StatusCreated, status, r.Message)
	assert.Equal(t, "pending", r.Status)
	assert.Equal(t, "app", r.Kind)
	assert.Equal(t, "install", r.Op)
	assert.Equal(t, "lang-eo", r.Name)
	assert.Equal(t, "1.0.0", r.Version)
	sum := sha256.Sum256([]byte(packManifest("1.0.0")))
	assert.Equal(t, hex.EncodeToString(sum[:]), r.SHA256, "a language pack is held to its manifest's hash")
	assert.NotNil(t, r.Permissions, "the permissions it asks for, frozen (none for a pack)")
	assert.Equal(t, "testutil", r.TokenLabel, "the request names the key it came through")
	admin, err := f.store.GetUser(context.Background(), f.adminID)
	require.NoError(t, err)
	assert.Equal(t, admin.Label(), r.Requester, "a person is named one way (model.PersonLabel)")
	assert.Equal(t, "Esperanto konuşan ekip için dil paketi gerekiyor", r.Reason, "the requester's words, Turkish characters intact")
	require.NotNil(t, r.Created)
	assert.True(t, *r.Created)
	assert.Contains(t, r.Message, "administrator", "the answer says who decides")
	assert.Empty(t, f.installedApps(t), "a request installs nothing")

	status, again := f.appRequest(t)
	require.Equal(t, http.StatusOK, status, "the same source again: the pending request, not a second one")
	assert.Equal(t, r.ID, again.ID)
	require.NotNil(t, again.Created)
	assert.False(t, *again.Created)

	status, raw := withToken(t, http.MethodGet, f.url("/api/admin/plugin-requests"), f.token, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var list struct {
		Requests []requestWire `json:"requests"`
	}
	require.NoError(t, json.Unmarshal(raw, &list))
	require.Len(t, list.Requests, 1, "one request, however often it was asked for")

	status, raw = withToken(t, http.MethodGet, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d", r.ID)), f.token, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"manifest"`, "the detail carries the frozen manifest")

	creates := f.auditActions(t, "plugin_request.create")
	require.Len(t, creates, 1, "the second ask wrote no row")
	assert.Equal(t, fmt.Sprint(r.ID), creates[0].TargetID)
	assert.Equal(t, "testutil", creates[0].Metadata["token_label"])
	require.NotNil(t, creates[0].UserID)
	assert.Equal(t, f.adminID, *creates[0].UserID)

	rows, _, err := f.notif.List(context.Background(), &f.adminID, notify.AdminBell, false, 50, 0)
	require.NoError(t, err)
	n := 0
	for _, row := range rows {
		if row.Event == string(notify.EventPluginRequested) {
			n++
		}
	}
	assert.Equal(t, 1, n, "the administrators are told once")
}

// A request says why: one without a reason is refused.
func TestPluginRequests_NeedAReason(t *testing.T) {
	f := newPRFix(t)
	status, raw := withToken(t, http.MethodPost, f.url("/api/admin/plugin-requests"), f.token,
		map[string]any{"kind": "app", "manifest_url": f.src.manifestURL()})
	assert.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Contains(t, string(raw), "reason_required")
}

// Approval installs the bytes the request froze, with the permissions it
// froze — and only a signed-in administrator may give it.
func TestPluginRequests_ApprovalInstallsTheFrozenBytes(t *testing.T) {
	f := newPRFix(t)
	status, r := f.appRequest(t)
	require.Equal(t, http.StatusCreated, status, r.Message)
	approve := f.url(fmt.Sprintf("/api/admin/plugin-requests/%d/approve", r.ID))

	status, raw := withToken(t, http.MethodPost, approve, f.token, map[string]any{})
	assert.Equal(t, http.StatusForbidden, status, "a key cannot approve — not even its own request: %s", raw)
	assert.Contains(t, string(raw), "session_required")
	assert.Empty(t, f.installedApps(t))

	status, raw = doReq(t, f.admin, http.MethodPost, approve, map[string]any{})
	require.Equal(t, http.StatusOK, status, string(raw))
	got := decodeRequest(t, raw)
	assert.Equal(t, "approved", got.Status)

	apps := f.installedApps(t)
	require.Len(t, apps, 1)
	assert.Equal(t, "lang-eo", apps[0]["name"])
	assert.Equal(t, r.SHA256, apps[0]["sha256"], "exactly the bytes the request froze")

	approvals := f.auditActions(t, "plugin_request.approve")
	require.Len(t, approvals, 1)
	require.NotNil(t, approvals[0].UserID)
	assert.Equal(t, f.adminID, *approvals[0].UserID)

	status, raw = doReq(t, f.admin, http.MethodPost, approve, map[string]any{})
	assert.Equal(t, http.StatusConflict, status, "an approved request is not approved twice: %s", raw)
	assert.Contains(t, string(raw), "not_pending")
}

// The source changed between the request and its approval: nothing is
// installed, the request is superseded, and a new request freezes the new
// bytes.
func TestPluginRequests_ASourceThatChangedSupersedesTheRequest(t *testing.T) {
	f := newPRFix(t)
	status, r := f.appRequest(t)
	require.Equal(t, http.StatusCreated, status, r.Message)

	f.src.set(func(s *source) { s.manifest = packManifest("1.0.1") })

	status, raw := doReq(t, f.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d/approve", r.ID)), map[string]any{})
	require.Equal(t, http.StatusConflict, status, string(raw))
	assert.Contains(t, string(raw), "superseded")
	got := decodeRequest(t, raw)
	assert.Equal(t, "superseded", got.Status)
	assert.NotEmpty(t, got.Note, "the request says why")
	assert.Empty(t, f.installedApps(t), "nothing the administrator did not see was installed")

	sup := f.auditActions(t, "plugin_request.supersede")
	require.Len(t, sup, 1)
	assert.NotEmpty(t, sup[0].Metadata["why"])

	status, again := f.appRequest(t)
	require.Equal(t, http.StatusCreated, status, "a closed request no longer answers for its source: %s", again.Message)
	assert.NotEqual(t, r.ID, again.ID)
	assert.Equal(t, "1.0.1", again.Version)
}

// Rejecting installs nothing and keeps the administrator's reason.
func TestPluginRequests_RejectionInstallsNothing(t *testing.T) {
	f := newPRFix(t)
	status, r := f.appRequest(t)
	require.Equal(t, http.StatusCreated, status, r.Message)
	reject := f.url(fmt.Sprintf("/api/admin/plugin-requests/%d/reject", r.ID))

	status, raw := withToken(t, http.MethodPost, reject, f.token, map[string]any{"reason": "no"})
	assert.Equal(t, http.StatusForbidden, status, "a key cannot reject either: %s", raw)

	status, raw = doReq(t, f.admin, http.MethodPost, reject, map[string]any{"reason": "Şimdilik gerek yok"})
	require.Equal(t, http.StatusOK, status, string(raw))
	got := decodeRequest(t, raw)
	assert.Equal(t, "rejected", got.Status)
	assert.Equal(t, "Şimdilik gerek yok", got.Note)
	assert.Empty(t, f.installedApps(t))

	rej := f.auditActions(t, "plugin_request.reject")
	require.Len(t, rej, 1)
	assert.Equal(t, "Şimdilik gerek yok", rej[0].Metadata["note"])

	status, raw = doReq(t, f.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d/approve", r.ID)), map[string]any{})
	assert.Equal(t, http.StatusConflict, status, "a rejected request cannot be approved: %s", raw)
}

// A request nobody decides expires after its time (14 days by default), is
// audited once, and can no longer be approved.
func TestPluginRequests_Expire(t *testing.T) {
	f := newPRFix(t)
	status, r := f.appRequest(t)
	require.Equal(t, http.StatusCreated, status, r.Message)

	f.clock.add(13 * 24 * time.Hour)
	status, raw := withToken(t, http.MethodGet, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d", r.ID)), f.token, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Equal(t, "pending", decodeRequest(t, raw).Status, "13 days: still waiting")

	f.clock.add(2 * 24 * time.Hour)
	status, raw = withToken(t, http.MethodGet, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d", r.ID)), f.token, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Equal(t, "expired", decodeRequest(t, raw).Status, "15 days: expired")

	status, raw = doReq(t, f.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d/approve", r.ID)), map[string]any{})
	assert.Equal(t, http.StatusConflict, status, "an expired request cannot be approved: %s", raw)
	assert.Empty(t, f.installedApps(t))

	// Read again: still one expiry row.
	_, _ = withToken(t, http.MethodGet, f.url("/api/admin/plugin-requests"), f.token, nil)
	exp := f.auditActions(t, "plugin_request.expire")
	require.Len(t, exp, 1)
	assert.Nil(t, exp[0].UserID, "nobody expired it")
}

// A storage plugin request freezes the build its source publishes; a source
// that publishes another build by the approval supersedes it — before
// anything is downloaded, let alone run.
func TestPluginRequests_StoragePluginFromItsSource(t *testing.T) {
	f := newPRFix(t)
	f.src.set(func(s *source) { s.feed = s.feedFor("1.0.0", s.binary) })

	status, raw := withToken(t, http.MethodPost, f.url("/api/admin/plugin-requests"), f.token, map[string]any{
		"kind": "storage", "name": "myfs", "source": f.src.feedURL(), "reason": "S3 dışı arşiv için",
	})
	require.Equal(t, http.StatusCreated, status, string(raw))
	r := decodeRequest(t, raw)
	sum := sha256.Sum256([]byte("a build"))
	assert.Equal(t, hex.EncodeToString(sum[:]), r.SHA256)
	assert.Equal(t, "1.0.0", r.Version)
	assert.Empty(t, r.Permissions, "a storage plugin has no permission list")

	f.src.set(func(s *source) { s.binary = []byte("another build"); s.feed = s.feedFor("1.0.1", s.binary) })
	status, raw = doReq(t, f.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d/approve", r.ID)), map[string]any{})
	require.Equal(t, http.StatusConflict, status, string(raw))
	assert.Equal(t, "superseded", decodeRequest(t, raw).Status)
	_, err := f.store.GetPluginByName(context.Background(), "myfs")
	assert.Error(t, err, "nothing was installed")
}

// A storage plugin's upgrade request, approved: the plugin moves to exactly
// the build that was frozen. (The plugin is off, so the upgrade only puts the
// file in place — no binary has to run in a unit test.)
func TestPluginRequests_StoragePluginUpgradeApproved(t *testing.T) {
	f := newPRFix(t)
	ctx := context.Background()
	f.src.set(func(s *source) { s.feed = s.feedFor("1.1.0", s.binary) })
	row, err := f.store.CreatePlugin(ctx, &model.Plugin{Name: "myfs", Kind: model.PluginKindBinary, Binary: "myfs",
		SHA256: strings.Repeat("a", 64), Version: "1.0.0", Driver: "myfs", Source: f.src.feedURL()})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(f.mgrDir, "myfs"), 0o755))
	require.NoError(t, f.mgr.Load(ctx))

	status, raw := withToken(t, http.MethodPost, f.url("/api/admin/plugin-requests"), f.token, map[string]any{
		"kind": "storage", "op": "upgrade", "name": "myfs", "reason": "hata düzeltmesi",
	})
	require.Equal(t, http.StatusCreated, status, string(raw))
	r := decodeRequest(t, raw)
	assert.Equal(t, "1.1.0", r.Version)
	after, err := f.store.GetPlugin(ctx, row.ID)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("a", 64), after.SHA256, "a request upgrades nothing")

	status, raw = doReq(t, f.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d/approve", r.ID)), map[string]any{})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Equal(t, "approved", decodeRequest(t, raw).Status)
	after, err = f.store.GetPlugin(ctx, row.ID)
	require.NoError(t, err)
	assert.Equal(t, r.SHA256, after.SHA256, "the frozen build is what was put in place")
}

// A storage plugin by address: the request hashes what the address serves;
// bytes that changed by the approval are refused before they are run.
func TestPluginRequests_StoragePluginByAddress(t *testing.T) {
	f := newPRFix(t)
	status, raw := withToken(t, http.MethodPost, f.url("/api/admin/plugin-requests"), f.token, map[string]any{
		"kind": "storage", "name": "myfs", "url": f.src.tls.URL + "/myfs", "sha256": strings.Repeat("0", 64), "reason": "x",
	})
	assert.Equal(t, http.StatusBadRequest, status, "a pin the address does not serve is refused at once: %s", raw)
	assert.Contains(t, string(raw), "sha256_mismatch")

	status, raw = withToken(t, http.MethodPost, f.url("/api/admin/plugin-requests"), f.token, map[string]any{
		"kind": "storage", "name": "myfs", "url": f.src.tls.URL + "/myfs", "reason": "x",
	})
	require.Equal(t, http.StatusCreated, status, string(raw))
	r := decodeRequest(t, raw)
	sum := sha256.Sum256([]byte("a build"))
	assert.Equal(t, hex.EncodeToString(sum[:]), r.SHA256, "the server hashed what the address served")

	f.src.set(func(s *source) { s.binary = []byte("swapped") })
	status, raw = doReq(t, f.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d/approve", r.ID)), map[string]any{})
	require.Equal(t, http.StatusConflict, status, string(raw))
	assert.Equal(t, "superseded", decodeRequest(t, raw).Status)
	_, err := f.store.GetPluginByName(context.Background(), "myfs")
	assert.Error(t, err, "nothing was installed")
}

// ── Multi-tenant ───────────────────────────────────────────────────────

// Plugins are the platform operator's: a tenant administrator can neither
// leave a request, read them, nor decide one — session or key.
func TestPluginRequests_TenantAdminIsRefused(t *testing.T) {
	f := newMTFix(t, true)
	useProductionAuthChain(t, f.Store)
	adminA, err := f.Store.GetUserByEmail(context.Background(), "admin@alpha.test")
	require.NoError(t, err)
	tok := testutil.NewAPIToken(t, f.Store, adminA.ID, "admin")

	body := map[string]any{"kind": "app", "manifest_url": "http://127.0.0.1:1/filex-app.json", "reason": "x"}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/admin/plugin-requests"},
		{http.MethodGet, "/api/admin/plugin-requests"},
		{http.MethodGet, "/api/admin/plugin-requests/1"},
		{http.MethodPost, "/api/admin/plugin-requests/1/approve"},
		{http.MethodPost, "/api/admin/plugin-requests/1/reject"},
	} {
		status, raw := doReq(t, f.AdminA, c.method, f.URL+c.path, body)
		assert.Equal(t, http.StatusForbidden, status, "tenant admin session %s %s: %s", c.method, c.path, raw)
		assert.Contains(t, string(raw), "supertenant_only")
		status, raw = withToken(t, c.method, f.URL+c.path, tok, body)
		assert.Equal(t, http.StatusForbidden, status, "tenant admin key %s %s: %s", c.method, c.path, raw)
	}
}

// ── MCP ────────────────────────────────────────────────────────────────

// The admin MCP catalogue reads plugins and leaves requests; it has NO tool
// that installs, changes, approves or rejects anything.
func TestPluginRequests_MCPCatalogueHasNoApproval(t *testing.T) {
	f := newPRFix(t)
	code, body := mcpPost(t, &http.Client{}, f.url("/api/ai/mcp"), f.token, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	require.Equal(t, http.StatusOK, code, body)
	names := toolNames(t, body)

	want := []string{
		"admin_plugins_list", "admin_plugin_get", "admin_plugins_check_updates",
		"admin_app_plugins_list", "admin_app_plugin_get", "admin_app_plugin_logs", "admin_app_plugins_check_updates",
		"admin_plugin_request_install", "admin_plugin_request_upgrade",
		"admin_plugin_requests_list", "admin_plugin_request_get",
	}
	for _, n := range want {
		assert.Contains(t, names, n)
	}
	allowed := map[string]bool{}
	for _, n := range want {
		allowed[n] = true
	}
	for n := range names {
		assert.NotContains(t, n, "approve", "no approval tool")
		assert.NotContains(t, n, "reject", "no rejection tool")
		if strings.HasPrefix(n, "admin_plugin") || strings.HasPrefix(n, "admin_app_plugin") {
			assert.True(t, allowed[n], "%s: every plugin tool reads or requests, nothing else", n)
		}
	}
	assert.Contains(t, names["admin_plugin_request_install"], "administrator", "the tool tells the agent who decides")
}

// Calling the request tool leaves a pending request and says it waits.
func TestPluginRequests_MCPToolLeavesARequest(t *testing.T) {
	f := newPRFix(t)
	payload := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"admin_plugin_request_install",`+
		`"arguments":{"kind":"app","manifest_url":%q,"reason":"ekip için"}}}`, f.src.manifestURL())
	code, body := mcpPost(t, &http.Client{}, f.url("/api/ai/mcp"), f.token, payload)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, `"isError":true`, body)
	assert.Contains(t, body, "pending")
	assert.Contains(t, body, "admin panel")
	assert.Empty(t, f.installedApps(t), "the tool installed nothing")
	rows, err := f.store.ListPluginRequests(context.Background(), "pending", 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "testutil", rows[0].TokenLabel)
}

// toolNames reads tools/list: name → description.
func toolNames(t *testing.T, body string) map[string]string {
	t.Helper()
	// The streamable transport may answer as an SSE frame.
	if i := strings.Index(body, "{"); i > 0 {
		body = body[i:]
	}
	if i := strings.LastIndex(body, "}"); i >= 0 {
		body = body[:i+1]
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &resp), body)
	out := map[string]string{}
	for _, tool := range resp.Result.Tools {
		out[tool.Name] = tool.Description
	}
	require.NotEmpty(t, out)
	return out
}
