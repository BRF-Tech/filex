package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// A permission a LATER version added, as that version stored it: this
// version's catalogue does not know it. Saving on this version must not drop
// it - that is how 0.50 lost files.encrypt (PR #86, Berk Başarır): the
// built-in User role's page read the list without the key it did not know and
// wrote it back without it, and the version that knew the key had already
// merged it once and never again.
const laterKey = "files.from_a_later_version"

// laterFolderKey is a later version's permission in a role's folder part.
const laterFolderKey = "share.from_a_later_version"

func builtinList(t *testing.T, pf *permFix) []string {
	t.Helper()
	raw, err := pf.Store.GetSetting(context.Background(), model.SettingPermissionDefaults)
	require.NoError(t, err)
	var keys []string
	require.NoError(t, json.Unmarshal([]byte(raw), &keys))
	return keys
}

func TestPermAdmin_ALaterVersionsKeySurvivesTheBuiltinRolesSave(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	stored, err := json.Marshal(append(perm.Standard.Without(perm.AccessFTP).Strings(), laterKey))
	require.NoError(t, err)
	require.NoError(t, pf.Store.UpsertSetting(ctx, model.SettingPermissionDefaults, string(stored)))

	// The page reads the list - without the key this version does not know -
	// and saves it back with one change, as an administrator does.
	status, body := fxJSON(t, "GET", pf.URL+"/api/admin/roles/builtin", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	var got struct {
		Permissions []string `json:"permissions"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	require.NotContains(t, got.Permissions, laterKey, "this version cannot show a key it does not know")
	next := perm.FromStrings(got.Permissions).Without(perm.AccessSFTP).Strings()

	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/builtin", pf.adminTok, map[string]any{"permissions": next})
	require.Equal(t, http.StatusOK, status, body)
	keys := builtinList(t, pf)
	assert.Contains(t, keys, laterKey, "the later version's key is not this version's to drop")
	assert.NotContains(t, keys, string(perm.AccessSFTP), "the change the administrator made is saved")

	// Sent back as it is stored, it is accepted and kept once.
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/builtin", pf.adminTok, map[string]any{"permissions": append(next, laterKey)})
	require.Equal(t, http.StatusOK, status, "a key the list already holds is not a new unknown key: %s", body)
	n := 0
	for _, k := range builtinList(t, pf) {
		if k == laterKey {
			n++
		}
	}
	assert.Equal(t, 1, n, "kept once")

	// A key nobody stored is still refused.
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/builtin", pf.adminTok, map[string]any{"permissions": append(next, "files.nobody_knows")})
	require.Equal(t, http.StatusBadRequest, status, body)
	assert.Contains(t, body, "files.nobody_knows")
}

func TestPermAdmin_ALaterVersionsKeySurvivesACustomRolesSave(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	rule, err := pf.Store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name:        "Read, add in Drop",
		Enabled:     true,
		Permissions: []string{string(perm.FilesDownload), laterKey},
		Effects:     map[string]string{string(perm.FilesCreate): model.PermAllow, laterFolderKey: model.PermAllow},
		Conditions:  model.PermRuleConditions{Paths: []string{"Drop"}},
	})
	require.NoError(t, err)
	url := pf.URL + "/api/admin/roles/" + idStr(rule.ID)

	// The role editor sends the role back as the list gave it, renamed.
	body := map[string]any{
		"name": "Read, add in Drop (renamed)", "enabled": true,
		"permissions": rule.Permissions, "effects": rule.Effects, "conditions": rule.Conditions,
		"targets": []any{}, "settings": map[string]any{},
	}
	status, resp := fxJSON(t, "PUT", url, pf.adminTok, body)
	require.Equal(t, http.StatusOK, status, "the role's own stored keys are not new unknown keys: %s", resp)
	saved, err := pf.Store.GetPermissionRule(ctx, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, "Read, add in Drop (renamed)", saved.Name)
	assert.Contains(t, saved.Permissions, laterKey)
	assert.Equal(t, model.PermAllow, saved.Effects[laterFolderKey])

	// A client that builds the lists from this version's catalogue leaves the
	// later keys out; they stay anyway.
	body["permissions"] = []string{string(perm.FilesDownload), string(perm.FilesTag)}
	body["effects"] = map[string]string{string(perm.FilesCreate): model.PermAllow}
	status, resp = fxJSON(t, "PUT", url, pf.adminTok, body)
	require.Equal(t, http.StatusOK, status, resp)
	saved, err = pf.Store.GetPermissionRule(ctx, rule.ID)
	require.NoError(t, err)
	assert.Contains(t, saved.Permissions, string(perm.FilesTag), "the administrator's change is saved")
	assert.Contains(t, saved.Permissions, laterKey, "the own list keeps the later key")
	assert.Equal(t, model.PermAllow, saved.Effects[laterFolderKey], "the folder part keeps the later key")

	// A key the role never held is refused, in its list and in its folder part.
	body["permissions"] = []string{string(perm.FilesDownload), "files.nobody_knows"}
	status, resp = fxJSON(t, "PUT", url, pf.adminTok, body)
	require.Equal(t, http.StatusBadRequest, status, resp)
	body["permissions"] = []string{string(perm.FilesDownload)}
	body["effects"] = map[string]string{"share.nobody_knows": model.PermAllow}
	status, resp = fxJSON(t, "PUT", url, pf.adminTok, body)
	require.Equal(t, http.StatusBadRequest, status, resp)

	// A new role cannot bring an unknown key at all.
	status, resp = fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "New with a later key", "enabled": true, "permissions": []string{laterKey},
	})
	require.Equal(t, http.StatusBadRequest, status, resp)

	// Taking the folders away takes the whole folder part, the later key too:
	// an Allow without folders means nothing.
	body["effects"] = map[string]string{}
	body["conditions"] = map[string]any{}
	status, resp = fxJSON(t, "PUT", url, pf.adminTok, body)
	require.Equal(t, http.StatusOK, status, resp)
	saved, err = pf.Store.GetPermissionRule(ctx, rule.ID)
	require.NoError(t, err)
	assert.Empty(t, saved.Effects)
	assert.Contains(t, saved.Permissions, laterKey, "the own list still keeps it")
}

func TestPermAdmin_ALaterVersionsKeySurvivesAPersonsExceptions(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	require.NoError(t, pf.Store.SetUserPermissionOverrides(ctx, pf.UserA,
		map[string]string{string(perm.FilesDelete): model.PermDeny, laterKey: model.PermAllow}, nil))
	url := pf.URL + "/api/admin/users/" + idStr(pf.UserA) + "/exceptions"

	// The page sends the exceptions back as it read them, one changed.
	status, body := fxJSON(t, "PUT", url, pf.adminTok, map[string]any{"overrides": map[string]string{
		string(perm.FilesDelete): model.PermDeny, string(perm.FilesPurge): model.PermDeny, laterKey: model.PermAllow,
	}})
	require.Equal(t, http.StatusOK, status, "an exception the person already has is not a new unknown key: %s", body)
	got, err := pf.Store.GetUserPermissionOverrides(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Equal(t, model.PermDeny, got[string(perm.FilesPurge)])
	assert.Equal(t, model.PermAllow, got[laterKey])

	// "Clear exceptions" clears what this version shows; the later key stays.
	status, body = fxJSON(t, "PUT", url, pf.adminTok, map[string]any{"overrides": map[string]string{}})
	require.Equal(t, http.StatusOK, status, body)
	got, err = pf.Store.GetUserPermissionOverrides(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{laterKey: model.PermAllow}, got)

	// A key the person never had is refused.
	status, body = fxJSON(t, "PUT", url, pf.adminTok, map[string]any{"overrides": map[string]string{"files.nobody_knows": model.PermAllow}})
	require.Equal(t, http.StatusBadRequest, status, body)
}
