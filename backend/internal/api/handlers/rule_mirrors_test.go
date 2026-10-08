package handlers_test

// filex #211: the rules the clients used to keep copies of, published and
// enforced by the server (the audit's B2, B4, B12, B16, B19, B20 and A11).

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/comments"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/editkind"
	"github.com/brf-tech/filex/backend/internal/tagname"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/version"
)

func capsOf(t *testing.T, c *http.Client, base string) map[string]any {
	t.Helper()
	res, err := c.Get(base + "/api/files/capabilities")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	out := map[string]any{}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return out
}

// B2 + B20: the edit kinds and the input limits are published to everybody
// (a property of the build), from the values the server enforces.
func TestCapabilities_PublishesTheRules(t *testing.T) {
	srv, _, _ := testutil.NewTestServer(t)
	caps := capsOf(t, &http.Client{}, srv.URL)

	raw, err := json.Marshal(caps["edit_kinds"])
	require.NoError(t, err)
	var kinds editkind.Kinds
	require.NoError(t, json.Unmarshal(raw, &kinds))
	assert.Equal(t, editkind.Published(), kinds)
	assert.Contains(t, kinds.Office, "docm", "the explorer did not count .docm as office")
	assert.Contains(t, kinds.Text, "properties", "save-text saves it; the preview offered no Edit")
	assert.Contains(t, kinds.Text, "graphql", "the preview offered Edit; save-text refused it")

	limits, _ := caps["limits"].(map[string]any)
	require.NotNil(t, limits)
	assert.EqualValues(t, tagname.MaxRunes, limits["tag_max_runes"])
	assert.EqualValues(t, comments.MaxBodyLen, limits["comment_max_runes"])
	assert.EqualValues(t, e2epolicy.MaxRequestChars, limits["e2e_request_reason_max_runes"])
	assert.EqualValues(t, 16<<10, limits["app_state_max_bytes"])
	assert.EqualValues(t, 8<<20, limits["app_ui_save_chunk_bytes"])
}

// A11: the release, the commit and the build time, apart - no client parses
// "v0.46.0 (hash, time)" any more.
func TestCapabilities_TheVersionInParts(t *testing.T) {
	v, c, d := version.Version, version.Commit, version.Date
	t.Cleanup(func() { version.Version, version.Commit, version.Date = v, c, d })

	version.Version, version.Commit, version.Date = "v0.54.0", "a2d7e34d19aa", "2026-10-08T03:41:30Z"
	srv, _, _ := testutil.NewTestServer(t)
	caps := capsOf(t, &http.Client{}, srv.URL)
	assert.Equal(t, "v0.54.0", caps["release"])
	assert.Equal(t, "a2d7e34d19aa", caps["commit"])
	assert.Equal(t, "2026-10-08T03:41:30Z", caps["built"])

	// An unstamped part is left out, never "unknown". (A second server: the
	// variables are written before anything that reads them starts.)
	srv.Close()
	version.Commit, version.Date = "unknown", "unknown"
	srv2, _, _ := testutil.NewTestServer(t)
	caps = capsOf(t, &http.Client{}, srv2.URL)
	assert.Equal(t, "v0.54.0", caps["release"])
	_, hasCommit := caps["commit"]
	_, hasBuilt := caps["built"]
	assert.False(t, hasCommit)
	assert.False(t, hasBuilt)
}

// B16: which notification events cannot happen here, why, and whether this
// caller could change it - the server's answer, said in the reader's words.
func TestCapabilities_EventsThatCannotHappen(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)

	anon := capsOf(t, &http.Client{}, srv.URL)
	_, said := anon["event_off"]
	assert.False(t, said, "an anonymous caller chooses no notifications")

	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	offOf := func(caps map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		m, _ := caps["event_off"].(map[string]any)
		for k, v := range m {
			out[k], _ = v.(map[string]any)
		}
		return out
	}
	admin := offOf(capsOf(t, client, srv.URL))
	require.Contains(t, admin, "file.infected", "scanning is off in the test server")
	assert.Equal(t, "antivirus", admin["file.infected"]["reason"])
	assert.Equal(t, true, admin["file.infected"]["fixable"])
	assert.Equal(t, "Virus scanning is off (Protection).", admin["file.infected"]["text"])
	require.Contains(t, admin, "e2e.escrow_used")
	assert.Equal(t, "escrow", admin["e2e.escrow_used"]["reason"])
	require.Contains(t, admin, "e2e.request_decided")
	assert.Equal(t, "e2e_approval", admin["e2e.request_decided"]["reason"])
	assert.NotContains(t, admin, "file.created", "an event that can happen anywhere is not listed")

	testutil.SeedRegularUser(t, store, "member@example.com", "Passw0rd!long")
	jar, _ := cookiejar.New(nil)
	member := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, member, "member@example.com", "Passw0rd!long")
	m := offOf(capsOf(t, member, srv.URL))
	require.Contains(t, m, "file.infected")
	assert.Equal(t, false, m["file.infected"]["fixable"], "a member cannot switch scanning on: the switch is not offered")
	assert.Equal(t, false, m["e2e.request_created"]["fixable"])
}

// B4: the Add user form's username is identity.Suggest's - the one a first
// SSO sign-in gets - and its display name is the server's too.
func TestUsersSuggest_IsTheIdentityRule(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	ask := func(c *http.Client, addr string) (int, map[string]string) {
		res, err := c.Get(srv.URL + "/api/admin/users/suggest?email=" + url.QueryEscape(addr))
		require.NoError(t, err)
		defer res.Body.Close()
		out := map[string]string{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	code, got := ask(client, "gözlük@corp.example")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "gozluk", got["username"], "the browser's copy said g.zl.k")
	assert.Equal(t, "Gözlük", got["name"])

	_, got = ask(client, "Jane.Doe+filex@corp.example")
	assert.Equal(t, "jane.doe", got["username"])
	assert.Equal(t, "Jane Doe", got["name"])

	_, got = ask(client, "ja")
	assert.Equal(t, "", got["username"], "half an address is not padded into a name")

	testutil.SeedRegularUser(t, store, "plain@example.com", "Passw0rd!long")
	jar, _ := cookiejar.New(nil)
	plain := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, plain, "plain@example.com", "Passw0rd!long")
	code, _ = ask(plain, "x@y.z")
	assert.Equal(t, http.StatusForbidden, code, "the admin users area")
}

// B19: one app's kept state is held to its share of the account document on
// the server, not only in the page.
func TestUserPrefs_AnAppsStateIsHeldToItsShare(t *testing.T) {
	h, _, ada, _ := newPrefsFixture(t)
	small, _ := json.Marshal(map[string]any{"sketch": map[string]any{"k": "v"}})
	doc, _ := json.Marshal(map[string]any{"prefs": map[string]any{"appState": string(small)}})
	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "web", string(doc)).Code)

	big, _ := json.Marshal(map[string]any{"sketch": map[string]any{"k": strings.Repeat("x", 17<<10)}})
	doc, _ = json.Marshal(map[string]any{"prefs": map[string]any{"appState": string(big)}})
	rec := prefsPut(t, h, ada, "web", string(doc))
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "APP_STATE_TOO_LARGE")
	assert.Contains(t, rec.Body.String(), `"app":"sketch"`)
	assert.Contains(t, prefsGet(t, h, ada, "web").Body.String(), `\"v\"`, "the refused save changed nothing")
}

// B19: an interface reads the file it was opened with through the server,
// which checks the app the way it checks a save.
func TestAppUIRead_ReadsThroughTheServer(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	f.writeFile(t, "doc.sketch", "v1")

	get := func(u string) (int, string) {
		res, err := f.admin.Get(f.srv.URL + u)
		require.NoError(t, err)
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	code, body := get("/api/files/plugins/ui/sketch/editor/read?path=" + url.QueryEscape("main://doc.sketch"))
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "v1", body)

	code, _ = get("/api/files/plugins/ui/nope/editor/read?path=" + url.QueryEscape("main://doc.sketch"))
	assert.Equal(t, http.StatusNotFound, code, "an app that is not there reads nothing")
	code, _ = get("/api/files/plugins/ui/sketch/editor/read")
	assert.Equal(t, http.StatusBadRequest, code)
}
