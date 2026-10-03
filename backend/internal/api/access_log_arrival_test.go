package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// levelRecorder is accessLogRecorder with a level: what a deployment at that
// FILEX_LOG_LEVEL would write.
type levelRecorder struct {
	accessLogRecorder
	min slog.Level
}

func (h *levelRecorder) Enabled(_ context.Context, l slog.Level) bool { return l >= h.min }

func (h *levelRecorder) with(msg string) []map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []map[string]string
	for _, r := range h.records {
		if r.Message != msg {
			continue
		}
		attrs := map[string]string{}
		r.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.String(); return true })
		out = append(out, attrs)
	}
	return out
}

// The access line is written when the answer is FINISHED, so a request that
// arrived and hung left no line at all and could not be told from one the
// browser never sent (task #81: a sign-in page whose i18n chunk the log never
// showed). At debug level the arrival is written as it happens, with the
// socket it came on; at info level nothing changes.
func TestAccessLog_DebugWritesTheArrivalWithItsSocket(t *testing.T) {
	for _, tc := range []struct {
		name   string
		level  slog.Level
		starts int
	}{
		{"debug", slog.LevelDebug, 1},
		{"info", slog.LevelInfo, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &levelRecorder{min: tc.level}
			prev := slog.Default()
			slog.SetDefault(slog.New(h))
			t.Cleanup(func() { slog.SetDefault(prev) })

			started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			srv := LoggerAt("")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				close(started)
				<-release
			}))
			req := httptest.NewRequest(http.MethodGet, "/admin/assets/i18n-x.js", nil)
			req.RemoteAddr = "127.0.0.1:54321"
			go func() {
				srv.ServeHTTP(httptest.NewRecorder(), req)
				close(done)
			}()
			<-started

			// The handler is still holding the request: no finished line yet.
			if got := h.with("http"); len(got) != 0 {
				t.Fatalf("a finished line before the answer: %v", got)
			}
			starts := h.with("http start")
			if len(starts) != tc.starts {
				t.Fatalf("arrival lines = %d, want %d: %v", len(starts), tc.starts, starts)
			}
			if tc.starts == 1 {
				s := starts[0]
				if s["method"] != "GET" || s["path"] != "/admin/assets/i18n-x.js" || s["peer"] != "127.0.0.1:54321" {
					t.Fatalf("the arrival line does not say what came in on which socket: %v", s)
				}
			}

			close(release)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the request never finished")
			}
			if got := h.with("http"); len(got) != 1 {
				t.Fatalf("finished lines = %d, want 1", len(got))
			}
		})
	}
}
