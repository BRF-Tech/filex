package handlers_test

// A whole storage given to a person or a group (#117), at the HTTP doors.
//
// Nothing new stands behind the feature: the admin page's Access section and
// the explorer's storage row both post an ordinary grant on the storage ROOT
// (`path: "alpha://"`, stored as path_prefix ""), and the acl package's own
// tests (acl_storage_root_test.go) pin what such a row means. These pin what
// the doors do with it - the row they write, what the person can then reach,
// that revoking it leaves folder grants alone, and every line the permission
// routes already hold (RBAC off, the account-role ceiling, the tenant, a
// key's `root:`) - and that a whole storage is never handed to a public link.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// storageRootFix is tenant alpha's member and a storage with RBAC on: a file
// two folders down (Team/deep/plan.txt), another folder (Private) and the
// fixture's report.txt at the top. The member holds no grant yet.
func storageRootFix(t *testing.T) *permFix {
	t.Helper()
	pf := newPermFix(t)
	for _, step := range [][2]string{{"alpha://", "Team"}, {"alpha://Team", "deep"}, {"alpha://", "Private"}} {
		status, body := fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": step[0], "name": step[1]})
		require.Equal(t, http.StatusOK, status, "new folder %s in %s: %s", step[1], step[0], body)
	}
	fxUpload(t, pf.URL, pf.adminTok, "alpha://Team/deep", "plan.txt", "the deep plan")
	fxUpload(t, pf.URL, pf.adminTok, "alpha://Private", "salary.txt", "numbers")
	pf.StA.RBACEnabled = true
	require.NoError(t, pf.Store.UpdateStorage(context.Background(), pf.StA))
	return pf
}

// grantAt posts a person's grant as the administrator and returns its id.
func grantAt(t *testing.T, pf *permFix, wire string, userID int64, level string) int64 {
	t.Helper()
	status, body := fxJSON(t, "POST", pf.URL+"/api/files/permissions", pf.adminTok, map[string]any{
		"path": wire, "user_id": userID, "level": level,
	})
	require.Equal(t, http.StatusOK, status, "grant %s on %s: %s", level, wire, body)
	return int64(decode(t, body)["id"].(float64))
}

// downloadAs reads one file through the explorer's download door.
func downloadAs(t *testing.T, pf *permFix, tok, wire string) (int, string) {
	t.Helper()
	return fxReq(t, "GET", pf.URL+"/api/files/manager?action=download&path="+url.QueryEscape(wire), tok, nil, "")
}

// panelAt is the permissions panel's answer for one path.
func panelAt(t *testing.T, pf *permFix, wire string) (direct, inherited []map[string]any, rbac bool) {
	t.Helper()
	status, body := fxJSON(t, "GET", pf.URL+"/api/files/permissions?path="+url.QueryEscape(wire), pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	var got struct {
		Direct    []map[string]any `json:"direct"`
		Inherited []map[string]any `json:"inherited"`
		RBAC      bool             `json:"storage_rbac"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &got), body)
	return got.Direct, got.Inherited, got.RBAC
}

func TestStorageRootGrant_GivesThePersonTheWholeStorage(t *testing.T) {
	pf := storageRootFix(t)

	status, _ := fxIndex(t, pf.URL, pf.memberTok, "alpha://Team/deep")
	require.NotEqual(t, http.StatusOK, status, "no grant yet: the folder is out of reach")

	status, body := fxJSON(t, "POST", pf.URL+"/api/files/permissions", pf.adminTok, map[string]any{
		"path": "alpha://", "user_id": pf.UserA, "level": model.GrantEditor,
	})
	require.Equal(t, http.StatusOK, status, body)
	row := decode(t, body)
	assert.Equal(t, "", row["path_prefix"], "a grant on the storage root is the empty prefix (model.FileGrant)")
	assert.Equal(t, true, row["is_dir"], "the storage root is a folder")
	assert.EqualValues(t, pf.StA.ID, row["storage_id"])
	rootID := int64(row["id"].(float64))

	// The storage is in the person's drive list now...
	status, body = fxReq(t, "GET", pf.URL+"/api/files/manager?action=index", pf.memberTok, nil, "")
	require.Equal(t, http.StatusOK, status, body)
	var top struct {
		Storages []string `json:"storages"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &top), body)
	assert.Contains(t, top.Storages, "alpha", "a root grant puts the storage in the list")

	// ...and every folder in it, however deep, without a grant of its own.
	status, names := fxIndex(t, pf.URL, pf.memberTok, "alpha://")
	require.Equal(t, http.StatusOK, status)
	assert.Subset(t, names, []string{"Team", "Private", "report.txt"})
	status, names = fxIndex(t, pf.URL, pf.memberTok, "alpha://Team/deep")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, names, "plan.txt")
	status, got := downloadAs(t, pf, pf.memberTok, "alpha://Team/deep/plan.txt")
	require.Equal(t, http.StatusOK, status, got)
	assert.Equal(t, "the deep plan", got)
	// Editor on the whole storage: a write anywhere in it is theirs.
	status, body = fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://Private", "mine.txt", "mine")
	assert.Equal(t, http.StatusOK, status, "an editor of the storage writes in any folder of it: %s", body)

	// The panel says where the access comes from: the root's own grant at the
	// root, an inherited one below it.
	direct, _, rbac := panelAt(t, pf, "alpha://")
	assert.True(t, rbac)
	require.Len(t, direct, 1, "the root's own grants")
	assert.EqualValues(t, rootID, direct[0]["id"])
	assert.Equal(t, "user", direct[0]["kind"])
	assert.Equal(t, "member@alpha.test", direct[0]["user_email"])
	direct, inherited, _ := panelAt(t, pf, "alpha://Team/deep")
	assert.Empty(t, direct)
	require.Len(t, inherited, 1)
	assert.EqualValues(t, rootID, inherited[0]["id"])
	assert.Equal(t, true, inherited[0]["inherited"])
}

// The storage list says the caller's level at each storage's ROOT
// (storage_info[].perm), so the explorer can offer "Access…" on a storage row
// to whoever the panel routes let manage the whole storage - an owner of the
// root, not only an administrator. Before 0.57 the list said nothing, and an
// owner of the root could manage it through the API but not from the
// explorer.
func TestStorageRootGrant_TheStorageListSaysTheLevelAtEachRoot(t *testing.T) {
	pf := storageRootFix(t)
	levels := func(tok string) map[string]string {
		t.Helper()
		status, body := fxReq(t, "GET", pf.URL+"/api/files/manager?action=index", tok, nil, "")
		require.Equal(t, http.StatusOK, status, body)
		var got struct {
			Info []struct {
				Name string `json:"name"`
				Perm string `json:"perm"`
			} `json:"storage_info"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &got), body)
		out := map[string]string{}
		for _, i := range got.Info {
			out[i.Name] = i.Perm
		}
		return out
	}

	assert.Equal(t, "owner", levels(pf.adminTok)["alpha"], "an administrator owns every root")
	_, listed := levels(pf.memberTok)["alpha"]
	assert.False(t, listed, "no grant: the storage is not listed, and not described")
	assert.Equal(t, "editor", levels(pf.memberTok)["alpha-arsiv"], "access control off: the role's own level")

	grantAt(t, pf, "alpha://Team", pf.UserA, model.GrantOwner)
	assert.Equal(t, "none", levels(pf.memberTok)["alpha"], "owning a folder is not owning the storage")

	grantAt(t, pf, "alpha://", pf.UserA, model.GrantEditor)
	assert.Equal(t, "editor", levels(pf.memberTok)["alpha"])

	grantAt(t, pf, "alpha://", pf.UserA, model.GrantOwner)
	assert.Equal(t, "owner", levels(pf.memberTok)["alpha"])
	// ...and an owner of the root may do what the menu then offers.
	status, body := fxJSON(t, "GET", pf.URL+"/api/files/permissions?path="+url.QueryEscape("alpha://"), pf.memberTok, nil)
	assert.Equal(t, http.StatusOK, status, "the owner of the root reads its grants: %s", body)
	status, body = fxJSON(t, "POST", pf.URL+"/api/files/permissions", pf.memberTok, map[string]any{
		"path": "alpha://", "user_id": pf.UserB, "level": model.GrantViewer,
	})
	assert.Equal(t, http.StatusOK, status, "and gives the whole storage to someone else: %s", body)
}

// The issue's own words: "revoke access to an entire Storage without
// modifying individual folder permissions". Revoking deletes ONE row.
func TestStorageRootGrant_RevokingItKeepsFolderGrants(t *testing.T) {
	pf := storageRootFix(t)
	ctx := context.Background()
	root := grantAt(t, pf, "alpha://", pf.UserA, model.GrantEditor)
	folder := grantAt(t, pf, "alpha://Team", pf.UserA, model.GrantViewer)

	status, body := fxJSON(t, "DELETE", pf.URL+"/api/files/permissions/"+idStr(root), pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)

	gone, err := pf.Store.GetFileGrant(ctx, root)
	assert.True(t, err != nil || gone == nil, "the root grant is gone")
	kept, err := pf.Store.GetFileGrant(ctx, folder)
	require.NoError(t, err, "the folder grant is still there")
	require.NotNil(t, kept)
	assert.Equal(t, "Team", kept.PathPrefix)
	assert.Equal(t, model.GrantViewer, kept.Level, "and unchanged")

	// The folder grant still answers, the rest of the storage is gone.
	status, names := fxIndex(t, pf.URL, pf.memberTok, "alpha://Team/deep")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, names, "plan.txt")
	status, _ = downloadAs(t, pf, pf.memberTok, "alpha://Private/salary.txt")
	assert.NotEqual(t, http.StatusOK, status, "outside the folder grant nothing is left")
	status, _ = fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://Team", "late.txt", "x")
	assert.NotEqual(t, http.StatusOK, status, "the folder grant is a viewer's: the editor rights went with the root")
}

// A narrower folder grant raises, never lowers: the root's editor still
// writes inside a folder where the same person holds only viewer.
func TestStorageRootGrant_TheHigherGrantCounts(t *testing.T) {
	pf := storageRootFix(t)
	grantAt(t, pf, "alpha://", pf.UserA, model.GrantEditor)
	grantAt(t, pf, "alpha://Team", pf.UserA, model.GrantViewer)
	status, body := fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://Team/deep", "notes.txt", "x")
	assert.Equal(t, http.StatusOK, status, "a viewer grant on a folder does not narrow the storage's editor: %s", body)
}

func TestStorageRootGrant_AGroupGetsTheWholeStorage(t *testing.T) {
	pf := storageRootFix(t)
	gid := newGroup(t, pf, map[string]any{"name": "Whole storage readers"})
	status, body := fxJSON(t, "POST", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members", pf.adminTok, map[string]any{"user_ids": []int64{pf.UserA}})
	require.Equal(t, http.StatusOK, status, body)

	status, body = fxJSON(t, "POST", pf.URL+"/api/files/permissions", pf.adminTok, map[string]any{
		"path": "alpha://", "group_id": gid, "level": model.GrantViewer,
	})
	require.Equal(t, http.StatusOK, status, body)
	row := decode(t, body)
	assert.Equal(t, "", row["path_prefix"])
	assert.Equal(t, true, row["is_dir"])
	assert.EqualValues(t, gid, row["group_id"])
	grantID := int64(row["id"].(float64))

	status, names := fxIndex(t, pf.URL, pf.memberTok, "alpha://Team/deep")
	require.Equal(t, http.StatusOK, status, "a member reaches the whole storage through the group")
	assert.Contains(t, names, "plan.txt")
	status, _ = fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://Team", "nope.txt", "x")
	assert.NotEqual(t, http.StatusOK, status, "the group was given viewer")

	direct, _, _ := panelAt(t, pf, "alpha://")
	require.Len(t, direct, 1)
	assert.Equal(t, "group", direct[0]["kind"])
	assert.Equal(t, "Whole storage readers", direct[0]["group_name"])

	status, body = fxJSON(t, "DELETE", pf.URL+"/api/files/permissions/groups/"+idStr(grantID), pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	status, _ = fxIndex(t, pf.URL, pf.memberTok, "alpha://Team/deep")
	assert.NotEqual(t, http.StatusOK, status, "revoked: the storage is not the member's any more")
}

// With per-item access control off everyone opens the storage by their
// role, so a grant would change nothing: the door says so, for a person and
// for a group, and writes nothing.
func TestStorageRootGrant_RefusedWhileRBACIsOff(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	gid := newGroup(t, pf, map[string]any{"name": "Early birds"})

	status, body := fxJSON(t, "POST", pf.URL+"/api/files/permissions", pf.adminTok, map[string]any{
		"path": "alpha://", "user_id": pf.UserA, "level": model.GrantEditor,
	})
	assert.Equal(t, http.StatusConflict, status, body)
	status, body = fxJSON(t, "POST", pf.URL+"/api/files/permissions", pf.adminTok, map[string]any{
		"path": "alpha://", "group_id": gid, "level": model.GrantViewer,
	})
	assert.Equal(t, http.StatusConflict, status, body)

	people, err := pf.Store.ListFileGrantsByStorage(ctx, pf.StA.ID)
	require.NoError(t, err)
	assert.Empty(t, people)
	groups, err := pf.Store.ListGroupFileGrantsByStorage(ctx, pf.StA.ID)
	require.NoError(t, err)
	assert.Empty(t, groups)
}

// The account role is the ceiling on the whole storage as on a folder.
func TestStorageRootGrant_AViewerAccountIsGivenViewerOnly(t *testing.T) {
	pf := storageRootFix(t)
	ctx := context.Background()
	hash, err := local.HashPassword("ViewerPass!1")
	require.NoError(t, err)
	v, err := pf.Store.CreateUser(ctx, "viewer@example.com", hash, model.RoleViewer, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, pf.Store.SetUserProvider(ctx, v.ID, mustSupertenant(t, pf), ""))

	for _, level := range []string{model.GrantEditor, model.GrantOwner} {
		status, body := fxJSON(t, "POST", pf.URL+"/api/files/permissions", pf.adminTok, map[string]any{
			"path": "alpha://", "user_id": v.ID, "level": level,
		})
		assert.Equal(t, http.StatusBadRequest, status, "%s for a viewer account: %s", level, body)
	}
	grantAt(t, pf, "alpha://", v.ID, model.GrantViewer)
}

// Another tenant's storage reads as unknown, and so does another tenant's
// person: a tenant administrator hands out its own storages to its own
// people.
func TestStorageRootGrant_AnotherTenantIsUnknown(t *testing.T) {
	f := newMTFix(t, true)
	t.Cleanup(perm.Invalidate)
	ctx := context.Background()
	for _, st := range []*model.Storage{f.StA, f.StB} {
		st.RBACEnabled = true
		require.NoError(t, f.Store.UpdateStorage(ctx, st))
	}

	status, body := sessionJSON(t, f.AdminA, "POST", f.URL+"/api/files/permissions", map[string]any{
		"path": "bravo://", "user_id": f.UserA, "level": model.GrantViewer,
	})
	assert.Equal(t, http.StatusNotFound, status, "bravo's storage, from alpha's administrator: %s", body)
	status, body = sessionJSON(t, f.AdminA, "GET", f.URL+"/api/files/permissions?path="+url.QueryEscape("bravo://"), nil)
	assert.Equal(t, http.StatusNotFound, status, "nor is its panel readable: %s", body)
	written, err := f.Store.ListFileGrantsByStorage(ctx, f.StB.ID)
	require.NoError(t, err)
	assert.Empty(t, written)

	status, body = sessionJSON(t, f.AdminA, "POST", f.URL+"/api/files/permissions", map[string]any{
		"path": "alpha://", "user_id": f.UserB, "level": model.GrantViewer,
	})
	assert.Equal(t, http.StatusNotFound, status, "bravo's member, on alpha's storage: %s", body)

	status, body = sessionJSON(t, f.AdminA, "POST", f.URL+"/api/files/permissions", map[string]any{
		"path": "alpha://", "user_id": f.UserA, "level": model.GrantViewer,
	})
	assert.Equal(t, http.StatusOK, status, "alpha's own storage to alpha's own member: %s", body)
}

// A key confined to one folder (`root:`) cannot give - or change, or take
// back - the storage that folder is in, even when its account is an
// administrator who could from the panel.
func TestStorageRootGrant_ARootKeyStaysInItsFolder(t *testing.T) {
	pf := storageRootFix(t)
	ctx := context.Background()
	admin, err := pf.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	tok := issueToken(t, pf.Store, admin.ID, "read,write,delete,root:alpha://Team", nil)

	// A folder beside the root is refused, whatever the body is labelled.
	body := map[string]any{"path": "alpha://Private", "user_id": pf.UserA, "level": model.GrantEditor}
	status, resp := fxJSON(t, "POST", pf.URL+"/api/files/permissions", tok, body)
	assert.Equal(t, http.StatusForbidden, status, "JSON: %s", resp)
	raw, _ := json.Marshal(body)
	status, resp = fxReq(t, "POST", pf.URL+"/api/files/permissions", tok, strings.NewReader(string(raw)), "text/plain")
	assert.Equal(t, http.StatusForbidden, status, "the same body as text/plain: %s", resp)
	written, err := pf.Store.ListFileGrantsByStorage(ctx, pf.StA.ID)
	require.NoError(t, err)
	assert.Empty(t, written, "nothing was given")

	// The storage itself cannot even be named: to a confined key `alpha://` is
	// its own folder (confine resolves a storage-root path to the root, as a
	// listing of `alpha://` opens Team), so the grant lands on Team - inside
	// the root - and never on the storage (path prefix "").
	body["path"] = "alpha://"
	status, resp = fxJSON(t, "POST", pf.URL+"/api/files/permissions", tok, body)
	require.Equal(t, http.StatusOK, status, "JSON: %s", resp)
	assert.Equal(t, "Team", decode(t, resp)["path_prefix"], "JSON: %s", resp)
	raw, _ = json.Marshal(body)
	status, resp = fxReq(t, "POST", pf.URL+"/api/files/permissions", tok, strings.NewReader(string(raw)), "text/plain")
	require.Equal(t, http.StatusOK, status, "the same body as text/plain: %s", resp)
	assert.Equal(t, "Team", decode(t, resp)["path_prefix"], "the same body as text/plain: %s", resp)
	written, err = pf.Store.ListFileGrantsByStorage(ctx, pf.StA.ID)
	require.NoError(t, err)
	require.NotEmpty(t, written)
	for _, g := range written {
		assert.Equal(t, "Team", g.PathPrefix, "the storage was not given: %+v", g)
	}

	root := grantAt(t, pf, "alpha://", pf.UserA, model.GrantViewer)
	status, resp = fxJSON(t, "PATCH", pf.URL+"/api/files/permissions/"+idStr(root), tok, map[string]any{"level": model.GrantOwner})
	assert.Equal(t, http.StatusForbidden, status, "change: %s", resp)
	status, resp = fxJSON(t, "DELETE", pf.URL+"/api/files/permissions/"+idStr(root), tok, nil)
	assert.Equal(t, http.StatusForbidden, status, "revoke: %s", resp)
	kept, err := pf.Store.GetFileGrant(ctx, root)
	require.NoError(t, err)
	assert.Equal(t, model.GrantViewer, kept.Level, "the grant is as it was")
}

// A whole storage goes to people, never to a public link: not by the share
// door (download or file request), not by the invitation's link fallback for
// an address with no account, not by the agent door. Before 0.57 the
// invitation answered "not indexed yet" - only the missing catalogue row
// stood between it and a link to everything - and the agent door a 500.
func TestStorageRootGrant_NoPublicLinkToAWholeStorage(t *testing.T) {
	pf := storageRootFix(t)
	ctx := context.Background()
	links := func() int64 {
		t.Helper()
		_, n, err := pf.Store.ListAllShares(ctx, nil, false, 500, 0)
		require.NoError(t, err)
		return n
	}
	before := links()

	for _, kind := range []string{"", model.ShareKindDrop} {
		req := map[string]any{"path": "alpha://"}
		if kind != "" {
			req["kind"] = kind
		}
		status, body := fxJSON(t, "POST", pf.URL+"/api/files/share", pf.adminTok, req)
		assert.GreaterOrEqual(t, status, 400, "share door, kind %q: %s", kind, body)
		assert.Less(t, status, 500, "a refusal, not a fault (kind %q): %s", kind, body)
	}

	status, body := fxJSON(t, "POST", pf.URL+"/api/files/permissions/invite", pf.adminTok, map[string]any{
		"path": "alpha://", "email": "guest@example.com", "level": model.GrantViewer,
	})
	require.Equal(t, http.StatusBadRequest, status, body)
	refusal := decode(t, body)
	assert.Equal(t, "root_link", refusal["error"])
	assert.NotEmpty(t, refusal["message"], "a sentence for the person")
	assert.NotContains(t, body, "/s/", "no link in the answer")

	status, body = fxJSON(t, "POST", pf.URL+"/api/ai/share", pf.adminTok, map[string]any{"path": "alpha://"})
	assert.Equal(t, http.StatusBadRequest, status, "agent door: a call to fix, not a 500 to retry: %s", body)
	assert.Contains(t, body, "storage root")

	assert.Equal(t, before, links(), "no link was made")

	// The rule is about the root alone: a folder in it is still invited to.
	status, body = fxJSON(t, "POST", pf.URL+"/api/files/permissions/invite", pf.adminTok, map[string]any{
		"path": "alpha://Team", "email": "guest@example.com", "level": model.GrantViewer,
	})
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "shared", decode(t, body)["mode"])
	assert.Equal(t, before+1, links())
}

// The invitation's link for an address with no account is a public link, so
// it asks the one rule every link door asks (public_link_rule.go) - not the
// share.links permission alone, as it did before 0.57: an end-to-end
// encrypted folder is never linked, whichever door asks.
//
// RED PROOF (before 0.57): 200, mode "shared", a live link to the folder.
func TestStorageRootGrant_TheInvitationLinkAsksTheOneLinkRule(t *testing.T) {
	pf := storageRootFix(t)
	ctx := context.Background()
	key := "/Team/" + e2e.MarkerName
	_, err := pf.Store.CreateNode(ctx, &model.Node{
		StorageID: pf.StA.ID, Name: e2e.MarkerName, Path: key, PathHash: pathkey.Hash(pf.StA.ID, key),
		Type: model.NodeTypeFile, Size: 1,
	})
	require.NoError(t, err)
	_, before, err := pf.Store.ListAllShares(ctx, nil, false, 500, 0)
	require.NoError(t, err)

	status, body := fxJSON(t, "POST", pf.URL+"/api/files/permissions/invite", pf.adminTok, map[string]any{
		"path": "alpha://Team", "email": "guest@example.com", "level": model.GrantViewer,
	})
	assert.Equal(t, http.StatusConflict, status, body)
	assert.Contains(t, body, "E2E_ENCRYPTED")

	_, after, err := pf.Store.ListAllShares(ctx, nil, false, 500, 0)
	require.NoError(t, err)
	assert.Equal(t, before, after, "no link into the encrypted folder")
}
