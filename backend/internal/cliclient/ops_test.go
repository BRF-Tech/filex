package cliclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// opSequence answers GET /api/files/ops/5 with each state in turn, then the
// last one for ever.
func opSequence(t *testing.T, states ...map[string]any) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/files/ops/5" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		st := states[min(n, len(states)-1)]
		n++
		mu.Unlock()
		op := map[string]any{"id": 5, "kind": "copy", "total": 2}
		for k, v := range st {
			op[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(op)
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { mu.Lock(); defer mu.Unlock(); return n }
}

// WaitOp follows an op through its states to the end, and only then answers.
func TestWaitOp_FollowsTheOpToItsEnd(t *testing.T) {
	srv, asked := opSequence(t,
		map[string]any{"status": "pending"},
		map[string]any{"status": "running", "done": 1},
		map[string]any{"status": "ok", "done": 2},
	)
	c := New(Conn{URL: srv.URL, Token: "t"})
	c.OpPollInterval = time.Millisecond

	op, raw, err := c.WaitOp(context.Background(), 5)
	require.NoError(t, err)
	assert.Equal(t, "ok", op.Status)
	assert.Equal(t, 2, op.Done)
	assert.Equal(t, 3, asked(), "asked until the op was finished, not once")
	assert.Contains(t, string(raw), `"status":"ok"`, "the raw answer is the FINAL row")
}

// An op that ended partly done is an error that says how much failed - a
// script must not read "2 of 3 copied" as success.
func TestWaitOp_PartialIsAnError(t *testing.T) {
	srv, _ := opSequence(t, map[string]any{"status": "partial", "total": 3, "failed": 1, "error": "b.txt: permission denied"})
	c := New(Conn{URL: srv.URL, Token: "t"})

	op, _, err := c.WaitOp(context.Background(), 5)
	require.Error(t, err)
	var oe *OpError
	require.True(t, errors.As(err, &oe))
	assert.Same(t, op, oe.Op)
	assert.Contains(t, err.Error(), "1 of 3")
	assert.Contains(t, err.Error(), "b.txt: permission denied")
}

// Stopping the wait (Ctrl-C) says the server carries on - the op is not
// cancelled by a client that stops listening.
func TestWaitOp_CancelledWaitSaysTheServerCarriesOn(t *testing.T) {
	srv, _ := opSequence(t, map[string]any{"status": "running"})
	c := New(Conn{URL: srv.URL, Token: "t"})
	c.OpPollInterval = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, _, err := c.WaitOp(ctx, 5)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "carries on")
}

// An encrypted archive's 401 is not a session problem: it must not read as
// IsUnauthorized, which earns the "run filex client login" hint.
func TestExtractArchive_PasswordRefusal_IsNotASessionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"wrong password","code":"BAD_PASSWORD"}`))
	}))
	t.Cleanup(srv.Close)
	c := New(Conn{URL: srv.URL, Token: "t"})

	_, _, err := c.ExtractArchive(context.Background(), "docs://a.7z", "", "yanlis", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrArchivePassword)
	assert.False(t, IsUnauthorized(err))
	assert.Contains(t, err.Error(), "wrong")
}

// ExtractArchive refuses a destination on another storage before sending: the
// server extracts on the archive's own storage only.
func TestExtractArchive_OtherStorageDestination_Refused(t *testing.T) {
	c := New(Conn{URL: "http://127.0.0.1:1", Token: "t"})
	_, _, err := c.ExtractArchive(context.Background(), "docs://a.7z", "depo://x", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "own storage")
}
