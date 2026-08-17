package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/s3api"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The access-key surface, driven through the real router. The unit tests in
// internal/protocolauth pin the issuing RULES; these pin that the endpoint
// applies them, hands the secret back exactly once, and cannot be pointed at
// somebody else's account.

func newKeyServer(t *testing.T) (*httptest.Server, *http.Client, db.Store) {
	t.Helper()
	return testutil.NewTestServerWith(t, func(c *config.Config) {}, func(d *api.Deps) {
		// A real box: without one, issuing refuses — which is its own case in
		// the protocolauth tests.
		box, err := secretbox.New("test-environment-secret-key")
		if err != nil {
			t.Fatalf("secretbox: %v", err)
		}
		d.ProtocolAuth = protocolauth.New(d.Store, d.ACL, d.Cfg.MultiTenant)
		d.ProtocolAuth.Secrets = box
	})
}

func jarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}

func postJSON(t *testing.T, c *http.Client, url string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := c.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestS3KeyIsMintedAndTheSecretIsShownOnce(t *testing.T) {
	srv, client, store := newKeyServer(t)
	const pw = "KeyPass!1"
	seedUser(t, store, "keys@example.com", pw)
	testutil.LoginAs(t, srv, client, "keys@example.com", pw)

	code, body := postJSON(t, client, srv.URL+"/api/auth/s3-keys", map[string]any{"label": "rclone"})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, body)
	}
	secret, _ := body["secret"].(string)
	if secret == "" {
		t.Fatal("no secret in the create response — the credential would be unusable")
	}

	// …and never again. A listing that could return it would make every
	// read-only view of this page a credential leak.
	resp, err := client.Get(srv.URL + "/api/auth/s3-keys")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp.Body.Close()
	raw := new(bytes.Buffer)
	if _, err := raw.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read list: %v", err)
	}
	if bytes.Contains(raw.Bytes(), []byte(secret)) {
		t.Fatal("the listing endpoint returned the secret")
	}
	if bytes.Contains(raw.Bytes(), []byte("secret_enc")) {
		t.Error("the listing endpoint returned the sealed secret — it has no business on the wire either")
	}
	// An empty collection must still be a collection, not null (the shape that
	// broke three endpoints this month).
	if !bytes.Contains(raw.Bytes(), []byte(`"keys":[`)) {
		t.Errorf("listing shape = %s, want a keys array", raw.String())
	}
}

// A key minted against someone else's token would inherit their permissions.
func TestS3KeyCannotBeMintedAgainstAnotherAccountsToken(t *testing.T) {
	srv, client, store := newKeyServer(t)
	const pw = "KeyPass!1"
	seedUser(t, store, "mine@example.com", pw)
	theirs := seedUser(t, store, "theirs@example.com", pw)

	plain := "tok_" + "abcdabcdabcdabcdabcdabcdabcdabcd"
	other, err := store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID: theirs.ID, Label: "theirs", TokenHash: apitoken.HashToken(plain),
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	testutil.LoginAs(t, srv, client, "mine@example.com", pw)
	code, _ := postJSON(t, client, srv.URL+"/api/auth/s3-keys", map[string]any{
		"label": "steal", "api_token_id": other.ID,
	})
	if code != http.StatusNotFound {
		t.Fatalf("minting against another account's token = %d, want 404", code)
	}
}

// Another account's key must look ABSENT, not forbidden: a 403 would turn the
// endpoint into a way to enumerate other people's credentials.
func TestS3KeyOfAnotherAccountIs404NotForbidden(t *testing.T) {
	srv, client, store := newKeyServer(t)
	const pw = "KeyPass!1"
	seedUser(t, store, "a@example.com", pw)
	seedUser(t, store, "b@example.com", pw)

	testutil.LoginAs(t, srv, client, "b@example.com", pw)
	code, body := postJSON(t, client, srv.URL+"/api/auth/s3-keys", map[string]any{"label": "b"})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, body)
	}
	key, _ := body["key"].(map[string]any)
	id := int64(key["id"].(float64))

	other := jarClient(t)
	testutil.LoginAs(t, srv, other, "a@example.com", pw)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/auth/s3-keys/"+strconv.FormatInt(id, 10), nil)
	resp, err := other.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("deleting another account's key = %d, want 404", resp.StatusCode)
	}

	// And it is still there for its owner.
	//
	// Measured: with the handler's ownership check removed this line still
	// passes — the DELETE is scoped to the owner in SQL as well, so the data
	// survives even when the layer above it forgets. The status code is what
	// goes wrong first (204 instead of 404), which is why both are asserted.
	if k, err := store.GetS3AccessKeyByID(context.Background(), id); err != nil || k == nil {
		t.Error("the key was deleted by an account that does not own it")
	}
}

// Disable stops a key without losing it; delete removes it.
func TestS3KeyCanBeDisabledAndDeletedByItsOwner(t *testing.T) {
	srv, client, store := newKeyServer(t)
	const pw = "KeyPass!1"
	seedUser(t, store, "owner@example.com", pw)
	testutil.LoginAs(t, srv, client, "owner@example.com", pw)

	_, body := postJSON(t, client, srv.URL+"/api/auth/s3-keys", map[string]any{"label": "temp"})
	key, _ := body["key"].(map[string]any)
	id := int64(key["id"].(float64))
	path := srv.URL + "/api/auth/s3-keys/" + strconv.FormatInt(id, 10)

	if code, _ := postJSON(t, client, path+"/state", map[string]any{"disabled": true}); code != http.StatusOK {
		t.Fatalf("disable = %d, want 200", code)
	}
	k, err := store.GetS3AccessKeyByID(context.Background(), id)
	if err != nil || k == nil || k.DisabledAt == nil {
		t.Fatalf("key was not disabled: %+v (%v)", k, err)
	}
	if k.Usable(k.CreatedAt) {
		t.Error("a disabled key still reports itself usable")
	}

	req, _ := http.NewRequest(http.MethodDelete, path, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", resp.StatusCode)
	}
	if k, err := store.GetS3AccessKeyByID(context.Background(), id); err == nil && k != nil {
		t.Error("the key survived its own deletion")
	}
}

// ⚠⚠ The endpoint handed back with a key must be the S3 ENDPOINT, not the
// application's URL. They differ whenever a dedicated host is configured, and a
// client pointed at the application root reaches the web app — which is how the
// first real-client run failed, with rclone parsing an HTML redirect as XML
// ("XML syntax error on line 10", 2026-08-16).
func TestKeyResponseCarriesTheEndpointNotTheAppURL(t *testing.T) {
	cases := map[string]struct {
		domain    string
		publicURL string
		endpoint  string
		pathStyle bool
	}{
		"no dedicated host": {
			domain: "", publicURL: "https://fm.example.com",
			endpoint: "https://fm.example.com/s3", pathStyle: true,
		},
		"dedicated host": {
			domain: "s3.example.com", publicURL: "https://fm.example.com",
			endpoint: "https://s3.example.com", pathStyle: false,
		},
		"plain http install": {
			domain: "s3.local", publicURL: "http://127.0.0.1:8080",
			endpoint: "http://s3.local", pathStyle: false,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := s3api.EndpointURL(c.publicURL, c.domain); got != c.endpoint {
				t.Errorf("EndpointURL = %q, want %q", got, c.endpoint)
			}
			if got := s3api.PathStyleRequired(c.domain); got != c.pathStyle {
				t.Errorf("PathStyleRequired = %v, want %v", got, c.pathStyle)
			}
		})
	}
}

// The list and create answers both carry the connection facts, because a key
// without an endpoint is not a usable credential — and a UI that assembled the
// URL itself would be a second place to get it wrong.
func TestKeyEndpointsCarryTheConnectionFacts(t *testing.T) {
	srv, client, store := newKeyServer(t)
	const pw = "KeyPass!1"
	seedUser(t, store, "facts@example.com", pw)
	testutil.LoginAs(t, srv, client, "facts@example.com", pw)

	for _, call := range []struct {
		name   string
		do     func() *http.Response
		status int
	}{
		{"list", func() *http.Response {
			res, err := client.Get(srv.URL + "/api/auth/s3-keys")
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			return res
		}, http.StatusOK},
		{"create", func() *http.Response {
			res, err := client.Post(srv.URL+"/api/auth/s3-keys", "application/json",
				bytes.NewReader([]byte(`{"label":"facts"}`)))
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			return res
		}, http.StatusCreated},
	} {
		t.Run(call.name, func(t *testing.T) {
			res := call.do()
			defer res.Body.Close()
			if res.StatusCode != call.status {
				t.Fatalf("status = %d, want %d", res.StatusCode, call.status)
			}
			var body map[string]any
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			ep, _ := body["endpoint"].(string)
			if ep == "" {
				t.Fatal("no endpoint in the answer — the UI would have to invent one")
			}
			if _, ok := body["path_style"]; !ok {
				t.Error("no path_style flag: without a dedicated host a current SDK fails at DNS")
			}
			if _, ok := body["enabled"]; !ok {
				t.Error("no enabled flag: the UI would hand out a key for an endpoint that 404s")
			}
		})
	}
}
