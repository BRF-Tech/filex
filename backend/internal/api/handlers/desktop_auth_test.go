package handlers_test

// Desktop pairing: POST /api/auth/desktop/complete (browser half) and
// POST /api/auth/desktop/exchange (app half).
//
// Two defects these guard, both measured before the fix:
//
//   - The token the pairing minted carried no kind, so it was stored as an
//     `app` token (migration 00030's default). The desktop IS one person's
//     client, yet its window answered 403 `app_token` on the API keys, S3 keys,
//     SSH keys and NFS panels, and the explorer hid Recent, Starred and Shared
//     with me, because capabilities said `caller_kind: "app"`.
//   - The browser half sat behind MiddlewareWithToken, so ANY API token could
//     call it: a read-only integration token came back with a fresh
//     read,write,delete token for its owner, with no `root:` confinement.
//     Minting `user` tokens without closing that would have let an embed's
//     shared proxy token turn itself into a personal credential.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const desktopPW = "DesktopPairPass!1"

// desktopSession creates an account with the given role and returns a client
// holding its browser session — the only credential the browser half accepts —
// together with the session token itself, which the SPA also sends as
// `Authorization: Bearer` (web/src/stores/auth.ts keeps it as filex.bearer).
func desktopSession(t *testing.T, srvURL string, store db.Store, role, email string) (*http.Client, int64, string) {
	t.Helper()
	hash, err := local.HashPassword(desktopPW)
	require.NoError(t, err)
	u, err := store.CreateUser(context.Background(), email, hash, role, "en", "UTC")
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]string{"email": email, "password": desktopPW})
	resp, err := client.Post(srvURL+"/api/auth/login", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "login as %s", email)
	session := ""
	for _, c := range resp.Cookies() {
		if c.Name == local.SessionCookieName {
			session = c.Value
		}
	}
	require.NotEmpty(t, session, "login set no session cookie")
	return client, u.ID, session
}

// desktopPair runs both halves of a pairing. `complete` performs the browser
// half with whatever credential the test is exercising; the app half is always
// a bare client, exactly like the desktop, which has no session yet.
//
// Returns the browser half's status and body, and the token the app half was
// handed ("" when the browser half was refused).
func desktopPair(t *testing.T, srvURL string, complete func(url, body string) (int, string)) (int, string, string) {
	t.Helper()
	verifier := "desktop-verifier-" + randToken(t)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := "state-" + randToken(t)

	st, body := complete(srvURL+"/api/auth/desktop/complete",
		`{"state":"`+state+`","challenge":"`+challenge+`","label":"filex desktop — test"}`)
	if st != http.StatusOK {
		return st, body, ""
	}
	var parked struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &parked))
	require.NotEmpty(t, parked.Code, "the browser half answered 200 without a code: %s", body)

	req, err := http.NewRequest(http.MethodPost, srvURL+"/api/auth/desktop/exchange",
		strings.NewReader(`{"state":"`+state+`","code":"`+parked.Code+`","verifier":"`+verifier+`"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "exchange: %s", raw)
	var handed struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(raw, &handed))
	require.NotEmpty(t, handed.Token)
	return st, body, handed.Token
}

// postWith returns a `complete` func that posts with a session client.
func postWith(t *testing.T, client *http.Client) func(url, body string) (int, string) {
	return func(url, body string) (int, string) {
		t.Helper()
		resp, err := client.Post(url, "application/json", strings.NewReader(body))
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
}

// onlyDesktopToken returns the one token row the pairing created for userID.
func onlyDesktopToken(t *testing.T, store db.Store, userID int64) *model.APIToken {
	t.Helper()
	rows, err := store.ListAPITokensByUser(context.Background(), userID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "exactly one token for the pairing")
	return rows[0]
}

// TestDesktopPairing_SessionMintsAPersonalToken — the desktop is one person's
// own client, so its token is a person's: kind `user`, and the surfaces that
// are refused to integrations answer it.
func TestDesktopPairing_SessionMintsAPersonalToken(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	client, uid, _ := desktopSession(t, srv.URL, store, model.RoleUser, "desk-user@test.local")

	st, body, tok := desktopPair(t, srv.URL, postWith(t, client))
	require.Equal(t, http.StatusOK, st, body)

	row := onlyDesktopToken(t, store, uid)
	assert.Equal(t, model.TokenKindUser, row.Kind, "a desktop pairing is a person's credential")
	assert.Equal(t, "read,write,delete", row.Scopes, "the desktop reads and writes its owner's files, and nothing more")
	assert.Equal(t, "filex desktop — test", row.Label)

	// What the desktop window actually does with it: its own API keys panel,
	// and the explorer's person surfaces.
	st, body = callWithToken(t, http.MethodGet, srv.URL+"/api/tokens", tok, "")
	assert.Equal(t, http.StatusOK, st, "the desktop's API keys panel: %s", body)
	st, body = callWithToken(t, http.MethodGet, srv.URL+"/api/auth/s3-keys", tok, "")
	assert.Equal(t, http.StatusOK, st, "the desktop's S3 keys panel: %s", body)
	_, body = callWithToken(t, http.MethodGet, srv.URL+"/api/files/capabilities", tok, "")
	var caps map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &caps))
	assert.Equal(t, model.TokenKindUser, caps["caller_kind"], "the explorer must draw Recent, Starred and Shared with me")
}

// TestDesktopPairing_SessionBearerIsStillASession — the SPA sends its login
// session as `Authorization: Bearer` too. That header shape is shared with API
// tokens, and the refusal below must not mistake a session carried in it for
// one: this is the request the browser really makes.
func TestDesktopPairing_SessionBearerIsStillASession(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	_, uid, session := desktopSession(t, srv.URL, store, model.RoleUser, "desk-bearer@test.local")

	st, body, _ := desktopPair(t, srv.URL, func(url, body string) (int, string) {
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+session)
		resp, err := (&http.Client{}).Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	})
	require.Equal(t, http.StatusOK, st, body)
	assert.Equal(t, model.TokenKindUser, onlyDesktopToken(t, store, uid).Kind)
}

// TestDesktopPairing_ViewerGetsAReadOnlyToken — the desktop gets what its
// owner could mint for themselves at /api/tokens, and a viewer can mint only
// read-only tokens there.
func TestDesktopPairing_ViewerGetsAReadOnlyToken(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	client, uid, _ := desktopSession(t, srv.URL, store, model.RoleViewer, "desk-viewer@test.local")

	st, body, _ := desktopPair(t, srv.URL, postWith(t, client))
	require.Equal(t, http.StatusOK, st, body)

	row := onlyDesktopToken(t, store, uid)
	assert.Equal(t, model.TokenKindUser, row.Kind)
	assert.Equal(t, "read", row.Scopes, "a viewer's desktop must not carry write or delete")
}

// TestDesktopPairing_RefusesEveryAPIToken — the browser half is for a signed-in
// browser, and nothing else. A token of either kind, with any scopes, is
// refused with a reason, and no credential is minted or parked.
func TestDesktopPairing_RefusesEveryAPIToken(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	uid, _ := testutil.SeedAdminUser(t, store)

	for _, tc := range []struct {
		name, kind, scopes string
	}{
		// The embed's shared proxy token: pairing through it would turn an
		// integration credential into a personal one.
		{"app token", model.TokenKindApp, "read,write,delete"},
		// A person's own read-only CLI token: pairing through it would hand
		// back a read,write,delete token.
		{"read-only user token", model.TokenKindUser, "read"},
		// Even a token that could do everything the desktop's could.
		{"full user token", model.TokenKindUser, "read,write,delete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plain := "tok_" + randToken(t)
			_, err := store.CreateAPIToken(context.Background(), &model.APIToken{
				UserID: uid, Label: tc.name, TokenHash: apitoken.HashToken(plain),
				Scopes: tc.scopes, Kind: tc.kind,
			})
			require.NoError(t, err)
			before, err := store.ListAPITokensByUser(context.Background(), uid)
			require.NoError(t, err)

			st, body, handed := desktopPair(t, srv.URL, func(url, body string) (int, string) {
				return callWithToken(t, http.MethodPost, url, plain, body)
			})
			assert.Equal(t, http.StatusForbidden, st, "body: %s", body)
			assert.Empty(t, handed)
			var out map[string]any
			require.NoError(t, json.Unmarshal([]byte(body), &out))
			assert.Equal(t, "session_required", out["reason"])
			assert.NotContains(t, out, "code", "nothing may be parked for the app half to collect")
			msg, _ := out["error"].(string)
			assert.Contains(t, msg, "browser", "the message has to say where pairing is done")

			after, err := store.ListAPITokensByUser(context.Background(), uid)
			require.NoError(t, err)
			assert.Len(t, after, len(before), "a refused pairing must not mint a token")
		})
	}
}
