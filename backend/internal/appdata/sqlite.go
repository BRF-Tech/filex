package appdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// ── One SQLite file per app ────────────────────────────────────────────────
//
// <dir>/<app>.db, WAL. Opened on the first call and kept open; Drop closes it
// and removes the file with its -wal and -shm companions.

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS kv (
    scope   TEXT    NOT NULL,
    k       TEXT    NOT NULL,
    v       TEXT    NOT NULL,
    rev     INTEGER NOT NULL,
    counter INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (scope, k)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS usage (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    keys  INTEGER NOT NULL,
    bytes INTEGER NOT NULL
);
INSERT OR IGNORE INTO usage (id, keys, bytes) VALUES (1, 0, 0);
`

// SQLite keeps every app's data in a file of its own.
type SQLite struct {
	dir      string
	maxBytes int64

	mu  sync.Mutex
	dbs map[string]*sql.DB
}

// OpenSQLite stores apps' data under dir (created 0700). maxBytes is each
// app's limit (0: DefaultMaxBytes).
func OpenSQLite(dir string, maxBytes int64) (*SQLite, error) {
	if dir == "" {
		return nil, errors.New("appdata: no directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("appdata: %w", err)
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &SQLite{dir: dir, maxBytes: maxBytes, dbs: map[string]*sql.DB{}}, nil
}

func (s *SQLite) Driver() string  { return "sqlite" }
func (s *SQLite) NodeLocal() bool { return true }

func (s *SQLite) path(app string) string { return filepath.Join(s.dir, app+".db") }

func (s *SQLite) db(app string) (*sql.DB, error) {
	if !appRe.MatchString(app) {
		return nil, ErrApp
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if db := s.dbs[app]; db != nil {
		return db, nil
	}
	dsn := "file:" + s.path(app) + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// WAL lets readers run beside the one writer; more than a few
	// connections buys nothing for one app's file.
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(sqliteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("appdata: %s: %w", app, err)
	}
	s.dbs[app] = db
	return db, nil
}

func (s *SQLite) Get(ctx context.Context, app, scope, key string) (Entry, bool, error) {
	if err := check(app, scope, key); err != nil {
		return Entry{}, false, err
	}
	db, err := s.db(app)
	if err != nil {
		return Entry{}, false, err
	}
	e := Entry{Key: key}
	var counter int
	err = db.QueryRowContext(ctx, `SELECT v, rev, counter FROM kv WHERE scope = ? AND k = ?`, scope, key).Scan(&e.Value, &e.Rev, &counter)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	e.Counter = counter != 0
	return e, true, nil
}

func (s *SQLite) Put(ctx context.Context, app, scope, key, value string, c Cond) (int64, error) {
	if err := check(app, scope, key); err != nil {
		return 0, err
	}
	if !validValue(value) {
		return 0, ErrValue
	}
	db, err := s.db(app)
	if err != nil {
		return 0, err
	}
	return write(ctx, db, sqliteDialect, s.maxBytes, app, scope, key, putRule(value, c))
}

func (s *SQLite) Incr(ctx context.Context, app, scope, key string, by int64) (int64, error) {
	if err := check(app, scope, key); err != nil {
		return 0, err
	}
	db, err := s.db(app)
	if err != nil {
		return 0, err
	}
	var n int64
	_, err = write(ctx, db, sqliteDialect, s.maxBytes, app, scope, key, incrRule(by, &n))
	return n, err
}

func (s *SQLite) Delete(ctx context.Context, app, scope, key string, ifRev int64) (bool, error) {
	if err := check(app, scope, key); err != nil {
		return false, err
	}
	db, err := s.db(app)
	if err != nil {
		return false, err
	}
	return remove(ctx, db, sqliteDialect, app, scope, key, ifRev)
}

func (s *SQLite) List(ctx context.Context, app, scope, prefix, after string, limit int) ([]Entry, string, error) {
	if err := checkList(app, scope, prefix, after); err != nil {
		return nil, "", err
	}
	db, err := s.db(app)
	if err != nil {
		return nil, "", err
	}
	return list(ctx, db, sqliteDialect, app, scope, prefix, after, clampLimit(limit))
}

func (s *SQLite) Usage(ctx context.Context, app string) (int64, int64, error) {
	if !appRe.MatchString(app) {
		return 0, 0, ErrApp
	}
	if _, err := os.Stat(s.path(app)); errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	db, err := s.db(app)
	if err != nil {
		return 0, 0, err
	}
	var keys, bytes int64
	err = db.QueryRowContext(ctx, `SELECT keys, bytes FROM usage WHERE id = 1`).Scan(&keys, &bytes)
	return keys, bytes, err
}

// Drop closes the app's file and removes it. The path is built from a
// validated app name under the store's own directory, never from input.
func (s *SQLite) Drop(_ context.Context, app string) error {
	if !appRe.MatchString(app) {
		return ErrApp
	}
	s.mu.Lock()
	if db := s.dbs[app]; db != nil {
		_ = db.Close()
		delete(s.dbs, app)
	}
	s.mu.Unlock()
	base := s.path(app)
	var first error
	for _, p := range []string{base, base + "-wal", base + "-shm"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}

func (s *SQLite) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for app, db := range s.dbs {
		_ = db.Close()
		delete(s.dbs, app)
	}
	return nil
}
