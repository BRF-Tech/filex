package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestLoginTOTPEnforcement verifies that once a user has TOTP enabled, the
// password alone is no longer sufficient — a valid second factor is required
// and no session cookie is issued otherwise.
func TestLoginTOTPEnforcement(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	ctx := context.Background()

	email, password := testutil.SeedAdmin(t, store)
	u, err := store.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	const secret = "JBSWY3DPEHPK3PXP" // valid base32, no padding
	if err := store.SetTotpPendingSecret(ctx, u.ID, secret, []string{"AAAAA-BBBBB"}); err != nil {
		t.Fatalf("set pending: %v", err)
	}
	if err := store.ActivateTotp(ctx, u.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}

	login := func(body map[string]string) (*http.Response, bool) {
		t.Helper()
		raw, _ := json.Marshal(body)
		resp, err := client.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("login post: %v", err)
		}
		hasCookie := false
		for _, c := range resp.Cookies() {
			if c.Name == authlocal.SessionCookieName && c.Value != "" {
				hasCookie = true
			}
		}
		return resp, hasCookie
	}

	// 1. Correct password, NO code → rejected, no cookie.
	resp, cookie := login(map[string]string{"email": email, "password": password})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || cookie {
		t.Fatalf("missing TOTP should be 401 with no cookie; got %d cookie=%v", resp.StatusCode, cookie)
	}

	// 2. Correct password, WRONG code → rejected.
	resp, cookie = login(map[string]string{"email": email, "password": password, "totp": "000000"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || cookie {
		t.Fatalf("wrong TOTP should be 401 with no cookie; got %d cookie=%v", resp.StatusCode, cookie)
	}

	// 3. Correct password + valid live code → success + cookie.
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	resp, cookie = login(map[string]string{"email": email, "password": password, "totp": code})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !cookie {
		t.Fatalf("valid TOTP should be 200 with cookie; got %d cookie=%v", resp.StatusCode, cookie)
	}
}

// TestLoginNoTOTPUnaffected confirms users without TOTP still log in with
// just email + password.
func TestLoginNoTOTPUnaffected(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	testutil.SeedRegularUser(t, store, "plain@test.local", "PlainPass!1")

	raw, _ := json.Marshal(map[string]string{"email": "plain@test.local", "password": "PlainPass!1"})
	resp, err := client.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
