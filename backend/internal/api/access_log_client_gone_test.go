package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A 5xx written after the client went away is a 499, not a server failure
// (task #260). The 0.56.3 GitHub gate's one 500 was the bell's unread count:
// WebKit cancelled it as the page was left, the store read returned "context
// canceled", the handler answered 500 and the log blamed the server.

func serveLogged(t *testing.T, ctx context.Context, status int) (int, map[string]string) {
	t.Helper()
	rec := captureAccessLog(t)
	h := Logger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/notifications/unread-count", nil).WithContext(ctx)
	h.ServeHTTP(w, r)
	lines := rec.lines()
	if len(lines) != 1 {
		t.Fatalf("%d access-log lines, want 1", len(lines))
	}
	return w.Code, lines[0]
}

func TestAccessLog_AServerErrorAfterTheClientLeftIs499(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, line := serveLogged(t, ctx, http.StatusInternalServerError)
	if code != StatusClientClosedRequest || line["status"] != "499" {
		t.Errorf("written %d, logged %s; want 499 for a client that left", code, line["status"])
	}
}

func TestAccessLog_AServerErrorWithTheClientStillThereStays500(t *testing.T) {
	code, line := serveLogged(t, context.Background(), http.StatusInternalServerError)
	if code != http.StatusInternalServerError || line["status"] != "500" {
		t.Errorf("written %d, logged %s; want 500", code, line["status"])
	}
}

// A refusal is an answer about the request, whatever became of the client.
func TestAccessLog_ARefusalAfterTheClientLeftStaysARefusal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, line := serveLogged(t, ctx, http.StatusForbidden)
	if line["status"] != "403" {
		t.Errorf("logged %s, want 403", line["status"])
	}
}

// A deadline is the SERVER running out of time: still its failure.
func TestAccessLog_ADeadlineIsStillTheServers(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, line := serveLogged(t, ctx, http.StatusServiceUnavailable)
	if line["status"] != "503" {
		t.Errorf("logged %s, want 503 for a deadline", line["status"])
	}
}
