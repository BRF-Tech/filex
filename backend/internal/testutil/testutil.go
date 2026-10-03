// Package testutil provides shared helpers for unit + integration tests
// across the filex codebase.
//
// All helpers are designed for *testing.T-driven setups: they call
// t.Helper(), t.Cleanup() (instead of returning a manual close fn) and
// t.Fatalf on failure. They also gate themselves on testing.Short() where
// network or heavy disk IO is involved, so `go test -short ./...` stays
// fast.
package testutil

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/tenantstore"

	// Register drivers via init() blocks.
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// dbCounter ensures every NewTestDB call gets a unique DSN — modernc/sqlite
// shares one in-memory cache per `:memory:` connection, but each unique DSN
// produces a fresh isolated DB even within the same process. We use a shared
// cache with a per-test name so tests don't pollute one another.
var dbCounter struct {
	sync.Mutex
	n int
}

// NewTestDB opens an in-memory SQLite DB, runs all migrations, and returns
// the underlying *sql.DB plus a typed Store. Cleanup is registered via
// t.Cleanup so tests don't have to remember to close.
func NewTestDB(t *testing.T) (*sql.DB, db.Store) {
	t.Helper()

	dbCounter.Lock()
	dbCounter.n++
	id := dbCounter.n
	dbCounter.Unlock()

	// One unique cache per test using shared cache; explicit `mode=memory`
	// matches modernc.org/sqlite's accepted DSN dialect.
	dsn := fmt.Sprintf("file:filex_test_%d?mode=memory&cache=shared", id)

	drv := db.MustGet("sqlite")
	conn, err := drv.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("testutil: open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := db.Migrate(context.Background(), drv, conn); err != nil {
		t.Fatalf("testutil: migrate: %v", err)
	}
	return conn, drv.NewStore(conn)
}

// SeedAdmin creates an admin user with a known password and returns the
// (email, plaintext password) tuple suitable for LoginAs.
func SeedAdmin(t *testing.T, store db.Store) (string, string) {
	t.Helper()
	email := "admin@test.local"
	password := "TestAdminPass!1"

	hash, err := authlocal.HashPassword(password)
	if err != nil {
		t.Fatalf("testutil: hash: %v", err)
	}
	if _, err := store.CreateUser(context.Background(), email, hash, model.RoleAdmin, "en", "UTC"); err != nil {
		t.Fatalf("testutil: create admin: %v", err)
	}
	return email, password
}

// SeedAdminUser creates an admin user and returns its (id, email). Useful
// for tests that need the user_id to bind an API token.
func SeedAdminUser(t *testing.T, store db.Store) (int64, string) {
	t.Helper()
	email := "admin2@test.local"
	hash, err := authlocal.HashPassword("TestAdminPass!1")
	if err != nil {
		t.Fatalf("testutil: hash: %v", err)
	}
	u, err := store.CreateUser(context.Background(), email, hash, model.RoleAdmin, "en", "UTC")
	if err != nil {
		t.Fatalf("testutil: create admin: %v", err)
	}
	return u.ID, email
}

// SeedRegularUser creates a non-admin user and returns its credentials.
func SeedRegularUser(t *testing.T, store db.Store, email, password string) {
	t.Helper()
	hash, err := authlocal.HashPassword(password)
	if err != nil {
		t.Fatalf("testutil: hash: %v", err)
	}
	if _, err := store.CreateUser(context.Background(), email, hash, model.RoleUser, "en", "UTC"); err != nil {
		t.Fatalf("testutil: create user: %v", err)
	}
}

// NewTestServer wires a fully working HTTP server backed by an in-memory
// SQLite DB plus a tmp-dir local storage. The returned httptest.Server is
// stopped via t.Cleanup; callers receive a cookie jar pre-installed on the
// returned http.Client.
//
// LocalAuth is wired so /api/auth/login works.
func NewTestServer(t *testing.T) (*httptest.Server, *http.Client, db.Store) {
	return NewTestServerCfg(t, nil)
}

// NewTestServerCfg is NewTestServer with a config hook applied before the
// router is built — for exercising config-dependent behavior (e.g.
// FILEX_COOKIE_DOMAIN stamping a Domain on the session cookie).
func NewTestServerCfg(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *http.Client, db.Store) {
	return NewTestServerWith(t, mutate, nil)
}

// NewTestServerWith is NewTestServerCfg plus a hook to mutate api.Deps before
// the router is built — e.g. to inject an OIDC driver so the full router +
// middleware chain can exercise /api/auth/oidc/callback end to end.
func NewTestServerWith(t *testing.T, cfgMutate func(*config.Config), depsMutate func(*api.Deps)) (*httptest.Server, *http.Client, db.Store) {
	t.Helper()

	_, raw := NewTestDB(t)
	// Mirror internal/server.New: handlers see the QUOTA-ACCOUNTING store, so
	// a handler test exercises the same node-write behaviour the running
	// product has (owner stamped, usage_bytes moved). A harness that quietly
	// differs from production is how a suite goes green over a broken feature.
	accounting := quotastore.New(raw)
	// …and the IDENTITY wrapper too, for the same reason: a fixture that
	// creates accounts nobody named would let a dual-side-login test pass
	// against a store production does not have (migration 00025).
	var store db.Store = identitystore.New(accounting)
	// …and the TENANT wrapper, which is the one this harness was missing.
	//
	// ⚠⚠ internal/server.New hands the HANDLERS `tenantstore.New(store)`
	// (server.go:831) and keeps the raw store only for background services.
	// This harness stopped one wrapper short, so every multi-tenant handler
	// test in the package was measuring an UNSCOPED store: `ListStorages`,
	// `ListEnabledStorages` and `ListUsers` returned every tenant's rows, and
	// a test asserting "the tenant sees only its own" could only pass if the
	// handler happened to filter a second time by itself. It was found by a
	// dashboard test that expected the other tenant's storage to be absent and
	// watched it come back — the harness, not the product, was wrong.
	//
	// The wrapper is inert unless the context carries a tenant scope, so
	// single-tenant tests are unchanged by construction.
	store = tenantstore.New(store)

	// Local auth driver wired to the same store.
	localDrv := authlocal.New(store)
	if err := localDrv.Init(context.Background(), nil); err != nil {
		t.Fatalf("testutil: local auth init: %v", err)
	}
	auth.SetEnabled([]auth.Driver{localDrv})

	// Capability service — no probes needed, just calls store.ListExternalServices.
	caps := capability.New(store)

	// Sync worker — store is enough for the API surface; no storages enabled.
	worker := syncpkg.New(store)

	// Storage resolver — always errors for test, since handlers we exercise
	// don't actually stream files.
	resolver := func(_ int64) (storage.Driver, error) {
		return nil, fmt.Errorf("testutil: no storage configured")
	}

	cfg := config.Default()
	cfg.PublicURL = "http://test.local"
	// Tighten CORS so cors middleware doesn't echo arbitrary origins back.
	cfg.CORS.AllowedOrigins = []string{"*"}
	if cfgMutate != nil {
		cfgMutate(&cfg)
	}

	// ⚠ The SAME line internal/server.New runs (server.go → AttachSecret). The
	// instance secret signs the PIN unlock cookie and, since migration 00049,
	// seals a share's PIN so its owner and an admin can be shown it again.
	// Without this the harness measured an instance that has NO
	// FILEX_SECRET_KEY on every test — a harness that quietly differs from
	// production is how a suite goes green over a broken feature. Inert when
	// the config carries no key, which is the default.
	shareSvc := share.NewService(store)
	shareSvc.AttachSecret(cfg.SecretKey)

	deps := &api.Deps{
		Cfg:   cfg,
		Store: store,
		// Quota is wired by default: with it nil the service short-circuits
		// to "unlimited, no error", so the whole /api/admin/quota surface
		// answered 200 in tests no matter what the DB said and the H5
		// no-rows-is-a-500 bug was invisible here.
		Quota:           accounting.Quota(),
		Worker:          worker,
		Caps:            caps,
		Share:           shareSvc,
		StorageResolver: resolver,
		Embed:           embed.FS{},
	}
	// Sign-in as the server wires it (internal/server → authsetup): `local`
	// as the environment's provider, the Identity providers page's on top,
	// and the handlers holding the running set's proxies — so a provider
	// switched on through the page in a test is reachable the way it is in
	// production.
	box, err := secretbox.New(cfg.SecretKey)
	if err != nil {
		t.Fatalf("testutil: secret box: %v", err)
	}
	live, err := authsetup.New(context.Background(), authsetup.Options{
		Store: store, Box: box, MultiTenant: cfg.MultiTenant,
		RecoveryLogin: cfg.Auth.RecoveryLogin, PublicURL: cfg.PublicURL,
	}, []authsetup.Entry{authsetup.NewEnvEntry("local", "the test harness", nil, localDrv, nil, false)})
	if err != nil {
		t.Fatalf("testutil: auth: %v", err)
	}
	live.Start(context.Background())
	live.OnSwap(func(set *authsetup.Set) {
		caps.SetAuthDrivers(set.Names())
		caps.SetRecoveryLogin(set.Recovery())
	})
	deps.AuthLive = live
	deps.LocalAuth = live.Login()
	deps.OIDCAuth = live.OIDC()
	deps.Directory = live.Dir()
	if depsMutate != nil {
		depsMutate(deps)
	}
	router := api.BuildRouter(deps)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	servers.Store(srv.URL, store)
	t.Cleanup(func() { servers.Delete(srv.URL) })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("testutil: cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}
	return srv, client, store
}

// LoginAs posts to /api/auth/login and returns the freshly-issued cookie
// value. The cookie is also stored on the supplied client's jar.
//
// On a multi-tenant server it signs the person in the way a tenant's person
// signs in on the platform's page: with THEIR OWN tenant's realm in the Realm
// field (docs/MULTI-TENANCY.md, Realms) — found from the account in the
// server's store, for a server this package built. The realm rules themselves
// (an empty realm is the platform's own tenant, another tenant's account is
// refused) are measured by requests that spell the realm out, never through
// this helper.
func LoginAs(t *testing.T, srv *httptest.Server, client *http.Client, email, password string) string {
	t.Helper()
	return LoginAsIn(t, srv, client, accountRealm(srv, email), email, password)
}

// servers maps a test server's URL to its store, so LoginAs can find an
// account's realm.
var servers sync.Map

// accountRealm is the realm of the tenant the account named by identifier
// belongs to, "" for the platform's own (or an unknown server or account).
func accountRealm(srv *httptest.Server, identifier string) string {
	if srv == nil {
		return ""
	}
	v, ok := servers.Load(srv.URL)
	if !ok {
		return ""
	}
	store := v.(db.Store)
	ctx := context.Background()
	u, err := identity.Resolve(ctx, store, identifier)
	if err != nil || u == nil || u.ProviderID == nil {
		return ""
	}
	p, err := store.GetProvider(ctx, *u.ProviderID)
	if err != nil {
		return ""
	}
	return p.LoginRealm()
}

// LoginAsIn is LoginAs in a tenant realm: what a tenant's person types in the
// sign-in form's Realm field on the platform's page of a multi-tenant install
// (docs/MULTI-TENANCY.md, Realms). An empty realm is the platform's own; a
// single-tenant server ignores the field.
func LoginAsIn(t *testing.T, srv *httptest.Server, client *http.Client, realm, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
		"realm":    realm,
	})
	resp, err := client.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("testutil: login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("testutil: login: expected 200, got %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var answer struct {
		Handoff *struct {
			Origin string `json:"origin"`
			Code   string `json:"code"`
		} `json:"handoff"`
	}
	_ = json.Unmarshal(raw, &answer)
	if answer.Handoff != nil {
		return redeemHandoff(t, srv, client, answer.Handoff.Origin, answer.Handoff.Code)
	}
	for _, c := range resp.Cookies() {
		if c.Name == authlocal.SessionCookieName {
			return c.Value
		}
	}
	t.Fatalf("testutil: login: no session cookie in response")
	return ""
}

// redeemHandoff finishes a sign-in the server handed to a tenant's own address
// (handlers.Auth.handOff) the way the browser does: the ticket is presented on
// that address — the test server answers every Host — and the session it opens
// is put in the client's jar for the server's URL, where the test goes on
// making its requests (a session is scoped by its account's tenant, not by the
// host).
func redeemHandoff(t *testing.T, srv *httptest.Server, client *http.Client, origin, code string) string {
	t.Helper()
	u, err := url.Parse(origin)
	if err != nil {
		t.Fatalf("testutil: handoff origin %q: %v", origin, err)
	}
	body, _ := json.Marshal(map[string]string{"code": code})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/handoff", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("testutil: handoff: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Host = u.Host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("testutil: handoff: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("testutil: handoff: expected 200, got %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == authlocal.SessionCookieName && c.Value != "" {
			if client.Jar != nil {
				if su, perr := url.Parse(srv.URL); perr == nil {
					client.Jar.SetCookies(su, []*http.Cookie{{Name: c.Name, Value: c.Value, Path: "/"}})
				}
			}
			return c.Value
		}
	}
	t.Fatalf("testutil: handoff: no session cookie in response")
	return ""
}

// TmpFilePath returns a path inside t.TempDir() suitable for file create.
func TmpFilePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

// ReadJSON decodes resp.Body into out. Calls t.Fatal on decode failure.
func ReadJSON(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("testutil: decode json: %v", err)
	}
}

// MustEmbedFS is a tiny convenience that returns a fresh embed.FS with no
// files — handy for code that requires an embed.FS but doesn't care about
// its contents.
func MustEmbedFS() embed.FS { return embed.FS{} }

// NewAPIToken mints an API token for a user and returns the plaintext secret.
//
// It lives here because three protocol suites need the same thing: a token to
// authenticate with, with a chosen scope string. Each of them writing its own
// would mean three places that could drift from how the product actually
// hashes a token.
func NewAPIToken(t *testing.T, store db.Store, userID int64, scopes string) string {
	t.Helper()
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("testutil: random: %v", err)
	}
	secret := "filex_" + hex.EncodeToString(raw[:])
	if _, err := store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID:    userID,
		Label:     "testutil",
		TokenHash: apitoken.HashToken(secret),
		Scopes:    scopes,
	}); err != nil {
		t.Fatalf("testutil: create token: %v", err)
	}
	return secret
}
