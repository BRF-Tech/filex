package handlers_test

// The embedded store screen (#162, handlers/app_store_view.go): who sees it
// (the administrator's settings: everyone, roles, groups), what a person may
// do there (read the catalog filex verified, leave a request on the Install
// requests list) and may not (any of the administrator's store doors), and
// the approval: a fresh link from the connected store, the same store review,
// and the install closing the request.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/appstore/storetest"
	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const (
	screenCode    = "fxc_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	screenUser    = "ada@test.local"
	screenUserPw  = "Ada-Passw0rd!"
	screenTokenID = "0123456789abcdef"
)

type screenFix struct {
	*asFix
	user   *http.Client
	userID int64
}

// newScreenFix: a trusted store with a signed catalog (lang-eo 1.0.0), and a
// person who is not an administrator, signed in.
func newScreenFix(t *testing.T) *screenFix {
	t.Helper()
	f := newAsFix(t)
	f.trust(t)
	f.st.SetIndex("idx-1", f.st.IndexDoc(map[string]any{"name": "lang-eo", "kind": "language_pack", "version": "1.0.0"}), false)
	testutil.SeedRegularUser(t, f.store, screenUser, screenUserPw)
	u, err := f.store.GetUserByEmail(context.Background(), screenUser)
	require.NoError(t, err)
	c := freshClient(t)
	testutil.LoginAs(t, f.srv, c, screenUser, screenUserPw)
	return &screenFix{asFix: f, user: c, userID: u.ID}
}

// as sends a request with the person's session.
func (f *screenFix) as(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, f.srv.URL+path, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.user.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (f *screenFix) setView(t *testing.T, settings map[string]any) {
	t.Helper()
	code, body := f.call(t, "", http.MethodPut, "/api/admin/app-plugins/store-view", map[string]any{"settings": settings})
	require.Equal(t, http.StatusOK, code, "store view: %s", body)
}

func (f *screenFix) connect(t *testing.T) {
	t.Helper()
	f.st.AddConnectCode(screenCode)
	code, body := f.call(t, "", http.MethodPost, "/api/admin/app-plugins/stores/connection", map[string]any{"store": f.st.Origin(), "code": screenCode})
	require.Equal(t, http.StatusOK, code, "connect: %s", body)
}

func visible(t *testing.T, body []byte) bool {
	t.Helper()
	var v struct {
		Visible bool `json:"visible"`
	}
	require.NoError(t, json.Unmarshal(body, &v))
	return v.Visible
}

// A person never reaches the administrator's store doors: the intent, the
// trust, the connection, the settings, the approval - whatever they see.
func TestStoreScreen_APersonReachesNoneOfTheAdministratorsDoors(t *testing.T) {
	f := newScreenFix(t)
	f.setView(t, map[string]any{"enabled": true, "stores": []string{f.st.Origin()}, "audience": "everyone"})
	f.connect(t)
	for _, r := range []struct{ method, path string }{
		{http.MethodPost, "/api/admin/app-plugins/store-intent"},
		{http.MethodPost, "/api/admin/app-plugins/store-intent/install"},
		{http.MethodPost, "/api/admin/app-plugins/stores"},
		{http.MethodGet, "/api/admin/app-plugins/stores/connection?store=" + f.st.Origin()},
		{http.MethodPost, "/api/admin/app-plugins/stores/connection"},
		{http.MethodDelete, "/api/admin/app-plugins/stores/connection?store=" + f.st.Origin()},
		{http.MethodGet, "/api/admin/app-plugins/store-view"},
		{http.MethodPut, "/api/admin/app-plugins/store-view"},
		{http.MethodGet, "/api/admin/plugin-requests"},
		{http.MethodPost, "/api/admin/plugin-requests/1/approve"},
	} {
		code, body := f.as(t, r.method, r.path, map[string]any{"store": f.st.Origin(), "token": "tokentoken", "code": screenCode})
		assert.Equal(t, http.StatusForbidden, code, "%s %s: %s", r.method, r.path, body)
	}
	assert.Empty(t, f.st.IntentAsks(), "nothing a person did asked the store for a link")
	// The administrator's own API key does not reach the new doors either.
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/app-plugins/store-view"},
		{http.MethodPut, "/api/admin/app-plugins/store-view"},
		{http.MethodPost, "/api/admin/app-plugins/stores/connection"},
		{http.MethodDelete, "/api/admin/app-plugins/stores/connection?store=" + f.st.Origin()},
	} {
		code, body := f.call(t, f.key, r.method, r.path, map[string]any{"store": f.st.Origin(), "code": screenCode})
		assert.Equal(t, http.StatusForbidden, code, "admin key %s %s: %s", r.method, r.path, body)
	}
}

func TestStoreScreen_WhoSeesItFollowsTheSettings(t *testing.T) {
	f := newScreenFix(t)
	ctx := context.Background()
	catalog := "/api/app-store/catalog?store=" + f.st.Origin()

	// Off until an administrator turns it on.
	_, body := f.as(t, http.MethodGet, "/api/app-store", nil)
	assert.False(t, visible(t, body))
	code, body := f.as(t, http.MethodGet, catalog, nil)
	assert.Equal(t, http.StatusNotFound, code, "%s", body)
	assert.Equal(t, "store_screen_hidden", errCode(body))
	assert.Equal(t, 0, f.st.IndexReads(), "a hidden screen reads nothing from the store")

	// Everyone.
	f.setView(t, map[string]any{"enabled": true, "stores": []string{f.st.Origin()}, "audience": "everyone"})
	_, body = f.as(t, http.MethodGet, "/api/app-store", nil)
	assert.True(t, visible(t, body))
	code, body = f.as(t, http.MethodGet, catalog, nil)
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.Contains(t, string(body), `"name":"lang-eo"`)
	assert.NotContains(t, string(body), "manifest_sha256", "the pins stay on the server")
	// A store the screen does not show.
	code, _ = f.as(t, http.MethodGet, "/api/app-store/catalog?store=https://other.example", nil)
	assert.Equal(t, http.StatusNotFound, code)

	// Administrators only: the person no longer sees it, the administrator does.
	f.setView(t, map[string]any{"enabled": true, "stores": []string{f.st.Origin()}, "audience": "roles", "roles": []string{"admin"}})
	_, body = f.as(t, http.MethodGet, "/api/app-store", nil)
	assert.False(t, visible(t, body))
	_, body = f.call(t, "", http.MethodGet, "/api/app-store", nil)
	assert.True(t, visible(t, body))

	// A group: hidden until the person is in it.
	g, err := f.store.CreateGroup(ctx, &model.Group{Name: "Designers"})
	require.NoError(t, err)
	f.setView(t, map[string]any{"enabled": true, "stores": []string{f.st.Origin()}, "audience": "groups", "groups": []int64{g.ID}})
	_, body = f.as(t, http.MethodGet, "/api/app-store", nil)
	assert.False(t, visible(t, body))
	require.NoError(t, f.store.AddGroupMember(ctx, g.ID, f.userID))
	_, body = f.as(t, http.MethodGet, "/api/app-store", nil)
	assert.True(t, visible(t, body))
	// A group that does not exist is not taken.
	code, body = f.call(t, "", http.MethodPut, "/api/admin/app-plugins/store-view", map[string]any{"settings": map[string]any{
		"enabled": true, "stores": []string{f.st.Origin()}, "audience": "groups", "groups": []int64{g.ID + 999}}})
	assert.Equal(t, http.StatusBadRequest, code, "%s", body)

	// The person's API key: the screen is a person's, not a program's.
	tok := testutil.NewAPIToken(t, f.store, f.userID, "read,write")
	_, body = f.call(t, tok, http.MethodGet, "/api/app-store", nil)
	assert.False(t, visible(t, body))
	code, body = f.call(t, tok, http.MethodGet, catalog, nil)
	assert.Equal(t, http.StatusForbidden, code, "%s", body)
	code, body = f.call(t, tok, http.MethodPost, "/api/app-store/requests", map[string]any{"store": f.st.Origin(), "app": "lang-eo", "reason": "x"})
	assert.Equal(t, http.StatusForbidden, code, "%s", body)
}

func TestStoreScreen_ARequestIsApprovedThroughTheStoreReview(t *testing.T) {
	f := newScreenFix(t)
	f.setView(t, map[string]any{"enabled": true, "stores": []string{f.st.Origin()}, "audience": "everyone"})

	// No reason, no app: refused.
	code, body := f.as(t, http.MethodPost, "/api/app-store/requests", map[string]any{"store": f.st.Origin(), "app": "lang-eo"})
	assert.Equal(t, http.StatusBadRequest, code, "%s", body)
	assert.Equal(t, "reason_required", errCode(body))
	code, _ = f.as(t, http.MethodPost, "/api/app-store/requests", map[string]any{"store": f.st.Origin(), "app": "nope", "reason": "x"})
	assert.Equal(t, http.StatusNotFound, code)

	code, body = f.as(t, http.MethodPost, "/api/app-store/requests", map[string]any{"store": f.st.Origin(), "app": "lang-eo", "reason": "Esperanto for the team"})
	require.Equal(t, http.StatusCreated, code, "%s", body)
	var made struct {
		Request struct {
			ID         int64  `json:"id"`
			SourceKind string `json:"source_kind"`
			Status     string `json:"status"`
			Version    string `json:"version"`
		} `json:"request"`
	}
	require.NoError(t, json.Unmarshal(body, &made))
	assert.Equal(t, "store", made.Request.SourceKind)
	assert.Equal(t, "1.0.0", made.Request.Version)
	id := strconv.FormatInt(made.Request.ID, 10)
	// Asking again answers the waiting request.
	code, _ = f.as(t, http.MethodPost, "/api/app-store/requests", map[string]any{"store": f.st.Origin(), "app": "lang-eo", "reason": "again"})
	assert.Equal(t, http.StatusOK, code)

	// The administrator sees it on the Install requests list.
	code, body = f.call(t, "", http.MethodGet, "/api/admin/plugin-requests", nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, string(body), `"source_kind":"store"`)

	// Not connected: the approval says so, and nothing is asked.
	code, body = f.call(t, "", http.MethodPost, "/api/admin/plugin-requests/"+id+"/approve", map[string]any{})
	assert.Equal(t, http.StatusConflict, code, "%s", body)
	assert.Equal(t, appstore.CodeNotConnected, errCode(body))

	// Connected: the store makes a fresh link for this filex.
	f.connect(t)
	f.publish("tokentoken-9", "1.0.0", "Nuligi", func(p map[string]any) { p["token_id"] = screenTokenID })
	f.st.OnIntent(func(ask storetest.IntentAsk) (string, string, int) { return "tokentoken-9", screenTokenID, 0 })
	code, body = f.call(t, "", http.MethodPost, "/api/admin/plugin-requests/"+id+"/approve", map[string]any{})
	require.Equal(t, http.StatusOK, code, "%s (store refused %v)", body, f.st.Refused())
	var appr struct {
		Request struct {
			Status string `json:"status"`
		} `json:"request"`
		StoreIntent struct {
			Store string `json:"store"`
			Token string `json:"token"`
		} `json:"store_intent"`
	}
	require.NoError(t, json.Unmarshal(body, &appr))
	assert.Equal(t, "pending", appr.Request.Status, "nothing is installed by the approval itself")
	assert.Equal(t, f.st.Origin(), appr.StoreIntent.Store)
	assert.Equal(t, "tokentoken-9", appr.StoreIntent.Token)
	asks := f.st.IntentAsks()
	require.Len(t, asks, 1)
	assert.Equal(t, "lang-eo", asks[0].App)
	assert.Equal(t, "1.0.0", asks[0].Version)
	_, installed := f.reg.ByName("lang-eo")
	assert.False(t, installed)

	// The same store review as a magic link; its install closes the request.
	code, rb, body := f.review(t, "tokentoken-9")
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.Contains(t, string(body), `"request_id":`+id)
	code, body = f.install(t, rb, nil)
	require.Equal(t, http.StatusCreated, code, "%s", body)
	assert.Contains(t, string(body), `"status":"approved"`)
	_, installed = f.reg.ByName("lang-eo")
	assert.True(t, installed)
	code, body = f.call(t, "", http.MethodGet, "/api/admin/plugin-requests/"+id, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, string(body), `"status":"approved"`)
	// The person sees their request decided.
	code, body = f.as(t, http.MethodGet, "/api/app-store/requests", nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, string(body), `"status":"approved"`)
	assert.NotContains(t, string(body), `"decider"`, "who decided stays in the panel")
}

func TestStoreScreen_TheStoreUnreachable(t *testing.T) {
	f := newScreenFix(t)
	f.setView(t, map[string]any{"enabled": true, "stores": []string{f.st.Origin()}, "audience": "everyone"})
	catalog := "/api/app-store/catalog?store=" + f.st.Origin()
	code, body := f.as(t, http.MethodGet, catalog, nil)
	require.Equal(t, http.StatusOK, code, "%s", body)
	f.st.SetDown(true)
	// Within its ten minutes the catalog is the one read before.
	code, body = f.as(t, http.MethodGet, catalog, nil)
	require.Equal(t, http.StatusOK, code, "%s", body)
	assert.Contains(t, string(body), `"name":"lang-eo"`)
	// A request still lands, from the catalog filex holds.
	code, body = f.as(t, http.MethodPost, "/api/app-store/requests", map[string]any{"store": f.st.Origin(), "app": "lang-eo", "reason": "x"})
	require.Equal(t, http.StatusCreated, code, "%s", body)
}

func TestStoreScreen_AStoreNeverReadAndDownIsABadGateway(t *testing.T) {
	f := newScreenFix(t)
	f.setView(t, map[string]any{"enabled": true, "stores": []string{f.st.Origin()}, "audience": "everyone"})
	f.st.SetDown(true)
	code, body := f.as(t, http.MethodGet, "/api/app-store/catalog?store="+f.st.Origin(), nil)
	assert.Equal(t, http.StatusBadGateway, code, "%s", body)
	assert.Equal(t, appstore.CodeUnreachable, errCode(body))
}

// The desktop app shows the same screen (docs/DESKTOP.md): its own pairing -
// the key /api/auth/desktop/complete mints, Source desktop - is a person's
// client and reaches the screen; any other key of the same person does not.
func TestStoreScreen_TheDesktopPairingIsAPerson(t *testing.T) {
	f := newScreenFix(t)
	f.setView(t, map[string]any{"enabled": true, "stores": []string{f.st.Origin()}, "audience": "everyone"})
	plain := "tok_desktop_" + strconv.FormatInt(f.userID, 10)
	_, err := f.store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID: f.userID, Label: "filex desktop", TokenHash: apitoken.HashToken(plain), Scopes: "read,write,delete",
		Kind: model.TokenKindUser, Source: model.TokenSourceDesktop,
	})
	require.NoError(t, err)
	_, body := f.call(t, plain, http.MethodGet, "/api/app-store", nil)
	assert.True(t, visible(t, body), "the desktop pairing sees the screen the person sees")
	code, body := f.call(t, plain, http.MethodGet, "/api/app-store/catalog?store="+f.st.Origin(), nil)
	require.Equal(t, http.StatusOK, code, "%s", body)
	code, body = f.call(t, plain, http.MethodPost, "/api/app-store/requests", map[string]any{"store": f.st.Origin(), "app": "lang-eo", "reason": "from the desktop"})
	require.Equal(t, http.StatusCreated, code, "%s", body)

	// The same person's ordinary API key: not a person's screen.
	other := testutil.NewAPIToken(t, f.store, f.userID, "read,write")
	_, body = f.call(t, other, http.MethodGet, "/api/app-store", nil)
	assert.False(t, visible(t, body))
	code, body = f.call(t, other, http.MethodGet, "/api/app-store/catalog?store="+f.st.Origin(), nil)
	assert.Equal(t, http.StatusForbidden, code, "%s", body)
	assert.Equal(t, "session_required", errCode(body))
	// An app token minted on the desktop door is still an app token.
	appPlain := "tok_desktop_app_" + strconv.FormatInt(f.userID, 10)
	_, err = f.store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID: f.userID, Label: "an embed", TokenHash: apitoken.HashToken(appPlain), Scopes: "read,write",
		Kind: model.TokenKindApp, Source: model.TokenSourceDesktop,
	})
	require.NoError(t, err)
	_, body = f.call(t, appPlain, http.MethodGet, "/api/app-store", nil)
	assert.False(t, visible(t, body))
}
