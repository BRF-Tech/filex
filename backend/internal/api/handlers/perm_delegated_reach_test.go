package handlers_test

// A delegated administrator (admin.users, not the admin role) judged by the
// RESULT of what they do, not by the words of the request (PR #75 review).
//
// The request-word checks already there — "an Allow you do not hold", "a
// custom role whose list you do not hold", "a built-in role above your own" —
// miss every way of handing out a permission without naming it: lifting a
// Deny (on oneself or anyone), ending a restrictive custom role, picking the
// built-in User role for an account whose custom role took something away,
// creating an account on a built-in role that holds more than the caller.
// And a delegated administrator who may reset the password of an account
// that holds MORE than they do simply signs in as it.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

func TestPermAdmin_DelegatedReach(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	super, err := pf.Store.GetSupertenant(ctx)
	require.NoError(t, err)
	seed := func(email string) int64 {
		id := seedUserIn(t, pf.Store, pf.ProvA, email)
		require.NoError(t, pf.Store.SetUserProvider(ctx, id, super.ID, ""))
		return id
	}
	setOv := func(id int64, m map[string]string) {
		require.NoError(t, pf.Store.SetUserPermissionOverrides(ctx, id, m, nil))
		perm.Invalidate()
	}
	overridesOf := func(id int64) map[string]string {
		got, err := pf.Store.GetUserPermissionOverrides(ctx, id)
		require.NoError(t, err)
		return got
	}

	// The delegated administrator manages users, and an administrator took
	// files.delete away from them.
	pf.override(t, map[string]string{"admin.users": model.PermAllow, "files.delete": model.PermDeny})
	session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
	put := func(id int64, overrides map[string]string) (int, string) {
		return sessionJSON(t, session, "PUT", pf.URL+"/api/admin/users/"+idStr(id)+"/exceptions", map[string]any{"overrides": overrides})
	}

	t.Run("lifting a Deny on themselves", func(t *testing.T) {
		status, body := put(pf.UserA, map[string]string{"admin.users": model.PermAllow})
		require.Equal(t, http.StatusForbidden, status, "would regain files.delete: %s", body)
		assert.Equal(t, model.PermDeny, overridesOf(pf.UserA)["files.delete"], "the Deny is still there")
	})

	t.Run("lifting a Deny on someone else", func(t *testing.T) {
		other := seed("plain@alpha.test")
		setOv(other, map[string]string{"files.delete": model.PermDeny})
		status, body := put(other, map[string]string{})
		require.Equal(t, http.StatusForbidden, status, "hands out files.delete, which the caller lacks: %s", body)
		assert.Equal(t, model.PermDeny, overridesOf(other)["files.delete"])

		status, body = put(other, map[string]string{"files.delete": model.PermDeny, "share.links": model.PermDeny})
		require.Equal(t, http.StatusOK, status, "taking more away is still the job: %s", body)
	})

	t.Run("ending a restrictive custom role", func(t *testing.T) {
		status, body := fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
			"name": "No delete", "enabled": true, "permissions": perm.Standard.Without(perm.FilesDelete).Strings(),
		})
		require.Equal(t, http.StatusCreated, status, body)
		noDelete := int64(decode(t, body)["id"].(float64))
		x := seed("restricted@alpha.test")
		status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(x)+"/roles", pf.adminTok, map[string]any{"role_id": noDelete})
		require.Equal(t, http.StatusOK, status, body)

		status, body = sessionJSON(t, session, "PUT", pf.URL+"/api/admin/users/"+idStr(x)+"/roles", map[string]any{"role": "user"})
		require.Equal(t, http.StatusForbidden, status, "the built-in User role holds files.delete: %s", body)
		status, body = sessionJSON(t, session, "PUT", pf.URL+"/api/admin/users/"+idStr(x)+"/roles", map[string]any{"role_id": nil})
		require.Equal(t, http.StatusForbidden, status, "no custom role is the built-in User role: %s", body)
		held, err := pf.Store.GetUserCustomRole(ctx, x)
		require.NoError(t, err)
		assert.Equal(t, noDelete, held, "still on the restrictive role")

		// The account page's role field (PATCH) is the same change.
		status, body = sessionJSON(t, session, "PATCH", pf.URL+"/api/admin/users/"+idStr(x), map[string]any{"role": "viewer"})
		require.Equal(t, http.StatusOK, status, "Viewer takes away, nothing more: %s", body)
		status, body = sessionJSON(t, session, "PATCH", pf.URL+"/api/admin/users/"+idStr(x), map[string]any{"role": "user"})
		require.Equal(t, http.StatusForbidden, status, "back to User would hand out files.delete: %s", body)
		u, err := pf.Store.GetUser(ctx, x)
		require.NoError(t, err)
		assert.Equal(t, model.RoleViewer, u.Role)
	})

	t.Run("creating an account that holds more", func(t *testing.T) {
		status, body := sessionJSON(t, session, "POST", pf.URL+"/api/admin/users", map[string]any{
			"email": "fresh-user@alpha.test", "password": "Str0ng!Pass", "role": "user"})
		require.Equal(t, http.StatusForbidden, status, "a User account holds files.delete: %s", body)
		_, err := pf.Store.GetUserByEmail(ctx, "fresh-user@alpha.test")
		assert.Error(t, err, "and was not created")

		status, body = sessionJSON(t, session, "POST", pf.URL+"/api/admin/users", map[string]any{
			"email": "fresh-viewer@alpha.test", "password": "Str0ng!Pass", "role": "viewer"})
		require.Less(t, status, 300, "a Viewer holds nothing the caller lacks: %s", body)
	})

	t.Run("taking over an account that holds more", func(t *testing.T) {
		auditor := seed("auditor@alpha.test")
		setOv(auditor, map[string]string{"admin.users": model.PermAllow, "admin.audit": model.PermAllow, "files.delete": model.PermDeny})

		status, body := sessionJSON(t, session, "POST", pf.URL+"/api/admin/users/"+idStr(auditor)+"/reset-password", nil)
		require.Equal(t, http.StatusForbidden, status, "their new password would be admin.audit: %s", body)
		assert.NotContains(t, body, "new_password")
		status, body = sessionJSON(t, session, "PATCH", pf.URL+"/api/admin/users/"+idStr(auditor), map[string]any{"password": "Taken0ver!Pass"})
		require.Equal(t, http.StatusForbidden, status, "setting their password is the same takeover: %s", body)
		loginOK, _ := sessionJSON(t, &http.Client{}, "POST", pf.URL+"/api/auth/login", map[string]any{"email": "auditor@alpha.test", "password": "Taken0ver!Pass"})
		assert.NotEqual(t, http.StatusOK, loginOK, "the password did not change")

		// An account that holds no more than the caller can still be helped.
		low := seed("low@alpha.test")
		setOv(low, map[string]string{"files.delete": model.PermDeny})
		status, body = sessionJSON(t, session, "POST", pf.URL+"/api/admin/users/"+idStr(low)+"/reset-password", nil)
		require.Equal(t, http.StatusOK, status, "a reset for someone within reach: %s", body)
	})
}

// A custom role belongs to a tenant (provider_id); given to an account of
// another tenant it would bind nobody — Resolve treats it as switched off —
// so the assignment is refused rather than stored as a silent no-op.
func TestRoles_RoleOfAnotherTenantIsRefused(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	status, body := fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "Alpha only", "enabled": true, "provider_id": pf.ProvA,
		"permissions": perm.Standard.Without(perm.FilesDelete).Strings(),
	})
	require.Equal(t, http.StatusCreated, status, body)
	alphaRole := int64(decode(t, body)["id"].(float64))

	// The member lives in the platform tenant (single-tenant fixture).
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok, map[string]any{"role_id": alphaRole})
	require.Equal(t, http.StatusBadRequest, status, "a role of another tenant: %s", body)
	held, err := pf.Store.GetUserCustomRole(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Zero(t, held)
}
