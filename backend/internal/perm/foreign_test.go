package perm_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// A permission a later version added and stored: this version's catalogue
// does not know it (foreign.go).
const later = "files.from_a_later_version"

func storedRaw(t *testing.T, store db.Store, key string) []string {
	t.Helper()
	raw, err := store.GetSetting(context.Background(), key)
	require.NoError(t, err)
	var keys []string
	require.NoError(t, json.Unmarshal([]byte(raw), &keys))
	return keys
}

func storeRaw(t *testing.T, store db.Store, key string, keys []string) {
	t.Helper()
	b, err := json.Marshal(keys)
	require.NoError(t, err)
	require.NoError(t, store.UpsertSetting(context.Background(), key, string(b)))
}

// The built-in roles' save keeps what the page could not show: 0.50 did not,
// and lost files.encrypt that way (PR #86).
func TestSaveRoleBase_KeepsALaterVersionsKey(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	storeRaw(t, store, model.SettingPermissionDefaults, append(perm.Standard.Strings(), later))
	storeRaw(t, store, model.SettingPermissionViewerDefaults, append(perm.ReadOnly.Strings(), later))

	got, err := perm.LoadRoleBase(ctx, store, model.RoleUser)
	require.NoError(t, err)
	require.Equal(t, perm.Standard, got, "read as this version knows it")

	require.NoError(t, perm.SaveRoleBase(ctx, store, model.RoleUser, got.Without(perm.AccessFTP)))
	require.NoError(t, perm.SaveRoleBase(ctx, store, model.RoleViewer, perm.Of(perm.FilesDownload)))
	user := storedRaw(t, store, model.SettingPermissionDefaults)
	require.Contains(t, user, later)
	require.NotContains(t, user, string(perm.AccessFTP), "the change is saved")
	require.Equal(t, []string{string(perm.FilesDownload), later}, storedRaw(t, store, model.SettingPermissionViewerDefaults))

	// Saved again, it is there once.
	require.NoError(t, perm.SaveDefaults(ctx, store, perm.Standard))
	n := 0
	for _, k := range storedRaw(t, store, model.SettingPermissionDefaults) {
		if k == later {
			n++
		}
	}
	require.Equal(t, 1, n)

	foreign, err := perm.StoredForeign(ctx, store, model.RoleUser)
	require.NoError(t, err)
	require.Equal(t, []string{later}, foreign)
}

func TestNormalizeRuleEdit_KeepsALaterVersionsKeys(t *testing.T) {
	prev := &model.PermissionRule{
		Name: "Drop", Enabled: true,
		Permissions: []string{string(perm.FilesDownload), later},
		Effects:     map[string]string{string(perm.FilesCreate): model.PermAllow, "share.later": model.PermDeny},
		Conditions:  model.PermRuleConditions{Paths: []string{"Drop"}},
	}

	// Sent back as it was read.
	r := &model.PermissionRule{
		Name: "Drop", Enabled: true,
		Permissions: []string{string(perm.FilesDownload), later},
		Effects:     map[string]string{string(perm.FilesCreate): model.PermAllow, "share.later": model.PermAllow},
		Conditions:  model.PermRuleConditions{Paths: []string{"Drop"}},
	}
	require.NoError(t, perm.NormalizeRuleEdit(r, prev))
	require.Equal(t, []string{string(perm.FilesDownload), later}, r.Permissions)
	require.Equal(t, model.PermDeny, r.Effects["share.later"], "the stored decision is kept: this version cannot change it")

	// Left out by a client that builds its lists from this catalogue.
	r = &model.PermissionRule{
		Name: "Drop", Enabled: true,
		Permissions: []string{string(perm.FilesTag)},
		Effects:     map[string]string{string(perm.FilesCreate): model.PermAllow},
		Conditions:  model.PermRuleConditions{Paths: []string{"Drop"}},
	}
	require.NoError(t, perm.NormalizeRuleEdit(r, prev))
	require.Equal(t, []string{string(perm.FilesTag), later}, r.Permissions)
	require.Equal(t, map[string]string{string(perm.FilesCreate): model.PermAllow, "share.later": model.PermDeny}, r.Effects)

	// Without folders there is no folder part, the later key's either.
	r = &model.PermissionRule{Name: "Drop", Enabled: true, Permissions: []string{string(perm.FilesDownload)}}
	require.NoError(t, perm.NormalizeRuleEdit(r, prev))
	require.Empty(t, r.Effects)
	require.Equal(t, []string{string(perm.FilesDownload), later}, r.Permissions)

	// A key prev never held is refused, in either place, and a new role
	// cannot bring one at all.
	r = &model.PermissionRule{Name: "Drop", Enabled: true, Permissions: []string{"files.nobody_knows"}}
	require.ErrorIs(t, perm.NormalizeRuleEdit(r, prev), perm.ErrInvalid)
	r = &model.PermissionRule{
		Name: "Drop", Enabled: true,
		Effects:    map[string]string{"share.nobody_knows": model.PermAllow},
		Conditions: model.PermRuleConditions{Paths: []string{"Drop"}},
	}
	require.ErrorIs(t, perm.NormalizeRuleEdit(r, prev), perm.ErrInvalid)
	r = &model.PermissionRule{Name: "New", Enabled: true, Permissions: []string{later}}
	require.ErrorIs(t, perm.NormalizeRule(r), perm.ErrInvalid)
}

func TestValidateEffectsEdit_OnlyWhatWasStored(t *testing.T) {
	stored := map[string]string{string(perm.FilesDelete): model.PermDeny, later: model.PermAllow}
	require.NoError(t, perm.ValidateEffectsEdit(map[string]string{later: model.PermAllow}, stored))
	require.ErrorIs(t, perm.ValidateEffectsEdit(map[string]string{"files.nobody_knows": model.PermAllow}, stored), perm.ErrInvalid)
	require.ErrorIs(t, perm.ValidateEffects(map[string]string{later: model.PermAllow}), perm.ErrInvalid, "without a stored map nothing unknown passes")

	merged := perm.KeepForeignEffects(perm.WithoutForeign(map[string]string{later: model.PermDeny, string(perm.FilesPurge): model.PermDeny}), stored)
	require.Equal(t, map[string]string{string(perm.FilesPurge): model.PermDeny, later: model.PermAllow}, merged)
	require.False(t, perm.Foreign(string(perm.FilesEncrypt)))
	require.False(t, perm.Foreign("app.sign.request"), "an app permission has rules of its own")
	require.True(t, perm.Foreign(later))
}
