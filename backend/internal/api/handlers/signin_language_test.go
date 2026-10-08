package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// An account that holds no language is given the one its sign-in arrived in,
// by the server, at the sign-in (handlers/auth.go adoptSignInLanguage). Up to
// the 0.54 full run (001b652e) the web panel wrote its screen's language to
// such an account after the sign-in, from the page the sign-in was leaving,
// and the browser suites hung on the half-sent write.

// seedLanguageless makes an account holding `locale` ("" = none).
func seedLanguageless(t *testing.T, store db.Store, email, password, locale string) int64 {
	t.Helper()
	hash, err := authlocal.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := store.CreateUser(context.Background(), email, hash, "user", locale, "UTC")
	if err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	return u.ID
}

// signInSpeaking posts a password sign-in carrying acceptLanguage and returns the
// locale the answer's user carries.
func signInSpeaking(t *testing.T, srv, email, password, acceptLanguage string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequest(http.MethodPost, srv+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if acceptLanguage != "" {
		req.Header.Set("Accept-Language", acceptLanguage)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d", email, resp.StatusCode)
	}
	var out struct {
		User struct {
			Locale string `json:"locale"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.User.Locale
}

func accountLocaleOf(t *testing.T, store db.Store, id int64) string {
	t.Helper()
	u, err := store.GetUser(context.Background(), id)
	if err != nil {
		t.Fatalf("get user %d: %v", id, err)
	}
	return u.Locale
}

func TestSignIn_AnAccountWithNoLanguageTakesTheSignInsLanguage(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	const pw = "SignInLanguagePass!1"
	id := seedLanguageless(t, store, "yeni@example.com", pw, "")

	if got := signInSpeaking(t, srv.URL, "yeni@example.com", pw, "tr-TR,tr;q=0.9,en;q=0.8"); got != "tr" {
		t.Errorf("the sign-in's answer says locale %q, want tr", got)
	}
	if got := accountLocaleOf(t, store, id); got != "tr" {
		t.Errorf("the account holds %q after the sign-in, want tr", got)
	}

	// Decided once: a later sign-in in another language changes nothing.
	if got := signInSpeaking(t, srv.URL, "yeni@example.com", pw, "en-US,en;q=0.9"); got != "tr" {
		t.Errorf("a second sign-in's answer says %q, want the account's tr", got)
	}
	if got := accountLocaleOf(t, store, id); got != "tr" {
		t.Errorf("a second sign-in changed the account to %q, want tr", got)
	}
}

func TestSignIn_AnAccountWithALanguageKeepsIt(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	const pw = "SignInLanguagePass!1"
	id := seedLanguageless(t, store, "eski@example.com", pw, "en")

	if got := signInSpeaking(t, srv.URL, "eski@example.com", pw, "tr-TR,tr;q=0.9"); got != "en" {
		t.Errorf("the sign-in's answer says locale %q, want the account's en", got)
	}
	if got := accountLocaleOf(t, store, id); got != "en" {
		t.Errorf("the sign-in changed the account's language to %q, want en", got)
	}
}

func TestSignIn_ALanguageNotOfferedLeavesTheAccountWithoutOne(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	const pw = "SignInLanguagePass!1"
	id := seedLanguageless(t, store, "uzak@example.com", pw, "")

	// Neither language is shipped nor added by a pack here: the account reads
	// the instance's until somebody picks one.
	signInSpeaking(t, srv.URL, "uzak@example.com", pw, "xx-YY,zz;q=0.5")
	if got := accountLocaleOf(t, store, id); got != "" {
		t.Errorf("an unoffered language was stored as %q, want none", got)
	}
	// No header at all: the same.
	signInSpeaking(t, srv.URL, "uzak@example.com", pw, "")
	if got := accountLocaleOf(t, store, id); got != "" {
		t.Errorf("a sign-in with no Accept-Language stored %q, want none", got)
	}
}
