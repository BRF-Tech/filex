package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// The access log (`msg=http`, one line per request) said WHAT was asked and
// never WHO asked or WHICH manager verb it was: `method=POST
// path=/api/files/manager status=500`. With one account behind several tokens
// (a desktop, a CLI, a proxy) and every file operation behind that one path,
// "which client failed, doing what" had no answer in the log.
//
// Logger is the outermost middleware and authentication happens further in,
// on a context Logger never sees — so these tests drive it through a router
// with an inner middleware that authenticates the way the real ones do
// (auth.WithUser / auth.WithToken), followed by the real auth.TenantResolver.

type accessLogRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *accessLogRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *accessLogRecorder) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *accessLogRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *accessLogRecorder) WithGroup(string) slog.Handler      { return h }

// lines returns the attributes of every `http` record, oldest first.
func (h *accessLogRecorder) lines() []map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []map[string]string
	for _, r := range h.records {
		if r.Message != "http" {
			continue
		}
		attrs := map[string]string{}
		r.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.String(); return true })
		out = append(out, attrs)
	}
	return out
}

func captureAccessLog(t *testing.T) *accessLogRecorder {
	t.Helper()
	h := &accessLogRecorder{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// accessLogRouter mounts Logger exactly as BuildRouter does (outermost), then
// an inner "auth" middleware that also rewrites the query string in place the
// way confine.Middleware does, then the real multi-tenant resolver. The caller
// is a real account in a real tenant ("acme"), so the ids below are the
// store's, not constants the test made up. Returns the router and the user id.
func accessLogRouter(t *testing.T) (http.Handler, int64) {
	t.Helper()
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	prov, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateUser(ctx, "someone@example.com", "x", model.RoleUser, "en", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserProvider(ctx, created.ID, prov.ID, ""); err != nil {
		t.Fatal(err)
	}
	u, err := store.GetUser(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	signedIn := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.WithUser(r.Context(), u)
			ctx = auth.WithToken(ctx, &model.APIToken{ID: 42, UserID: u.ID, Label: "cli"})
			// confine.Middleware rewrites r.URL.RawQuery on the SAME url.URL
			// the outer middleware holds.
			r.URL.RawQuery = "path=rewritten"
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	scoped := auth.TenantResolver(store, true)
	r := chi.NewRouter()
	r.Use(Logger)
	r.With(signedIn, scoped).Get("/api/files/manager", ok)
	r.With(signedIn, scoped).Post("/api/files/manager", ok)
	r.With(signedIn, scoped).Get("/api/files/stat", ok)
	r.Get("/api/files/search", ok)
	return r, u.ID
}

func TestAccessLog_NamesTheCallerAndTheVerb(t *testing.T) {
	logs := captureAccessLog(t)
	router, uid := accessLogRouter(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/files/manager?action=search&path=main://&filter=quarterly-salaries&sig=s3cr3t")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	lines := logs.lines()
	if len(lines) != 1 {
		t.Fatalf("want one http line, got %d: %v", len(lines), lines)
	}
	line := lines[0]
	for key, want := range map[string]string{
		"user_id":  strconv.FormatInt(uid, 10),
		"token_id": "42",
		"tenant":   "acme",
		// Read BEFORE the handler: the inner middleware rewrote the query to
		// `path=rewritten`, which has no action at all.
		"action": "search",
		"path":   "/api/files/manager",
		"status": "200",
	} {
		if line[key] != want {
			t.Errorf("%s = %q, want %q (line %v)", key, line[key], want, line)
		}
	}
	// The query string is never repeated: it carries search text, signatures,
	// tickets and PINs on other routes.
	for key, v := range line {
		for _, secret := range []string{"quarterly-salaries", "s3cr3t", "filter", "sig="} {
			if strings.Contains(v, secret) {
				t.Errorf("attribute %s leaks %q from the query string: %q", key, secret, v)
			}
		}
	}
}

func TestAccessLog_ActionIsAManagerVerbOrOther(t *testing.T) {
	logs := captureAccessLog(t)
	router, _ := accessLogRouter(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	do := func(method, path string) map[string]string {
		t.Helper()
		before := len(logs.lines())
		req, err := http.NewRequest(method, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		lines := logs.lines()
		if len(lines) != before+1 {
			t.Fatalf("%s %s: want one new http line, got %d", method, path, len(lines)-before)
		}
		return lines[len(lines)-1]
	}

	// `q` is the manager's legacy spelling of `action` (manager.go List).
	if got := do(http.MethodGet, "/api/files/manager?q=index&path=main://")["action"]; got != "index" {
		t.Errorf("legacy ?q= verb: action = %q, want index", got)
	}
	// The verb every sync client sends every round: logged as itself.
	if got := do(http.MethodGet, "/api/files/manager?action=changes&path=main://&since=x.1")["action"]; got != "changes" {
		t.Errorf("changes: action = %q", got)
	}
	if got := do(http.MethodPost, "/api/files/manager?action=rename")["action"]; got != "rename" {
		t.Errorf("POST verb: action = %q, want rename", got)
	}
	// Anything that is not one of the manager's verbs is not repeated: an
	// unknown value is whatever the caller typed.
	if got := do(http.MethodGet, "/api/files/manager?action=drop%20table%20users")["action"]; got != "other" {
		t.Errorf("unknown verb: action = %q, want other", got)
	}
	// No verb at all (the native ?storage=&parent= listing): no action.
	if line := do(http.MethodGet, "/api/files/manager?storage=1"); line["action"] != "" {
		t.Errorf("no verb: action = %q, want none", line["action"])
	}
	// Only /api/files/manager has verbs. On /api/files/search `q` is the
	// user's search text, and it must never reach the log.
	line := do(http.MethodGet, "/api/files/search?q=payroll+2026&action=search")
	if line["action"] != "" {
		t.Errorf("search route: action = %q, want none", line["action"])
	}
	for key, v := range line {
		if strings.Contains(v, "payroll") {
			t.Errorf("attribute %s leaks the search text: %q", key, v)
		}
	}
	if got := do(http.MethodGet, "/api/files/stat?id=3&action=delete")["action"]; got != "" {
		t.Errorf("other route: action = %q, want none", got)
	}
}

func TestAccessLog_AnonymousRequestHasNoCaller(t *testing.T) {
	logs := captureAccessLog(t)
	router, _ := accessLogRouter(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/files/search?q=x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	lines := logs.lines()
	if len(lines) != 1 {
		t.Fatalf("want one http line, got %d", len(lines))
	}
	for _, key := range []string{"user_id", "token_id", "tenant", "action"} {
		if v, ok := lines[0][key]; ok {
			t.Errorf("anonymous request logged %s=%q", key, v)
		}
	}
}
