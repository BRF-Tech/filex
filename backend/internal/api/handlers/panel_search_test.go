package handlers_test

// The admin panel's search (task #168, handlers/panel_search.go,
// docs/ADMIN-PANEL.md → Search), over real HTTP.
//
// What is held here is the owner's line: a person finds only what they may
// open. Each kind is read through the list route the panel's own page calls,
// behind that route's gate - so a delegated administrator holding admin.users
// finds people and groups and nothing else, a tenant's administrator finds
// their own tenant's, and a plain account is refused outright. And the rows
// carry no secret: an API key by its name, a share by its file, never the
// link's token.
//
// RED on the code before #168: GET /api/admin/panel-search and the recent
// searches routes did not exist (404), so every request below fails its
// first status assertion.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/model"
)

type panelAnswer struct {
	Results []struct {
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		Label  string `json:"label"`
		Detail string `json:"detail"`
		App    string `json:"app"`
	} `json:"results"`
	Searched []string `json:"searched"`
}

func panelDecode(t *testing.T, body string) panelAnswer {
	t.Helper()
	var out panelAnswer
	require.NoError(t, json.Unmarshal([]byte(body), &out), body)
	return out
}

// labels answers the rows of one kind, by label.
func (a panelAnswer) labels(kind string) []string {
	var out []string
	for _, r := range a.Results {
		if r.Kind == kind {
			out = append(out, r.Label)
		}
	}
	return out
}

// seedConvert names something of every kind "convert": a person, a group, an
// API key and a storage.
func seedConvert(t *testing.T, pf *permFix) {
	t.Helper()
	ctx := context.Background()
	super, err := pf.Store.GetSupertenant(ctx)
	require.NoError(t, err)
	who := seedUserIn(t, pf.Store, super.ID, "convert.bot@alpha.test")
	require.NoError(t, pf.Store.UpdateUserDisplayName(ctx, who, "Convert Bot"))
	_, err = pf.Store.CreateGroup(ctx, &model.Group{Name: "Convert crew"})
	require.NoError(t, err)
	admin, err := pf.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	_, err = pf.Store.CreateAPIToken(ctx, &model.APIToken{
		UserID: admin.ID, Label: "convert-pipeline", TokenHash: apitoken.HashToken("tok_convert_never_shown"),
		Scopes: "read", Kind: model.TokenKindApp,
	})
	require.NoError(t, err)
	_, err = pf.Store.CreateStorage(ctx, &model.Storage{
		Name: "convert-archive", Driver: "local", MountPath: "/convert-archive", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + strings.ReplaceAll(t.TempDir(), `\`, `\\`) + `"}`),
	})
	require.NoError(t, err)
}

func TestPanelSearch_PlainAccountIsRefused(t *testing.T) {
	pf := newPermFix(t)
	member := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
	for _, p := range []string{"/api/admin/panel-search?q=convert", "/api/admin/panel-search/recent"} {
		status, body := sessionJSON(t, member, http.MethodGet, pf.URL+p, nil)
		assert.Equal(t, http.StatusForbidden, status, "%s answered a plain account: %s", p, body)
	}
	status, body := sessionJSON(t, member, http.MethodPost, pf.URL+"/api/admin/panel-search/recent", map[string]any{"query": "convert"})
	assert.Equal(t, http.StatusForbidden, status, body)
}

// A delegated administrator holding admin.users opens Users and Groups and
// nothing else: the search finds people and groups, and neither the API key,
// nor the storage, nor anything of a kind whose page they cannot open.
func TestPanelSearch_DelegatedAdminFindsOnlyWhatTheirPermissionOpens(t *testing.T) {
	pf := newPermFix(t)
	seedConvert(t, pf)
	pf.override(t, map[string]string{"admin.users": model.PermAllow})
	delegated := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)

	status, body := sessionJSON(t, delegated, http.MethodGet, pf.URL+"/api/admin/panel-search?q=convert", nil)
	require.Equal(t, http.StatusOK, status, body)
	got := panelDecode(t, body)
	assert.ElementsMatch(t, []string{"user", "group"}, got.Searched, "the lists admin.users opens, and only those")
	assert.Contains(t, got.labels("user"), "Convert Bot")
	assert.Contains(t, got.labels("group"), "Convert crew")
	assert.Empty(t, got.labels("key"), "an API key is an administrator's page")
	assert.Empty(t, got.labels("storage"), "a storage is an administrator's page")
	assert.NotContains(t, body, "convert-pipeline")
	assert.NotContains(t, body, "convert-archive")

	// Asking for a kind they may not open by name changes nothing.
	status, body = sessionJSON(t, delegated, http.MethodGet, pf.URL+"/api/admin/panel-search?q=convert&kinds=key,storage", nil)
	require.Equal(t, http.StatusOK, status, body)
	got = panelDecode(t, body)
	assert.Empty(t, got.Results, body)
	assert.Empty(t, got.Searched, body)

	// The administrator finds every kind.
	status, body = sessionJSON(t, pf.AdminA, http.MethodGet, pf.URL+"/api/admin/panel-search?q=convert", nil)
	require.Equal(t, http.StatusOK, status, body)
	got = panelDecode(t, body)
	for _, k := range []string{"user", "group", "key", "storage", "share", "app"} {
		assert.Contains(t, got.Searched, k, "an administrator may open the %s list", k)
	}
	assert.Contains(t, got.labels("user"), "Convert Bot")
	assert.Contains(t, got.labels("group"), "Convert crew")
	assert.Contains(t, got.labels("key"), "convert-pipeline")
	assert.Contains(t, got.labels("storage"), "convert-archive")

	// A prefix narrows to its kind.
	status, body = sessionJSON(t, pf.AdminA, http.MethodGet, pf.URL+"/api/admin/panel-search?q=convert&kinds=user", nil)
	require.Equal(t, http.StatusOK, status, body)
	got = panelDecode(t, body)
	assert.Equal(t, []string{"user"}, got.Searched)
	for _, r := range got.Results {
		assert.Equal(t, "user", r.Kind, body)
	}
}

// A tenant's administrator finds their own tenant's people, keys and
// storages; the platform operator finds everybody's.
func TestPanelSearch_TenantAdminFindsOnlyTheirOwnTenant(t *testing.T) {
	f := newMTFix(t, true)
	ctx := context.Background()
	for _, email := range []string{"member@alpha.test", "member@bravo.test"} {
		u, err := f.Store.GetUserByEmail(ctx, email)
		require.NoError(t, err)
		label := strings.Split(strings.Split(email, "@")[1], ".")[0] + "-sync"
		_, err = f.Store.CreateAPIToken(ctx, &model.APIToken{
			UserID: u.ID, Label: label, TokenHash: apitoken.HashToken("tok_" + label), Scopes: "read", Kind: model.TokenKindApp,
		})
		require.NoError(t, err)
	}

	status, body := mtGet(t, f.AdminA, f.URL+"/api/admin/panel-search?q=member")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, panelDecode(t, body).labels("user"), "member@alpha.test")
	assert.NotContains(t, body, "member@bravo.test", "another tenant's person")

	status, body = mtGet(t, f.AdminA, f.URL+"/api/admin/panel-search?q=sync")
	require.Equal(t, http.StatusOK, status, body)
	got := panelDecode(t, body)
	assert.Contains(t, got.labels("key"), "alpha-sync")
	assert.NotContains(t, got.labels("key"), "bravo-sync", "another tenant's API key")

	status, body = mtGet(t, f.AdminA, f.URL+"/api/admin/panel-search?q=bravo")
	require.Equal(t, http.StatusOK, status, body)
	got = panelDecode(t, body)
	assert.NotContains(t, got.labels("storage"), "bravo", "another tenant's storage")
	assert.NotContains(t, got.Searched, "app", "apps are the platform operator's")

	status, body = mtGet(t, f.Super, f.URL+"/api/admin/panel-search?q=member")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "member@alpha.test")
	assert.Contains(t, body, "member@bravo.test")
}

// A row says what it is and where; it never carries the secret of the list it
// came from - an API key's value or hash, a share's link token.
func TestPanelSearch_RowsCarryNoSecret(t *testing.T) {
	pf := newPermFix(t)
	seedConvert(t, pf)
	ctx := context.Background()

	status, raw := fxJSON(t, http.MethodPost, pf.URL+"/api/files/share", pf.adminTok, map[string]any{"path": "alpha://report.txt"})
	require.Equal(t, http.StatusOK, status, raw)
	var created struct {
		ID    int64 `json:"id"`
		Share struct {
			ID int64 `json:"id"`
		} `json:"share"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &created))
	sid := created.ID
	if sid == 0 {
		sid = created.Share.ID
	}
	require.NotZero(t, sid, raw)
	sh, err := pf.Store.GetShareByID(ctx, sid)
	require.NoError(t, err)
	require.NotEmpty(t, sh.Token)

	status, body := sessionJSON(t, pf.AdminA, http.MethodGet, pf.URL+"/api/admin/panel-search?q=report", nil)
	require.Equal(t, http.StatusOK, status, body)
	got := panelDecode(t, body)
	assert.Contains(t, got.labels("share"), "report.txt", body)
	assert.NotContains(t, body, sh.Token, "a share's link token")
	assert.NotContains(t, body, "/s/", "a share's address")

	status, body = sessionJSON(t, pf.AdminA, http.MethodGet, pf.URL+"/api/admin/panel-search?q=pipeline", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, panelDecode(t, body).labels("key"), "convert-pipeline")
	assert.NotContains(t, body, "tok_convert_never_shown")
	assert.NotContains(t, body, apitoken.HashToken("tok_convert_never_shown"))
}

// Turkish letters and case fold on the server too: "kullanici" finds
// "Kullanıcı", "SIRKET" finds "Şirket".
func TestPanelSearch_FoldsTurkishLettersAndCase(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	super, err := pf.Store.GetSupertenant(ctx)
	require.NoError(t, err)
	who := seedUserIn(t, pf.Store, super.ID, "ayse@alpha.test")
	require.NoError(t, pf.Store.UpdateUserDisplayName(ctx, who, "Ayşe Çağlayan"))
	_, err = pf.Store.CreateGroup(ctx, &model.Group{Name: "Şirket Yönetimi"})
	require.NoError(t, err)

	for q, want := range map[string]string{"ayse caglayan": "Ayşe Çağlayan", "AYŞE": "Ayşe Çağlayan", "sirket yonetimi": "Şirket Yönetimi", "SİRKET": "Şirket Yönetimi"} {
		status, body := sessionJSON(t, pf.AdminA, http.MethodGet, pf.URL+"/api/admin/panel-search?q="+url.QueryEscape(q), nil)
		require.Equal(t, http.StatusOK, status, body)
		got := panelDecode(t, body)
		all := append(got.labels("user"), got.labels("group")...)
		assert.Contains(t, all, want, "%q", q)
	}
}

// The recent searches are the caller's own: kept newest first, one row per
// query, at most model.RecentSearchKeep; another person's row cannot be
// removed (404, as for one that does not exist).
func TestPanelSearch_RecentSearches(t *testing.T) {
	pf := newPermFix(t)
	pf.override(t, map[string]string{"admin.users": model.PermAllow})
	other := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
	me := pf.AdminA
	base := pf.URL + "/api/admin/panel-search/recent"

	type row struct {
		ID    int64  `json:"id"`
		Query string `json:"query"`
	}
	list := func(c *http.Client) []row {
		t.Helper()
		status, body := sessionJSON(t, c, http.MethodGet, base, nil)
		require.Equal(t, http.StatusOK, status, body)
		var ans struct {
			Searches []row `json:"searches"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &ans), body)
		return ans.Searches
	}
	queries := func(rows []row) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.Query
		}
		return out
	}
	add := func(c *http.Client, q string) {
		t.Helper()
		status, body := sessionJSON(t, c, http.MethodPost, base, map[string]any{"query": q})
		require.Equal(t, http.StatusCreated, status, body)
	}

	add(me, "convert")
	add(me, "file:rapor")
	add(me, "convert")
	assert.Equal(t, []string{"convert", "file:rapor"}, queries(list(me)), "the same words again move to the top, once")

	// Somebody else's list is their own, and so are their ids.
	add(other, "groups")
	assert.Equal(t, []string{"groups"}, queries(list(other)))
	mine := list(me)
	status, body := sessionJSON(t, other, http.MethodDelete, fmt.Sprintf("%s/%d", base, mine[0].ID), nil)
	assert.Equal(t, http.StatusNotFound, status, "another person's search: %s", body)
	assert.Len(t, list(me), 2, "still there")

	// One of my own goes.
	status, body = sessionJSON(t, me, http.MethodDelete, fmt.Sprintf("%s/%d", base, mine[0].ID), nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, []string{"file:rapor"}, queries(list(me)))

	// The list keeps the newest RecentSearchKeep.
	for i := 0; i < model.RecentSearchKeep+5; i++ {
		add(me, fmt.Sprintf("q%02d", i))
	}
	rows := list(me)
	require.Len(t, rows, model.RecentSearchKeep)
	assert.Equal(t, fmt.Sprintf("q%02d", model.RecentSearchKeep+4), rows[0].Query, "newest first")
	assert.NotContains(t, queries(rows), "file:rapor", "the oldest went")

	// Clearing mine leaves theirs.
	status, body = sessionJSON(t, me, http.MethodDelete, base, nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Empty(t, list(me))
	assert.Equal(t, []string{"groups"}, queries(list(other)))

	// Nothing, and too much, is refused.
	status, _ = sessionJSON(t, me, http.MethodPost, base, map[string]any{"query": "   "})
	assert.Equal(t, http.StatusBadRequest, status)
	status, _ = sessionJSON(t, me, http.MethodPost, base, map[string]any{"query": strings.Repeat("a", model.RecentSearchMaxLen+1)})
	assert.Equal(t, http.StatusBadRequest, status)
}
