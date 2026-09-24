package cliclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// streamServer is a minimal /api/files/ws-ticket + /api/ws pair whose
// behaviour each test chooses.
type streamServer struct {
	srv         *httptest.Server
	ack         bool          // answer `watch` with `watching` (a 0.43+ server)
	ticketCode  int           // non-200 → the ticket endpoint refuses
	dropAfter   time.Duration // close the socket this long after the ack
	connections atomic.Int32
	mu          sync.Mutex
	watched     [][]string
	bearer      []string
}

func newStreamServer(t *testing.T) *streamServer {
	s := &streamServer{ack: true, ticketCode: http.StatusOK}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/files/ws-ticket":
			s.mu.Lock()
			s.bearer = append(s.bearer, r.Header.Get("Authorization"))
			s.mu.Unlock()
			if s.ticketCode != http.StatusOK {
				w.WriteHeader(s.ticketCode)
				_, _ = w.Write([]byte(`{"error":"realtime unavailable"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ticket":"t1","ws_url":"wss://somewhere.else/api/ws"}`))
		case "/api/ws":
			if r.URL.Query().Get("ticket") != "t1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			s.connections.Add(1)
			defer c.CloseNow()
			ctx := r.Context()
			for {
				_, data, err := c.Read(ctx)
				if err != nil {
					return
				}
				var m struct {
					Type  string   `json:"type"`
					Paths []string `json:"paths"`
				}
				_ = json.Unmarshal(data, &m)
				if m.Type != "watch" {
					continue
				}
				s.mu.Lock()
				s.watched = append(s.watched, m.Paths)
				s.mu.Unlock()
				if !s.ack {
					continue // an older server: the message is simply ignored
				}
				ack, _ := json.Marshal(map[string]any{"type": "watching", "roots": m.Paths, "errors": []any{}})
				_ = c.Write(ctx, websocket.MessageText, ack)
				frame := []byte(`{"type":"tree_change","root":"docs://proj","dirs":["a/b"],"action":"upload","name":"x.txt"}`)
				_ = c.Write(ctx, websocket.MessageText, frame)
				if s.dropAfter > 0 {
					time.Sleep(s.dropAfter)
					return
				}
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

type streamRec struct {
	mu        sync.Mutex
	states    []StreamState
	changes   []TreeChange
	connected int
}

func (r *streamRec) stream(c *Client) *ChangeStream {
	return &ChangeStream{
		Client: c,
		OnChange: func(tc TreeChange) {
			r.mu.Lock()
			r.changes = append(r.changes, tc)
			r.mu.Unlock()
		},
		OnState: func(s StreamState, _ string) {
			r.mu.Lock()
			r.states = append(r.states, s)
			r.mu.Unlock()
		},
		OnConnected: func() {
			r.mu.Lock()
			r.connected++
			r.mu.Unlock()
		},
		AckTimeout:       200 * time.Millisecond,
		UnsupportedRetry: time.Hour,
		MaxBackoff:       50 * time.Millisecond,
		PingEvery:        time.Hour,
	}
}

func (r *streamRec) snapshot() ([]StreamState, []TreeChange, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]StreamState(nil), r.states...), append([]TreeChange(nil), r.changes...), r.connected
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The whole handshake with the credential the client already has: the bearer
// mints the ticket, the socket is opened on THIS client's address (not the
// ws_url the server advertises), the roots are watched, frames arrive.
func TestChangeStreamSubscribesAndDelivers(t *testing.T) {
	s := newStreamServer(t)
	rec := &streamRec{}
	cs := rec.stream(&Client{BaseURL: s.srv.URL, Token: "tok", HTTP: s.srv.Client()})
	cs.SetRoots([]string{"docs://proj"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cs.Run(ctx)

	eventually(t, "a change frame", func() bool { _, ch, _ := rec.snapshot(); return len(ch) == 1 })
	states, changes, connected := rec.snapshot()
	require.Equal(t, []StreamState{StreamLive}, states)
	require.Equal(t, 1, connected)
	require.Equal(t, "docs://proj", changes[0].Root)
	require.Equal(t, []string{"a/b"}, changes[0].Dirs)
	s.mu.Lock()
	require.Equal(t, "Bearer tok", s.bearer[0], "the ticket must be minted with the client's own token")
	require.Equal(t, [][]string{{"docs://proj"}}, s.watched)
	s.mu.Unlock()

	// Re-subscribing on a root change, on the same connection.
	cs.SetRoots([]string{"docs://proj", "docs://other"})
	eventually(t, "the re-watch", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.watched) == 2 })
	require.EqualValues(t, 1, s.connections.Load())
}

// ⚠ An older server ignores `watch`. Silence must read as "no stream here"
// (polling), not as "nothing changed" — and must not claim a connection.
func TestChangeStreamOlderServerIsPolling(t *testing.T) {
	s := newStreamServer(t)
	s.ack = false
	rec := &streamRec{}
	cs := rec.stream(&Client{BaseURL: s.srv.URL, Token: "tok", HTTP: s.srv.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cs.Run(ctx)
	eventually(t, "the polling state", func() bool { st, _, _ := rec.snapshot(); return len(st) > 0 })
	states, _, connected := rec.snapshot()
	require.Equal(t, StreamPolling, states[0])
	require.Zero(t, connected, "no full pass is owed for a subscription that never happened")
}

// realtime switched off on the server: the ticket endpoint answers 503.
func TestChangeStreamRealtimeOffIsPolling(t *testing.T) {
	s := newStreamServer(t)
	s.ticketCode = http.StatusServiceUnavailable
	rec := &streamRec{}
	cs := rec.stream(&Client{BaseURL: s.srv.URL, Token: "tok", HTTP: s.srv.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cs.Run(ctx)
	eventually(t, "the polling state", func() bool { st, _, _ := rec.snapshot(); return len(st) > 0 })
	states, _, _ := rec.snapshot()
	require.Equal(t, StreamPolling, states[0])
}

// A dropped connection is reconnected with backoff, and EVERY successful
// (re)subscribe is reported — changes made while it was down were announced to
// nobody, and the caller answers OnConnected with a full pass.
func TestChangeStreamReconnectsAndAsksForAFullPass(t *testing.T) {
	s := newStreamServer(t)
	s.dropAfter = 20 * time.Millisecond
	rec := &streamRec{}
	cs := rec.stream(&Client{BaseURL: s.srv.URL, Token: "tok", HTTP: s.srv.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cs.Run(ctx)
	eventually(t, "three subscribes", func() bool { _, _, n := rec.snapshot(); return n >= 3 })
	states, _, _ := rec.snapshot()
	joined := make([]string, len(states))
	for i, s := range states {
		joined[i] = string(s)
	}
	require.Contains(t, strings.Join(joined, ","), "connected,offline,connected", "the drop must be visible as offline between two connections")
}

func TestWebSocketURLFollowsTheClientsOwnAddress(t *testing.T) {
	require.Equal(t, "wss://fm.example.com/api/ws", (&Client{BaseURL: "https://fm.example.com/"}).webSocketURL())
	require.Equal(t, "ws://127.0.0.1:5471/api/ws", (&Client{BaseURL: "http://127.0.0.1:5471"}).webSocketURL())
	require.Equal(t, "wss://host/sub/api/ws", (&Client{BaseURL: "https://host/sub"}).webSocketURL())
}
