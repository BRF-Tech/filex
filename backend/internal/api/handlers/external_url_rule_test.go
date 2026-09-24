package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// An external service's address is an address or nothing. "bu-bir-adres-
// degil" was stored and then probed (release-candidate sweep, 2026-09-22,
// QA #38 — a form that accepts what can never work). The refusal is said in
// the reader's language; the admin page checks the same rule first.
func TestExternal_RefusesAnAddressThatIsNotOne(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)
	// The reader's language is the account's (requestLang).
	if st, body := doJSON(t, client, http.MethodPatch, srv.URL+"/api/auth/profile", map[string]any{"locale": "tr"}); st != http.StatusOK {
		t.Fatalf("profile: %d %v", st, body)
	}

	patch := func(body map[string]any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/admin/external/onlyoffice", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept-Language", "tr")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	st, body := patch(map[string]any{"url": "bu-bir-adres-degil", "enabled": true})
	if st != http.StatusBadRequest || body["error"] != "url_invalid" {
		t.Fatalf("an address that is not one was accepted: %d %v", st, body)
	}
	if msg, _ := body["message"].(string); msg != "http:// ya da https:// ile başlayan tam bir adres girin ya da boş bırakın." {
		t.Fatalf("the refusal is not the catalogue's Turkish sentence: %q", body["message"])
	}
	if st, body := patch(map[string]any{"callback_url": "ftp://x"}); st != http.StatusBadRequest {
		t.Fatalf("a callback address that is not http(s) was accepted: %d %v", st, body)
	}
	for _, ok := range []string{"https://office.example.com", "http://onlyoffice:80", ""} {
		if st, body := patch(map[string]any{"url": ok}); st != http.StatusOK {
			t.Fatalf("%q refused: %d %v", ok, st, body)
		}
	}
}
