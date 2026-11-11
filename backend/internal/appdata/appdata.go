// Package appdata is an app's own data: a key-value store per app, kept
// BESIDE filex and never in filex's own database (docs/APP-PLUGINS-API.md →
// An app's own data). An app reaches it only through the data_* host
// functions; it cannot name a table, see one of filex's, or run a query.
//
// Two places it can live:
//
//   - a file per app under the data directory (SQLite, the default). Fast
//     (a point read is single-digit microseconds) and simple, but the data
//     directory is per node (docs/DEPLOYMENT.md → Scaling): behind several
//     filex replicas each node keeps its own, which is why NodeLocal says so;
//   - a PostgreSQL database of its own (FILEX_APP_DATA_DSN), which every
//     replica shares. Never filex's own database: an app's rows have no
//     business beside filex's tables, and an operator who points this at
//     filex's DSN is refused.
//
// Every key is in a scope the HOST chooses (`app`, `t:<tenant>`), so an app
// cannot read another tenant's keys by spelling them. Keys are printable
// ASCII, so every engine orders and compares them byte for byte.
package appdata

import (
	"context"
	"errors"
	"regexp"
	"unicode/utf8"
)

// Entry is one key.
type Entry struct {
	Key     string
	Value   string
	Rev     int64
	Counter bool
}

// Cond makes a write conditional.
type Cond struct {
	// IfAbsent: only when the key does not exist.
	IfAbsent bool
	// IfRev: only when the key is at this revision (0 asks nothing).
	IfRev int64
}

// Errors the host functions answer with their wire codes.
var (
	// ErrConflict: a condition did not hold (busy).
	ErrConflict = errors.New("appdata: the condition does not hold")
	// ErrQuota: the app's store is full (too_large).
	ErrQuota = errors.New("appdata: the app's data is at its size limit")
	// ErrCounter: a value written to a counter key, or a counter added to a
	// value key (invalid).
	ErrCounter = errors.New("appdata: a counter key is written by incr only")
	// ErrKey / ErrScope / ErrValue: what was asked cannot be stored (invalid).
	ErrKey   = errors.New("appdata: a key is 1 to 512 printable ASCII characters")
	ErrScope = errors.New("appdata: no such scope")
	ErrValue = errors.New("appdata: a value is valid UTF-8, at most 64 KiB")
	// ErrApp: not an app name.
	ErrApp = errors.New("appdata: not an app name")
)

// Limits (the wire contract's, repeated here so the store holds them
// whoever calls it).
const (
	MaxKeyBytes   = 512
	MaxValueBytes = 64 << 10
	MaxList       = 500
	// DefaultMaxBytes is an app's store when the operator says nothing
	// (FILEX_APP_DATA_MAX_MB).
	DefaultMaxBytes = 256 << 20
)

// Store is where every app's data lives.
type Store interface {
	Get(ctx context.Context, app, scope, key string) (Entry, bool, error)
	Put(ctx context.Context, app, scope, key, value string, c Cond) (int64, error)
	Delete(ctx context.Context, app, scope, key string, ifRev int64) (bool, error)
	// List answers the keys that start with prefix, after `after`, in byte
	// order, at most limit; next is "" on the last page.
	List(ctx context.Context, app, scope, prefix, after string, limit int) ([]Entry, string, error)
	Incr(ctx context.Context, app, scope, key string, by int64) (int64, error)
	// Usage is how many keys an app keeps and their bytes (keys + values).
	Usage(ctx context.Context, app string) (keys, bytes int64, err error)
	// Drop erases everything an app keeps.
	Drop(ctx context.Context, app string) error
	// Driver is "sqlite" or "postgres"; NodeLocal is true when the data is
	// this node's alone.
	Driver() string
	NodeLocal() bool
	Close() error
}

var appRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

var scopeRe = regexp.MustCompile(`^(app|t:[0-9]{1,19})$`)

// TenantScope is the scope of a tenant's keys (0: the platform's own).
func TenantScope(tenant int64) string {
	if tenant < 0 {
		tenant = 0
	}
	return "t:" + itoa(tenant)
}

// AppScope is the scope every tenant shares.
const AppScope = "app"

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func check(app, scope, key string) error {
	if !appRe.MatchString(app) {
		return ErrApp
	}
	if !scopeRe.MatchString(scope) {
		return ErrScope
	}
	return CheckKey(key)
}

// CheckKey refuses a key that is empty, longer than MaxKeyBytes or holds a
// byte outside printable ASCII (0x21-0x7E; no space).
func CheckKey(key string) error {
	if key == "" || len(key) > MaxKeyBytes {
		return ErrKey
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return ErrKey
		}
	}
	return nil
}

// checkPrefix is CheckKey for a listing's prefix and cursor, which may be
// empty.
func checkPrefix(p string) error {
	if p == "" {
		return nil
	}
	return CheckKey(p)
}

// upper is the least string greater than every key that starts with prefix:
// keys are printable ASCII, so prefix + DEL (0x7F) is above all of them.
func upper(prefix string) string { return prefix + "\x7f" }

func validValue(v string) bool {
	return len(v) <= MaxValueBytes && utf8.ValidString(v)
}

func clampLimit(n int) int {
	if n <= 0 || n > MaxList {
		return 100
	}
	return n
}
