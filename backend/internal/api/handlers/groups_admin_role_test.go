package handlers_test

// Administrator through a group (migration 00086): a group can make its
// members administrators — typically one linked to an LDAP or SSO group, so
// who administers filex is managed in the directory. Leaving it gives back
// the level from before; the last administrator is never demoted; only a
// signed-in full administrator sets it up or changes who is in it.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/group"
	"github.com/brf-tech/filex/backend/internal/model"
)

func TestGroups_GiveAdministrator(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	body := map[string]any{"name": "IT Admins", "gives_admin": true}

	// An API key may not make administrators; a signed-in administrator may.
	status, resp := fxJSON(t, "POST", pf.URL+"/api/admin/groups", pf.adminTok, body)
	require.Equal(t, http.StatusForbidden, status, resp)
	assert.Contains(t, resp, "session_required")
	status, resp = sessionJSON(t, pf.AdminA, "POST", pf.URL+"/api/admin/groups", body)
	require.Equal(t, http.StatusCreated, status, resp)
	g := decode(t, resp)["group"].(map[string]any)
	assert.Equal(t, true, g["gives_admin"])
	gid := idStr(int64(g["id"].(float64)))

	status, resp = fxJSON(t, "POST", pf.URL+"/api/admin/groups", pf.adminTok, map[string]any{"name": "Both", "gives_admin": true, "role_id": 1})
	require.Equal(t, http.StatusBadRequest, status, "administrator and a role at once: %s", resp)

	// Joining makes them an administrator.
	status, resp = sessionJSON(t, pf.AdminA, "POST", pf.URL+"/api/admin/groups/"+gid+"/members", map[string]any{"user_ids": []int64{pf.UserA}})
	require.Equal(t, http.StatusOK, status, resp)
	u, err := pf.Store.GetUser(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Equal(t, model.RoleAdmin, u.Role)
	assert.True(t, u.AdminByGroup)

	// Demoting them by hand would be undone at once: refused, saying why.
	status, resp = sessionJSON(t, pf.AdminA, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", map[string]any{"role": "user"})
	require.Equal(t, http.StatusConflict, status, resp)
	assert.Contains(t, resp, "through a group")

	// Leaving gives back the level from before.
	status, resp = fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+gid+"/members/"+idStr(pf.UserA), pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, resp)
	u, err = pf.Store.GetUser(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Equal(t, model.RoleUser, u.Role)
	assert.False(t, u.AdminByGroup)

	// An administrator made by hand is not demoted by leaving a group.
	status, resp = sessionJSON(t, pf.AdminA, "POST", pf.URL+"/api/admin/groups/"+gid+"/members", map[string]any{"user_ids": []int64{pf.UserA}})
	require.Equal(t, http.StatusOK, status, resp)
	require.NoError(t, pf.Store.UpdateUserRole(ctx, pf.UserA, model.RoleAdmin)) // as the person's page does
	status, resp = fxJSON(t, "DELETE", pf.URL+"/api/admin/groups/"+gid+"/members/"+idStr(pf.UserA), pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, resp)
	u, err = pf.Store.GetUser(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Equal(t, model.RoleAdmin, u.Role, "made by hand: stays")
}

// A delegated administrator neither sets it up nor changes who is in it.
func TestGroups_GiveAdministratorIsFullAdminsOnly(t *testing.T) {
	pf := newPermFix(t)
	other := seedUserIn(t, pf.Store, mustSupertenant(t, pf), "colleague@alpha.test")
	status, resp := sessionJSON(t, pf.AdminA, "POST", pf.URL+"/api/admin/groups", map[string]any{"name": "IT Admins", "gives_admin": true})
	require.Equal(t, http.StatusCreated, status, resp)
	gid := idStr(int64(decode(t, resp)["group"].(map[string]any)["id"].(float64)))

	pf.override(t, map[string]string{"admin.users": model.PermAllow})
	session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
	status, resp = sessionJSON(t, session, "POST", pf.URL+"/api/admin/groups", map[string]any{"name": "Mine", "gives_admin": true})
	require.Equal(t, http.StatusForbidden, status, resp)
	status, resp = sessionJSON(t, session, "POST", pf.URL+"/api/admin/groups/"+gid+"/members", map[string]any{"user_ids": []int64{other}})
	require.Equal(t, http.StatusForbidden, status, resp)
	status, resp = sessionJSON(t, session, "PUT", pf.URL+"/api/admin/groups/"+gid, map[string]any{"name": "Renamed"})
	require.Equal(t, http.StatusForbidden, status, resp)
	status, resp = sessionJSON(t, session, "DELETE", pf.URL+"/api/admin/groups/"+gid, nil)
	require.Equal(t, http.StatusForbidden, status, resp)
}

// Linked to an LDAP group, the directory decides who administers filex; the
// last administrator stays one when the directory drops them.
func TestGroups_AdministratorFromTheDirectory(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	_, err := pf.Store.CreateGroup(ctx, &model.Group{Name: "IT Admins", GivesAdmin: true,
		Links: []model.GroupLink{{Kind: model.GroupLinkLDAP, Value: itAdmins}}})
	require.NoError(t, err)
	u := ldapAccount(t, pf, pf.UserA, model.MainDirectory)

	_, err = group.SyncLinked(ctx, pf.Store, u, model.GroupLinkLDAP, []string{itAdmins, "it-admins"})
	require.NoError(t, err)
	u, err = pf.Store.GetUser(ctx, pf.UserA)
	require.NoError(t, err)
	require.Equal(t, model.RoleAdmin, u.Role)

	// The local administrators are switched off, as a company moving to the
	// directory would: the directory's administrator is now the last one.
	users, err := pf.Store.ListUsers(ctx)
	require.NoError(t, err)
	var others []int64
	for _, o := range users {
		if o.ID != u.ID && o.IsAdmin() {
			others = append(others, o.ID)
			require.NoError(t, pf.Store.SetUserEnabled(ctx, o.ID, false))
		}
	}
	_, err = group.SyncLinked(ctx, pf.Store, u, model.GroupLinkLDAP, nil)
	require.NoError(t, err)
	u, err = pf.Store.GetUser(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Equal(t, model.RoleAdmin, u.Role, "the last administrator is never demoted")

	// With another administrator on, leaving the directory group demotes.
	for _, id := range others {
		require.NoError(t, pf.Store.SetUserEnabled(ctx, id, true))
	}
	_, err = group.SyncLinked(ctx, pf.Store, u, model.GroupLinkLDAP, []string{itAdmins})
	require.NoError(t, err)
	_, err = group.SyncLinked(ctx, pf.Store, u, model.GroupLinkLDAP, nil)
	require.NoError(t, err)
	u, err = pf.Store.GetUser(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Equal(t, model.RoleUser, u.Role)
}

const itAdmins = "cn=it-admins,ou=groups,dc=example,dc=com"

// ldapAccount labels an account as one an LDAP directory made.
func ldapAccount(t *testing.T, pf *permFix, id int64, directory string) *model.User {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, pf.Store.SetUserAuthSource(ctx, id, model.AuthSourceLDAP))
	require.NoError(t, pf.Store.SetUserAuthDirectory(ctx, id, directory))
	u, err := pf.Store.GetUser(ctx, id)
	require.NoError(t, err)
	return u
}

// A group that makes administrators is reached only by its full DN, and
// only by people of its own directory: a common name matches a group of
// that name anywhere, and another directory (a partner's) names its groups
// — their DNs included — as it likes.
func TestGroups_AdministratorLinksAreStrict(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()

	status, resp := sessionJSON(t, pf.AdminA, "POST", pf.URL+"/api/admin/groups", map[string]any{
		"name": "By name", "gives_admin": true, "links": []map[string]string{{"kind": "ldap", "value": "it-admins"}},
	})
	require.Equal(t, http.StatusBadRequest, status, resp)
	assert.Contains(t, resp, "full DN")

	_, err := pf.Store.CreateGroup(ctx, &model.Group{Name: "IT Admins", GivesAdmin: true,
		Links: []model.GroupLink{{Kind: model.GroupLinkLDAP, Value: itAdmins}}})
	require.NoError(t, err)

	partner := ldapAccount(t, pf, seedUserIn(t, pf.Store, mustSupertenant(t, pf), "pia@partner.test"), "ldap-partner")
	_, err = group.SyncLinked(ctx, pf.Store, partner, model.GroupLinkLDAP, []string{itAdmins, "it-admins"})
	require.NoError(t, err)
	got, err := pf.Store.GetUser(ctx, partner.ID)
	require.NoError(t, err)
	assert.NotEqual(t, model.RoleAdmin, got.Role, "another directory's group of the same DN")

	local, err := pf.Store.GetUser(ctx, pf.UserA)
	require.NoError(t, err)
	_, err = group.SyncLinked(ctx, pf.Store, local, model.GroupLinkLDAP, []string{itAdmins})
	require.NoError(t, err)
	got, err = pf.Store.GetUser(ctx, pf.UserA)
	require.NoError(t, err)
	assert.NotEqual(t, model.RoleAdmin, got.Role, "an account no directory made")

	// The same link on an ordinary group still matches by name, as before.
	plain, err := pf.Store.CreateGroup(ctx, &model.Group{Name: "Ops", Links: []model.GroupLink{{Kind: model.GroupLinkLDAP, Value: "it-admins"}}})
	require.NoError(t, err)
	_, err = group.SyncLinked(ctx, pf.Store, partner, model.GroupLinkLDAP, []string{itAdmins, "it-admins"})
	require.NoError(t, err)
	ms, err := pf.Store.ListUserGroupMemberships(ctx, partner.ID)
	require.NoError(t, err)
	require.Len(t, ms, 1)
	assert.Equal(t, plain.ID, ms[0].GroupID)
}
