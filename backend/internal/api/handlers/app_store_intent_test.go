package handlers_test

// Installing an app from a store's magic link, and the licenses of paid apps
// (handlers/app_store_intent.go, internal/appstore), through the production
// router: the session gate, the origin guard, the trust question, the
// signed link, the pins held against the repository twice (at the review and
// at the install), the store told how it ended, a paid app held until its
// license holds - and the key in no answer.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/appstore/storetest"
	"github.com/brf-tech/filex/backend/internal/auth"
	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/netguard"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/tenantstore"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// fakeGitHub serves repositories' files at <base>/<owner>/<name>/<ref>/<path>
// (Options.GitHubRawBase), changeable mid-test.
type fakeGitHub struct {
	mu    sync.Mutex
	files map[string][]byte
	srv   *httptest.Server
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	g := &fakeGitHub{files: map[string][]byte{}}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		b, ok := g.files[r.URL.Path]
		g.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGitHub) put(path string, b []byte) {
	g.mu.Lock()
	g.files[path] = b
	g.mu.Unlock()
}

// storePack is the language pack the store sells: no module, so the test
// needs no wasm build. version + a word make distinct bytes.
func storePack(version, word string) []byte {
	return []byte(`{"manifest_version":1,"name":"lang-eo","version":"` + version + `","label":{"en":"Esperanto","tr":"Esperanto dili"},` +
		`"languages":["en","tr"],"permissions":[],"ui_locales":{"eo":{"common.cancel":"` + word + `"}}}`)
}

func shaHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

type asFix struct {
	srv     *httptest.Server
	admin   *http.Client
	store   db.Store
	reg     *wasmplugin.Registry
	svc     *appstore.Service
	st      *storetest.Store
	gh      *fakeGitHub
	adminID int64
	key     string // an admin-scoped API key of the same administrator
	rootKey string // an admin-scoped, root:-confined key
	appKey  string // an app token on the administrator
}

func newAsFix(t *testing.T) *asFix {
	t.Helper()
	return newAsFixWith(t, nil)
}

// newAsFixWith is newAsFix with the configuration changed before the router
// is built (mut nil: as newAsFix).
func newAsFixWith(t *testing.T, mut func(cfg *config.Config)) *asFix {
	t.Helper()
	return newAsFixDeps(t, mut, nil)
}

// newAsFixDeps is newAsFixWith with the router's dependencies changed too,
// over the fixture's store (withDeps nil: as newAsFixWith) - a storage plugin
// manager for the storage plugins' store links (#215).
func newAsFixDeps(t *testing.T, mut func(cfg *config.Config), withDeps func(d *api.Deps, store db.Store)) *asFix {
	t.Helper()
	ctx := context.Background()
	_, raw := testutil.NewTestDB(t)
	accounting := quotastore.New(raw)
	var store db.Store = identitystore.New(accounting)
	localDrv := authlocal.New(store)
	require.NoError(t, localDrv.Init(ctx, nil))
	auth.SetEnabled([]auth.Driver{localDrv})
	useProductionAuthChain(t, store)

	gh := newFakeGitHub(t)
	st := storetest.New()
	t.Cleanup(st.Close)
	st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)
	st.AddKey("lic-1", appstore.UseLicense, appstore.KeyActive)

	cfg := config.Default()
	cfg.PublicURL = "http://test.local"
	cfg.PublicURLSet = true
	if mut != nil {
		mut(&cfg)
	}
	resolver := func(id int64) (storage.Driver, error) { return nil, fmt.Errorf("unknown storage %d", id) }
	reg, err := wasmplugin.New(wasmplugin.Options{
		Store: store, Dir: filepath.Join(t.TempDir(), "app-plugins"),
		SecretKey: "0123456789abcdef0123456789abcdef", StorageResolver: resolver,
		LoopbackSources: true, GitHubRawBase: gh.srv.URL,
	})
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(context.Background()) })
	box, err := secretbox.New("0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	svc := appstore.New(appstore.Options{
		Store: store, Client: appstore.NewClient(netguard.Policy{Loopback: true}, "filex-test"), Box: box,
		Loopback: true, Holder: reg, FilexVersion: "0.52.0",
	})
	svc.Start(ctx)

	deps := &api.Deps{
		Cfg: cfg, Store: tenantstore.New(store), Quota: accounting.Quota(),
		Worker: syncpkg.New(store), Caps: capability.New(store), Share: share.NewService(store),
		StorageResolver: resolver, AppPlugins: reg, AppStore: svc, LocalAuth: localDrv,
	}
	if withDeps != nil {
		withDeps(deps, store)
	}
	srv := httptest.NewServer(api.BuildRouter(deps))
	t.Cleanup(srv.Close)

	client := freshClient(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	admin, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)
	f := &asFix{srv: srv, admin: client, store: store, reg: reg, svc: svc, st: st, gh: gh, adminID: admin.ID}
	f.key = testutil.NewAPIToken(t, store, admin.ID, "read,write,delete,mcp,admin")
	f.rootKey = testutil.NewAPIToken(t, store, admin.ID, "admin,root:main://projects")
	f.appKey = appToken(t, store, admin.ID)
	return f
}

func appToken(t *testing.T, store db.Store, uid int64) string {
	t.Helper()
	plain := "tok_app_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	_, err := store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID: uid, Label: "an embed", TokenHash: apitoken.HashToken(plain), Scopes: "read,write,delete,mcp,admin", Kind: model.TokenKindApp,
	})
	require.NoError(t, err)
	return plain
}

// call sends one JSON request with the administrator's session, or with a
// token (no cookie) when token != "". hdr adds headers.
func (f *asFix) call(t *testing.T, token, method, path string, body any, hdr ...string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	c := f.admin
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		c = &http.Client{}
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// publish puts a version of the pack in the fake repository (at its tag and
// at its commit) and a signed link for it at the store.
func (f *asFix) publish(token, version, word string, mut func(p map[string]any)) []byte {
	m := storePack(version, word)
	c := f.release("Owner/lang-eo", "v"+version, m)
	p := f.st.IntentPayload("tid-"+token, "lang-eo", version, "Owner/lang-eo", "v"+version, time.Now().Add(30*time.Minute))
	p["kind"] = "language_pack"
	p["manifest_sha256"] = shaHex(m)
	p["commit"] = c
	if mut != nil {
		mut(p)
	}
	f.st.PutIntent(token, &storetest.IntentEntry{Payload: p, KeyID: "idx-1"})
	return m
}

func (f *asFix) trust(t *testing.T) {
	t.Helper()
	code, body := f.call(t, "", http.MethodPost, "/api/admin/app-plugins/stores",
		map[string]any{"store": f.st.Origin(), "fingerprints": f.st.Fingerprints()})
	require.Equal(t, http.StatusOK, code, "trust: %s", body)
}

type reviewBody struct {
	Handle string `json:"handle"`
	Store  string `json:"store"`
	Intent struct {
		App              string `json:"app"`
		Version          string `json:"version"`
		Paid             bool   `json:"paid"`
		LicenseKeyPrefix string `json:"license_key_prefix"`
	} `json:"intent"`
	Review struct {
		Kind        string `json:"kind"`
		Permissions []struct {
			ID string `json:"id"`
		} `json:"permissions"`
		ManifestSHA256 string `json:"manifest_sha256"`
	} `json:"review"`
	UpgradeOf *struct {
		Version string `json:"version"`
	} `json:"upgrade_of"`
}

func (f *asFix) review(t *testing.T, token string) (int, reviewBody, []byte) {
	t.Helper()
	code, body := f.call(t, "", http.MethodPost, "/api/admin/app-plugins/store-intent", map[string]any{"store": f.st.Origin(), "token": token})
	var rb reviewBody
	_ = json.Unmarshal(body, &rb)
	return code, rb, body
}

func (f *asFix) install(t *testing.T, rb reviewBody, extra map[string]any) (int, []byte) {
	t.Helper()
	perms := []string{}
	for _, p := range rb.Review.Permissions {
		perms = append(perms, p.ID)
	}
	body := map[string]any{"handle": rb.Handle, "permissions": perms}
	for k, v := range extra {
		body[k] = v
	}
	return f.call(t, "", http.MethodPost, "/api/admin/app-plugins/store-intent/install", body)
}

func errCode(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	return e.Error
}

// ── Who may ────────────────────────────────────────────────────────────

func TestStoreInstall_NoKeyOfAnyKindReachesTheStoreRoutes(t *testing.T) {
	f := newAsFix(t)
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/admin/app-plugins/stores"},
		{http.MethodPost, "/api/admin/app-plugins/stores"},
		{http.MethodDelete, "/api/admin/app-plugins/stores?store=" + f.st.Origin()},
		{http.MethodPost, "/api/admin/app-plugins/store-intent"},
		{http.MethodPost, "/api/admin/app-plugins/store-intent/install"},
		{http.MethodPost, "/api/admin/app-plugins/store-intent/cancel"},
		{http.MethodGet, "/api/admin/app-plugins/licenses"},
		{http.MethodGet, "/api/admin/app-plugins/1/license"},
		{http.MethodPut, "/api/admin/app-plugins/1/license"},
		{http.MethodPost, "/api/admin/app-plugins/1/license/verify"},
	}
	for name, tok := range map[string]string{"admin-scoped key": f.key, "root:-confined admin key": f.rootKey, "app token": f.appKey} {
		for _, r := range routes {
			code, body := f.call(t, tok, r.method, r.path, map[string]any{"store": f.st.Origin(), "fingerprints": f.st.Fingerprints(), "token": "tokentoken"})
			assert.Equal(t, http.StatusForbidden, code, "%s %s %s: %s", name, r.method, r.path, body)
		}
	}
	assert.Equal(t, "", f.svc.TrustStatus(context.Background(), f.st.Origin()), "no key may have trusted the store")
	assert.Equal(t, 0, f.st.KeyReads()+f.st.IntentReads(), "no key may have made filex ask the store anything")
}

// The trust is its own POST, through the origin guard: a page elsewhere
// cannot trust a store with the administrator's cookie, and no GET changes
// anything.
func TestStoreInstall_TrustIsNotACrossSiteRequest(t *testing.T) {
	f := newAsFix(t)
	code, body := f.call(t, "", http.MethodPost, "/api/admin/app-plugins/stores",
		map[string]any{"store": f.st.Origin(), "fingerprints": f.st.Fingerprints()},
		"Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site")
	assert.Equal(t, http.StatusForbidden, code, "%s", body)
	assert.Equal(t, "cross_origin_refused", errCode(body))
	code, body = f.call(t, "", http.MethodPost, "/api/admin/app-plugins/stores",
		map[string]any{"store": f.st.Origin(), "fingerprints": f.st.Fingerprints()},
		"Origin", "https://files.test.local", "Sec-Fetch-Site", "same-site")
	assert.Equal(t, http.StatusForbidden, code, "a sibling host: %s", body)
	code, _ = f.call(t, "", http.MethodGet, "/api/admin/app-plugins/stores?store="+f.st.Origin()+"&fingerprints="+strings.Join(f.st.Fingerprints(), ","), nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "", f.svc.TrustStatus(context.Background(), f.st.Origin()), "nothing trusted the store")
	// Fingerprints the administrator was not shown are not accepted.
	code, body = f.call(t, "", http.MethodPost, "/api/admin/app-plugins/stores", map[string]any{"store": f.st.Origin(), "fingerprints": []string{"index:idx-1:00"}})
	assert.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodeKeyChanged, errCode(body))
}

// ── The flow ───────────────────────────────────────────────────────────

func TestStoreInstall_TrustReviewInstallComplete(t *testing.T) {
	f := newAsFix(t)
	f.publish("tokentoken-1", "1.0.0", "Nuligi", nil)

	code, _, body := f.review(t, "tokentoken-1")
	require.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodeTrustRequired, errCode(body))
	assert.Contains(t, string(body), "fingerprints")
	assert.Equal(t, 0, f.st.IntentReads(), "an untrusted store's link is not fetched")

	f.trust(t)
	code, rb, body := f.review(t, "tokentoken-1")
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.Equal(t, "lang-eo", rb.Intent.App)
	assert.Equal(t, wasmplugin.KindLanguagePack, rb.Review.Kind)
	assert.NotEmpty(t, rb.Handle)
	_, ok := f.reg.ByName("lang-eo")
	assert.False(t, ok, "a review installs nothing")

	code, body = f.install(t, rb, nil)
	require.Equal(t, http.StatusCreated, code, "%s", body)
	p, ok := f.reg.ByName("lang-eo")
	require.True(t, ok)
	st, _ := p.State()
	assert.Equal(t, wasmplugin.StateRunning, st)
	c := f.st.Completions()
	require.Len(t, c, 1)
	assert.Equal(t, "installed", c[0].Result)
	assert.True(t, strings.HasPrefix(c[0].InstanceID, "fx-"))

	// The same link again: used here, whatever the store says.
	code, _, body = f.review(t, "tokentoken-1")
	assert.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodeIntentUsed, errCode(body))

	rows, err := f.store.ListAuditRecent(context.Background(), 100)
	require.NoError(t, err)
	acts := []string{}
	for _, r := range rows {
		acts = append(acts, r.Action)
	}
	assert.Contains(t, acts, "app_store.trust")
	assert.Contains(t, acts, "app_store.install")
	assert.NotContains(t, acts, "app-plugins.create", "the store rows are the record; no generic row beside them")
}

func TestStoreInstall_WhatTheRepositoryServesIsWhatTheStorePinned(t *testing.T) {
	f := newAsFix(t)
	f.trust(t)

	// At the review: the commit the store signed now serves other bytes.
	m := f.publish("tokentoken-1", "1.0.0", "Nuligi", nil)
	f.gh.put("/Owner/lang-eo/"+commitOf(m)+"/filex-app.json", storePack("1.0.0", "Changed"))
	f.gh.put("/Owner/lang-eo/v1.0.0/filex-app.json", storePack("1.0.0", "Changed"))
	code, _, body := f.review(t, "tokentoken-1")
	assert.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodePinMismatch, errCode(body))
	assert.Contains(t, string(body), "manifest_sha256")

	// Between the review and the install: the review passed, the install
	// reads again and refuses.
	m = f.publish("tokentoken-2", "1.0.0", "Nuligi", nil)
	code, rb, body := f.review(t, "tokentoken-2")
	require.Equal(t, http.StatusOK, code, "%s", body)
	f.gh.put("/Owner/lang-eo/"+commitOf(m)+"/filex-app.json", storePack("1.0.0", "Swapped"))
	f.gh.put("/Owner/lang-eo/v1.0.0/filex-app.json", storePack("1.0.0", "Swapped"))
	code, body = f.install(t, rb, nil)
	assert.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodePinMismatch, errCode(body))
	_, ok := f.reg.ByName("lang-eo")
	assert.False(t, ok, "nothing installed")

	// A tampered link, and one the store says is gone.
	f.publish("tokentoken-3", "1.0.0", "Nuligi", nil)
	f.st.Intent("tokentoken-3").Tamper = true
	code, _, body = f.review(t, "tokentoken-3")
	assert.Equal(t, http.StatusBadGateway, code, "%s", body)
	assert.Equal(t, appstore.CodeSignature, errCode(body))
	f.publish("tokentoken-4", "1.0.0", "Nuligi", nil)
	f.st.Intent("tokentoken-4").Status = http.StatusGone
	code, _, body = f.review(t, "tokentoken-4")
	assert.Equal(t, http.StatusGone, code, "%s", body)
	assert.Equal(t, appstore.CodeIntentGone, errCode(body))
}

// A store link installs or upgrades, never goes back: an old link replayed
// after a newer version is installed is refused.
func TestStoreInstall_UpgradesNeverGoesBack(t *testing.T) {
	f := newAsFix(t)
	f.trust(t)
	f.publish("tokentoken-old", "1.0.0", "Nuligi", nil) // an old link, kept
	f.publish("tokentoken-1", "1.1.0", "Nuligi", nil)
	_, rb, _ := f.review(t, "tokentoken-1")
	code, body := f.install(t, rb, nil)
	require.Equal(t, http.StatusCreated, code, "%s", body)

	code, _, body = f.review(t, "tokentoken-old")
	assert.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodeVersionRollback, errCode(body))

	f.publish("tokentoken-2", "1.2.0", "Nuligi", nil)
	code, rb, body = f.review(t, "tokentoken-2")
	require.Equal(t, http.StatusOK, code, "%s", body)
	require.NotNil(t, rb.UpgradeOf)
	assert.Equal(t, "1.1.0", rb.UpgradeOf.Version)
	code, body = f.install(t, rb, nil)
	require.Equal(t, http.StatusCreated, code, "%s", body)
	p, _ := f.reg.ByName("lang-eo")
	assert.Equal(t, "1.2.0", p.Row.Version)
}

func TestStoreInstall_CancelTellsTheStore(t *testing.T) {
	f := newAsFix(t)
	f.trust(t)
	f.publish("tokentoken-1", "1.0.0", "Nuligi", nil)
	_, rb, _ := f.review(t, "tokentoken-1")
	code, body := f.call(t, "", http.MethodPost, "/api/admin/app-plugins/store-intent/cancel", map[string]any{"handle": rb.Handle})
	require.Equal(t, http.StatusNoContent, code, "%s", body)
	c := f.st.Completions()
	require.Len(t, c, 1)
	assert.Equal(t, "cancelled", c[0].Result)
	code, body = f.install(t, rb, nil)
	assert.Equal(t, http.StatusNotFound, code, "a cancelled review installs nothing: %s", body)
}

// ── Paid apps ──────────────────────────────────────────────────────────

const storeLicKey = "FXL-7Q2M-K9P4-ZZ31"

func TestStoreInstall_APaidAppRunsWhileItsLicenseHolds(t *testing.T) {
	ctx := context.Background()
	f := newAsFix(t)
	f.trust(t)
	result := appstore.ResultValid
	var mu sync.Mutex
	f.st.License = func(req appstore.LicenseRequest) (map[string]any, string) {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now().UTC()
		return map[string]any{"result": result, "app": req.App, "licensee": "Acme Ltd.", "instance_id": req.InstanceID,
			"checked_at": now.Format(time.RFC3339Nano), "next_check_by": now.Add(24 * time.Hour).Format(time.RFC3339),
			"grace_until": now.Add(7 * 24 * time.Hour).Format(time.RFC3339)}, ""
	}
	f.publish("tokentoken-1", "1.0.0", "Nuligi", func(p map[string]any) {
		p["paid"] = true
		p["license_key"] = storeLicKey
	})
	code, rb, body := f.review(t, "tokentoken-1")
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.True(t, rb.Intent.Paid)
	assert.Equal(t, appstore.Prefix(storeLicKey), rb.Intent.LicenseKeyPrefix)
	assert.NotContains(t, string(body), storeLicKey, "the review shows the key's prefix only")

	code, body = f.install(t, rb, nil)
	require.Equal(t, http.StatusCreated, code, "%s", body)
	assert.NotContains(t, string(body), storeLicKey)
	p, _ := f.reg.ByName("lang-eo")
	st, _ := p.State()
	assert.Equal(t, wasmplugin.StateRunning, st, "valid license: the app runs")
	v := f.st.Verifies()
	require.NotEmpty(t, v)
	assert.Equal(t, storeLicKey, v[0].Key, "the key the link carried was sent to the store")

	id := strconv.FormatInt(p.Row.ID, 10)
	code, body = f.call(t, "", http.MethodGet, "/api/admin/app-plugins/"+id+"/license", nil)
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.NotContains(t, string(body), storeLicKey)
	assert.Contains(t, string(body), `"status":"valid"`)

	// The store revokes it: the next check holds the app, removes nothing.
	mu.Lock()
	result = appstore.ResultRevoked
	mu.Unlock()
	code, body = f.call(t, "", http.MethodPost, "/api/admin/app-plugins/"+id+"/license/verify", nil)
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.Contains(t, string(body), `"status":"revoked"`)
	p, ok := f.reg.ByName("lang-eo")
	require.True(t, ok, "a revoked license removes nothing")
	st, serr := p.State()
	assert.Equal(t, wasmplugin.StateUnlicensed, st, serr)
	code, body = f.call(t, "", http.MethodGet, "/api/admin/app-plugins/"+id, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, string(body), `"state":"unlicensed"`)

	// The app reads its own status, never the key.
	code, body = f.call(t, "", http.MethodGet, "/api/files/plugins/license/lang-eo", nil)
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.JSONEq(t, `{"status":"revoked"}`, string(body))

	// A new key, checked at once.
	mu.Lock()
	result = appstore.ResultValid
	mu.Unlock()
	code, body = f.call(t, "", http.MethodPut, "/api/admin/app-plugins/"+id+"/license", map[string]any{"key": "FXL-NEWK-EY00-0001"})
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.NotContains(t, string(body), "FXL-NEWK-EY00-0001")
	v = f.st.Verifies()
	assert.Equal(t, "FXL-NEWK-EY00-0001", v[len(v)-1].Key)
	p, _ = f.reg.ByName("lang-eo")
	st, _ = p.State()
	assert.Equal(t, wasmplugin.StateRunning, st)

	// Nowhere in the audit log.
	rows, err := f.store.ListAuditRecent(ctx, 200)
	require.NoError(t, err)
	raw, _ := json.Marshal(rows)
	assert.NotContains(t, string(raw), storeLicKey)
	assert.NotContains(t, string(raw), "FXL-NEWK-EY00-0001")
	assert.Contains(t, string(raw), "app_plugin.license_held")

	// Removing the app forgets its license.
	code, _ = f.call(t, "", http.MethodDelete, "/api/admin/app-plugins/"+id, nil)
	require.Equal(t, http.StatusNoContent, code)
	lic, _ := f.svc.LicenseOf(ctx, "lang-eo")
	assert.Nil(t, lic)
}

// A paid link without a key: installed, held, and said so.
func TestStoreInstall_APaidAppWithoutAKeyIsHeld(t *testing.T) {
	f := newAsFix(t)
	f.trust(t)
	f.publish("tokentoken-1", "1.0.0", "Nuligi", func(p map[string]any) { p["paid"] = true })
	_, rb, _ := f.review(t, "tokentoken-1")
	code, body := f.install(t, rb, nil)
	require.Equal(t, http.StatusCreated, code, "%s", body)
	assert.Contains(t, string(body), `"status":"missing"`)
	p, _ := f.reg.ByName("lang-eo")
	st, _ := p.State()
	assert.Equal(t, wasmplugin.StateUnlicensed, st)
}
