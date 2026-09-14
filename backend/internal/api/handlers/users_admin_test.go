package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// doJSON issues an authenticated (cookie-jar) request and returns the status
// and decoded body.
func doJSON(t *testing.T, client *http.Client, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestLastAdminProtection exercises finding #3: the final admin can't be
// deleted, but once a second admin exists the first becomes deletable.
func TestLastAdminProtection(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	ctx := context.Background()

	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	admin, _ := store.GetUserByEmail(ctx, email)

	// Deleting the only admin (here, self) must be refused with 409.
	status, _ := doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/admin/users/%d", srv.URL, admin.ID), nil)
	if status != http.StatusConflict {
		t.Fatalf("delete last admin: want 409, got %d", status)
	}

	// Invalid role on create must be rejected.
	status, _ = doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]string{
		"email": "x@test.local", "password": "Whatever!1", "role": "superuser",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("invalid role create: want 400, got %d", status)
	}

	// Create a second admin (with a display name).
	status, created := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]string{
		"email": "admin2@test.local", "password": "SecondAdmin!1", "role": "admin", "display_name": "Second Admin",
	})
	if status != http.StatusOK {
		t.Fatalf("create second admin: want 200, got %d", status)
	}
	if created["display_name"] != "Second Admin" {
		t.Fatalf("display_name not returned on create: %v", created["display_name"])
	}

	// Now the first admin is no longer the last → deletable.
	status, _ = doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/admin/users/%d", srv.URL, admin.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("delete non-last admin: want 200, got %d", status)
	}
}

// TestDisplayNameRoundTrip exercises finding #4: display_name persists through
// create → get → update.
func TestDisplayNameRoundTrip(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	ctx := context.Background()

	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	status, _ := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]string{
		"email": "named@test.local", "password": "NamedUser!1", "display_name": "Original",
	})
	if status != http.StatusOK {
		t.Fatalf("create: want 200, got %d", status)
	}
	u, _ := store.GetUserByEmail(ctx, "named@test.local")

	status, got := doJSON(t, client, http.MethodGet, fmt.Sprintf("%s/api/admin/users/%d", srv.URL, u.ID), nil)
	if status != http.StatusOK || got["display_name"] != "Original" {
		t.Fatalf("get: want display_name=Original, got status=%d val=%v", status, got["display_name"])
	}

	status, _ = doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/admin/users/%d", srv.URL, u.ID), map[string]string{
		"display_name": "Renamed",
	})
	if status != http.StatusOK {
		t.Fatalf("patch: want 200, got %d", status)
	}
	refreshed, _ := store.GetUser(ctx, u.ID)
	if refreshed.DisplayName != "Renamed" {
		t.Fatalf("update did not persist display_name: %q", refreshed.DisplayName)
	}
}

// TestCreateUserWithoutPassword — issue #25. The admin "Add user" dialog marks
// the password optional, which is how an SSO account is added ahead of its
// first sign-in, and the server refused every such request with "email and
// password required". An account created without a password must exist, must
// refuse every password (it has none to match), and must become usable with
// the password an admin then resets it to — the reporter's WebDAV case.
func TestCreateUserWithoutPassword(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	ctx := context.Background()

	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]string{
		"email": "sso-only@test.local", "display_name": "SSO Only", "role": "user",
	})
	if status != http.StatusOK {
		t.Fatalf("create without a password: want 200, got %d (%v)", status, body)
	}
	u, err := store.GetUserByEmail(ctx, "sso-only@test.local")
	if err != nil || u == nil {
		t.Fatalf("account not created: %v", err)
	}
	if u.PasswordHash != "" {
		t.Fatalf("an account created without a password must not have one")
	}

	for _, guess := range []string{"", "x", "SSO Only"} {
		st, _ := doJSON(t, &http.Client{}, http.MethodPost, srv.URL+"/api/auth/login", map[string]string{
			"email": "sso-only@test.local", "password": guess,
		})
		if st == http.StatusOK {
			t.Fatalf("an account without a password accepted %q", guess)
		}
	}

	status, reset := doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/api/admin/users/%d/reset-password", srv.URL, u.ID), nil)
	fresh, _ := reset["new_password"].(string)
	if status != http.StatusOK || fresh == "" {
		t.Fatalf("reset-password: want 200 with new_password, got %d (%v)", status, reset)
	}
	st, _ := doJSON(t, &http.Client{}, http.MethodPost, srv.URL+"/api/auth/login", map[string]string{
		"email": "sso-only@test.local", "password": fresh,
	})
	if st != http.StatusOK {
		t.Fatalf("the reset password does not sign in: %d", st)
	}

	status, _ = doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]string{"password": "NoEmail!1"})
	if status != http.StatusBadRequest {
		t.Fatalf("create without an e-mail: want 400, got %d", status)
	}
}
