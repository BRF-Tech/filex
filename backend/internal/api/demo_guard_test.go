package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDemoGuardBlocks is the guard's decision table, path by path.
//
// The HTTP-level proof lives in handlers/demo_exposure_test.go; this one
// exists for the edges that are hard to reach through a router — a prefix that
// merely looks like a guarded one, and the read verbs that must never be
// caught.
func TestDemoGuardBlocks(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		// The operator surface, whichever front door it is behind.
		{"POST", "/api/admin/users", true},
		{"DELETE", "/api/admin/users/2", true},
		{"PATCH", "/api/admin/settings", true},
		{"PUT", "/api/admin/settings/site_name", true},
		{"POST", "/api/ai/admin/users", true},
		{"PATCH", "/api/ai/admin/settings", true},
		// The identity of the SHARED account.
		{"POST", "/api/auth/password", true},
		{"PATCH", "/api/auth/profile", true},
		{"POST", "/api/auth/totp/enroll", true},
		{"POST", "/api/auth/totp/verify", true},

		// Reading the admin panel is the whole point of a demo.
		{"GET", "/api/admin/users", false},
		{"HEAD", "/api/admin/settings", false},
		// A refused CORS preflight would answer the browser's question about
		// the wrong request, with the wrong status.
		{"OPTIONS", "/api/admin/settings", false},

		// The product itself. A visitor uploads, renames, shares and tags —
		// that is what they came for, and the nightly restore is what makes it
		// safe.
		{"POST", "/api/files/manager", false},
		{"POST", "/api/files/upload/init", false},
		{"POST", "/api/files/share", false},
		{"DELETE", "/api/files/share/3", false},
		{"POST", "/api/files/manager/tags", false},
		{"POST", "/api/auth/login", false},
		{"POST", "/api/auth/logout", false},
		{"POST", "/api/tokens", false},

		// ⚠ Segment-aware prefixes. "/api/admin" must not claim a route that
		// merely starts with the same letters.
		{"POST", "/api/administer", false},
		{"POST", "/api/admins", false},
		{"POST", "/api/auth/profiles-export", false},
		// …and must claim the mount point itself.
		{"POST", "/api/admin", true},
	}

	for _, c := range cases {
		if got := demoGuardBlocks(c.method, c.path); got != c.want {
			t.Errorf("demoGuardBlocks(%q, %q) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

// TestDemoGuardIsInertOffADemo is the promise to every ordinary install: with
// the flag off the middleware is the handler it wraps.
func TestDemoGuardIsInertOffADemo(t *testing.T) {
	reached := false
	h := DemoGuard(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/admin/users/2", nil))

	if !reached || rec.Code != http.StatusOK {
		t.Fatalf("an ordinary install must be untouched: reached=%v code=%d", reached, rec.Code)
	}
}

// TestDemoGuardExplainsItself — the refusal is read by a person evaluating the
// product, so it has to say what happened and what to do about it.
func TestDemoGuardExplainsItself(t *testing.T) {
	h := DemoGuard(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("the guarded handler must not run")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/admin/settings", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"public demo", "read-only", "run your own filex"} {
		if !strings.Contains(body, want) {
			t.Fatalf("refusal %q does not mention %q", body, want)
		}
	}
}
