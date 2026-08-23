package server

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// recordingHandler keeps every record so a test can assert on level and
// attributes, the two things the error tracker keys on.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) find(msg string) (slog.Record, map[string]string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message == msg {
			attrs := map[string]string{}
			r.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.String(); return true })
			return r, attrs, true
		}
	}
	return slog.Record{}, nil, false
}

func captureLogs(t *testing.T) *recordingHandler {
	t.Helper()
	h := &recordingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// TestServeListener_ShutdownIsInfo — a listener that returns because the
// server is shutting down is an INFO line. Before this every deploy filed
// "ftps: listener stopped" at ERROR with the error tracker.
func TestServeListener_ShutdownIsInfo(t *testing.T) {
	h := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	serveListener(ctx, "ftps", func() error { return errors.New("accept tcp: use of closed network connection") })

	r, attrs, ok := h.find("ftps: listener stopped")
	if !ok {
		t.Fatal("no listener stopped line")
	}
	if r.Level != slog.LevelInfo {
		t.Errorf("level = %v, want INFO", r.Level)
	}
	if attrs["reason"] != "shutdown" {
		t.Errorf("reason = %q, want shutdown", attrs["reason"])
	}
}

// TestServeListener_CleanReturnIsInfo — ftpserverlib returns nil after Stop;
// that is still a stop worth one INFO line and nothing more.
func TestServeListener_CleanReturnIsInfo(t *testing.T) {
	h := captureLogs(t)
	serveListener(context.Background(), "ftps", func() error { return nil })
	r, attrs, ok := h.find("ftps: listener stopped")
	if !ok || r.Level != slog.LevelInfo {
		t.Fatalf("want INFO stop line, got ok=%v level=%v", ok, r.Level)
	}
	if _, hasErr := attrs["err"]; hasErr {
		t.Errorf("a clean return must not carry err: %v", attrs)
	}
}

// TestServeListener_UnexpectedIsError — a listener that dies while the
// server is still meant to be running is the one case that is an error.
func TestServeListener_UnexpectedIsError(t *testing.T) {
	h := captureLogs(t)
	serveListener(context.Background(), "ftps", func() error { return errors.New("ftpsrv: listen :2121: bind: address already in use") })
	r, attrs, ok := h.find("ftps: listener stopped")
	if !ok {
		t.Fatal("no listener stopped line")
	}
	if r.Level != slog.LevelError {
		t.Errorf("level = %v, want ERROR", r.Level)
	}
	if attrs["reason"] != "unexpected" || attrs["err"] == "" {
		t.Errorf("attrs = %v", attrs)
	}
}

// TestInitWithBackoff_AttemptLevels — intermediate attempts are WARN and
// name the driver, the error and the count; the last one is ERROR.
func TestInitWithBackoff_AttemptLevels(t *testing.T) {
	h := captureLogs(t)
	calls := 0
	err := initWithBackoff(context.Background(), "oidc", func(context.Context) error {
		calls++
		return errors.New("discovery: 502 Bad Gateway")
	}, []time.Duration{0, time.Millisecond, time.Millisecond})
	if err == nil || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}

	warn, wattrs, ok := h.find("driver init attempt failed, will retry")
	if !ok || warn.Level != slog.LevelWarn {
		t.Fatalf("want a WARN retry line, got ok=%v level=%v", ok, warn.Level)
	}
	if wattrs["driver"] != "oidc" || wattrs["attempt"] != "1" || wattrs["of"] != "3" || wattrs["err"] != "discovery: 502 Bad Gateway" {
		t.Errorf("retry attrs = %v", wattrs)
	}

	final, fattrs, ok := h.find("driver init failed after all attempts")
	if !ok || final.Level != slog.LevelError {
		t.Fatalf("want an ERROR final line, got ok=%v level=%v", ok, final.Level)
	}
	if fattrs["driver"] != "oidc" || fattrs["attempt"] != "3" || fattrs["of"] != "3" || fattrs["err"] == "" {
		t.Errorf("final attrs = %v", fattrs)
	}

	h.mu.Lock()
	errors_ := 0
	for _, r := range h.records {
		if r.Level == slog.LevelError {
			errors_++
		}
	}
	h.mu.Unlock()
	if errors_ != 1 {
		t.Errorf("exactly one ERROR line expected, got %d", errors_)
	}
}

// TestInitWithBackoff_SuccessIsQuiet — a first-try success logs nothing at
// WARN or above.
func TestInitWithBackoff_SuccessIsQuiet(t *testing.T) {
	h := captureLogs(t)
	if err := initWithBackoff(context.Background(), "oidc", func(context.Context) error { return nil }, []time.Duration{0, time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level >= slog.LevelWarn {
			t.Errorf("unexpected %v: %s", r.Level, r.Message)
		}
	}
}
