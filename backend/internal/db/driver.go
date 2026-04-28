// Package db defines a Driver interface that wraps a *sql.DB and exposes
// the typed Store interface used by the rest of the codebase.
//
// Each concrete driver (sqlite, mysql, postgres) lives under db/drivers/
// and adapts dialect-specific quirks (placeholder syntax, RETURNING
// support, JSON column type) into the same Store interface.
//
// Migrations are embedded per-dialect via embed.FS and run by goose.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrNoRows is returned by Store methods when a single-row query has no result.
var ErrNoRows = sql.ErrNoRows

// Driver describes a database engine integration.
type Driver interface {
	Name() string
	Open(ctx context.Context, dsn string) (*sql.DB, error)
	MigrationsFS() embed.FS // embed.FS rooted at the dialect's migration directory
	Dialect() string        // goose-compatible: "sqlite3", "postgres", "mysql"
	NewStore(db *sql.DB) Store
}

// Factory builds a fresh Driver instance.
type Factory func() Driver

var (
	regMu    sync.RWMutex
	registry = map[string]Factory{}
)

// Register adds a driver factory, called from drivers' init().
func Register(name string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("db: duplicate driver registration: " + name)
	}
	registry[name] = f
}

// Get returns a freshly-constructed driver instance, error if unknown.
func Get(name string) (Driver, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("db: unknown driver %q", name)
	}
	return f(), nil
}

// Names returns the registered driver names sorted alphabetically.
func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// MustGet panics on unknown driver — for tests / static configs.
func MustGet(name string) Driver {
	d, err := Get(name)
	if err != nil {
		panic(err)
	}
	return d
}

// EnsureNotEmpty surfaces the common "DSN missing" error consistently.
func EnsureNotEmpty(dsn string, hint string) error {
	if dsn == "" {
		return errors.New("db: empty DSN " + hint)
	}
	return nil
}
