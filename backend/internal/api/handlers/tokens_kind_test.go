package handlers_test

// Tests for the app/user token split (migration 00030).
//
// The incident these guard: an API token authenticates AS its owner
// (auth/apitoken_middleware.go sets WithUser(GetUser(tok.UserID))), and the
// embeds we actually run authenticate every visitor with ONE shared token
// injected by the host's proxy. v0.30.0 then put "API keys" into the
// explorer's navigation panel — so an embed visitor could list and revoke the
// credential the embed itself runs on. A token now declares what it IS, and
// only the person kind gets the person surfaces.
//
// Each test below was first run against the pre-split code and observed to
// fail there; see the commit message.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// seedKindToken writes an api_tokens row with an explicit kind and returns the
// plaintext secret. Deliberately NOT going through the HTTP mint routes: those
// pick the kind themselves, and these tests need to state it.
func seedKindToken(t *testing.T, store db.Store, userID int64, kind string) string {
	t.Helper()
	plain := "tok_" + randToken(t)
	_, err := store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID:    userID,
		Label:     kind + "-token",
		TokenHash: apitoken.HashToken(plain),
		Kind:      kind,
	})
	require.NoError(t, err)
	return plain
}

// callWithToken runs one request authenticated by an API token.
func callWithToken(t *testing.T, method, url, token, body string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	req.Header.Set("X-Filex-Token", token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	// A bare client: no cookie jar, so nothing can fall through to a session
	// and make a token test pass for the wrong reason.
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// TestSelfTokens_AppTokenRefused — the whole point. An app token may not
// list, mint, edit or revoke its owner's tokens.
func TestSelfTokens_AppTokenRefused(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	uid, _ := testutil.SeedAdminUser(t, store)
	appTok := seedKindToken(t, store, uid, model.TokenKindApp)

	// A token that already exists, so DELETE/PATCH have a real id to aim at
	// and cannot pass merely because the row was missing.
	victim, err := store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID: uid, Label: "the proxy's own credential",
		TokenHash: apitoken.HashToken("tok_" + randToken(t)),
		Kind:      model.TokenKindApp,
	})
	require.NoError(t, err)
	victimPath := srv.URL + "/api/tokens/" + strconv.FormatInt(victim.ID, 10)

	for _, tc := range []struct{ name, method, url, body string }{
		{"list", http.MethodGet, srv.URL + "/api/tokens", ""},
		{"create", http.MethodPost, srv.URL + "/api/tokens", `{"label":"mine"}`},
		{"update", http.MethodPatch, victimPath, `{"label":"renamed"}`},
		{"delete", http.MethodDelete, victimPath, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, body := callWithToken(t, tc.method, tc.url, appTok, tc.body)
			// 403, not 404: the route exists and the credential is valid —
			// what is refused is this KIND of credential using it.
			assert.Equal(t, http.StatusForbidden, st, "body: %s", body)
			var out map[string]any
			require.NoError(t, json.Unmarshal([]byte(body), &out))
			assert.Equal(t, "app_token", out["reason"])
			// The message has to be readable by whoever is staring at a proxy
			// log, so it names the token and the way out.
			msg, _ := out["error"].(string)
			assert.Contains(t, msg, "app token")
			assert.Contains(t, msg, `{"kind":"user"}`)
		})
	}

	// And the refusal was real, not cosmetic: nothing was created or removed.
	rows, lerr := store.ListAPITokensByUser(context.Background(), uid)
	require.NoError(t, lerr)
	assert.Len(t, rows, 2, "app token must not have minted or revoked anything")
}

// TestSelfTokens_UserTokenUnchanged — the half that must not regress. A
// person's own token behaves exactly as it did before the split.
func TestSelfTokens_UserTokenUnchanged(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	uid, _ := testutil.SeedAdminUser(t, store)
	userTok := seedKindToken(t, store, uid, model.TokenKindUser)

	st, body := callWithToken(t, http.MethodGet, srv.URL+"/api/tokens", userTok, "")
	require.Equal(t, http.StatusOK, st, "body: %s", body)
	var list struct {
		Tokens []*model.APIToken `json:"tokens"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &list))
	require.Len(t, list.Tokens, 1)
	assert.Equal(t, model.TokenKindUser, list.Tokens[0].Kind, "kind must reach the client")

	st, body = callWithToken(t, http.MethodPost, srv.URL+"/api/tokens", userTok, `{"label":"my cli"}`)
	require.Equal(t, http.StatusCreated, st, "body: %s", body)
	var created struct {
		Row *model.APIToken `json:"row"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	// Self-service always mints a person's token — the caller cannot ask for
	// "app" and quietly hide surfaces from themselves.
	assert.Equal(t, model.TokenKindUser, created.Row.Kind)

	st, body = callWithToken(t, http.MethodDelete,
		srv.URL+"/api/tokens/"+strconv.FormatInt(created.Row.ID, 10), userTok, "")
	assert.Equal(t, http.StatusOK, st, "body: %s", body)
}

// TestSelfTokens_SessionUnchanged — a cookie/OIDC session carries no token at
// all, and must read as a person rather than defaulting into the app branch.
func TestSelfTokens_SessionUnchanged(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/tokens")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp2, err := client.Post(srv.URL+"/api/tokens", "application/json",
		bytes.NewBufferString(`{"label":"session-minted"}`))
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusCreated, resp2.StatusCode)
	var created struct {
		Row *model.APIToken `json:"row"`
	}
	testutil.ReadJSON(t, resp2, &created)
	assert.Equal(t, model.TokenKindUser, created.Row.Kind)
}

// TestCapabilities_CallerKind — what the explorer reads to decide whether to
// draw the identity surfaces. A session and a user token are both "user"; only
// an app token is "app"; and an anonymous fetch (the share/drop pages make one)
// must not read as an app.
func TestCapabilities_CallerKind(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	uid, _ := testutil.SeedAdminUser(t, store)

	kindOf := func(body string) string {
		var out map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &out))
		s, _ := out["caller_kind"].(string)
		return s
	}

	_, body := callWithToken(t, http.MethodGet, srv.URL+"/api/files/capabilities",
		seedKindToken(t, store, uid, model.TokenKindApp), "")
	assert.Equal(t, model.TokenKindApp, kindOf(body), "app token")

	_, body = callWithToken(t, http.MethodGet, srv.URL+"/api/files/capabilities",
		seedKindToken(t, store, uid, model.TokenKindUser), "")
	assert.Equal(t, model.TokenKindUser, kindOf(body), "user token")

	resp, err := client.Get(srv.URL + "/api/files/capabilities")
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, model.TokenKindUser, kindOf(string(raw)), "cookie session")

	resp2, err := (&http.Client{}).Get(srv.URL + "/api/files/capabilities")
	require.NoError(t, err)
	raw2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode, "capabilities must stay public")
	assert.Equal(t, model.TokenKindUser, kindOf(string(raw2)), "anonymous")
}

// TestAPIToken_ExistingRowsAreApps — migration 00030's behaviour for data that
// was already there. A row written without a kind (every row that existed
// before the split, and any caller that forgets the field) reads back as an
// app, because the restricting direction is the one that costs an admin one
// edit instead of costing an embed its credential.
func TestAPIToken_ExistingRowsAreApps(t *testing.T) {
	conn, store := testutil.NewTestDB(t)
	ctx := context.Background()
	u, err := store.CreateUser(ctx, "legacy@test.local", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)

	// Raw INSERT that never mentions `kind` — this is a pre-00030 row.
	hash := apitoken.HashToken("tok_legacy")
	_, err = conn.ExecContext(ctx,
		`INSERT INTO api_tokens (user_id, label, token_hash, scopes, usernames) VALUES (?,?,?,?,?)`,
		u.ID, "minted in 2025", hash, "read", "")
	require.NoError(t, err)

	got, err := store.GetAPITokenByHash(ctx, hash)
	require.NoError(t, err)
	assert.Equal(t, model.TokenKindApp, got.Kind)
	assert.True(t, got.IsApp())

	// The escape hatch: an admin can hand it back to its person.
	user := model.TokenKindUser
	require.NoError(t, store.UpdateAPITokenMeta(ctx, got.ID, nil, nil, &user))
	got2, err := store.GetAPITokenByHash(ctx, hash)
	require.NoError(t, err)
	assert.Equal(t, model.TokenKindUser, got2.Kind)
	assert.False(t, got2.IsApp())
}

// TestAdminTokens_DefaultKindIsApp — the admin mint surface issues
// integrations' credentials, so it defaults to "app" and takes "user" only
// when asked.
func TestAdminTokens_DefaultKindIsApp(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	mint := func(body string) *model.APIToken {
		t.Helper()
		resp, err := client.Post(srv.URL+"/api/admin/ai-tokens", "application/json",
			bytes.NewBufferString(body))
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var out struct {
			Row *model.APIToken `json:"row"`
		}
		testutil.ReadJSON(t, resp, &out)
		return out.Row
	}

	assert.Equal(t, model.TokenKindApp, mint(`{"label":"host proxy"}`).Kind)
	assert.Equal(t, model.TokenKindUser, mint(`{"label":"a laptop","kind":"user"}`).Kind)

	// A typo is refused, not folded into "app": silently downgrading "users"
	// would mint a credential whose owner cannot manage their own keys and
	// give them nothing to read.
	resp, err := client.Post(srv.URL+"/api/admin/ai-tokens", "application/json",
		bytes.NewBufferString(`{"label":"typo","kind":"users"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// ---------- the other three credential surfaces ----------

// credentialSurfaces are the self-service routes that all answer the same
// question — "show me, and let me mint, the credentials of the person calling"
// — and therefore share one gate (handlers.RequirePersonalCaller).
//
// ⚠ The first round of this change gated only /api/tokens, and a browser
// measurement then caught the other three still answering 200 to an app token:
// an embed visitor could no longer list the proxy's API tokens but could still
// mint an S3 access key bound to the token's owner. Same hole, different
// button. This table is what keeps the four together.
var credentialSurfaces = []struct {
	name string
	path string
	body string
}{
	{"api-tokens", "/api/tokens", `{"label":"mine"}`},
	{"s3-keys", "/api/auth/s3-keys", `{"label":"mine"}`},
	{"ssh-keys", "/api/auth/ssh-keys", `{"label":"mine","public_key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJexample x@y"}`},
	{"nfs-exports", "/api/auth/nfs-exports", `{"label":"mine"}`},
}

// TestSelfCredentials_AppTokenRefused — every self-service credential surface
// refuses an app token, with the same body shape from the same helper.
func TestSelfCredentials_AppTokenRefused(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	uid, _ := testutil.SeedAdminUser(t, store)
	appTok := seedKindToken(t, store, uid, model.TokenKindApp)

	for _, s := range credentialSurfaces {
		t.Run(s.name, func(t *testing.T) {
			for _, call := range []struct {
				method, body string
			}{{http.MethodGet, ""}, {http.MethodPost, s.body}} {
				st, body := callWithToken(t, call.method, srv.URL+s.path, appTok, call.body)
				assert.Equal(t, http.StatusForbidden, st, "%s %s body: %s", call.method, s.path, body)
				var out map[string]any
				require.NoError(t, json.Unmarshal([]byte(body), &out))
				// One refusal, one shape — three copies of it would be three
				// chances for one of them to drift.
				assert.Equal(t, "app_token", out["reason"])
				assert.Equal(t, model.TokenKindApp, out["token_kind"])
				msg, _ := out["error"].(string)
				assert.Contains(t, msg, "app token")
				assert.Contains(t, msg, `{"kind":"user"}`)
			}
		})
	}
}

// TestSelfCredentials_PersonUnchanged — the half that must not regress, on all
// four surfaces: a person's own token and a cookie session both still reach
// them. A 403 here would mean the gate fired on "has a token" rather than on
// "is an app".
func TestSelfCredentials_PersonUnchanged(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	uid, _ := testutil.SeedAdminUser(t, store)
	userTok := seedKindToken(t, store, uid, model.TokenKindUser)

	for _, s := range credentialSurfaces {
		t.Run(s.name, func(t *testing.T) {
			st, body := callWithToken(t, http.MethodGet, srv.URL+s.path, userTok, "")
			assert.Equal(t, http.StatusOK, st, "user token, body: %s", body)

			resp, err := client.Get(srv.URL + s.path)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode, "cookie session")
		})
	}
}

// TestSelfCredentials_ConfinedUserTokenAllowed — confinement and kind are
// DIFFERENT axes. A `root:`-scoped token is narrowed to one folder, which says
// nothing about whether a person is behind it; a gate that conflated the two
// would lock every confined person out of their own credentials.
func TestSelfCredentials_ConfinedUserTokenAllowed(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	uid, _ := testutil.SeedAdminUser(t, store)

	plain := "tok_" + randToken(t)
	_, err := store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID:    uid,
		Label:     "a person, confined to one folder",
		TokenHash: apitoken.HashToken(plain),
		Scopes:    "read,write,root:main://projects/acme",
		Kind:      model.TokenKindUser,
	})
	require.NoError(t, err)

	for _, s := range credentialSurfaces {
		t.Run(s.name, func(t *testing.T) {
			st, body := callWithToken(t, http.MethodGet, srv.URL+s.path, plain, "")
			assert.Equal(t, http.StatusOK, st, "confined but human, body: %s", body)
		})
	}
}

// TestCapabilities_NeverRejects — /api/files/capabilities is fetched by the
// login screen and by the public share/drop pages, so it must answer 200 to
// anything, whatever credential is (or is not) attached.
//
// ⚠ This is why the route uses auth.AnnotateToken and not
// MiddlewareWithToken(store, false): "optional auth" still rejects a disabled
// account with 403, and a disabled user's browser still holds a cookie — the
// login page's own probe would have failed closed.
func TestCapabilities_NeverRejects(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	ctx := context.Background()
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	uid, _ := testutil.SeedAdminUser(t, store)
	revoked := seedKindToken(t, store, uid, model.TokenKindApp)
	rows, err := store.ListAPITokensByUser(ctx, uid)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NoError(t, store.DeleteAPIToken(ctx, rows[0].ID))

	past := time.Now().Add(-time.Hour)
	expiredPlain := "tok_" + randToken(t)
	_, err = store.CreateAPIToken(ctx, &model.APIToken{
		UserID: uid, Label: "expired", TokenHash: apitoken.HashToken(expiredPlain),
		Kind: model.TokenKindApp, ExpiresAt: &past,
	})
	require.NoError(t, err)

	live := seedKindToken(t, store, uid, model.TokenKindApp)

	kindOf := func(body string) string {
		var out map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &out))
		s, _ := out["caller_kind"].(string)
		return s
	}

	for _, tc := range []struct{ name, token, want string }{
		{"revoked token", revoked, model.TokenKindUser},
		{"expired token", expiredPlain, model.TokenKindUser},
		{"garbage token", "not-a-token-at-all", model.TokenKindUser},
		{"live app token", live, model.TokenKindApp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, body := callWithToken(t, http.MethodGet, srv.URL+"/api/files/capabilities", tc.token, "")
			require.Equal(t, http.StatusOK, st, "body: %s", body)
			assert.Equal(t, tc.want, kindOf(body))
		})
	}

	// A username outside the token's allow-list is a hard 403 on /api/files —
	// but not here: a public probe answers, it just describes the token.
	t.Run("bad token username", func(t *testing.T) {
		req, rerr := http.NewRequest(http.MethodGet, srv.URL+"/api/files/capabilities", nil)
		require.NoError(t, rerr)
		req.Header.Set("X-Filex-Token", live)
		req.Header.Set("X-Filex-Token-User", "nobody-by-that-name")
		resp, derr := (&http.Client{}).Do(req)
		require.NoError(t, derr)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	// A disabled account's cookie is the case that made the wrong middleware
	// visible: the login page it lands on fetches this route.
	t.Run("disabled account cookie", func(t *testing.T) {
		users, uerr := store.ListUsers(ctx)
		require.NoError(t, uerr)
		var me int64
		for _, u := range users {
			if u.Email == email {
				me = u.ID
			}
		}
		require.NotZero(t, me)
		require.NoError(t, store.SetUserEnabled(ctx, me, false))
		resp, gerr := client.Get(srv.URL + "/api/files/capabilities")
		require.NoError(t, gerr)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
