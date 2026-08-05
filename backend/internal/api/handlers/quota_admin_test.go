package handlers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestAdminQuota_UnknownUserIs404 — /api/admin/quota/{user_id} used to answer
// 500 {"error":"sql: no rows in result set"} for an id that isn't a user.
// usage_bytes/quota_bytes are COALESCE'd columns on `users`, so no-rows can
// only mean "no such user" — that's a 404, not a server fault.
//
// olivov hit this reading the endpoint with *provider* ids (H5, 2026-08-05);
// a 500 gave them nothing to go on, which is the actual cost of the bug.
func TestAdminQuota_UnknownUserIs404(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"get", http.MethodGet, "/api/admin/quota/424242", nil},
		{"set", http.MethodPost, "/api/admin/quota/424242", map[string]any{"quota_bytes": 1024}},
		{"recompute", http.MethodPost, "/api/admin/quota/424242/recompute", nil},
		{"nested get", http.MethodGet, "/api/admin/users/424242/quota", nil},
		{"nested set", http.MethodPost, "/api/admin/users/424242/quota", map[string]any{"quota_bytes": 1024}},
		{"nested recompute", http.MethodPost, "/api/admin/users/424242/quota/recompute", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doJSON(t, client, tc.method, srv.URL+tc.path, tc.body)
			if status != http.StatusNotFound {
				t.Fatalf("want 404 for unknown user, got %d (%v)", status, body)
			}
		})
	}
}

// TestAdminQuota_ExistingUserWithoutQuota — a real user who has never had a
// quota set reads back as unlimited with a 200, not an error.
func TestAdminQuota_ExistingUserWithoutQuota(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	ctx := context.Background()

	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)
	admin, err := store.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("lookup seeded admin: %v", err)
	}

	status, body := doJSON(t, client, http.MethodGet,
		fmt.Sprintf("%s/api/admin/quota/%d", srv.URL, admin.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d (%v)", status, body)
	}
	if body["unlimited"] != true {
		t.Fatalf("want unlimited=true, got %v", body["unlimited"])
	}
	if body["quota_bytes"] != float64(0) {
		t.Fatalf("want quota_bytes=0, got %v", body["quota_bytes"])
	}
}
