package handlers_test

// Personal and team tags — the finding, and every rule of the fix.
//
// ⚠⚠ The finding (tester, 2026-09-22), reproduced on v0.42 with this file's
// requests before the fix:
//
//	single tenant   A  POST tags ["Müşteri Teklifi"] -> 200 {"tags":["müşteri teklifi"]}
//	                B  GET tags/all                   -> 200 {"tags":["müşteri teklifi"]}
//	                Super(admin) GET tags/all         -> 200 {"tags":["müşteri teklifi"]}
//	                B  GET tags?node_id               -> 200 {"tags":["müşteri teklifi"]}
//	                B  POST tags []                   -> 200; A's tag gone
//	two tenants     B (bravo) GET tags/all            -> 200 {"tags":["müşteri teklifi"]}   (alpha's)
//
// Each test below goes red if its half of the fix is taken out — the report
// lists which was broken and watched.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

type tagWire struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type tagsAnswer struct {
	Tags        []string  `json:"tags"`
	Items       []tagWire `json:"items"`
	CanEditTeam *bool     `json:"can_edit_team"`
}

func tagsPost(t *testing.T, c *http.Client, base string, body any) (int, tagsAnswer, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := c.Post(base+"/api/files/manager/tags", "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	var out tagsAnswer
	_ = json.Unmarshal(buf.Bytes(), &out)
	return resp.StatusCode, out, buf.String()
}

func tagsGet(t *testing.T, c *http.Client, u string) (int, tagsAnswer, string) {
	t.Helper()
	code, body := mtGet(t, c, u)
	var out tagsAnswer
	_ = json.Unmarshal([]byte(body), &out)
	return code, out, body
}

func (f *mtFix) nodeTags(t *testing.T, c *http.Client, nodeID int64) (int, tagsAnswer, string) {
	return tagsGet(t, c, fmt.Sprintf("%s/api/files/manager/tags?node_id=%d", f.URL, nodeID))
}

func (f *mtFix) allTags(t *testing.T, c *http.Client) []tagWire {
	t.Helper()
	code, out, body := tagsGet(t, c, f.URL+"/api/files/manager/tags/all")
	require.Equal(t, http.StatusOK, code, body)
	return out.Items
}

func (f *mtFix) tagged(t *testing.T, c *http.Client, tag, kind string) []string {
	t.Helper()
	q := url.Values{"tag": {tag}}
	if kind != "" {
		q.Set("kind", kind)
	}
	code, body := mtGet(t, c, f.URL+"/api/files/manager/tagged?"+q.Encode())
	require.Equal(t, http.StatusOK, code, body)
	var out struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	names := []string{}
	for _, n := range out.Nodes {
		names = append(names, n.Name)
	}
	return names
}

func items(ts ...string) []map[string]string {
	out := []map[string]string{}
	for i := 0; i+1 < len(ts); i += 2 {
		out = append(out, map[string]string{"name": ts[i], "kind": ts[i+1]})
	}
	return out
}

// PERSONAL: only its owner sees it, and nobody else can take it away. The
// finding's first half, on a single-tenant install and on a two-tenant one.
func TestTags_PersonalIsTheOwnersAlone(t *testing.T) {
	for _, mt := range []bool{false, true} {
		t.Run(fmt.Sprintf("multiTenant=%v", mt), func(t *testing.T) {
			f := newMTFix(t, mt)
			n := f.seedFile(t, f.StA, f.RootA, "teklif.pdf", "x")

			code, out, body := tagsPost(t, f.A, f.URL, map[string]any{
				"node_id": n.ID, "items": items("Müşteri Teklifi", "personal"),
			})
			require.Equal(t, http.StatusOK, code, body)
			require.Equal(t, []tagWire{{"Müşteri Teklifi", "personal"}}, out.Items)

			for who, c := range map[string]*http.Client{"B": f.B, "AdminA": f.AdminA, "Super": f.Super} {
				require.Empty(t, f.allTags(t, c), "%s was told about A's personal tag", who)
				require.Empty(t, f.tagged(t, c, "müşteri teklifi", ""), "%s listed A's file through A's personal tag", who)
			}
			// AdminA is an admin of alpha (single-tenant: of the instance) and
			// can see the file — but not A's personal tag on it.
			code, got, body := f.nodeTags(t, f.AdminA, n.ID)
			require.Equal(t, http.StatusOK, code, body)
			require.Empty(t, got.Items, "an admin saw another user's personal tag on the file")

			// Clearing "every tag I can see" from another account leaves A's alone.
			code, _, body = tagsPost(t, f.AdminA, f.URL, map[string]any{"node_id": n.ID, "items": items()})
			require.Equal(t, http.StatusOK, code, body)
			_, got, _ = f.nodeTags(t, f.A, n.ID)
			require.Equal(t, []tagWire{{"Müşteri Teklifi", "personal"}}, got.Items, "another account removed A's personal tag")
			require.Equal(t, []string{"teklif.pdf"}, f.tagged(t, f.A, "MÜŞTERİ TEKLİFİ", "personal"))
		})
	}
}

// TEAM: everyone in the tenant who can see the file sees it; an editor may
// remove it.
func TestTags_TeamIsSharedWithinTheTenant(t *testing.T) {
	f := newMTFix(t, true)
	n := f.seedFile(t, f.StA, f.RootA, "rapor.pdf", "x")
	code, _, body := tagsPost(t, f.A, f.URL, map[string]any{"node_id": n.ID, "items": items("Rapor", "team")})
	require.Equal(t, http.StatusOK, code, body)

	require.Equal(t, []tagWire{{"Rapor", "team"}}, f.allTags(t, f.AdminA), "a tenant colleague must see a team tag")
	require.Equal(t, []string{"rapor.pdf"}, f.tagged(t, f.AdminA, "rapor", "team"))
	require.Equal(t, []tagWire{{"Rapor", "team"}}, f.allTags(t, f.Super), "the platform operator reaches every tenant")

	// A colleague with edit rights may take it off (AdminA is an admin, so owner).
	code, got, body := tagsPost(t, f.AdminA, f.URL, map[string]any{"node_id": n.ID, "items": items()})
	require.Equal(t, http.StatusOK, code, body)
	require.Empty(t, got.Items)
	_, got, _ = f.nodeTags(t, f.A, n.ID)
	require.Empty(t, got.Items)
}

// A team tag never crosses a tenant boundary — not even on a file both
// tenants can see, and not through the search box.
func TestTags_TeamNeverCrossesTenants(t *testing.T) {
	f := newMTFix(t, true)
	n := f.seedFile(t, f.StA, f.RootA, "ortak.pdf", "x")
	code, _, body := tagsPost(t, f.A, f.URL, map[string]any{"node_id": n.ID, "items": items("Gizli Proje", "team")})
	require.Equal(t, http.StatusOK, code, body)

	require.Empty(t, f.allTags(t, f.B), "bravo was told alpha's team tag")
	require.Empty(t, f.tagged(t, f.B, "gizli proje", ""))

	// Now bravo can SEE the file: the storage is linked to both tenants.
	require.NoError(t, f.Store.LinkProviderStorage(context.Background(), f.ProvB, f.StA.ID))
	code, got, body := f.nodeTags(t, f.B, n.ID)
	require.Equal(t, http.StatusOK, code, "bravo must reach the shared file: %s", body)
	require.Empty(t, got.Items, "alpha's team tag reached bravo on a shared file")
	require.Empty(t, f.allTags(t, f.B))
	search := f.URL + "/api/files/search?q=" + url.QueryEscape(`tag:"gizli proje"`)
	code, body = mtGet(t, f.A, search)
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "ortak.pdf", "control: alpha's own tag search must find the file")
	code, body = mtGet(t, f.B, search)
	require.Equal(t, http.StatusOK, code, body)
	require.NotContains(t, body, "ortak.pdf", "bravo's tag search matched alpha's team tag")

	// Bravo's own team tag on the same file is bravo's; alpha never sees it.
	code, _, body = tagsPost(t, f.B, f.URL, map[string]any{"node_id": n.ID, "items": items("Gizli Proje", "team")})
	require.Equal(t, http.StatusOK, code, body)
	_, got, _ = f.nodeTags(t, f.A, n.ID)
	require.Equal(t, []tagWire{{"Gizli Proje", "team"}}, got.Items, "alpha sees exactly one team tag — its own")
	// And bravo clearing its view of the file does not touch alpha's.
	code, _, body = tagsPost(t, f.B, f.URL, map[string]any{"node_id": n.ID, "items": items()})
	require.Equal(t, http.StatusOK, code, body)
	_, got, _ = f.nodeTags(t, f.A, n.ID)
	require.Equal(t, []tagWire{{"Gizli Proje", "team"}}, got.Items)
}

// A VIEWER sees team tags and cannot change them; personal tags are theirs to
// keep. The finding's second half: "the other user could remove it".
func TestTags_ViewerCannotChangeTeamTags(t *testing.T) {
	f := newMTFix(t, false)
	n := f.seedFile(t, f.StA, f.RootA, "sozlesme.pdf", "x")
	code, _, body := tagsPost(t, f.A, f.URL, map[string]any{"node_id": n.ID, "items": items("Sözleşme", "team")})
	require.Equal(t, http.StatusOK, code, body)

	viewer := f.seedViewer(t, "viewer@platform.test")
	code, got, body := f.nodeTags(t, viewer, n.ID)
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, []tagWire{{"Sözleşme", "team"}}, got.Items, "a viewer must SEE team tags")
	require.NotNil(t, got.CanEditTeam)
	require.False(t, *got.CanEditTeam, "the picker must not offer team editing to a viewer")

	code, _, body = tagsPost(t, viewer, f.URL, map[string]any{"node_id": n.ID, "items": items()})
	require.Equal(t, http.StatusForbidden, code, "a viewer removed a team tag: %s", body)
	code, _, body = tagsPost(t, viewer, f.URL, map[string]any{"node_id": n.ID,
		"items": items("Sözleşme", "team", "Ek", "team")})
	require.Equal(t, http.StatusForbidden, code, "a viewer added a team tag: %s", body)
	// The old client shape: leaving the team tag OUT of the list is a removal.
	code, _, body = tagsPost(t, viewer, f.URL, map[string]any{"node_id": n.ID, "tags": []string{}})
	require.Equal(t, http.StatusForbidden, code, "a viewer's old client removed a team tag: %s", body)
	_, got, _ = f.nodeTags(t, f.A, n.ID)
	require.Equal(t, []tagWire{{"Sözleşme", "team"}}, got.Items, "the team tag must survive every refused write")

	// Personal tags are the viewer's own: allowed, and the team tag round-trips.
	code, out, body := tagsPost(t, viewer, f.URL, map[string]any{"node_id": n.ID,
		"items": items("Sözleşme", "team", "Okunacak", "personal")})
	require.Equal(t, http.StatusOK, code, body)
	require.ElementsMatch(t, []tagWire{{"Sözleşme", "team"}, {"Okunacak", "personal"}}, out.Items)
	// …and so is the old client that round-trips every name it was shown.
	code, _, body = tagsPost(t, viewer, f.URL, map[string]any{"node_id": n.ID,
		"tags": []string{"Sözleşme", "Okunacak", "Yeni"}})
	require.Equal(t, http.StatusOK, code, body)
}

// What a pre-v0.43 client gets: its `{tags:[…]}` makes PERSONAL tags unless
// it says `kind: "team"`, and names it round-trips keep their kind.
func TestTags_OldClientDefaultIsPersonal(t *testing.T) {
	f := newMTFix(t, false)
	n := f.seedFile(t, f.StA, f.RootA, "eski.pdf", "x")
	f.tag(t, n.ID, "arşiv") // a migrated (team) tag
	code, out, body := tagsPost(t, f.A, f.URL, map[string]any{"node_id": n.ID, "tags": []string{"arşiv", "Taslak"}})
	require.Equal(t, http.StatusOK, code, body)
	require.ElementsMatch(t, []tagWire{{"arşiv", "team"}, {"Taslak", "personal"}}, out.Items)
	require.ElementsMatch(t, []string{"arşiv", "Taslak"}, out.Tags, "the old `tags` field must still answer")
	require.Equal(t, []tagWire{{"arşiv", "team"}}, f.allTags(t, f.B), "an old client's new tag reached another user")

	code, out, body = tagsPost(t, f.A, f.URL, map[string]any{"node_id": n.ID, "tags": []string{"arşiv", "Taslak", "Ortak"}, "kind": "team"})
	require.Equal(t, http.StatusOK, code, body)
	require.ElementsMatch(t, []tagWire{{"arşiv", "team"}, {"Taslak", "personal"}, {"Ortak", "team"}}, out.Items)

	code, _, body = tagsPost(t, f.A, f.URL, map[string]any{"node_id": n.ID, "tags": []string{"x"}, "kind": "shared"})
	require.Equal(t, http.StatusBadRequest, code, body)
}

// Case is kept as typed; "the same tag" is decided by tagname.Key, which
// merges the Turkish i's and folds everything else.
func TestTags_CaseKeptMatchingFolded(t *testing.T) {
	f := newMTFix(t, false)
	n := f.seedFile(t, f.StA, f.RootA, "a.pdf", "x")
	m := f.seedFile(t, f.StA, f.RootA, "b.pdf", "x")

	code, out, body := tagsPost(t, f.A, f.URL, map[string]any{"node_id": n.ID,
		"items": items("Müşteri Teklifi", "team", "IŞIK", "personal", "INVOICE", "personal")})
	require.Equal(t, http.StatusOK, code, body)
	require.ElementsMatch(t, []tagWire{{"Müşteri Teklifi", "team"}, {"IŞIK", "personal"}, {"INVOICE", "personal"}}, out.Items,
		"names must come back exactly as typed")

	// Typed differently on a second file: the SAME tags, first spelling kept.
	code, out, body = tagsPost(t, f.A, f.URL, map[string]any{"node_id": m.ID,
		"items": items("MÜŞTERİ TEKLİFİ", "team", "ışık", "personal", "invoice", "personal")})
	require.Equal(t, http.StatusOK, code, body)
	require.ElementsMatch(t, []tagWire{{"Müşteri Teklifi", "team"}, {"IŞIK", "personal"}, {"INVOICE", "personal"}}, out.Items)
	require.Len(t, f.allTags(t, f.A), 3, "differently-cased names must not make twin tags")
	require.ElementsMatch(t, []string{"a.pdf", "b.pdf"}, f.tagged(t, f.A, "müşteri teklifi", "team"))
	require.ElementsMatch(t, []string{"a.pdf", "b.pdf"}, f.tagged(t, f.A, "Işık", ""))

	// Accents are part of the word.
	code, out, body = tagsPost(t, f.A, f.URL, map[string]any{"node_id": m.ID,
		"items": items("MÜŞTERİ TEKLİFİ", "team", "ışık", "personal", "invoice", "personal", "Musteri Teklifi", "team")})
	require.Equal(t, http.StatusOK, code, body)
	require.Len(t, out.Items, 4, "müşteri and musteri are different words: %s", body)
}

// One name, two kinds: the tag view can show either, or both.
func TestTags_TaggedViewByKind(t *testing.T) {
	f := newMTFix(t, false)
	mine := f.seedFile(t, f.StA, f.RootA, "benim.pdf", "x")
	ours := f.seedFile(t, f.StA, f.RootA, "bizim.pdf", "x")
	_, _, _ = tagsPost(t, f.A, f.URL, map[string]any{"node_id": mine.ID, "items": items("Rapor", "personal")})
	_, _, _ = tagsPost(t, f.A, f.URL, map[string]any{"node_id": ours.ID, "items": items("Rapor", "team")})

	require.ElementsMatch(t, []string{"benim.pdf", "bizim.pdf"}, f.tagged(t, f.A, "rapor", ""))
	require.Equal(t, []string{"benim.pdf"}, f.tagged(t, f.A, "rapor", "personal"))
	require.Equal(t, []string{"bizim.pdf"}, f.tagged(t, f.A, "rapor", "team"))
	require.Equal(t, []tagWire{{"Rapor", "personal"}, {"Rapor", "team"}}, f.allTags(t, f.A),
		"both kinds listed, personal first for one name")
	require.Equal(t, []tagWire{{"Rapor", "team"}}, f.allTags(t, f.B))
	code, body := mtGet(t, f.A, f.URL+"/api/files/manager/tagged?tag=rapor&kind=everyone")
	require.Equal(t, http.StatusBadRequest, code, body)
}

// A tag is only named to someone who can see a file carrying it: on an RBAC
// storage a member with no grant learns neither the file nor the tag.
func TestTags_OnlyNamedToWhoCanSeeTheFile(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	st, err := f.Store.CreateStorage(ctx, &model.Storage{
		Name: "ik", Driver: "local", MountPath: "/ik", ConfigJSON: []byte(`{}`),
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true, RBACEnabled: true,
	})
	require.NoError(t, err)
	n, err := f.Store.CreateNode(ctx, &model.Node{StorageID: st.ID, Name: "maaslar.xlsx", Path: "/maaslar.xlsx",
		PathHash: pathkey.Hash(st.ID, "/maaslar.xlsx"), Type: model.NodeTypeFile, Size: 1})
	require.NoError(t, err)
	code, _, body := tagsPost(t, f.Super, f.URL, map[string]any{"node_id": n.ID, "items": items("İşten Çıkarmalar", "team")})
	require.Equal(t, http.StatusOK, code, body)

	require.Empty(t, f.allTags(t, f.A), "a member with no grant was told the tag's name")
	require.Empty(t, f.tagged(t, f.A, "işten çıkarmalar", ""), "a member with no grant listed the file")
	code, _, body = f.nodeTags(t, f.A, n.ID)
	require.Equal(t, http.StatusNotFound, code, body)
	code, _, body = tagsPost(t, f.A, f.URL, map[string]any{"node_id": n.ID, "items": items("x", "personal")})
	require.Equal(t, http.StatusNotFound, code, body)

	// With a viewer grant the member sees it — and still may not change it.
	_, err = f.Store.CreateFileGrant(ctx, &model.FileGrant{StorageID: st.ID, PathPrefix: "", IsDir: true, UserID: f.UserA, Level: model.GrantViewer})
	require.NoError(t, err)
	require.Equal(t, []tagWire{{"İşten Çıkarmalar", "team"}}, f.allTags(t, f.A))
	code, _, body = tagsPost(t, f.A, f.URL, map[string]any{"node_id": n.ID, "items": items()})
	require.Equal(t, http.StatusForbidden, code, body)
}

// seedViewer adds a role=viewer account homed where the fixture's users are
// and logs it in.
func (f *mtFix) seedViewer(t *testing.T, email string) *http.Client {
	t.Helper()
	ctx := context.Background()
	hash, err := authlocal.HashPassword(mtUserPass)
	require.NoError(t, err)
	u, err := f.Store.CreateUser(ctx, email, hash, model.RoleViewer, "tr", "UTC")
	require.NoError(t, err)
	a, err := f.Store.GetUser(ctx, f.UserA)
	require.NoError(t, err)
	require.NotNil(t, a.ProviderID)
	require.NoError(t, f.Store.SetUserProvider(ctx, u.ID, *a.ProviderID, ""))
	return mtLogin(t, f.Srv, email, mtUserPass)
}
