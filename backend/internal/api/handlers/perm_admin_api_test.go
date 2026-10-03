package handlers_test

// The admin API for permissions (handlers/permissions_admin.go): shapes, and
// above all the lines a delegated administrator must not cross.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &m), body)
	return m
}

func TestPermAdmin_CatalogueDefaultsAndRules(t *testing.T) {
	pf := newPermFix(t)

	status, body := fxJSON(t, "GET", pf.URL+"/api/admin/roles/catalogue", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	cat := decode(t, body)
	assert.Len(t, cat["permissions"], 28)
	assert.Len(t, cat["presets"], 5)

	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/roles/builtin", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, perm.PresetStandard, decode(t, body)["preset"], "an install that never saved defaults answers Standard")

	narrowed := perm.Standard.Without(perm.AccessSFTP).Strings()
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/builtin", pf.adminTok, map[string]any{"permissions": narrowed})
	require.Equal(t, http.StatusOK, status, body)
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/builtin", pf.adminTok, map[string]any{"permissions": []string{"admin.full"}})
	require.Equal(t, http.StatusBadRequest, status, "admin.full cannot be a default: %s", body)
	// A bad app decision leaves the list as it was: both halves or neither.
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/builtin", pf.adminTok, map[string]any{
		"permissions": perm.Standard.Strings(), "apps": map[string]string{"sign.request": "deny"},
	})
	require.Equal(t, http.StatusBadRequest, status, "not an app key: %s", body)

	// The member now starts without SFTP.
	status, body = fxJSON(t, "GET", pf.URL+"/api/auth/me/permissions", pf.memberTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.NotContains(t, decode(t, body)["allowed"], "access.sftp")

	// A role, end to end through the API.
	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "Contractors", "enabled": true,
		"permissions": perm.Standard.Without(perm.FilesDelete).Strings(),
		"settings":    map[string]any{"share_link_max_days": 7, "blocked_extensions": []string{".EXE"}},
	})
	require.Equal(t, http.StatusCreated, status, body)
	rule := decode(t, body)
	ruleID := idStr(int64(rule["id"].(float64)))
	assert.Equal(t, []any{"exe"}, rule["settings"].(map[string]any)["blocked_extensions"], "normalised on the way in")
	assert.NotContains(t, rule["permissions"], "files.delete")
	builtinMembers := func() map[string]any {
		status, body := fxJSON(t, "GET", pf.URL+"/api/admin/roles", pf.adminTok, nil)
		require.Equal(t, http.StatusOK, status, body)
		return decode(t, body)["builtin_members"].(map[string]any)
	}
	before := builtinMembers()
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok, map[string]any{"role_id": rule["id"]})
	require.Equal(t, http.StatusOK, status, body)
	after := builtinMembers()
	assert.Equal(t, before["user"].(float64)-1, after["user"], "the member left User for the custom role")
	assert.Equal(t, before["admin"], after["admin"])

	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/exceptions", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	eff := decode(t, body)["effective"].(map[string]any)
	var del map[string]any
	for _, p := range eff["permissions"].([]any) {
		if p.(map[string]any)["key"] == "files.delete" {
			del = p.(map[string]any)
		}
	}
	require.NotNil(t, del)
	assert.Equal(t, false, del["allowed"])
	assert.Equal(t, "Contractors", del["source"].(map[string]any)["rule_name"], "the Users page can say which rule")
	assert.EqualValues(t, 7, eff["settings"].(map[string]any)["share_link_max_days"])

	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "allow/deny everywhere", "effects": map[string]string{"files.delete": "deny"},
	})
	require.Equal(t, http.StatusBadRequest, status, "Allow/Deny is only for some folders; the list is the role: %s", body)
	for _, kind := range []string{"everyone", "user"} {
		status, body = fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
			"name": "old style", "targets": []map[string]string{{"kind": kind, "value": idStr(pf.UserA)}},
			"effects": map[string]string{"files.delete": "deny"},
		})
		require.Equal(t, http.StatusBadRequest, status, "a %s target — roles are given on the person's page: %s", kind, body)
	}

	status, _ = fxJSON(t, "DELETE", pf.URL+"/api/admin/roles/"+ruleID+"?to=user", pf.adminTok, nil)
	require.Equal(t, http.StatusNoContent, status)
	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/roles", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, decode(t, body)["rules"])
}

func TestPermAdmin_DelegatedCannotEscalate(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	admin, err := pf.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	other := seedUserIn(t, pf.Store, pf.ProvA, "colleague@alpha.test")
	super, err := pf.Store.GetSupertenant(ctx)
	require.NoError(t, err)
	require.NoError(t, pf.Store.SetUserProvider(ctx, other, super.ID, ""))

	// The member manages users but holds neither admin.audit nor anything
	// the Standard preset lacks.
	pf.override(t, map[string]string{"admin.users": model.PermAllow})
	session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
	put := func(id int64, overrides map[string]string) (int, string) {
		return sessionJSON(t, session, "PUT", pf.URL+"/api/admin/users/"+idStr(id)+"/exceptions", map[string]any{"overrides": overrides})
	}

	status, body := sessionJSON(t, session, "GET", pf.URL+"/api/admin/roles/catalogue", nil)
	require.Equal(t, http.StatusOK, status, "admin.users reads the catalogue: %s", body)

	status, body = put(other, map[string]string{"files.delete": "deny"})
	require.Equal(t, http.StatusOK, status, "taking a permission away is the job: %s", body)
	status, body = put(other, map[string]string{"files.download": "allow"})
	require.Equal(t, http.StatusOK, status, "allowing one they hold is fine: %s", body)

	status, body = put(other, map[string]string{"admin.audit": "allow"})
	require.Equal(t, http.StatusForbidden, status, "allowing what they do not hold: %s", body)
	status, body = put(other, map[string]string{"admin.full": "allow"})
	require.Equal(t, http.StatusBadRequest, status, "admin.full is the role: %s", body)
	status, body = put(admin.ID, map[string]string{"files.delete": "deny"})
	require.Equal(t, http.StatusForbidden, status, "an administrator's account: %s", body)

	// Rules and defaults are an administrator's alone.
	status, body = sessionJSON(t, session, "POST", pf.URL+"/api/admin/roles", map[string]any{
		"name": "me", "enabled": true,
		"permissions": []string{"admin.audit"},
	})
	require.Equal(t, http.StatusForbidden, status, "a delegated admin writing a rule: %s", body)
	status, body = sessionJSON(t, session, "PUT", pf.URL+"/api/admin/roles/builtin", map[string]any{"permissions": []string{"admin.audit"}})
	require.Equal(t, http.StatusForbidden, status, "a delegated admin writing the defaults: %s", body)

	got, err := pf.Store.GetUserPermissionOverrides(ctx, other)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"files.download": "allow"}, got, "only the permitted write landed")
}

func TestPermAdmin_ChangesAreAudited(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()

	status, body := fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/exceptions", pf.adminTok,
		map[string]any{"overrides": map[string]string{"files.delete": "deny"}})
	require.Equal(t, http.StatusOK, status, body)
	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "Contractors", "enabled": true,
		"permissions": perm.Standard.Without(perm.ShareLinks).Strings(),
	})
	require.Equal(t, http.StatusCreated, status, body)
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/builtin", pf.adminTok,
		map[string]any{"permissions": perm.Standard.Without(perm.AccessFTP).Strings()})
	require.Equal(t, http.StatusOK, status, body)

	rows, _, err := pf.Store.ListAuditFiltered(ctx, nil, "", nil, nil, 50, 0)
	require.NoError(t, err)
	byAction := map[string]map[string]any{}
	for _, r := range rows {
		byAction[r.Entry.Action] = r.Entry.Metadata
	}

	set := byAction["user.permissions_set"]
	require.NotNil(t, set, "the override change is its own action, not a generic user update")
	assert.Equal(t, map[string]any{"files.delete": "deny"}, set["after"])
	assert.Equal(t, map[string]any{}, set["before"])

	created := byAction["permission_rule.create"]
	require.NotNil(t, created)
	assert.Equal(t, "Contractors", created["rule"].(map[string]any)["name"])

	defaults := byAction["permissions.defaults_set"]
	require.NotNil(t, defaults)
	assert.NotContains(t, defaults["after"], "access.ftp")
	assert.Contains(t, defaults["before"], "access.ftp", "the before is what the install had: Standard")
}

// ── roles ──────────────────────────────────────────────────────────────────

func TestRoles_OneRolePerPerson(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	// Auditor allows admin.audit: making it, giving it and moving people
	// into it is an administrator's in the panel, not a key's (#120) — so the
	// roles are made and given from the administrator's session.
	mk := func(name string, list perm.Set) int64 {
		status, body := sessionJSON(t, pf.AdminA, "POST", pf.URL+"/api/admin/roles", map[string]any{
			"name": name, "enabled": true, "permissions": list.Strings(),
		})
		require.Equal(t, http.StatusCreated, status, "a role nobody holds yet: %s", body)
		return int64(decode(t, body)["id"].(float64))
	}
	contractor := mk("Contractor", perm.Standard.Without(perm.FilesDelete))
	auditor := mk("Auditor", perm.ReadOnly.With(perm.AdminAudit))
	put := func(id any) (int, string) {
		return sessionJSON(t, pf.AdminA, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", map[string]any{"role_id": id})
	}
	roleOf := func() string {
		u, err := pf.Store.GetUser(ctx, pf.UserA)
		require.NoError(t, err)
		return u.Role
	}

	status, body := put(contractor)
	require.Equal(t, http.StatusOK, status, body)
	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status)
	assert.EqualValues(t, contractor, decode(t, body)["role_id"])
	status, body = deleteItem(t, pf, pf.memberTok, "report.txt")
	got := requireDenied(t, "delete as a Contractor", status, body, perm.FilesDelete, perm.SourceRule)
	assert.Contains(t, got["message"], "role “Contractor”")

	assert.Equal(t, model.RoleUser, roleOf(), "a role that can change files needs the User level")

	// Giving another role REPLACES the first; a role that changes nothing
	// puts its holder on the Viewer level, so it stays read-only everywhere.
	status, body = put(auditor)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, model.RoleViewer, roleOf(), "Auditor can only look at files")
	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/roles", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, map[string]any{idStr(pf.UserA): float64(auditor)}, decode(t, body)["assignments"], "one role, the new one")

	// Editing the role so it can add files moves its people up with it.
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/"+idStr(auditor), pf.adminTok, map[string]any{
		"name": "Auditor", "enabled": true, "permissions": perm.ReadOnly.With(perm.AdminAudit).With(perm.FilesCreate).Strings(),
	})
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, model.RoleUser, roleOf())

	// Picking a built-in role by hand ends the custom one.
	status, body = fxJSON(t, "PATCH", pf.URL+"/api/admin/users/"+idStr(pf.UserA), pf.adminTok, map[string]any{"role": "viewer"})
	require.Equal(t, http.StatusOK, status, body)
	held, err := pf.Store.GetUserCustomRole(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Zero(t, held)

	// A switched-off role gives nothing: the person does NOT fall back to
	// the whole User role (which would give Contractor's people delete).
	status, body = put(contractor)
	require.Equal(t, http.StatusOK, status, body)
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/"+idStr(contractor), pf.adminTok, map[string]any{
		"name": "Contractor", "enabled": false, "permissions": perm.Standard.Without(perm.FilesDelete).Strings(),
	})
	require.Equal(t, http.StatusOK, status, body)
	status, body = deleteItem(t, pf, pf.memberTok, "report.txt")
	require.Equal(t, http.StatusUnauthorized, status, "not even the API: %s", body)
	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/exceptions", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Empty(t, decode(t, body)["effective"].(map[string]any)["allowed"])
	assert.Contains(t, body, `"kind":"role_off"`, "the Users page can say why")

	// Deleting a role people hold asks what they become…
	status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/roles/"+idStr(contractor), pf.adminTok, nil)
	require.Equal(t, http.StatusConflict, status, body)
	assert.EqualValues(t, 1, decode(t, body)["holders"])
	status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/roles/"+idStr(contractor)+"?to="+idStr(contractor), pf.adminTok, nil)
	require.Equal(t, http.StatusBadRequest, status, "not the role being deleted: %s", body)
	// …and moves them there — into a role with administration rights only
	// from the panel: a key would be giving Auditor by the back door.
	status, body = fxJSON(t, "DELETE", pf.URL+"/api/admin/roles/"+idStr(contractor)+"?to="+idStr(auditor), pf.adminTok, nil)
	require.Equal(t, http.StatusForbidden, status, body)
	assert.Contains(t, body, "session_required")
	status, _ = sessionJSON(t, pf.AdminA, "DELETE", pf.URL+"/api/admin/roles/"+idStr(contractor)+"?to="+idStr(auditor), nil)
	require.Equal(t, http.StatusNoContent, status)
	held, err = pf.Store.GetUserCustomRole(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Equal(t, auditor, held)
	assert.Equal(t, model.RoleUser, roleOf(), "Auditor (as edited above) can add files")

	// A built-in role in the same call ends the custom one.
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok, map[string]any{"role": "viewer"})
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, model.RoleViewer, roleOf())
	held, err = pf.Store.GetUserCustomRole(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Zero(t, held)

	// Taken away by hand: null.
	status, body = put(auditor)
	require.Equal(t, http.StatusOK, status, body)
	status, body = put(nil)
	require.Equal(t, http.StatusOK, status, body)
	held, err = pf.Store.GetUserCustomRole(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Zero(t, held)
}

func TestRoles_AdministratorGivenARoleInOneStep(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	other, err := pf.Store.CreateUser(ctx, "second-admin@alpha.test", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	status, body := fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "Contractor", "enabled": true, "permissions": perm.Standard.Without(perm.FilesDelete).Strings(),
	})
	require.Equal(t, http.StatusCreated, status, body)
	roleID := decode(t, body)["id"]
	// Changing an administrator's role is the panel's, never a key's (#120).
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(other.ID)+"/roles", pf.adminTok, map[string]any{"role_id": roleID})
	require.Equal(t, http.StatusForbidden, status, body)
	assert.Contains(t, body, "session_required")
	// One call: no longer an administrator, now a Contractor.
	status, body = sessionJSON(t, pf.AdminA, "PUT", pf.URL+"/api/admin/users/"+idStr(other.ID)+"/roles", map[string]any{"role_id": roleID})
	require.Equal(t, http.StatusOK, status, body)
	u, err := pf.Store.GetUser(ctx, other.ID)
	require.NoError(t, err)
	assert.Equal(t, model.RoleUser, u.Role)
	held, err := pf.Store.GetUserCustomRole(ctx, other.ID)
	require.NoError(t, err)
	assert.EqualValues(t, roleID, held)

	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(other.ID)+"/roles", pf.adminTok, map[string]any{"role": "wizard"})
	require.Equal(t, http.StatusBadRequest, status, body)
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(other.ID)+"/roles", pf.adminTok, map[string]any{"role": "user", "role_id": roleID})
	require.Equal(t, http.StatusBadRequest, status, "one or the other: %s", body)
	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "Admins", "permissions": []string{"admin.full"},
	})
	require.Equal(t, http.StatusBadRequest, status, "admin.full is the Administrator role alone: %s", body)
}

func TestRoles_DelegatedAdminGivesOnlyWhatTheyHold(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	other := seedUserIn(t, pf.Store, pf.ProvA, "colleague@alpha.test")
	super, err := pf.Store.GetSupertenant(ctx)
	require.NoError(t, err)
	require.NoError(t, pf.Store.SetUserProvider(ctx, other, super.ID, ""))

	mk := func(name string, list perm.Set) int64 {
		status, body := sessionJSON(t, pf.AdminA, "POST", pf.URL+"/api/admin/roles", map[string]any{
			"name": name, "enabled": true, "permissions": list.Strings(),
		})
		require.Equal(t, http.StatusCreated, status, body)
		return int64(decode(t, body)["id"].(float64))
	}
	restrict := mk("Restricted", perm.Standard.Without(perm.FilesDelete))
	auditor := mk("Auditor", perm.ReadOnly.With(perm.AdminAudit))

	pf.override(t, map[string]string{"admin.users": model.PermAllow})
	session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)

	status, body := sessionJSON(t, session, "GET", pf.URL+"/api/admin/roles", nil)
	require.Equal(t, http.StatusOK, status, "admin.users lists the roles to pick from: %s", body)

	status, body = sessionJSON(t, session, "PUT", pf.URL+"/api/admin/users/"+idStr(other)+"/roles", map[string]any{"role_id": restrict})
	require.Equal(t, http.StatusOK, status, "a role that only takes away: %s", body)
	status, body = sessionJSON(t, session, "PUT", pf.URL+"/api/admin/users/"+idStr(other)+"/roles", map[string]any{"role_id": auditor})
	require.Equal(t, http.StatusForbidden, status, "a role allowing admin.audit, which the caller lacks: %s", body)

	status, body = sessionJSON(t, session, "PUT", pf.URL+"/api/admin/roles/"+idStr(restrict), map[string]any{"name": "x", "permissions": []string{"files.download"}})
	require.Equal(t, http.StatusForbidden, status, "editing a role stays an administrator's: %s", body)
}

func TestRoles_ViewerRoleIsEditableButCapped(t *testing.T) {
	pf := newPermFix(t)
	status, body := fxJSON(t, "GET", pf.URL+"/api/admin/roles/builtin?role=viewer", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, perm.PresetReadOnly, decode(t, body)["preset"])

	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/builtin?role=viewer", pf.adminTok, map[string]any{"permissions": []string{"files.download", "comments.write"}})
	require.Equal(t, http.StatusOK, status, body)
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/builtin?role=viewer", pf.adminTok, map[string]any{"permissions": []string{"files.delete"}})
	require.Equal(t, http.StatusBadRequest, status, "a viewer can never delete: %s", body)

	// The User role's defaults are untouched by the Viewer's.
	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/roles/builtin", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, perm.PresetStandard, decode(t, body)["preset"])
}

// The custom role editor's app-permission "Default" rests on the built-in
// role a role's people are on. The rule is the server's (perm.HolderRole);
// the editor asks it for the body it has, saved or not (lesson #759: 0.49.0
// kept a copy of the rule in the editor).
func TestPermAdmin_PreviewHolderRole(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	preview := func(body map[string]any) string {
		t.Helper()
		status, out := fxJSON(t, "POST", pf.URL+"/api/admin/roles/preview", pf.adminTok, body)
		require.Equal(t, http.StatusOK, status, out)
		return decode(t, out)["holder_role"].(string)
	}

	readOnly := map[string]any{"permissions": perm.ReadOnly.Strings()}
	assert.Equal(t, model.RoleViewer, preview(readOnly))
	assert.Equal(t, model.RoleUser, preview(map[string]any{"permissions": perm.Standard.Strings()}))
	assert.Equal(t, model.RoleViewer, preview(map[string]any{}), "an unfinished body — no name yet — is answered, not refused")
	someFolders := map[string]any{
		"permissions": perm.ReadOnly.Strings(),
		"effects":     map[string]string{"files.delete": "allow"},
		"conditions":  map[string]any{"paths": []string{"Scratch"}},
	}
	assert.Equal(t, model.RoleUser, preview(someFolders), "deleting in some folders needs the User level")
	assert.Equal(t, model.RoleViewer, preview(map[string]any{
		"permissions": perm.ReadOnly.Strings(),
		"effects":     map[string]string{"files.delete": "allow"},
		"conditions":  map[string]any{"paths": []string{" ", "/"}},
	}), "a blank path names no folder once saved")

	// The answer is what saving does: the same body, saved and given, puts
	// its holder on that level.
	for _, body := range []map[string]any{someFolders, readOnly} {
		want := preview(body)
		saved := map[string]any{"name": "Preview " + want, "enabled": true}
		for k, v := range body {
			saved[k] = v
		}
		status, out := fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, saved)
		require.Equal(t, http.StatusCreated, status, out)
		status, out = fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok, map[string]any{"role_id": decode(t, out)["id"]})
		require.Equal(t, http.StatusOK, status, out)
		u, err := pf.Store.GetUser(ctx, pf.UserA)
		require.NoError(t, err)
		assert.Equal(t, want, u.Role, "saved, %v puts its people on %s", body, want)
	}

	// Editing roles is an administrator's; so is asking what an edit comes to.
	status, out := fxJSON(t, "POST", pf.URL+"/api/admin/roles/preview", pf.memberTok, readOnly)
	assert.Equal(t, http.StatusForbidden, status, out)
	status, out = fxReq(t, "POST", pf.URL+"/api/admin/roles/preview", pf.adminTok, strings.NewReader("{"), "application/json")
	assert.Equal(t, http.StatusBadRequest, status, out)
}
