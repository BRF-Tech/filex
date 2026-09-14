package handlers_test

import (
	"net/http"
	"testing"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The dashboard's Recent activity names who did each thing — the page prints
// the row's `user_email` under the action. The payload never carried one (the
// audit LIST joins it, the dashboard read the bare rows), so every row on the
// admin's first page said "— · Profile": an activity feed with the actor
// missing from every line.
func TestDashboard_RecentActivityNamesTheActor(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	// Any audited write by the signed-in admin.
	if st, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]string{
		"email": "someone@test.local", "password": "Someone!1", "role": "user",
	}); st != http.StatusOK {
		t.Fatalf("create user: %d %v", st, body)
	}

	st, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/dashboard", nil)
	if st != http.StatusOK {
		t.Fatalf("dashboard: %d", st)
	}
	rows, _ := body["recent_activity"].([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if row["action"] == "user.create" {
			if row["user_email"] != email {
				t.Fatalf("the activity row does not name its actor: user_email=%v (row %v)", row["user_email"], row)
			}
			return
		}
	}
	t.Fatalf("no user.create row in recent_activity: %v", rows)
}
