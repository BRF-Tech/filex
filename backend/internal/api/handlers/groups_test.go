package handlers_test

// Groups (handlers/groups_admin.go, internal/group): a folder shared with a
// group reaches its members and only them; a group's role is the role of a
// member with none of their own; the delegated and tenant lines hold.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/group"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// listing is the member's view of one folder.
func listing(t *testing.T, pf *permFix, tok, wire string) (int, string) {
	t.Helper()
	return fxReq(t, "GET", pf.URL+"/api/files/manager?action=index&path="+url.QueryEscape(wire), tok, nil, "")
}

// newGroup creates a group through the API and returns its id.
func newGroup(t *testing.T, pf *permFix, body map[string]any) int64 {
	t.Helper()
	status, resp := fxJSON(t, "POST", pf.URL+"/api/admin/groups", pf.adminTok, body)
	require.Equal(t, http.StatusCreated, status, resp)
	return int64(decode(t, resp)["group"].(map[string]any)["id"].(float64))
}

func TestGroups_FolderGrantReachesMembersOnly(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	status, body := fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": "alpha://", "name": "Finance"})
	require.Equal(t, http.StatusOK, status, body)
	fxUpload(t, pf.URL, pf.adminTok, "alpha://Finance", "q1.txt", "numbers")
	pf.StA.RBACEnabled = true
	require.NoError(t, pf.Store.UpdateStorage(ctx, pf.StA))

	gid := newGroup(t, pf, map[string]any{"name": "Finance team"})
	status, body = fxJSON(t, "POST", pf.URL+"/api/files/permissions", pf.adminTok, map[string]any{
		"path": "alpha://Finance", "group_id": gid, "level": model.GrantEditor,
	})
	require.Equal(t, http.StatusOK, status, body)
	grantID := int64(decode(t, body)["id"].(float64))

	_, body = listing(t, pf, pf.memberTok, "alpha://Finance")
	assert.NotContains(t, body, "q1.txt", "not a member yet")

	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members", pf.adminTok, map[string]any{"user_ids": []int64{pf.UserA}})
	require.Equal(t, http.StatusOK, status, body)
	members := decode(t, body)["members"].([]any)
	require.Len(t, members, 1)
	assert.Equal(t, model.GroupSourceManual, members[0].(map[string]any)["source"])

	status, body = listing(t, pf, pf.memberTok, "alpha://Finance")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "q1.txt", "a member reaches what the group was granted")

	// The panel names the group; "shared with me" lists the folder once.
	status, body = fxJSON(t, "GET", pf.URL+"/api/files/permissions?path="+url.QueryEscape("alpha://Finance"), pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	direct := decode(t, body)["direct"].([]any)
	require.Len(t, direct, 1)
	assert.Equal(t, "group", direct[0].(map[string]any)["kind"])
	assert.Equal(t, "Finance team", direct[0].(map[string]any)["group_name"])

	_, err := pf.Store.CreateFileGrant(ctx, &model.FileGrant{StorageID: pf.StA.ID, PathPrefix: "Finance", IsDir: true, UserID: pf.UserA, Level: model.GrantViewer})
	require.NoError(t, err)
	status, body = fxJSON(t, "GET", pf.URL+"/api/files/manager/shared-with-me", pf.memberTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.EqualValues(t, 1, decode(t, body)["total"], "shared with the person and their group is one folder: %s", body)

	// The group's editor grant wins over the person's own viewer one.
	fxUpload(t, pf.URL, pf.memberTok, "alpha://Finance", "q2.txt", "more")

	// The level can change and the grant can go, by the group grant's own id.
	status, body = fxJSON(t, "PATCH", pf.URL+"/api/files/permissions/groups/"+idStr(grantID), pf.adminTok, map[string]any{"level": model.GrantViewer})
	require.Equal(t, http.StatusOK, status, body)
	g, err := pf.Store.GetGroupFileGrant(ctx, grantID)
	require.NoError(t, err)
	assert.Equal(t, model.GrantViewer, g.Level)

	// Leaving the group ends the reach (the person's own grant aside).
	require.NoError(t, pf.Store.DeleteFileGrant(ctx, mustUserGrant(t, pf, "Finance").ID))
	status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members/"+idStr(pf.UserA), pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	_, body = listing(t, pf, pf.memberTok, "alpha://Finance")
	assert.NotContains(t, body, "q1.txt", "a former member reaches nothing")

	// Deleting the group takes its grants along.
	status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+idStr(gid), pf.adminTok, nil)
	require.Equal(t, http.StatusNoContent, status, body)
	left, err := pf.Store.ListGroupFileGrantsByStorage(ctx, pf.StA.ID)
	require.NoError(t, err)
	assert.Empty(t, left)
}

func mustUserGrant(t *testing.T, pf *permFix, prefix string) *model.FileGrant {
	t.Helper()
	gs, err := pf.Store.ListFileGrantsByStorageUser(context.Background(), pf.StA.ID, pf.UserA)
	require.NoError(t, err)
	for _, g := range gs {
		if g.PathPrefix == prefix {
			return g
		}
	}
	t.Fatalf("no grant on %q", prefix)
	return nil
}

func TestGroups_RoleForMembersWithoutTheirOwn(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	mkRole := func(name string, s perm.Set) float64 {
		status, body := fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
			"name": name, "enabled": true, "permissions": s.Strings(),
		})
		require.Equal(t, http.StatusCreated, status, body)
		return decode(t, body)["id"].(float64)
	}
	noDelete := mkRole("No delete", perm.Standard.Without(perm.FilesDelete))
	readOnly := mkRole("Look only", perm.ReadOnly)

	gid := newGroup(t, pf, map[string]any{"name": "Contractors", "role_id": noDelete})
	status, body := fxJSON(t, "POST", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members", pf.adminTok, map[string]any{"user_ids": []int64{pf.UserA}})
	require.Equal(t, http.StatusOK, status, body)

	status, body = deleteItem(t, pf, pf.memberTok, "report.txt")
	got := requireDenied(t, "delete through the group's role", status, body, perm.FilesDelete, perm.SourceRule)
	src := got["source"].(map[string]any)
	assert.Equal(t, "No delete", src["rule_name"])
	assert.Equal(t, "Contractors", src["group_name"], "the refusal says where the role came from")
	assert.Contains(t, got["message"], "Contractors")

	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	roles := decode(t, body)
	assert.Nil(t, roles["role_id"], "no role of their own")
	assert.Equal(t, noDelete, roles["group_role"].(map[string]any)["role_id"])

	// A read-only group role moves the level underneath to Viewer, like
	// giving the role on the person's page does.
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/groups/"+idStr(gid), pf.adminTok, map[string]any{"name": "Contractors", "role_id": readOnly})
	require.Equal(t, http.StatusOK, status, body)
	u, err := pf.Store.GetUser(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Equal(t, model.RoleViewer, u.Role)

	// Their own role wins over the group's.
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok, map[string]any{"role_id": noDelete})
	require.Equal(t, http.StatusOK, status, body)
	status, body = fxJSON(t, "GET", pf.URL+"/api/auth/me/permissions", pf.memberTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, decode(t, body)["allowed"], "files.create", "their own No delete, not the group's Look only")

	// A role groups hold is not deleted without saying what they get.
	status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/roles/"+idStr(int64(readOnly)), pf.adminTok, nil)
	require.Equal(t, http.StatusConflict, status, body)
	assert.EqualValues(t, 1, decode(t, body)["groups"])
	status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/roles/"+idStr(int64(readOnly))+"?to="+idStr(int64(noDelete)), pf.adminTok, nil)
	require.Equal(t, http.StatusNoContent, status, body)
	g, err := pf.Store.GetGroup(ctx, gid)
	require.NoError(t, err)
	require.NotNil(t, g.RoleID)
	assert.EqualValues(t, noDelete, *g.RoleID, "the group moved to the role named")
}

func TestGroups_DelegatedAdminLines(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	other := seedUserIn(t, pf.Store, mustSupertenant(t, pf), "colleague@alpha.test")
	pf.override(t, map[string]string{"admin.users": model.PermAllow})
	session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)

	status, body := sessionJSON(t, session, "POST", pf.URL+"/api/admin/groups", map[string]any{"name": "Team"})
	require.Equal(t, http.StatusCreated, status, body)
	gid := idStr(int64(decode(t, body)["group"].(map[string]any)["id"].(float64)))

	status, body = sessionJSON(t, session, "POST", pf.URL+"/api/admin/groups/"+gid+"/members", map[string]any{"user_ids": []int64{other}})
	require.Equal(t, http.StatusOK, status, "managing other people's groups is the job: %s", body)
	status, body = sessionJSON(t, session, "POST", pf.URL+"/api/admin/groups/"+gid+"/members", map[string]any{"user_ids": []int64{pf.UserA}})
	require.Equal(t, http.StatusForbidden, status, "putting oneself in a group: %s", body)

	status, body = sessionJSON(t, session, "PUT", pf.URL+"/api/admin/groups/"+gid, map[string]any{
		"name": "Team", "links": []map[string]string{{"kind": "sso", "value": "everyone"}},
	})
	require.Equal(t, http.StatusForbidden, status, "links decide membership at sign-in — theirs too: %s", body)

	// A role allowing what the caller does not hold cannot reach the group.
	rule, err := pf.Store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Auditor", Enabled: true, Permissions: []string{"files.download", "admin.audit"},
		Targets: []model.PermRuleTarget{}, Effects: map[string]string{},
	})
	require.NoError(t, err)
	status, body = sessionJSON(t, session, "PUT", pf.URL+"/api/admin/groups/"+gid, map[string]any{"name": "Team", "role_id": rule.ID})
	require.Equal(t, http.StatusForbidden, status, "a role beyond the caller: %s", body)
	assert.Contains(t, body, "admin.audit")

	// The full administrator can do all three — signed in: the role carries
	// an admin-area permission, so it makes every member a delegated
	// administrator, which an API key may not hand out (#120).
	teamBody := map[string]any{"name": "Team", "role_id": rule.ID, "links": []map[string]string{{"kind": "sso", "value": "team"}}}
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/groups/"+gid, pf.adminTok, teamBody)
	require.Equal(t, http.StatusForbidden, status, "an API key: %s", body)
	assert.Contains(t, body, "session_required")
	status, body = sessionJSON(t, pf.AdminA, "PUT", pf.URL+"/api/admin/groups/"+gid, teamBody)
	require.Equal(t, http.StatusOK, status, body)

	// ...and a delegated administrator cannot then add people to it, which
	// would give them that role.
	third := seedUserIn(t, pf.Store, mustSupertenant(t, pf), "third@alpha.test")
	status, body = sessionJSON(t, session, "POST", pf.URL+"/api/admin/groups/"+gid+"/members", map[string]any{"user_ids": []int64{third}})
	require.Equal(t, http.StatusForbidden, status, "joining gives the role: %s", body)

	// A name is unique within its tenant.
	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/groups", pf.adminTok, map[string]any{"name": "Team"})
	require.Equal(t, http.StatusConflict, status, body)
}

func mustSupertenant(t *testing.T, pf *permFix) int64 {
	t.Helper()
	super, err := pf.Store.GetSupertenant(context.Background())
	require.NoError(t, err)
	return super.ID
}

func TestGroups_TenantIsolation(t *testing.T) {
	f := newMTFix(t, true)
	t.Cleanup(perm.Invalidate)

	// A tenant administrator's group is its tenant's, whatever the body says.
	status, body := sessionJSON(t, f.AdminA, "POST", f.URL+"/api/admin/groups", map[string]any{"name": "Alpha team", "provider_id": f.ProvB})
	require.Equal(t, http.StatusCreated, status, body)
	g := decode(t, body)["group"].(map[string]any)
	assert.EqualValues(t, f.ProvA, g["provider_id"])
	alphaGroup := idStr(int64(g["id"].(float64)))

	// Someone of another tenant cannot be put in it — and reads as unknown.
	status, body = sessionJSON(t, f.AdminA, "POST", f.URL+"/api/admin/groups/"+alphaGroup+"/members", map[string]any{"user_ids": []int64{f.UserB}})
	require.Equal(t, http.StatusNotFound, status, body)

	// The supertenant's group for bravo is invisible to alpha's administrator.
	status, body = sessionJSON(t, f.Super, "POST", f.URL+"/api/admin/groups", map[string]any{"name": "Bravo team", "provider_id": f.ProvB})
	require.Equal(t, http.StatusCreated, status, body)
	bravoGroup := int64(decode(t, body)["group"].(map[string]any)["id"].(float64))
	status, body = sessionJSON(t, f.AdminA, "GET", f.URL+"/api/admin/groups", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.NotContains(t, body, "Bravo team")
	status, _ = sessionJSON(t, f.AdminA, "GET", f.URL+"/api/admin/groups/"+idStr(bravoGroup), nil)
	assert.Equal(t, http.StatusNotFound, status)

	// ...and cannot be given a folder of alpha's, nor offered in its picker.
	f.StA.RBACEnabled = true
	require.NoError(t, f.Store.UpdateStorage(context.Background(), f.StA))
	status, body = sessionJSON(t, f.AdminA, "POST", f.URL+"/api/files/permissions", map[string]any{
		"path": "alpha://", "group_id": bravoGroup, "level": model.GrantViewer,
	})
	require.Equal(t, http.StatusNotFound, status, body)
	status, body = sessionJSON(t, f.A, "GET", f.URL+"/api/files/permissions/groups?q=team", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Alpha team")
	assert.NotContains(t, body, "Bravo team")
}

// The relations between people, groups and roles that the pages count and
// show — and that move when a group changes.
func TestGroups_RoleRelations(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	mkRole := func(name string, s perm.Set) float64 {
		status, body := fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{"name": name, "enabled": true, "permissions": s.Strings()})
		require.Equal(t, http.StatusCreated, status, body)
		return decode(t, body)["id"].(float64)
	}
	lookOnly := mkRole("Look only", perm.ReadOnly)
	noDelete := mkRole("No delete", perm.Standard.Without(perm.FilesDelete))
	join := func(gid int64, uid int64) {
		status, body := fxJSON(t, "POST", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members", pf.adminTok, map[string]any{"user_ids": []int64{uid}})
		require.Equal(t, http.StatusOK, status, body)
	}
	leave := func(gid int64, uid int64) {
		status, body := fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members/"+idStr(uid), pf.adminTok, nil)
		require.Equal(t, http.StatusOK, status, body)
	}
	levelOf := func(uid int64) string {
		u, err := pf.Store.GetUser(ctx, uid)
		require.NoError(t, err)
		return u.Role
	}

	// ── the level comes back (#5) ──
	auditors := newGroup(t, pf, map[string]any{"name": "Auditors", "role_id": lookOnly})
	join(auditors, pf.UserA)
	assert.Equal(t, model.RoleViewer, levelOf(pf.UserA), "a read-only group role makes a Viewer")
	leave(auditors, pf.UserA)
	assert.Equal(t, model.RoleUser, levelOf(pf.UserA), "leaving gives back the User level they had")

	viewerID := seedUserIn(t, pf.Store, mustSupertenant(t, pf), "viewer@alpha.test")
	require.NoError(t, pf.Store.UpdateUserRole(ctx, viewerID, model.RoleViewer))
	writers := newGroup(t, pf, map[string]any{"name": "Writers", "role_id": noDelete})
	join(writers, viewerID)
	assert.Equal(t, model.RoleUser, levelOf(viewerID))
	status, body := fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+idStr(writers), pf.adminTok, nil)
	require.Equal(t, http.StatusNoContent, status, body)
	assert.Equal(t, model.RoleViewer, levelOf(viewerID), "never the full User role the account never had")

	// ── priority decides between two group roles (#4) ──
	join(auditors, pf.UserA)
	finance := newGroup(t, pf, map[string]any{"name": "Finance", "role_id": noDelete})
	join(finance, pf.UserA)
	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "Auditors", decode(t, body)["group_role"].(map[string]any)["group_name"], "equal priority: the older group")
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/groups/"+idStr(finance), pf.adminTok, map[string]any{"name": "Finance", "role_id": noDelete, "priority": 5})
	require.Equal(t, http.StatusOK, status, body)
	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "Finance", decode(t, body)["group_role"].(map[string]any)["group_name"], "the higher priority now")
	assert.Equal(t, model.RoleUser, levelOf(pf.UserA), "and its level")

	// ── the pages count it (#1) ──
	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/roles", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	roles := decode(t, body)
	ga := roles["group_assignments"].(map[string]any)[idStr(pf.UserA)].(map[string]any)
	assert.EqualValues(t, noDelete, ga["role_id"])
	assert.Equal(t, "Finance", ga["group_name"])
	builtin := roles["builtin_members"].(map[string]any)
	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/users", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	users := 0
	for _, u := range decodeList(t, body) {
		if u["role"] == model.RoleUser && int64(u["id"].(float64)) != pf.UserA {
			users++
		}
	}
	assert.EqualValues(t, users, builtin["user"], "someone with a group's role is not counted on the built-in User role")

	// ── a built-in role does not override a group's, and the answer says so (#2) ──
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok, map[string]any{"role": "viewer"})
	require.Equal(t, http.StatusOK, status, body)
	put := decode(t, body)
	assert.Equal(t, model.RoleUser, put["role"], "the group's No delete keeps the User level")
	assert.Equal(t, "Finance", put["group_role"].(map[string]any)["group_name"])
}

// A changed SSO link takes effect at once, from the groups each account's
// last sign-in carried — not at their next sign-in (#7).
func TestGroups_SSOLinkAppliesAtOnce(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	require.NoError(t, pf.Store.SetUserSSOGroups(ctx, pf.UserA, []string{"marketing", "staff"}))
	gid := newGroup(t, pf, map[string]any{"name": "Marketing"})
	g, err := pf.Store.ListGroupMembers(ctx, gid)
	require.NoError(t, err)
	require.Empty(t, g)

	status, body := fxJSON(t, "PUT", pf.URL+"/api/admin/groups/"+idStr(gid), pf.adminTok, map[string]any{
		"name": "Marketing", "links": []map[string]string{{"kind": "sso", "value": "marketing"}},
	})
	require.Equal(t, http.StatusOK, status, body)
	members := decode(t, body)["members"].([]any)
	require.Len(t, members, 1, "their last sign-in carried marketing")
	assert.Equal(t, model.GroupSourceSSO, members[0].(map[string]any)["source"])

	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/groups/"+idStr(gid), pf.adminTok, map[string]any{"name": "Marketing", "links": []map[string]string{}})
	require.Equal(t, http.StatusOK, status, body)
	assert.Empty(t, decode(t, body)["members"], "the link is gone, and so is the membership it made")

	// A group created already linked fills at once too.
	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/groups", pf.adminTok, map[string]any{
		"name": "Staff", "links": []map[string]string{{"kind": "sso", "value": "staff"}},
	})
	require.Equal(t, http.StatusCreated, status, body)
	staff := int64(decode(t, body)["group"].(map[string]any)["id"].(float64))
	ms, err := pf.Store.ListGroupMembers(ctx, staff)
	require.NoError(t, err)
	assert.Len(t, ms, 1)
}

func decodeList(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal([]byte(body), &out); err == nil {
		return out
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &page), body)
	return page.Items
}

// The review's findings, each pinned (code review of the groups branch).
func TestGroups_ReviewFindings(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	lookOnly := func() float64 {
		status, body := fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{"name": "Look only", "enabled": true, "permissions": perm.ReadOnly.Strings()})
		require.Equal(t, http.StatusCreated, status, body)
		return decode(t, body)["id"].(float64)
	}()
	levelOf := func(uid int64) string {
		u, err := pf.Store.GetUser(ctx, uid)
		require.NoError(t, err)
		return u.Role
	}
	join := func(gid, uid int64) {
		status, body := fxJSON(t, "POST", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members", pf.adminTok, map[string]any{"user_ids": []int64{uid}})
		require.Equal(t, http.StatusOK, status, body)
	}

	t.Run("deleting a role with ?to=viewer is not undone by the kept level", func(t *testing.T) {
		gid := newGroup(t, pf, map[string]any{"name": "Readers", "role_id": lookOnly})
		join(gid, pf.UserA)
		require.Equal(t, model.RoleViewer, levelOf(pf.UserA))
		status, body := fxJSON(t, "DELETE", pf.URL+"/api/admin/roles/"+idStr(int64(lookOnly))+"?to=viewer", pf.adminTok, nil)
		require.Equal(t, http.StatusNoContent, status, body)
		assert.Equal(t, model.RoleViewer, levelOf(pf.UserA), "the administrator picked Viewer")
		// Reset for the next case.
		status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+idStr(gid), pf.adminTok, nil)
		require.Equal(t, http.StatusNoContent, status, body)
		require.NoError(t, pf.Store.UpdateUserRole(ctx, pf.UserA, model.RoleUser))
	})

	t.Run("a built-in level picked in the Role field is the one leaving gives back", func(t *testing.T) {
		readOnly := func() float64 {
			status, body := fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{"name": "Look only 2", "enabled": true, "permissions": perm.ReadOnly.Strings()})
			require.Equal(t, http.StatusCreated, status, body)
			return decode(t, body)["id"].(float64)
		}()
		// A Viewer account; the group's read-only role changes nothing yet.
		require.NoError(t, pf.Store.UpdateUserRole(ctx, pf.UserA, model.RoleViewer))
		gid := newGroup(t, pf, map[string]any{"name": "Readers 2", "role_id": readOnly})
		join(gid, pf.UserA)
		require.Equal(t, model.RoleViewer, levelOf(pf.UserA))
		// The administrator picks User: the group's role still caps them at
		// Viewer, and User is what leaving gives back. (The same role sent
		// again is no choice — TestGroups_UnchangedRoleCallKeepsTheLevel.)
		status, body := fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok, map[string]any{"role": "user"})
		require.Equal(t, http.StatusOK, status, body)
		assert.Equal(t, model.RoleViewer, levelOf(pf.UserA), "the group's read-only role still sets the level")
		status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members/"+idStr(pf.UserA), pf.adminTok, nil)
		require.Equal(t, http.StatusOK, status, body)
		assert.Equal(t, model.RoleUser, levelOf(pf.UserA), "the administrator's User, not the Viewer from before the group")
	})

	t.Run("a delegated administrator cannot change a group they are in", func(t *testing.T) {
		gid := newGroup(t, pf, map[string]any{"name": "Helpdesk"})
		join(gid, pf.UserA)
		pf.override(t, map[string]string{"admin.users": model.PermAllow})
		session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
		status, body := sessionJSON(t, session, "PUT", pf.URL+"/api/admin/groups/"+idStr(gid), map[string]any{"name": "Helpdesk", "priority": 5})
		require.Equal(t, http.StatusForbidden, status, "their own group's priority: %s", body)
		status, body = sessionJSON(t, session, "DELETE", pf.URL+"/api/admin/groups/"+idStr(gid), nil)
		require.Equal(t, http.StatusForbidden, status, "deleting their own group: %s", body)
		status, body = sessionJSON(t, session, "PUT", pf.URL+"/api/admin/groups/"+idStr(gid), map[string]any{"name": "Help desk"})
		require.Equal(t, http.StatusOK, status, "a rename changes nobody's access: %s", body)
		pf.override(t, nil)
	})

	t.Run("shared with me shows the highest level, capped by the account", func(t *testing.T) {
		status, body := fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": "alpha://", "name": "Both"})
		require.Equal(t, http.StatusOK, status, body)
		pf.StA.RBACEnabled = true
		require.NoError(t, pf.Store.UpdateStorage(ctx, pf.StA))
		gid := newGroup(t, pf, map[string]any{"name": "Editors"})
		join(gid, pf.UserA)
		_, err := pf.Store.CreateFileGrant(ctx, &model.FileGrant{StorageID: pf.StA.ID, PathPrefix: "Both", IsDir: true, UserID: pf.UserA, Level: model.GrantViewer})
		require.NoError(t, err)
		_, err = pf.Store.CreateGroupFileGrant(ctx, &model.FileGrant{StorageID: pf.StA.ID, PathPrefix: "Both", IsDir: true, GroupID: gid, Level: model.GrantEditor})
		require.NoError(t, err)
		permOf := func() string {
			status, body := fxJSON(t, "GET", pf.URL+"/api/files/manager/shared-with-me", pf.memberTok, nil)
			require.Equal(t, http.StatusOK, status, body)
			files := decode(t, body)["files"].([]any)
			require.Len(t, files, 1, body)
			return files[0].(map[string]any)["perm"].(string)
		}
		assert.Equal(t, "editor", permOf(), "their own viewer grant must not hide the group's editor one")
		require.NoError(t, pf.Store.UpdateUserRole(ctx, pf.UserA, model.RoleViewer))
		perm.Invalidate()
		assert.Equal(t, "viewer", permOf(), "a viewer account reaches it as a viewer")
	})
}

// The second review's findings, each pinned.
func TestGroups_SecondReviewFindings(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	super := mustSupertenant(t, pf)
	readOnly := func(name string, provider *int64) *model.PermissionRule {
		r, err := pf.Store.CreatePermissionRule(ctx, &model.PermissionRule{
			Name: name, Enabled: true, Permissions: perm.ReadOnly.Strings(), ProviderID: provider,
			Targets: []model.PermRuleTarget{}, Effects: map[string]string{},
		})
		require.NoError(t, err)
		perm.Invalidate()
		return r
	}
	levelOf := func(uid int64) string {
		u, err := pf.Store.GetUser(ctx, uid)
		require.NoError(t, err)
		return u.Role
	}
	join := func(gid, uid int64) {
		status, body := fxJSON(t, "POST", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members", pf.adminTok, map[string]any{"user_ids": []int64{uid}})
		require.Equal(t, http.StatusOK, status, body)
	}

	t.Run("a delegated administrator cannot reorder roles by priority", func(t *testing.T) {
		other := seedUserIn(t, pf.Store, super, "prio@alpha.test")
		gid := newGroup(t, pf, map[string]any{"name": "Prio"})
		join(gid, other)
		pf.override(t, map[string]string{"admin.users": model.PermAllow})
		session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
		status, body := sessionJSON(t, session, "PUT", pf.URL+"/api/admin/groups/"+idStr(gid), map[string]any{"name": "Prio", "priority": 9})
		require.Equal(t, http.StatusForbidden, status, "not in the group, still not theirs to order: %s", body)
		assert.Contains(t, body, "priority")
		pf.override(t, nil)
		status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/groups/"+idStr(gid), pf.adminTok, map[string]any{"name": "Prio", "priority": 9})
		require.Equal(t, http.StatusOK, status, "an administrator may: %s", body)
	})

	t.Run("moving a person to another tenant gives back the level their old group's role set", func(t *testing.T) {
		uid := seedUserIn(t, pf.Store, super, "mover@alpha.test")
		role := readOnly("Look only (tenant)", &super)
		g, err := pf.Store.CreateGroup(ctx, &model.Group{Name: "Tenant readers", ProviderID: &super, RoleID: &role.ID})
		require.NoError(t, err)
		join(g.ID, uid)
		require.Equal(t, model.RoleViewer, levelOf(uid))
		status, body := fxJSON(t, "PATCH", pf.URL+"/api/admin/users/"+idStr(uid), pf.adminTok, map[string]any{"provider_id": pf.ProvB})
		require.Equal(t, http.StatusOK, status, body)
		ms, err := pf.Store.ListUserGroupMemberships(ctx, uid)
		require.NoError(t, err)
		assert.Empty(t, ms, "out of the old tenant's group")
		assert.Equal(t, model.RoleUser, levelOf(uid), "and back on the level from before its role")
	})

	t.Run("a role changed through the user edit route is synced, and is the level to give back", func(t *testing.T) {
		// Viewer at first; a read-only group role changes nothing yet.
		uid := seedUserIn(t, pf.Store, super, "patched@alpha.test")
		require.NoError(t, pf.Store.UpdateUserRole(ctx, uid, model.RoleViewer))
		role := readOnly("Look only (patch)", nil)
		gid := newGroup(t, pf, map[string]any{"name": "Patch readers", "role_id": role.ID})
		join(gid, uid)
		require.Equal(t, model.RoleViewer, levelOf(uid))
		// The administrator picks User: while the group's read-only role
		// applies the level stays Viewer — and User is what leaving gives.
		// (An unchanged role sent along with other fields is no choice; see
		// TestGroups_FourthReviewFindings.)
		status, body := fxJSON(t, "PATCH", pf.URL+"/api/admin/users/"+idStr(uid), pf.adminTok, map[string]any{"role": "user"})
		require.Equal(t, http.StatusOK, status, body)
		assert.Equal(t, model.RoleViewer, levelOf(uid), "the group's read-only role still sets the level")
		status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members/"+idStr(uid), pf.adminTok, nil)
		require.Equal(t, http.StatusOK, status, body)
		assert.Equal(t, model.RoleUser, levelOf(uid), "the administrator's User, not the Viewer from before")
	})

	t.Run("a deleted role's groups are not moved onto another tenant's role", func(t *testing.T) {
		wide := readOnly("Install-wide", nil)
		foreign := readOnly("Bravo only", &pf.ProvB)
		gid := newGroup(t, pf, map[string]any{"name": "Wide holders", "role_id": wide.ID})
		status, body := fxJSON(t, "DELETE", pf.URL+"/api/admin/roles/"+idStr(wide.ID)+"?to="+idStr(foreign.ID), pf.adminTok, nil)
		require.Equal(t, http.StatusBadRequest, status, body)
		assert.Contains(t, body, "another tenant")
		g, err := pf.Store.GetGroup(ctx, gid)
		require.NoError(t, err)
		require.NotNil(t, g.RoleID)
		assert.Equal(t, wide.ID, *g.RoleID, "nothing moved")
		_, err = pf.Store.GetPermissionRule(ctx, wide.ID)
		assert.NoError(t, err, "and nothing was deleted")
	})
}

// Third review: a person moved off a deleted role onto a built-in one gets
// the role — and level — one of their groups gives them.
func TestGroups_DeletedRoleHoldersFollowTheirGroups(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	mk := func(name string, s perm.Set) int64 {
		status, body := fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{"name": name, "enabled": true, "permissions": s.Strings()})
		require.Equal(t, http.StatusCreated, status, body)
		return int64(decode(t, body)["id"].(float64))
	}
	own := mk("Own role", perm.Standard.Without(perm.FilesDelete))
	lookOnly := mk("Look only", perm.ReadOnly)
	status, body := fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok, map[string]any{"role_id": own})
	require.Equal(t, http.StatusOK, status, body)
	gid := newGroup(t, pf, map[string]any{"name": "Readers", "role_id": lookOnly})
	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members", pf.adminTok, map[string]any{"user_ids": []int64{pf.UserA}})
	require.Equal(t, http.StatusOK, status, body)

	status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/roles/"+idStr(own)+"?to=user", pf.adminTok, nil)
	require.Equal(t, http.StatusNoContent, status, body)
	u, err := pf.Store.GetUser(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Equal(t, model.RoleViewer, u.Role, "no role of their own now: the group's read-only role, and its level")
}

// Fourth review, on the port to 0.49.0.
func TestGroups_FourthReviewFindings(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	super := mustSupertenant(t, pf)

	t.Run("lifting a Deny is judged with the role the person's group gives", func(t *testing.T) {
		// The target's group role allows admin.audit; their own Deny takes it
		// away. A delegated administrator without admin.audit must not lift
		// that Deny: the result is admin.audit through the group.
		target := seedUserIn(t, pf.Store, super, "auditor@alpha.test")
		rule, err := pf.Store.CreatePermissionRule(ctx, &model.PermissionRule{
			Name: "Auditor", Enabled: true, Permissions: perm.ReadOnly.With(perm.AdminAudit).Strings(),
			Targets: []model.PermRuleTarget{}, Effects: map[string]string{},
		})
		require.NoError(t, err)
		g, err := pf.Store.CreateGroup(ctx, &model.Group{Name: "Audit desk", RoleID: &rule.ID})
		require.NoError(t, err)
		require.NoError(t, pf.Store.AddGroupMember(ctx, g.ID, target))
		require.NoError(t, pf.Store.SetUserPermissionOverrides(ctx, target, map[string]string{"admin.audit": model.PermDeny}, nil))
		perm.Invalidate()

		pf.override(t, map[string]string{"admin.users": model.PermAllow})
		session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
		status, body := sessionJSON(t, session, "PUT", pf.URL+"/api/admin/users/"+idStr(target)+"/exceptions", map[string]any{"overrides": map[string]string{}})
		require.Equal(t, http.StatusForbidden, status, "the Deny is all that keeps admin.audit from them: %s", body)
		assert.Contains(t, body, "admin.audit")
		pf.override(t, nil)
	})

	t.Run("sending the unchanged role keeps the level to give back", func(t *testing.T) {
		uid := seedUserIn(t, pf.Store, super, "unchanged@alpha.test")
		rule, err := pf.Store.CreatePermissionRule(ctx, &model.PermissionRule{
			Name: "Look only (unchanged)", Enabled: true, Permissions: perm.ReadOnly.Strings(),
			Targets: []model.PermRuleTarget{}, Effects: map[string]string{},
		})
		require.NoError(t, err)
		gid := newGroup(t, pf, map[string]any{"name": "Readers (unchanged)", "role_id": rule.ID})
		status, body := fxJSON(t, "POST", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members", pf.adminTok, map[string]any{"user_ids": []int64{uid}})
		require.Equal(t, http.StatusOK, status, body)
		status, body = fxJSON(t, "PATCH", pf.URL+"/api/admin/users/"+idStr(uid), pf.adminTok, map[string]any{"role": "viewer", "display_name": "Same level"})
		require.Equal(t, http.StatusOK, status, body)
		status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members/"+idStr(uid), pf.adminTok, nil)
		require.Equal(t, http.StatusOK, status, body)
		u, err := pf.Store.GetUser(ctx, uid)
		require.NoError(t, err)
		assert.Equal(t, model.RoleUser, u.Role, "the role was not changed, so the User level from before the group comes back")
	})
}

// Fifth review.
func TestGroups_FifthReviewFindings(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	super := mustSupertenant(t, pf)
	role := func(name string, s perm.Set) *model.PermissionRule {
		r, err := pf.Store.CreatePermissionRule(ctx, &model.PermissionRule{
			Name: name, Enabled: true, Permissions: s.Strings(), Targets: []model.PermRuleTarget{}, Effects: map[string]string{},
		})
		require.NoError(t, err)
		perm.Invalidate()
		return r
	}
	levelOf := func(uid int64) string {
		u, err := pf.Store.GetUser(ctx, uid)
		require.NoError(t, err)
		return u.Role
	}

	t.Run("a delegated administrator cannot widen someone by taking a restrictive group role away", func(t *testing.T) {
		lookOnly := role("Look only (5)", perm.ReadOnly)
		target := seedUserIn(t, pf.Store, super, "restricted@alpha.test")
		g, err := pf.Store.CreateGroup(ctx, &model.Group{Name: "Restricted", RoleID: &lookOnly.ID})
		require.NoError(t, err)
		require.NoError(t, pf.Store.AddGroupMember(ctx, g.ID, target))
		perm.Invalidate()
		// The caller manages users but may not make public links; the
		// built-in User role may.
		pf.override(t, map[string]string{"admin.users": model.PermAllow, "share.links": model.PermDeny})
		session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
		gid := idStr(g.ID)

		status, body := sessionJSON(t, session, "DELETE", pf.URL+"/api/admin/groups/"+gid+"/members/"+idStr(target), nil)
		require.Equal(t, http.StatusForbidden, status, "leaving would hand them share.links: %s", body)
		assert.Contains(t, body, "share.links")
		status, body = sessionJSON(t, session, "DELETE", pf.URL+"/api/admin/groups/"+gid, nil)
		require.Equal(t, http.StatusForbidden, status, "deleting the group, the same: %s", body)
		status, body = sessionJSON(t, session, "PUT", pf.URL+"/api/admin/groups/"+gid, map[string]any{"name": "Restricted", "role_id": nil})
		require.Equal(t, http.StatusForbidden, status, "clearing its role, the same: %s", body)
		ms, err := pf.Store.ListGroupMembers(ctx, g.ID)
		require.NoError(t, err)
		assert.Len(t, ms, 1, "nothing changed")
		pf.override(t, nil)

		status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+gid+"/members/"+idStr(target), pf.adminTok, nil)
		require.Equal(t, http.StatusOK, status, "an administrator may: %s", body)
	})

	t.Run("deleting a role resets only the people it was in force for", func(t *testing.T) {
		writer := role("Writer (5)", perm.Standard)
		readOnly := role("Read only (5)", perm.ReadOnly)
		carol := seedUserIn(t, pf.Store, super, "carol5@alpha.test")
		require.NoError(t, pf.Store.UpdateUserRole(ctx, carol, model.RoleViewer))
		a := newGroup(t, pf, map[string]any{"name": "A (5)", "role_id": writer.ID, "priority": 10})
		b := newGroup(t, pf, map[string]any{"name": "B (5)", "role_id": readOnly.ID})
		for _, gid := range []int64{a, b} {
			status, body := fxJSON(t, "POST", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members", pf.adminTok, map[string]any{"user_ids": []int64{carol}})
			require.Equal(t, http.StatusOK, status, body)
		}
		require.Equal(t, model.RoleUser, levelOf(carol), "A's role, the higher priority")

		status, body := fxJSON(t, "DELETE", pf.URL+"/api/admin/roles/"+idStr(readOnly.ID)+"?to=user", pf.adminTok, nil)
		require.Equal(t, http.StatusNoContent, status, body)
		status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+idStr(a)+"/members/"+idStr(carol), pf.adminTok, nil)
		require.Equal(t, http.StatusOK, status, body)
		assert.Equal(t, model.RoleViewer, levelOf(carol), "the deleted role was never hers: her Viewer from before the groups comes back")
	})

	t.Run("an install-wide group's SSO links match only the supertenant's sign-ins", func(t *testing.T) {
		ops, err := pf.Store.CreateGroup(ctx, &model.Group{Name: "Ops (5)", Links: []model.GroupLink{{Kind: model.GroupLinkSSO, Value: "ops"}}})
		require.NoError(t, err)
		bravo := seedUserIn(t, pf.Store, pf.ProvB, "ops@bravo.test")
		platform := seedUserIn(t, pf.Store, super, "ops@platform.test")
		for _, uid := range []int64{bravo, platform} {
			u, err := pf.Store.GetUser(ctx, uid)
			require.NoError(t, err)
			_, err = group.SyncLinked(ctx, pf.Store, u, model.GroupLinkSSO, []string{"ops"})
			require.NoError(t, err)
		}
		ms, err := pf.Store.ListGroupMembers(ctx, ops.ID)
		require.NoError(t, err)
		require.Len(t, ms, 1, "another tenant's `ops` is not the platform's")
		assert.Equal(t, platform, ms[0].UserID)
	})
}

// Sixth review: the role endpoint sent the same role again forgets nothing.
func TestGroups_UnchangedRoleCallKeepsTheLevel(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	uid := seedUserIn(t, pf.Store, mustSupertenant(t, pf), "noop@alpha.test")
	rule, err := pf.Store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Look only (noop)", Enabled: true, Permissions: perm.ReadOnly.Strings(),
		Targets: []model.PermRuleTarget{}, Effects: map[string]string{},
	})
	require.NoError(t, err)
	gid := newGroup(t, pf, map[string]any{"name": "Readers (noop)", "role_id": rule.ID})
	status, body := fxJSON(t, "POST", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members", pf.adminTok, map[string]any{"user_ids": []int64{uid}})
	require.Equal(t, http.StatusOK, status, body)

	for _, req := range []map[string]any{{"role_id": nil}, {"role": "viewer"}} {
		status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(uid)+"/roles", pf.adminTok, req)
		require.Equal(t, http.StatusOK, status, body)
	}
	status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+idStr(gid)+"/members/"+idStr(uid), pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	u, err := pf.Store.GetUser(ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, model.RoleUser, u.Role, "nothing changed on those calls, so the User level from before the group comes back")
}

// An install-wide group (no tenant) is the supertenant's: a tenant's
// administrator cannot see, change, delete or fill it, nor share a folder
// with it — each answers as a group that does not exist (404), so the
// refusal is no census of the platform's groups. (PR #77 made the built-in
// roles, the other install-wide rows a tenant admin could reach, the
// supertenant's the same way.)
func TestGroups_InstallWideIsTheSupertenants(t *testing.T) {
	f := newMTFix(t, true)
	t.Cleanup(perm.Invalidate)
	ctx := context.Background()

	status, body := sessionJSON(t, f.Super, "POST", f.URL+"/api/admin/groups", map[string]any{"name": "Platform ops"})
	require.Equal(t, http.StatusCreated, status, body)
	g := decode(t, body)["group"].(map[string]any)
	require.Nil(t, g["provider_id"], "the supertenant made it install-wide")
	gid := idStr(int64(g["id"].(float64)))

	status, body = sessionJSON(t, f.AdminA, "GET", f.URL+"/api/admin/groups", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.NotContains(t, body, "Platform ops", "not in a tenant admin's list")

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/admin/groups/" + gid, nil},
		{"PUT", "/api/admin/groups/" + gid, map[string]any{"name": "Taken over"}},
		{"DELETE", "/api/admin/groups/" + gid, nil},
		{"POST", "/api/admin/groups/" + gid + "/members", map[string]any{"user_ids": []int64{f.UserA}}},
		{"DELETE", "/api/admin/groups/" + gid + "/members/" + idStr(f.UserA), nil},
	} {
		status, body = sessionJSON(t, f.AdminA, c.method, f.URL+c.path, c.body)
		assert.Equal(t, http.StatusNotFound, status, "%s %s: %s", c.method, c.path, body)
	}

	// Not offered in the sharing panel, and not grantable on the tenant's own
	// storage.
	f.StA.RBACEnabled = true
	require.NoError(t, f.Store.UpdateStorage(ctx, f.StA))
	status, body = sessionJSON(t, f.A, "GET", f.URL+"/api/files/permissions/groups?q=ops", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.NotContains(t, body, "Platform ops")
	status, body = sessionJSON(t, f.AdminA, "POST", f.URL+"/api/files/permissions", map[string]any{
		"path": "alpha://", "group_id": g["id"], "level": model.GrantViewer,
	})
	assert.Equal(t, http.StatusNotFound, status, body)

	// Nothing changed.
	stored, err := f.Store.GetGroup(ctx, int64(g["id"].(float64)))
	require.NoError(t, err)
	assert.Equal(t, "Platform ops", stored.Name)
	ms, err := f.Store.ListGroupMembers(ctx, stored.ID)
	require.NoError(t, err)
	assert.Empty(t, ms)
}

// Seventh review: the result check previews the level a group's role will
// set, not the level the request left — a Viewer's ceiling would otherwise
// hide the writes the group's role gives once the level is synced.
func TestGroups_PreviewUsesTheGroupRolesLevel(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	super := mustSupertenant(t, pf)
	mk := func(name string, s perm.Set) *model.PermissionRule {
		r, err := pf.Store.CreatePermissionRule(ctx, &model.PermissionRule{
			Name: name, Enabled: true, Permissions: s.Strings(), Targets: []model.PermRuleTarget{}, Effects: map[string]string{},
		})
		require.NoError(t, err)
		return r
	}
	readOnly := mk("Read only (7)", perm.ReadOnly)
	noDelete := mk("Writer, no delete (7)", perm.Standard.Without(perm.FilesDelete).Without(perm.FilesPurge))
	editors := mk("Editors (7)", perm.Standard)
	g, err := pf.Store.CreateGroup(ctx, &model.Group{Name: "Editors (7)", RoleID: &editors.ID})
	require.NoError(t, err)

	setup := func(email string, own *model.PermissionRule, level string) int64 {
		uid := seedUserIn(t, pf.Store, super, email)
		require.NoError(t, pf.Store.AddGroupMember(ctx, g.ID, uid))
		// Their own role wins over the group's, and sets their level.
		status, body := fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(uid)+"/roles", pf.adminTok, map[string]any{"role_id": own.ID})
		require.Equal(t, http.StatusOK, status, body)
		u, err := pf.Store.GetUser(ctx, uid)
		require.NoError(t, err)
		require.Equal(t, level, u.Role)
		return uid
	}
	// a: their own read-only role (Viewer); ending it hands them Editors.
	a := setup("ro-a@alpha.test", readOnly, model.RoleViewer)
	// b: their own writing role without delete (User). Picking Viewer ends
	// it — judged at Viewer, Editors' delete hides under the ceiling; synced,
	// the group's role puts them back on User, with delete.
	b := setup("ro-b@alpha.test", noDelete, model.RoleUser)

	// The caller manages users but may not delete.
	pf.override(t, map[string]string{"admin.users": model.PermAllow, "files.delete": model.PermDeny})
	session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)

	status, body := sessionJSON(t, session, "PUT", pf.URL+"/api/admin/users/"+idStr(a)+"/roles", map[string]any{"role_id": nil})
	assert.Equal(t, http.StatusForbidden, status, "ending their own role hands them Editors at the User level: %s", body)
	assert.Contains(t, body, "files.delete")

	status, body = sessionJSON(t, session, "PATCH", pf.URL+"/api/admin/users/"+idStr(b), map[string]any{"role": "viewer"})
	assert.Equal(t, http.StatusForbidden, status, "the edit route, the same: %s", body)
	assert.Contains(t, body, "files.delete")
	pf.override(t, nil)

	for uid, own := range map[int64]int64{a: readOnly.ID, b: noDelete.ID} {
		held, err := pf.Store.GetUserCustomRole(ctx, uid)
		require.NoError(t, err)
		assert.Equal(t, own, held, "nothing changed")
	}
}

// Eighth review: moving an account to another tenant takes it out of the old
// tenant's groups — and a restrictive role with them — so a delegated
// administrator is judged by what it would hold afterwards.
func TestGroups_TenantMoveIsJudgedByTheResult(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	super := mustSupertenant(t, pf)
	lookOnly, err := pf.Store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Look only (8)", Enabled: true, Permissions: perm.ReadOnly.Strings(), ProviderID: &super,
		Targets: []model.PermRuleTarget{}, Effects: map[string]string{},
	})
	require.NoError(t, err)
	g, err := pf.Store.CreateGroup(ctx, &model.Group{Name: "Held back (8)", ProviderID: &super, RoleID: &lookOnly.ID})
	require.NoError(t, err)
	target := seedUserIn(t, pf.Store, super, "held@alpha.test")
	status, body := fxJSON(t, "POST", pf.URL+"/api/admin/groups/"+idStr(g.ID)+"/members", pf.adminTok, map[string]any{"user_ids": []int64{target}})
	require.Equal(t, http.StatusOK, status, body)

	// The caller manages users but may not make public links; in the new
	// tenant, out of the group, the target would be on the built-in User role.
	pf.override(t, map[string]string{"admin.users": model.PermAllow, "share.links": model.PermDeny})
	session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
	status, body = sessionJSON(t, session, "PATCH", pf.URL+"/api/admin/users/"+idStr(target), map[string]any{"provider_id": pf.ProvB})
	require.Equal(t, http.StatusForbidden, status, "leaving the group by moving tenant hands them share.links: %s", body)
	assert.Contains(t, body, "share.links")
	u, err := pf.Store.GetUser(ctx, target)
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, super, *u.ProviderID, "not moved")
	pf.override(t, nil)

	status, body = fxJSON(t, "PATCH", pf.URL+"/api/admin/users/"+idStr(target), pf.adminTok, map[string]any{"provider_id": pf.ProvB})
	require.Equal(t, http.StatusOK, status, "an administrator may: %s", body)
}

// Ninth review: a role moved to another tenant would reach none of the
// groups (or people) of its old tenant that hold it — they would drop to
// their built-in role unseen — so the move is refused while any hold it.
func TestGroups_RoleTenantMoveKeepsItsHolders(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	super := mustSupertenant(t, pf)
	status, body := fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "Shared role (9)", "enabled": true, "permissions": perm.ReadOnly.Strings(),
	})
	require.Equal(t, http.StatusCreated, status, body)
	role := decode(t, body)
	rid := int64(role["id"].(float64))
	g, err := pf.Store.CreateGroup(ctx, &model.Group{Name: "Bravo readers (9)", ProviderID: &pf.ProvB, RoleID: &rid})
	require.NoError(t, err)

	role["provider_id"] = super
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/"+idStr(rid), pf.adminTok, role)
	require.Equal(t, http.StatusBadRequest, status, "the Bravo group would lose it: %s", body)
	assert.Contains(t, body, "Bravo readers (9)")
	stored, err := pf.Store.GetPermissionRule(ctx, rid)
	require.NoError(t, err)
	assert.Nil(t, stored.ProviderID, "still install-wide")

	// With the group given another role first, the move goes through.
	g.RoleID = nil
	require.NoError(t, pf.Store.UpdateGroup(ctx, g))
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/"+idStr(rid), pf.adminTok, role)
	require.Equal(t, http.StatusOK, status, body)
}

// Tenth review.
func TestGroups_TenthReviewFindings(t *testing.T) {
	t.Run("a role and a tenant move in one request are judged together", func(t *testing.T) {
		pf := newPermFix(t)
		ctx := context.Background()
		super := mustSupertenant(t, pf)
		readOnly, err := pf.Store.CreatePermissionRule(ctx, &model.PermissionRule{
			Name: "Read only (10)", Enabled: true, Permissions: perm.ReadOnly.Strings(), ProviderID: &super,
			Targets: []model.PermRuleTarget{}, Effects: map[string]string{},
		})
		require.NoError(t, err)
		g, err := pf.Store.CreateGroup(ctx, &model.Group{Name: "Held (10)", ProviderID: &super, RoleID: &readOnly.ID})
		require.NoError(t, err)
		// A Viewer from the start: the group's read-only role moves nothing,
		// so no level is kept for them.
		target := seedUserIn(t, pf.Store, super, "combo@alpha.test")
		require.NoError(t, pf.Store.UpdateUserRole(ctx, target, model.RoleViewer))
		status, body := fxJSON(t, "POST", pf.URL+"/api/admin/groups/"+idStr(g.ID)+"/members", pf.adminTok, map[string]any{"user_ids": []int64{target}})
		require.Equal(t, http.StatusOK, status, body)

		pf.override(t, map[string]string{"admin.users": model.PermAllow, "share.links": model.PermDeny})
		session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
		// Apart, each is harmless: User keeps the group's read-only role; the
		// move alone leaves a Viewer. Together: the built-in User role.
		status, body = sessionJSON(t, session, "PATCH", pf.URL+"/api/admin/users/"+idStr(target), map[string]any{"role": "user", "provider_id": pf.ProvB})
		require.Equal(t, http.StatusForbidden, status, "role and tenant together hand them share.links: %s", body)
		assert.Contains(t, body, "share.links")
		u, err := pf.Store.GetUser(ctx, target)
		require.NoError(t, err)
		assert.Equal(t, model.RoleViewer, u.Role)
		require.NotNil(t, u.ProviderID)
		assert.Equal(t, super, *u.ProviderID, "nothing changed")
		pf.override(t, nil)
	})

	t.Run("a tenant admin edits their group that holds an install-wide role", func(t *testing.T) {
		f := newMTFix(t, true)
		t.Cleanup(perm.Invalidate)
		status, body := sessionJSON(t, f.Super, "POST", f.URL+"/api/admin/roles", map[string]any{
			"name": "Platform readers (10)", "enabled": true, "permissions": perm.ReadOnly.Strings(),
		})
		require.Equal(t, http.StatusCreated, status, body)
		rid := decode(t, body)["id"]
		status, body = sessionJSON(t, f.AdminA, "POST", f.URL+"/api/admin/groups", map[string]any{"name": "Alpha readers (10)"})
		require.Equal(t, http.StatusCreated, status, body)
		gid := idStr(int64(decode(t, body)["group"].(map[string]any)["id"].(float64)))
		status, body = sessionJSON(t, f.Super, "PUT", f.URL+"/api/admin/groups/"+gid, map[string]any{"name": "Alpha readers (10)", "role_id": rid})
		require.Equal(t, http.StatusOK, status, "the supertenant gives it the install-wide role: %s", body)

		status, body = sessionJSON(t, f.AdminA, "PUT", f.URL+"/api/admin/groups/"+gid, map[string]any{"name": "Alpha readers, renamed", "role_id": rid})
		require.Equal(t, http.StatusOK, status, "keeping the role it has is no new choice: %s", body)
		g := decode(t, body)["group"].(map[string]any)
		assert.Equal(t, "Alpha readers, renamed", g["name"])
		assert.EqualValues(t, rid, g["role_id"])

		// Giving it an install-wide role themselves stays refused.
		status, body = sessionJSON(t, f.AdminA, "POST", f.URL+"/api/admin/groups", map[string]any{"name": "Other (10)", "role_id": rid})
		assert.Equal(t, http.StatusBadRequest, status, body)
	})
}
