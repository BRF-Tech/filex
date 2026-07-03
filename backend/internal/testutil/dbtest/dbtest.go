// Package dbtest provides minimal DB+model test helpers without depending
// on auth/capability/share — so it is safe to import from the test files
// of those packages without creating an import cycle.
//
// If you need a full HTTP server harness (auth wiring, share service,
// capability probes), use internal/testutil instead.
package dbtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"

	// Register the SQLite driver so MustGet("sqlite") works.
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"
)

// dbCounter ensures every NewTestDB call gets a unique DSN. modernc/sqlite
// shares the in-memory cache per `:memory:` connection but each named DSN
// produces a fresh isolated DB even within the same process.
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

	dsn := fmt.Sprintf("file:filex_dbtest_%d?mode=memory&cache=shared", id)

	drv := db.MustGet("sqlite")
	conn, err := drv.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("dbtest: open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := db.Migrate(context.Background(), drv, conn); err != nil {
		t.Fatalf("dbtest: migrate: %v", err)
	}
	return conn, drv.NewStore(conn)
}

// hashPassword is a local copy of authlocal.HashPassword. We can't import
// authlocal here because the local driver's tests import this package, and
// that would create a cycle. Keep the cost in sync with the production
// driver (bcrypt.DefaultCost).
func hashPassword(t *testing.T, plain string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("dbtest: hash password: %v", err)
	}
	return string(h)
}

// SeedAdmin creates an admin user with a known password and returns the
// (email, plaintext password) tuple suitable for downstream login helpers.
func SeedAdmin(t *testing.T, store db.Store) (string, string) {
	t.Helper()
	email := "admin@test.local"
	password := "TestAdminPass!1"

	hash := hashPassword(t, password)
	if _, err := store.CreateUser(context.Background(), email, hash, model.RoleAdmin, "en", "UTC"); err != nil {
		t.Fatalf("dbtest: create admin: %v", err)
	}
	return email, password
}

// SeedRegularUser creates a non-admin user with the supplied credentials.
func SeedRegularUser(t *testing.T, store db.Store, email, password string) {
	t.Helper()
	hash := hashPassword(t, password)
	if _, err := store.CreateUser(context.Background(), email, hash, model.RoleUser, "en", "UTC"); err != nil {
		t.Fatalf("dbtest: create user: %v", err)
	}
}

// SeedUserWithRole creates a user with an explicit role and returns the
// generated user ID. Useful when tests need a specific role beyond the two
// helpers above.
func SeedUserWithRole(t *testing.T, store db.Store, email, password, role string) int64 {
	t.Helper()
	hash := hashPassword(t, password)
	user, err := store.CreateUser(context.Background(), email, hash, role, "en", "UTC")
	if err != nil {
		t.Fatalf("dbtest: create user(%s, role=%s): %v", email, role, err)
	}
	return user.ID
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
		t.Fatalf("dbtest: decode json: %v", err)
	}
}
