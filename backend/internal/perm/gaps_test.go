package perm_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

func gapIDs(gs []perm.Gap) []string {
	out := []string{}
	for _, g := range gs {
		out = append(out, g.ID)
	}
	return out
}

func idOf(id int64) string { return strconv.FormatInt(id, 10) }

// The case PR #86 reported (Berk Başarır). 0.51.0 gives the saved User role
// files.encrypt once; on the way back, 0.50's page writes the list back
// without the key it does not know; 0.51 again does not merge it a second
// time. The list is now a gap: shown, told once, and given back with one
// click - never by itself.
func TestGaps_TheUserRoleA050SaveLeftBehind(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, perm.SaveDefaults(ctx, store, v049Standard().Without(perm.AccessSFTP)))
	_, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.Contains(t, storedRaw(t, store, model.SettingPermissionDefaults), string(perm.FilesEncrypt), "0.51.0's merge")

	// 0.50: GET drops what it does not know, PUT writes the rest back.
	// A later version's key is in the list too, to see that nothing here
	// takes it away.
	as050 := append(v049Standard().Without(perm.AccessSFTP).Without(perm.AccessFTP).Strings(), later)
	storeRaw(t, store, model.SettingPermissionDefaults, as050)

	rep, err := perm.UpgradeCatalogue(ctx, store)
	require.NoError(t, err)
	require.False(t, rep.DefaultsChanged, "merged once, at the first upgrade: not given back by itself")

	open, err := perm.OpenGaps(ctx, store)
	require.NoError(t, err)
	require.Equal(t, []perm.Gap{{
		ID: "builtin:user:files.encrypt", Key: perm.FilesEncrypt, From: perm.FilesCreate, Role: model.RoleUser,
	}}, open)

	fresh, all, err := perm.AnnounceGaps(ctx, store)
	require.NoError(t, err)
	require.Equal(t, []string{"builtin:user:files.encrypt"}, gapIDs(fresh), "told once")
	require.Len(t, all, 1)
	fresh, _, err = perm.AnnounceGaps(ctx, store)
	require.NoError(t, err)
	require.Empty(t, fresh, "not at every start")

	before, after, err := perm.RestoreBuiltinGap(ctx, store, open[0])
	require.NoError(t, err)
	require.Equal(t, as050, before)
	require.Equal(t, append(append([]string(nil), as050...), string(perm.FilesEncrypt)), after, "the key added, nothing else touched")
	defaults, err := perm.LoadDefaults(ctx, store)
	require.NoError(t, err)
	require.Equal(t, v049Standard().Without(perm.AccessSFTP).Without(perm.AccessFTP).With(perm.FilesEncrypt), defaults)

	open, err = perm.OpenGaps(ctx, store)
	require.NoError(t, err)
	require.Empty(t, open)
}

// A User role never saved reads its preset, which holds files.encrypt; a list
// that does not allow adding files has nothing to lack; a Viewer cannot hold
// files.encrypt at all.
func TestGaps_NoneWhereNothingCanBeMissing(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	open, err := perm.OpenGaps(ctx, store)
	require.NoError(t, err)
	require.Empty(t, open, "nothing saved")

	storeRaw(t, store, model.SettingPermissionDefaults, []string{string(perm.FilesDownload), string(perm.AccountEdit)})
	storeRaw(t, store, model.SettingPermissionViewerDefaults, perm.ReadOnly.Strings())
	open, err = perm.OpenGaps(ctx, store)
	require.NoError(t, err)
	require.Empty(t, open)
}

// A custom role's folder part as 0.50's editor leaves it: Allow of
// files.create in some folders, files.encrypt gone, the role's own list
// without it. The other shapes are not gaps.
func TestGaps_ACustomRolesFolderPart(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	drop := mustRole(t, store, &model.PermissionRule{
		Name: "Drop box", Enabled: true, Permissions: []string{"files.download"},
		Effects:    map[string]string{"files.create": model.PermAllow},
		Conditions: model.PermRuleConditions{Paths: []string{"Drop"}},
	})
	mustRole(t, store, &model.PermissionRule{
		Name: "Own list holds it", Enabled: true, Permissions: []string{"files.download", "files.encrypt"},
		Effects:    map[string]string{"files.create": model.PermAllow},
		Conditions: model.PermRuleConditions{Paths: []string{"Drop"}},
	})
	mustRole(t, store, &model.PermissionRule{
		Name: "Decided", Enabled: true, Permissions: []string{"files.download"},
		Effects:    map[string]string{"files.create": model.PermAllow, "files.encrypt": model.PermDeny},
		Conditions: model.PermRuleConditions{Paths: []string{"Clients"}},
	})
	mustRole(t, store, &model.PermissionRule{
		Name: "May add, may not encrypt", Enabled: true, Permissions: []string{"files.download", "files.create"},
	})

	open, err := perm.OpenGaps(ctx, store)
	require.NoError(t, err)
	require.Len(t, open, 1, "only the folder part that lost it; a role's own list is not looked at")
	require.Equal(t, perm.Gap{
		ID: "role:" + idOf(drop.ID) + ":files.encrypt", Key: perm.FilesEncrypt, From: perm.FilesCreate,
		RuleID: drop.ID, RuleName: "Drop box",
	}, open[0])
}

// "It was on purpose": not shown and not told again while it stays that way.
// Closed and opened again (a later rollback), it is shown again.
func TestGaps_ADismissalHoldsWhileTheGapLasts(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	storeRaw(t, store, model.SettingPermissionDefaults, v049Standard().Strings())
	g, ok, err := perm.FindGap(ctx, store, "builtin:user:files.encrypt")
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, perm.DismissGap(ctx, store, g.ID))
	open, err := perm.OpenGaps(ctx, store)
	require.NoError(t, err)
	require.Empty(t, open)
	fresh, _, err := perm.AnnounceGaps(ctx, store)
	require.NoError(t, err)
	require.Empty(t, fresh)
	_, ok, err = perm.FindGap(ctx, store, g.ID)
	require.NoError(t, err)
	require.True(t, ok, "a dismissed gap can still be given back")

	// Closed by a save that gives the key back, then opened the other way.
	storeRaw(t, store, model.SettingPermissionDefaults, perm.Standard.Strings())
	fresh, open, err = perm.AnnounceGaps(ctx, store)
	require.NoError(t, err)
	require.Empty(t, fresh)
	require.Empty(t, open)
	storeRaw(t, store, model.SettingPermissionDefaults, v049Standard().Strings())
	fresh, _, err = perm.AnnounceGaps(ctx, store)
	require.NoError(t, err)
	require.Equal(t, []string{g.ID}, gapIDs(fresh), "a dismissal is for the gap it was given to")
}

// A save on this version from an editor that showed the key is a decision;
// one that did not show it (an API client) is pointed out like a rollback.
func TestGaps_ASaveThatShowedTheKeyIsADecision(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	storeRaw(t, store, model.SettingPermissionDefaults, perm.Standard.Strings())
	before, err := perm.BuiltinRoleGaps(ctx, store, model.RoleUser)
	require.NoError(t, err)
	require.Empty(t, before)

	require.NoError(t, perm.SaveDefaults(ctx, store, perm.Standard.Without(perm.FilesEncrypt)))
	after, err := perm.BuiltinRoleGaps(ctx, store, model.RoleUser)
	require.NoError(t, err)
	require.NoError(t, perm.NoteGapsSaved(ctx, store, before, after, nil))
	open, err := perm.OpenGaps(ctx, store)
	require.NoError(t, err)
	require.Len(t, open, 1, "a client that did not show the key")

	// Given back, then taken away again from the editor.
	_, _, err = perm.RestoreBuiltinGap(ctx, store, open[0])
	require.NoError(t, err)
	require.NoError(t, perm.NoteGapsSaved(ctx, store, open, nil, nil))
	require.NoError(t, perm.SaveDefaults(ctx, store, perm.Standard.Without(perm.FilesEncrypt)))
	after, err = perm.BuiltinRoleGaps(ctx, store, model.RoleUser)
	require.NoError(t, err)
	require.NoError(t, perm.NoteGapsSaved(ctx, store, nil, after, catalogueKeys()))
	open, err = perm.OpenGaps(ctx, store)
	require.NoError(t, err)
	require.Empty(t, open, "the administrator saw Encrypt and left it off")
}
