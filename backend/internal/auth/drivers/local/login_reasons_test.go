package local

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// The three ways a local login is refused answer the CALLER identically — one
// 401, no hint about which it was, because anything else enumerates accounts.
// These tests pin the other half of that bargain: the SERVER must be able to
// tell them apart. A Cypress run once saw every login 401 with nothing in the
// log to explain it and could not be reproduced; a store failure returning
// through the same statement as a typo is how that happens.

// captureReasons collects the `reason` attribute of every record logged while
// fn runs, together with its level.
type logged struct {
	level  slog.Level
	msg    string
	reason string
}

type reasonHandler struct {
	mu   *sync.Mutex
	recs *[]logged
}

func (h reasonHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h reasonHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h reasonHandler) WithGroup(string) slog.Handler            { return h }
func (h reasonHandler) Handle(_ context.Context, r slog.Record) error {
	var reason string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "reason" {
			reason = a.Value.String()
			return false
		}
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.recs = append(*h.recs, logged{level: r.Level, msg: r.Message, reason: reason})
	return nil
}

func captureReasons(t *testing.T, fn func()) []logged {
	t.Helper()
	var (
		mu   sync.Mutex
		recs []logged
	)
	prev := slog.Default()
	slog.SetDefault(slog.New(reasonHandler{mu: &mu, recs: &recs}))
	defer slog.SetDefault(prev)
	fn()
	mu.Lock()
	defer mu.Unlock()
	out := make([]logged, len(recs))
	copy(out, recs)
	return out
}

func findReason(recs []logged, reason string) (logged, bool) {
	for _, r := range recs {
		if r.reason == reason {
			return r, true
		}
	}
	return logged{}, false
}

func reasonsOf(recs []logged) string {
	var b []string
	for _, r := range recs {
		b = append(b, r.reason)
	}
	return strings.Join(b, ",")
}

// failingLookupStore is a Store whose user lookup is broken — a locked sqlite
// file, a dropped postgres connection. Embedding db.Store means only the one
// method that matters is overridden.
type failingLookupStore struct {
	db.Store
	err error
}

func (s failingLookupStore) GetUserByEmail(context.Context, string) (*model.User, error) {
	return nil, s.err
}

// TestLogin_WrongPassword_LogsReason — a typo names itself in the log.
func TestLogin_WrongPassword_LogsReason(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	email, _ := dbtest.SeedAdmin(t, store)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), nil))

	var err error
	recs := captureReasons(t, func() {
		_, _, err = d.Login(context.Background(), email, "not-the-password")
	})
	assert.True(t, errors.Is(err, auth.ErrUnauthorized), "the caller still gets one flat 401")
	got, ok := findReason(recs, "password mismatch")
	require.True(t, ok, "no `password mismatch` line; logged: %s", reasonsOf(recs))
	assert.Equal(t, slog.LevelDebug, got.level, "a typo is the caller's problem, not the operator's")
}

// TestLogin_UnknownAccount_LogsReason — distinguishable from a wrong password
// in the log, indistinguishable in the answer.
func TestLogin_UnknownAccount_LogsReason(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), nil))

	var err error
	recs := captureReasons(t, func() {
		_, _, err = d.Login(context.Background(), "ghost@nowhere.invalid", "anything")
	})
	assert.True(t, errors.Is(err, auth.ErrUnauthorized))
	got, ok := findReason(recs, "no such account")
	require.True(t, ok, "no `no such account` line; logged: %s", reasonsOf(recs))
	assert.Equal(t, slog.LevelDebug, got.level)
}

// TestLogin_NoLocalPassword_LogsReason — a directory/OIDC account has no local
// hash. It refuses like a wrong password and is nothing like one: no password
// will ever work until one is set.
func TestLogin_NoLocalPassword_LogsReason(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	_, err := store.CreateUser(context.Background(), "directory@example.invalid", "", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), nil))

	recs := captureReasons(t, func() {
		_, _, err = d.Login(context.Background(), "directory@example.invalid", "anything")
	})
	assert.True(t, errors.Is(err, auth.ErrUnauthorized))
	got, ok := findReason(recs, "account has no local password")
	require.True(t, ok, "no `account has no local password` line; logged: %s", reasonsOf(recs))
	assert.Equal(t, slog.LevelInfo, got.level, "an operator debugging a rollout needs to see this one")
}

// TestLogin_LookupFailure_IsNotUnauthorized — the case that cost the debugging
// session. A broken store must NOT come back as ErrUnauthorized: auth.LoginChain
// reads "not ErrUnauthorized" as "this driver could not judge" and reports it,
// which is the only place "the database is down" can be told from "wrong
// password". The client still gets a 401 — the handler answers 401 for any error.
func TestLogin_LookupFailure_IsNotUnauthorized(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	boom := errors.New("database is locked")
	d := New(failingLookupStore{Store: store, err: boom})
	require.NoError(t, d.Init(context.Background(), nil))

	var err error
	recs := captureReasons(t, func() {
		_, _, err = d.Login(context.Background(), "someone@example.invalid", "anything")
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, boom), "the store's error must survive, got %v", err)
	assert.False(t, errors.Is(err, auth.ErrUnauthorized),
		"a store failure reported as ErrUnauthorized is invisible to the chain and to the log")

	got, ok := findReason(recs, "user lookup failed")
	require.True(t, ok, "no `user lookup failed` line; logged: %s", reasonsOf(recs))
	assert.Equal(t, slog.LevelError, got.level, "the server could not judge — that is an operator's problem")
}

// TestLogin_UnusableHash_LogsError — a truncated or non-bcrypt password_hash is
// not a wrong password, and an account that can never log in until it is reset
// must not fail silently.
func TestLogin_UnusableHash_LogsError(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	_, err := store.CreateUser(context.Background(), "corrupt@example.invalid", "not-a-bcrypt-hash", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), nil))

	recs := captureReasons(t, func() {
		_, _, err = d.Login(context.Background(), "corrupt@example.invalid", "anything")
	})
	assert.True(t, errors.Is(err, auth.ErrUnauthorized), "the caller learns nothing extra")
	got, ok := findReason(recs, "bad password hash")
	require.True(t, ok, "no `bad password hash` line; logged: %s", reasonsOf(recs))
	assert.Equal(t, slog.LevelError, got.level)
}
