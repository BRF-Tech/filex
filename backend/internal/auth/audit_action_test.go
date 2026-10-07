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
		// Roles (internal/perm). /roles/builtin must not read as a role update.
		{"builtin role", http.MethodPut, "/api/admin/roles/builtin", "", "", "permissions.defaults_set", "permissions", ""},
		{"role create", http.MethodPost, "/api/admin/roles", "", "", "permission_rule.create", "permission_rule", ""},
		{"role update", http.MethodPut, "/api/admin/roles/4", "4", "", "permission_rule.update", "permission_rule", "4"},
		{"role delete", http.MethodDelete, "/api/admin/roles/4", "4", "", "permission_rule.delete", "permission_rule", "4"},
		{"user exceptions", http.MethodPut, "/api/admin/users/5/exceptions", "5", "", "user.permissions_set", "user", "5"},
		{"user roles", http.MethodPut, "/api/admin/users/5/roles", "5", "", "user.roles_set", "user", "5"},
		// Groups (internal/group). Member routes must not read as a group update.
		{"group create", http.MethodPost, "/api/admin/groups", "", "", "group.create", "group", ""},
		{"group update", http.MethodPut, "/api/admin/groups/2", "2", "", "group.update", "group", "2"},
		{"group delete", http.MethodDelete, "/api/admin/groups/2", "2", "", "group.delete", "group", "2"},
		{"group members add", http.MethodPost, "/api/admin/groups/2/members", "2", "", "group.members_add", "group", "2"},
		{"group member remove", http.MethodDelete, "/api/admin/groups/2/members/7", "2", "", "group.member_remove", "group", "2"},
		// A group's grant and a person's share numbers, not a resource.
		{"group grant revoke", http.MethodDelete, "/api/admin/grants/groups/5", "5", "", "group_grant.delete", "group_grant", "5"},
		{"person grant revoke", http.MethodDelete, "/api/admin/grants/5", "5", "", "grants.delete", "grants", "5"},
		// Tenants: a storage link names the tenant in its path, and must not
		// read as a tenant made or removed (the fallback's create / delete).
		{"tenant create", http.MethodPost, "/api/admin/providers", "", "", "providers.create", "providers", ""},
		{"tenant update", http.MethodPatch, "/api/admin/providers/3", "3", "", "providers.update", "providers", "3"},
		{"tenant storage link", http.MethodPost, "/api/admin/providers/3/storages", "3", "", "providers.storage_link", "providers", "3"},
		{"tenant storage unlink", http.MethodDelete, "/api/admin/providers/3/storages/9", "3", "", "providers.storage_unlink", "providers", "3"},
		{"tenant delete", http.MethodDelete, "/api/admin/providers/3", "3", "", "providers.delete", "providers", "3"},
		// Generic fallback: replica isn't in the explicit switch.
		{"generic replica patch", http.MethodPatch, "/api/admin/replica/settings", "", "", "replica.update", "replica", ""},
		{"generic queue retry", http.MethodPost, "/api/admin/queue/abc/retry", "abc", "", "queue.create", "queue", "abc"},
		// Who may encrypt writes its own rows (internal/e2epolicy audit.go):
		// the middleware names none, at the root or below it.
		{"e2e policy", http.MethodPatch, "/api/admin/e2e", "", "", "", "", ""},
		{"e2e policy, slash", http.MethodPatch, "/api/admin/e2e/", "", "", "", "", ""},
		{"e2e ceiling", http.MethodPatch, "/api/admin/e2e/tenants/3", "3", "", "", "", ""},
		{"e2e approval", http.MethodPost, "/api/admin/e2e/requests/9/approve", "9", "", "", "", ""},
		// A person's own recent searches (task #168) are not an admin change.
		{"recent search kept", http.MethodPost, "/api/admin/panel-search/recent", "", "", "", "", ""},
		{"recent search removed", http.MethodDelete, "/api/admin/panel-search/recent/4", "4", "", "", "", ""},
		{"recent searches cleared", http.MethodDelete, "/api/admin/panel-search/recent", "", "", "", "", ""},
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
		// A group's grant keeps its own action on the MCP / REST mirror too.
		{"ai group grant revoke", http.MethodDelete, "/api/ai/admin/grants/groups/5", "5", "", "ai.group_grant.delete", "group_grant", "5"},
		// The sign-in limit's families keep ONE name whatever the door: the
		// Sign-in security page's trail reads `login.` and `login_security.`,
		// and the door is in the row's `via` (DoorAction).
		{"ai sign-in unlock", http.MethodPost, "/api/ai/admin/login-security/unlock", "", "", "login.unlocked", "login", ""},
		{"ai sign-in settings", http.MethodPatch, "/api/ai/admin/login-security", "", "", "login_security.update", "login_security", ""},
		{"ai tenant storage link", http.MethodPost, "/api/ai/admin/providers/3/storages", "3", "", "ai.providers.storage_link", "providers", "3"},
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

func TestDoorAction(t *testing.T) {
	for _, c := range []struct {
		in   string
		ai   bool
		want string
	}{
		{"settings.update", false, "settings.update"},
		{"settings.update", true, "ai.settings.update"},
		{"login.unlocked", true, "login.unlocked"},
		{"login_security.update", true, "login_security.update"},
		{"loginx.update", true, "ai.loginx.update"},
		{"", true, ""},
	} {
		if got := DoorAction(c.in, c.ai); got != c.want {
			t.Fatalf("DoorAction(%q, %v) = %q, want %q", c.in, c.ai, got, c.want)
		}
	}
}
