package cliclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ── the server's change stream, for `filex sync` ─────────────────────────
//
// The server already tells open explorers the moment a folder changes (the
// realtime hub behind /api/ws). ChangeStream subscribes the sync engine to the
// same announcements with a RECURSIVE, presence-less `watch` on each paired
// server folder, so a save in the browser starts a sync of that folder within
// a second instead of on the next poll.
//
// Authentication is the credential the client already holds: the bearer token
// mints a single-use ticket (POST /api/files/ws-ticket) and the socket is
// opened with that ticket — the same door the embedded explorer uses, which
// is also the one that applies a confined token's root. No second secret.
//
// The stream is an accelerator, never the source of truth. It can drop
// (laptop asleep, proxy restart), and an older server has no `watch` at all;
// every (re)subscribe is reported through OnConnected so the caller can do a
// full pass for whatever it may have missed, and the caller keeps its interval
// poll either way.

// StreamState is what the stream can honestly say about itself.
type StreamState string

const (
	// StreamLive: subscribed; changes arrive as they happen.
	StreamLive StreamState = "connected"
	// StreamPolling: the server cannot announce changes (older than the
	// `watch` subscription, or realtime is switched off). Only the interval
	// poll finds them.
	StreamPolling StreamState = "polling"
	// StreamOffline: the server could not be reached; retrying with backoff.
	StreamOffline StreamState = "offline"
)

// TreeChange is one `tree_change` frame: something changed at or below Root.
// Dirs are relative to Root ("" = Root itself). Overflow means too many
// folders changed to name them — treat the whole root as changed.
type TreeChange struct {
	Root     string
	Dirs     []string
	Overflow bool
	Action   string
	Name     string
}

// ChangeStream follows the server's change stream for a set of watch roots.
type ChangeStream struct {
	Client *Client
	// OnChange receives every change frame. Called from the stream's reader
	// goroutine; it must not block for long.
	OnChange func(TreeChange)
	// OnState receives every state transition, with a human-readable detail.
	OnState func(state StreamState, detail string)
	// OnConnected runs after every successful subscribe — the first one and
	// every reconnect. Changes made while the stream was down were not
	// announced to anybody; the caller answers this by asking what changed.
	OnConnected func()
	// OnUnauthorized is told when the server refuses the client's token
	// (HTTP 401 on the ticket). The stream stops for good: retrying a
	// revoked token only fills the server's log.
	OnUnauthorized func(error)

	// Tunables. Zero values mean the production defaults; tests shrink them.
	PingEvery        time.Duration // keep-alive + dead-connection detection
	AckTimeout       time.Duration // how long to wait for `watching`
	UnsupportedRetry time.Duration // re-probe a server without the stream
	MaxBackoff       time.Duration // ceiling of the reconnect backoff

	mu      sync.Mutex
	roots   []string
	rewatch chan struct{}
	state   StreamState
}

const (
	defaultPingEvery        = 25 * time.Second
	defaultAckTimeout       = 10 * time.Second
	defaultUnsupportedRetry = 10 * time.Minute
	defaultMaxBackoff       = time.Minute
)

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// SetRoots replaces the folders being watched. Safe to call at any time; a
// live connection re-subscribes at once.
func (s *ChangeStream) SetRoots(roots []string) {
	sorted := append([]string(nil), roots...)
	sort.Strings(sorted)
	s.mu.Lock()
	same := strings.Join(sorted, "\x00") == strings.Join(s.roots, "\x00")
	s.roots = sorted
	if s.rewatch == nil {
		s.rewatch = make(chan struct{}, 1)
	}
	ch := s.rewatch
	s.mu.Unlock()
	if same {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *ChangeStream) currentRoots() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.roots...)
}

func (s *ChangeStream) setState(st StreamState, detail string) {
	s.mu.Lock()
	changed := s.state != st
	s.state = st
	s.mu.Unlock()
	if s.OnState != nil && (changed || st != StreamLive) {
		s.OnState(st, detail)
	}
}

// State reports the current state ("" before the first attempt).
func (s *ChangeStream) State() StreamState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// errNoStream marks a server that answered but cannot announce changes.
var errNoStream = errors.New("no change stream")

// Run keeps the stream up until ctx ends: connect, subscribe, read, and on any
// failure back off and do it again.
func (s *ChangeStream) Run(ctx context.Context) {
	s.mu.Lock()
	if s.rewatch == nil {
		s.rewatch = make(chan struct{}, 1)
	}
	s.mu.Unlock()
	attempt := 0
	for ctx.Err() == nil {
		err := s.session(ctx, func() { attempt = 0 })
		if ctx.Err() != nil {
			return
		}
		if IsUnauthorized(err) {
			s.setState(StreamOffline, err.Error())
			if s.OnUnauthorized != nil {
				s.OnUnauthorized(err)
			}
			return
		}
		var wait time.Duration
		if errors.Is(err, errNoStream) {
			s.setState(StreamPolling, err.Error())
			wait = orDefault(s.UnsupportedRetry, defaultUnsupportedRetry)
		} else {
			attempt++
			wait = backoff(attempt, orDefault(s.MaxBackoff, defaultMaxBackoff))
			s.setState(StreamOffline, fmt.Sprintf("%v; retrying in %s", err, wait.Round(time.Second)))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// backoff is 1 s, 2 s, 4 s … up to max, with ±20 % jitter so a server restart
// is not answered by every client in the same second.
func backoff(attempt int, max time.Duration) time.Duration {
	d := time.Second << min(attempt-1, 10)
	if d > max {
		d = max
	}
	j := time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
	return j
}

// session is one connection's life. It returns when the connection ends; the
// error says why. onLive runs once the subscription is acknowledged.
func (s *ChangeStream) session(ctx context.Context, onLive func()) error {
	ticket, err := s.Client.wsTicket(ctx)
	if err != nil {
		return err
	}
	dialCtx, cancelDial := context.WithTimeout(ctx, 20*time.Second)
	conn, _, err := websocket.Dial(dialCtx, s.Client.webSocketURL()+"?ticket="+ticket, &websocket.DialOptions{
		HTTPClient: wsHTTPClient(),
	})
	cancelDial()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	acks := make(chan struct{}, 4)
	readErr := make(chan error, 1)
	go func() {
		for {
			typ, data, err := conn.Read(sctx)
			if err != nil {
				readErr <- err
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var f struct {
				Type     string   `json:"type"`
				Root     string   `json:"root"`
				Dirs     []string `json:"dirs"`
				Overflow bool     `json:"overflow"`
				Action   string   `json:"action"`
				Name     string   `json:"name"`
			}
			if json.Unmarshal(data, &f) != nil {
				continue
			}
			switch f.Type {
			case "watching":
				select {
				case acks <- struct{}{}:
				default:
				}
			case "tree_change":
				if s.OnChange != nil {
					s.OnChange(TreeChange{Root: f.Root, Dirs: f.Dirs, Overflow: f.Overflow, Action: f.Action, Name: f.Name})
				}
			}
		}
	}()

	send := func(roots []string) error {
		if roots == nil {
			roots = []string{}
		}
		raw, _ := json.Marshal(map[string]any{"type": "watch", "paths": roots})
		wctx, wcancel := context.WithTimeout(sctx, 10*time.Second)
		defer wcancel()
		return conn.Write(wctx, websocket.MessageText, raw)
	}

	// Whatever SetRoots signalled before this point is covered by the roots
	// read now; a stale signal would only repeat the same subscription.
	s.mu.Lock()
	rewatch := s.rewatch
	s.mu.Unlock()
	select {
	case <-rewatch:
	default:
	}
	roots := s.currentRoots()
	if err := send(roots); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	// ⚠ The acknowledgement is the capability probe: a server without the
	// `watch` subscription ignores the message and never answers, and silence
	// is indistinguishable from "nothing changed" without it.
	select {
	case <-acks:
	case err := <-readErr:
		return fmt.Errorf("connection closed during subscribe: %w", err)
	case <-time.After(orDefault(s.AckTimeout, defaultAckTimeout)):
		return fmt.Errorf("%w: the server did not acknowledge the watch (filex older than 0.43, or realtime switched off)", errNoStream)
	case <-ctx.Done():
		return ctx.Err()
	}
	onLive()
	s.setState(StreamLive, fmt.Sprintf("watching %d folder(s)", len(roots)))
	if s.OnConnected != nil {
		s.OnConnected()
	}

	ping := time.NewTicker(orDefault(s.PingEvery, defaultPingEvery))
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return ctx.Err()
		case err := <-readErr:
			return fmt.Errorf("connection lost: %w", err)
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(sctx, 15*time.Second)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				return fmt.Errorf("connection went quiet: %w", err)
			}
		case <-rewatch:
			roots = s.currentRoots()
			if err := send(roots); err != nil {
				return fmt.Errorf("re-subscribe: %w", err)
			}
			s.setState(StreamLive, fmt.Sprintf("watching %d folder(s)", len(roots)))
		case <-acks:
		}
	}
}

// wsTicket mints a single-use socket ticket with the client's own token.
func (c *Client) wsTicket(ctx context.Context) (string, error) {
	req, err := c.newRequest(ctx, http.MethodPost, "/api/files/ws-ticket", nil, nil)
	if err != nil {
		return "", err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && (ae.Status == http.StatusNotFound || ae.Status == http.StatusServiceUnavailable || ae.Status == http.StatusMethodNotAllowed) {
			return "", fmt.Errorf("%w: %v", errNoStream, err)
		}
		return "", fmt.Errorf("ticket: %w", err)
	}
	var out struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Ticket == "" {
		return "", fmt.Errorf("%w: ticket response carried no ticket", errNoStream)
	}
	return out.Ticket, nil
}

// webSocketURL is BaseURL with the ws scheme and the socket path.
//
// ⚠ Derived from the address this client already talks to, NOT from the
// `ws_url` the ticket response advertises: that one is built from the server's
// configured public URL, and a sync client pointed at the server some other
// way (a LAN address, a tunnel, a port on localhost) would be sent somewhere
// it cannot reach. /api/ws lives on the same server as every other call this
// client makes.
func (c *Client) webSocketURL() string {
	base := strings.TrimRight(c.BaseURL, "/")
	switch {
	case strings.HasPrefix(base, "https://"):
		base = "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		base = "ws://" + strings.TrimPrefix(base, "http://")
	}
	return base + "/api/ws"
}

// wsHTTPClient is an HTTP/1.1-only client for the WebSocket handshake.
//
// ⚠ Not the API client: that transport negotiates HTTP/2 (it has to — see
// newHTTPClient), and a WebSocket upgrade cannot happen on an HTTP/2
// connection. Against a TLS server that offers h2, reusing it would fail
// every handshake and leave the engine on polling for no visible reason.
func wsHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 15 * time.Second,
		ForceAttemptHTTP2:   false,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
	}}
}
