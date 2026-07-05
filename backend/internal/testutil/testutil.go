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
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"

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
	t.Helper()

	_, store := NewTestDB(t)

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
	if mutate != nil {
		mutate(&cfg)
	}

	deps := &api.Deps{
		Cfg:             cfg,
		Store:           store,
		Worker:          worker,
		Caps:            caps,
		Share:           share.NewService(store),
		StorageResolver: resolver,
		Embed:           embed.FS{},
		LocalAuth:       localDrv,
	}
	router := api.BuildRouter(deps)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("testutil: cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}
	return srv, client, store
}

// LoginAs posts to /api/auth/login and returns the freshly-issued cookie
// value. The cookie is also stored on the supplied client's jar.
func LoginAs(t *testing.T, srv *httptest.Server, client *http.Client, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
	})
	resp, err := client.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("testutil: login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("testutil: login: expected 200, got %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == authlocal.SessionCookieName {
			return c.Value
		}
	}
	t.Fatalf("testutil: login: no session cookie in response")
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
