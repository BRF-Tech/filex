package handlers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The Audit page and the Panel's Recent activity say WHICH thing a row is
// about, not only its kind. Release-candidate sweep, 2026-09-21: "Kullanıcı:
// oluşturuldu — Kullanıcı" (no user named), "Depo #1" (an id), and the IP
// column read "127.0.0.1:54452" — the client's source port on every row.
func TestAudit_RowsNameTheirTargetAndCarryNoPort(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	st, made := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]string{
		"email": "ayse@test.local", "password": "Ayse-pass-1!", "role": "user",
	})
	if st != http.StatusOK {
		t.Fatalf("create user: %d %v", st, made)
	}
	uid := fmt.Sprint(made["id"])
	// A row of another resource, which the "user." filter must leave out.
	if st, body := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/settings", map[string]any{"site_name": "x"}); st != http.StatusOK {
		t.Fatalf("settings: %d %v", st, body)
	}
	// An update names its user by the id in its URL (read-time lookup)…
	if st, body := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/users/"+uid, map[string]any{"display_name": "Ayşe"}); st != http.StatusOK {
		t.Fatalf("update: %d %v", st, body)
	}
	if seen := auditTargets(t, client, srv.URL); seen["user.update"] != "ayse@test.local" {
		t.Fatalf("user.update names %q, want the account's e-mail (rows: %v)", seen["user.update"], seen)
	}
	// …and a delete keeps the name after the row is gone (write time).
	if st, body := doJSON(t, client, http.MethodDelete, srv.URL+"/api/admin/users/"+uid, nil); st != http.StatusOK {
		t.Fatalf("delete: %d %v", st, body)
	}
	seen := auditTargets(t, client, srv.URL)
	for _, a := range []string{"user.create", "user.delete"} {
		if seen[a] != "ayse@test.local" {
			t.Fatalf("%s names %q, want the account's e-mail (rows: %v)", a, seen[a], seen)
		}
	}

	st, dash := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/dashboard", nil)
	if st != http.StatusOK {
		t.Fatalf("dashboard: %d", st)
	}
	named := false
	for _, r := range dash["recent_activity"].([]any) {
		row := r.(map[string]any)
		if row["action"] == "user.create" && row["target_name"] == "ayse@test.local" {
			named = true
		}
	}
	if !named {
		t.Fatalf("the Panel's recent activity does not name the created user: %v", dash["recent_activity"])
	}
}

// auditTargets reads the "user." rows (the resource filter) as action → name,
// failing on a row of another resource or an address with a port.
func auditTargets(t *testing.T, client *http.Client, base string) map[string]string {
	t.Helper()
	st, body := doJSON(t, client, http.MethodGet, base+"/api/admin/audit?action=user.", nil)
	if st != http.StatusOK {
		t.Fatalf("audit: %d", st)
	}
	rows, _ := body["entries"].([]any)
	seen := map[string]string{}
	for _, r := range rows {
		row, _ := r.(map[string]any)
		e, _ := row["entry"].(map[string]any)
		action, _ := e["action"].(string)
		if !strings.HasPrefix(action, "user.") {
			t.Fatalf("the resource filter let %q through", action)
		}
		seen[action], _ = row["target_name"].(string)
		if ip, _ := e["ip"].(string); strings.Contains(ip, ":") && !strings.Contains(ip, "::") {
			t.Fatalf("the IP column carries a port: %q", ip)
		}
	}
	return seen
}
