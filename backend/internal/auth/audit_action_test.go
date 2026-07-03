package auth

import (
	"net/http"
	"testing"
)

// TestActionForPath covers the native /api/admin/* → action mapping, including
// the generic fallback for paths without an explicit switch case.
func TestActionForPath(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		id         string
		nm         string
		wantAction string
		wantType   string
		wantID     string
	}{
		{"settings update", http.MethodPatch, "/api/admin/settings", "", "", "settings.update", "setting", ""},
		{"user create", http.MethodPost, "/api/admin/users", "", "", "user.create", "user", ""},
		{"user update", http.MethodPatch, "/api/admin/users/5", "5", "", "user.update", "user", "5"},
		{"user delete", http.MethodDelete, "/api/admin/users/5", "5", "", "user.delete", "user", "5"},
		{"storage delete", http.MethodDelete, "/api/admin/storages/3", "3", "", "storage.delete", "storage", "3"},
		{"external update", http.MethodPatch, "/api/admin/external/onlyoffice", "", "onlyoffice", "external.update", "external", "onlyoffice"},
		// Generic fallback: replica isn't in the explicit switch.
		{"generic replica patch", http.MethodPatch, "/api/admin/replica/settings", "", "", "replica.update", "replica", ""},
		{"generic queue retry", http.MethodPost, "/api/admin/queue/abc/retry", "abc", "", "queue.create", "queue", "abc"},
		// Non-admin, non-mutating-significant path → empty.
		{"unmapped path", http.MethodPost, "/api/files/nope", "", "", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, tt, id := ActionForPath(c.method, c.path, c.id, c.nm)
			if a != c.wantAction || tt != c.wantType || id != c.wantID {
				t.Fatalf("ActionForPath(%s %s) = (%q,%q,%q), want (%q,%q,%q)",
					c.method, c.path, a, tt, id, c.wantAction, c.wantType, c.wantID)
			}
		})
	}
}

// TestAIAdminAction verifies the AI-surface path is normalized to the native
// form, mapped via the same switch, and the resulting action carries the "ai."
// prefix so it's distinguishable from native-panel writes.
func TestAIAdminAction(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		id         string
		nm         string
		wantAction string
		wantType   string
		wantID     string
	}{
		{"ai settings update", http.MethodPatch, "/api/ai/admin/settings", "", "", "ai.settings.update", "setting", ""},
		{"ai user delete", http.MethodDelete, "/api/ai/admin/users/7", "7", "", "ai.user.delete", "user", "7"},
		{"ai storage create", http.MethodPost, "/api/ai/admin/storages", "", "", "ai.storage.create", "storage", ""},
		{"ai external update", http.MethodPatch, "/api/ai/admin/external/drawio", "", "drawio", "ai.external.update", "external", "drawio"},
		{"ai generic replica", http.MethodPatch, "/api/ai/admin/replica/settings", "", "", "ai.replica.update", "replica", ""},
		// Bare prefix (no resource segment) → empty action, so no "ai." prefix
		// gets bolted onto an empty string. (Method gating is the caller's job —
		// shouldAudit / auditInvoke filter GETs before this is ever reached.)
		{"ai bare prefix", http.MethodPost, "/api/ai/admin", "", "", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, tt, id := AIAdminAction(c.method, c.path, c.id, c.nm)
			if a != c.wantAction || tt != c.wantType || id != c.wantID {
				t.Fatalf("AIAdminAction(%s %s) = (%q,%q,%q), want (%q,%q,%q)",
					c.method, c.path, a, tt, id, c.wantAction, c.wantType, c.wantID)
			}
		})
	}
}
