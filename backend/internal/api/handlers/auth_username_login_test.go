package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"testing"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// Dual-side login, measured through the real router rather than through the
// identity package: an account answers to its e-mail OR its username, and the
// two namespaces do not bleed into each other.
//
// The owner's requirement is that every credential works on every surface, so
// the test that matters is the one that drives the surface.

func seedUser(t *testing.T, store db.Store, email, password string) *model.User {
	t.Helper()
	hash, err := authlocal.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := store.CreateUser(context.Background(), email, hash, model.RoleUser, "en", "UTC")
	if err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	return u
}

// login posts an identifier + password and reports the status plus, on
// success, the account /api/auth/me says is signed in.
//
// Each call gets its own cookie jar so a previous success cannot make a later
// assertion pass: without that, "logged in as the wrong user" and "logged in
// as the right one" look identical.
func login(t *testing.T, srv string, identifier, password string) (int, string) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"email": identifier, "password": password})
	resp, err := client.Post(srv+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, ""
	}

	me, err := client.Get(srv + "/api/auth/me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	defer me.Body.Close()
	if me.StatusCode != http.StatusOK {
		t.Fatalf("me: status %d after a 200 login", me.StatusCode)
	}
	var out struct {
		User struct {
			Email    string `json:"email"`
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.NewDecoder(me.Body).Decode(&out); err != nil {
		t.Fatalf("me decode: %v", err)
	}
	return resp.StatusCode, out.User.Email
}

func TestLoginAcceptsEitherEmailOrUsername(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)

	const pw = "DualSidePass!1"
	u := seedUser(t, store, "ada@example.com", pw)

	// The account was named at creation time — no separate step, because the
	// store wrapper owns it (identitystore). If this is empty, every protocol
	// login below is unreachable for this account.
	if u.Username == "" {
		t.Fatal("a freshly created account has no username; identitystore is not wrapping CreateUser")
	}
	if u.Username != "ada" {
		t.Fatalf("username = %q, want %q (derived from the e-mail local part)", u.Username, "ada")
	}

	if code, email := login(t, srv.URL, "ada@example.com", pw); code != http.StatusOK || email != "ada@example.com" {
		t.Errorf("login by e-mail: code=%d email=%q, want 200 / ada@example.com", code, email)
	}
	if code, email := login(t, srv.URL, "ada", pw); code != http.StatusOK || email != "ada@example.com" {
		t.Errorf("login by username: code=%d email=%q, want 200 / ada@example.com", code, email)
	}
	// Case folding: FileZilla and WinSCP happily send whatever the user typed.
	if code, _ := login(t, srv.URL, "ADA", pw); code != http.StatusOK {
		t.Errorf("login by uppercase username: code=%d, want 200", code)
	}
}

func TestLoginRejectsWrongPasswordAndUnknownIdentifier(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	const pw = "DualSidePass!1"
	seedUser(t, store, "ada@example.com", pw)

	if code, _ := login(t, srv.URL, "ada", "wrong-password"); code != http.StatusUnauthorized {
		t.Errorf("username + wrong password: code=%d, want 401", code)
	}
	if code, _ := login(t, srv.URL, "nobodyhere", pw); code != http.StatusUnauthorized {
		t.Errorf("unknown username: code=%d, want 401", code)
	}
	// A structurally invalid username must look exactly like a missing one:
	// a login form may not distinguish "you typed nonsense" from "no such
	// account".
	if code, _ := login(t, srv.URL, "NOT A USERNAME/x", pw); code != http.StatusUnauthorized {
		t.Errorf("malformed identifier: code=%d, want 401", code)
	}
}

// The disambiguation rule is that `@` decides, and the lookup never tries the
// other namespace. This is the test that would catch a "fall back to the other
// one" convenience being added later — which is how one person's username
// could start answering for another person's e-mail.
func TestUsernameAndEmailNamespacesDoNotBleed(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	const pw = "DualSidePass!1"

	first := seedUser(t, store, "grace@example.com", pw) // → username "grace"
	second := seedUser(t, store, "grace@other.test", pw) // → "grace" taken, so "grace2"

	if first.Username != "grace" {
		t.Fatalf("first username = %q, want grace", first.Username)
	}
	if second.Username != "grace2" {
		t.Fatalf("second username = %q, want grace2 (collision suffix)", second.Username)
	}

	if code, email := login(t, srv.URL, "grace", pw); code != http.StatusOK || email != "grace@example.com" {
		t.Errorf("bare name must resolve as a USERNAME: code=%d email=%q", code, email)
	}
	if code, email := login(t, srv.URL, "grace@other.test", pw); code != http.StatusOK || email != "grace@other.test" {
		t.Errorf("an address must resolve as an E-MAIL: code=%d email=%q", code, email)
	}
	if code, email := login(t, srv.URL, "grace2", pw); code != http.StatusOK || email != "grace@other.test" {
		t.Errorf("suffixed username must reach the second account: code=%d email=%q", code, email)
	}
}

// Changing the username is a self-service action, and unlike the other profile
// fields it must FAIL LOUDLY: a form that appears to save a name the account
// did not get sends the user off to configure an SFTP client with a login that
// does not exist.
func TestProfileUsernameChange(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	const pw = "DualSidePass!1"
	seedUser(t, store, "ada@example.com", pw)
	seedUser(t, store, "grace@example.com", pw)
	testutil.LoginAs(t, srv, client, "ada@example.com", pw)

	patch := func(name string) int {
		body, _ := json.Marshal(map[string]string{"username": name})
		req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/auth/profile", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("patch: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if code := patch("lovelace"); code != http.StatusOK {
		t.Fatalf("valid rename: code=%d, want 200", code)
	}
	if code, email := login(t, srv.URL, "lovelace", pw); code != http.StatusOK || email != "ada@example.com" {
		t.Errorf("the new name must work immediately: code=%d email=%q", code, email)
	}
	// The old name is gone, not an alias — otherwise a released name could be
	// claimed by someone else while the previous owner still answers to it.
	if code, _ := login(t, srv.URL, "ada", pw); code != http.StatusUnauthorized {
		t.Errorf("old username still logs in: code=%d, want 401", code)
	}

	// Measured: with the handler's pre-check disabled this still answers 409,
	// because the unique index rejects the UPDATE and the handler reports that
	// too. The pre-check buys a clean message, not the guarantee.
	if code := patch("grace"); code != http.StatusConflict {
		t.Errorf("taken name: code=%d, want 409", code)
	}
	if code := patch("root"); code != http.StatusBadRequest {
		t.Errorf("reserved name: code=%d, want 400", code)
	}
	if code := patch("NOT valid"); code != http.StatusBadRequest {
		t.Errorf("malformed name: code=%d, want 400", code)
	}
	// After the rejections the account must still hold the name it accepted.
	if code, _ := login(t, srv.URL, "lovelace", pw); code != http.StatusOK {
		t.Errorf("a rejected rename must not disturb the current name: code=%d", code)
	}
}

// A reserved name is not a reason to refuse an account — the collision loop
// simply moves past it. `admin@…` is the address every first install uses, so
// this path runs on day one of every deployment.
func TestReservedDerivedNameGetsASuffixInsteadOfFailing(t *testing.T) {
	_, _, store := testutil.NewTestServer(t)
	u := seedUser(t, store, "admin@example.com", "DualSidePass!1")
	if u.Username != "admin2" {
		t.Fatalf("username = %q, want admin2 (admin is reserved)", u.Username)
	}
}
