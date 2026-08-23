package observability

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

// captureTransport keeps every event the SDK would have sent, so a test can
// look at the wire shape without a network.
type captureTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *captureTransport) Flush(time.Duration) bool              { return true }
func (t *captureTransport) FlushWithContext(context.Context) bool { return true }
func (t *captureTransport) Configure(sentry.ClientOptions)        {}
func (t *captureTransport) Close()                                {}
func (t *captureTransport) SendEvent(e *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, e)
}

func (t *captureTransport) last(tb testing.TB) *sentry.Event {
	tb.Helper()
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.events) == 0 {
		tb.Fatal("no event captured")
	}
	return t.events[len(t.events)-1]
}

func (t *captureTransport) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.events)
}

// newBridge wires a throw-away Sentry client to the capture transport and
// returns a logger that goes through the slog bridge, plus the transport.
func newBridge(t *testing.T) (*slog.Logger, *captureTransport) {
	t.Helper()
	tr := &captureTransport{}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:       "https://key@example.invalid/1",
		Transport: tr,
		Release:   "test",
	})
	if err != nil {
		t.Fatalf("sentry init: %v", err)
	}
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })
	return slog.New(WrapSlog(slog.NewTextHandler(io.Discard, nil))), tr
}

// TestBridge_AttributesTravelWithTheEvent — the reason the bridge exists: a
// "thumb generate failed" event is a question until `path` and `err` are on
// it. Every attribute must reach the event as a tag (short values) and in the
// `log` context (all of them). Before this, the event carried no tags at all:
// GlitchTip showed eleven identical "thumb generate failed" events with no
// file and no error in the list or the API.
func TestBridge_AttributesTravelWithTheEvent(t *testing.T) {
	log, tr := newBridge(t)
	log.Error("thumb generate failed",
		slog.String("path", "/a/b.webm"),
		slog.Int64("node", 4173),
		slog.String("err", "thumb: ffmpeg: exit status 234"))

	ev := tr.last(t)
	if ev.Message != "thumb generate failed" {
		t.Fatalf("message = %q", ev.Message)
	}
	if ev.Level != sentry.LevelError {
		t.Fatalf("level = %q, want error", ev.Level)
	}
	for k, want := range map[string]string{"path": "/a/b.webm", "node": "4173", "err": "thumb: ffmpeg: exit status 234"} {
		if got := ev.Tags[k]; got != want {
			t.Errorf("tag %s = %q, want %q (tags=%v)", k, got, want, ev.Tags)
		}
	}
	logCtx := ev.Contexts["log"]
	if logCtx == nil {
		t.Fatalf("no log context on event: %v", ev.Contexts)
	}
	if logCtx["path"] != "/a/b.webm" || logCtx["err"] != "thumb: ffmpeg: exit status 234" {
		t.Errorf("log context = %v", logCtx)
	}
	if src, _ := logCtx["source"].(string); !strings.HasPrefix(src, "sentry_test.go:") {
		t.Errorf("source = %q, want this test file", src)
	}
}

// TestBridge_LongValuesStayOutOfTags — an ffmpeg transcript in `err` is pages
// long and Sentry caps a tag at 200 characters. The tag keeps the first line
// when it is short, the context keeps the whole thing.
func TestBridge_LongValuesStayOutOfTags(t *testing.T) {
	log, tr := newBridge(t)
	long := "thumb: ffmpeg: exit status 234\n" + strings.Repeat("configuration: --enable-everything\n", 20)
	blob := strings.Repeat("x", 500)
	log.Error("thumb generate failed", slog.String("err", long), slog.String("blob", blob))

	ev := tr.last(t)
	if got := ev.Tags["err"]; got != "thumb: ffmpeg: exit status 234" {
		t.Errorf("err tag = %q, want the first line", got)
	}
	if _, ok := ev.Tags["blob"]; ok {
		t.Errorf("a 500-character value must not become a tag: %v", ev.Tags)
	}
	if ev.Contexts["log"]["err"] != long {
		t.Errorf("context must keep the full error")
	}
	if ev.Contexts["log"]["blob"] != blob {
		t.Errorf("context must keep the long value")
	}
}

// TestBridge_CredentialsAreFiltered — a key that names a credential never
// reaches the event with its value, in the tag or in the context.
func TestBridge_CredentialsAreFiltered(t *testing.T) {
	log, tr := newBridge(t)
	log.With(slog.String("client_secret", "s3cr3t")).Error("oidc init failed",
		slog.String("token", "tok"),
		slog.String("Authorization", "Bearer abc"),
		slog.String("cookie", "session=1"),
		slog.String("access_key", "AKIA"),
		slog.String("password", "pw"),
		slog.String("driver", "oidc"),
		slog.String("err", "boom"))

	ev := tr.last(t)
	for _, k := range []string{"client_secret", "token", "Authorization", "cookie", "access_key", "password"} {
		if got := ev.Tags[k]; got != filteredValue {
			t.Errorf("tag %s = %q, want %q", k, got, filteredValue)
		}
		if got := ev.Contexts["log"][k]; got != filteredValue {
			t.Errorf("context %s = %q, want %q", k, got, filteredValue)
		}
	}
	if ev.Tags["driver"] != "oidc" || ev.Tags["err"] != "boom" {
		t.Errorf("ordinary attributes must survive: %v", ev.Tags)
	}
	for _, v := range ev.Tags {
		if strings.Contains(v, "s3cr3t") || strings.Contains(v, "AKIA") || strings.Contains(v, "Bearer") {
			t.Fatalf("secret leaked into tags: %v", ev.Tags)
		}
	}
}

// TestBridge_WarnWithoutErrorIsNotAnIssue — the forwarding rule is unchanged:
// WARN is an issue only when it carries an error; INFO never is.
func TestBridge_WarnWithoutErrorIsNotAnIssue(t *testing.T) {
	log, tr := newBridge(t)
	log.Warn("ftps: disabled (FILEX_FTPS=0)")
	log.Info("ftps: listener stopped", slog.String("err", "closed"))
	if n := tr.count(); n != 0 {
		t.Fatalf("captured %d events, want 0", n)
	}
	log.Warn("driver init attempt failed", slog.String("driver", "oidc"), slog.Int("attempt", 2), slog.String("err", "502"))
	if n := tr.count(); n != 1 {
		t.Fatalf("captured %d events, want 1", n)
	}
	ev := tr.last(t)
	if ev.Level != sentry.LevelWarning || ev.Tags["attempt"] != "2" || ev.Tags["driver"] != "oidc" {
		t.Errorf("event = level %q tags %v", ev.Level, ev.Tags)
	}
}

func TestSensitiveKey(t *testing.T) {
	for k, want := range map[string]bool{
		"path": false, "err": false, "driver": false, "node": false, "keyring": false, "storage_keys_count": false,
		"key": true, "s3_key": true, "apiKey": true, "api_key": true, "secret": true, "client_secret": true,
		"token": true, "X-Filex-Token": true, "password": true, "passwd": true, "authorization": true,
		"cookie": true, "credentials": true, "private_key": true,
	} {
		if got := sensitiveKey(k); got != want {
			t.Errorf("sensitiveKey(%q) = %v, want %v", k, got, want)
		}
	}
}
