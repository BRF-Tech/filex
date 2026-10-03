package perm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func TestEffectiveRole(t *testing.T) {
	id := func(n int64) *int64 { return &n }
	tenantA, tenantB := id(10), id(20)
	on := func(rid int64, provider *int64) *model.PermissionRule {
		return &model.PermissionRule{ID: rid, Name: "r", Enabled: true, ProviderID: provider}
	}
	rules := []*model.PermissionRule{on(5, nil), on(6, nil), on(7, tenantB), on(9, nil)}
	noRole := &model.Group{ID: 1, Name: "No role"}
	foreign := &model.Group{ID: 2, Name: "Other tenant", RoleID: id(7), ProviderID: tenantB}
	first := &model.Group{ID: 3, Name: "First", RoleID: id(5), ProviderID: tenantA}
	second := &model.Group{ID: 4, Name: "Second", RoleID: id(6)}
	groups := []*model.Group{noRole, foreign, first, second}

	role, via := EffectiveRole(9, groups, rules, tenantA)
	assert.EqualValues(t, 9, role, "their own role wins")
	assert.Nil(t, via)

	role, via = EffectiveRole(0, groups, rules, tenantA)
	assert.EqualValues(t, 5, role, "equal priority: the lowest id in their tenant that holds a role")
	assert.Same(t, first, via)

	role, via = EffectiveRole(0, groups, rules, tenantB)
	assert.EqualValues(t, 7, role, "their own tenant's group, not tenant A's")
	assert.Same(t, foreign, via)

	role, via = EffectiveRole(0, []*model.Group{first, second}, rules, tenantB)
	assert.EqualValues(t, 6, role, "a tenant's group reaches only its own people; the install-wide one does")
	assert.Same(t, second, via)

	role, via = EffectiveRole(0, []*model.Group{noRole}, rules, nil)
	assert.Zero(t, role)
	assert.Nil(t, via)
}

func TestEffectiveRole_PriorityAndSwitchedOff(t *testing.T) {
	id := func(n int64) *int64 { return &n }
	rules := []*model.PermissionRule{
		{ID: 5, Name: "Five", Enabled: true},
		{ID: 6, Name: "Six", Enabled: true},
		{ID: 7, Name: "Off", Enabled: false},
		{ID: 8, Name: "Also off", Enabled: false},
	}
	low := &model.Group{ID: 1, RoleID: id(5), Priority: 0}
	high := &model.Group{ID: 2, RoleID: id(6), Priority: 10}
	role, via := EffectiveRole(0, []*model.Group{low, high}, rules, nil)
	assert.EqualValues(t, 6, role, "the higher priority wins over the lower id")
	assert.Same(t, high, via)

	off := &model.Group{ID: 3, RoleID: id(7), Priority: 50}
	role, via = EffectiveRole(0, []*model.Group{low, high, off}, rules, nil)
	assert.EqualValues(t, 6, role, "a switched-off role is skipped while another group's is on")
	assert.Same(t, high, via)

	alsoOff := &model.Group{ID: 4, RoleID: id(8), Priority: 20}
	role, via = EffectiveRole(0, []*model.Group{off, alsoOff}, rules, nil)
	assert.EqualValues(t, 7, role, "all switched off: the first — which gives nothing — never the built-in role")
	assert.Same(t, off, via)

	gone := &model.Group{ID: 5, RoleID: id(99), Priority: 100}
	role, _ = EffectiveRole(0, []*model.Group{gone, low}, rules, nil)
	assert.EqualValues(t, 5, role, "a role that no longer exists is no role")
}

func TestResolveNamesTheGroup(t *testing.T) {
	rule := &model.PermissionRule{ID: 5, Name: "No delete", Enabled: true, Permissions: Standard.Without(FilesDelete).Strings()}
	res := Resolve(Input{
		UserID: 1, Role: model.RoleUser, Defaults: Standard,
		CustomRoleID: 5, ViaGroup: &GroupRef{ID: 3, Name: "Contractors"},
		Rules: []*model.PermissionRule{rule},
	})
	require.False(t, res.Can(FilesDelete))
	src := res.Why(FilesDelete)
	assert.Equal(t, SourceRule, src.Kind)
	assert.Equal(t, "Contractors", src.GroupName)
	assert.EqualValues(t, 3, src.GroupID)

	// An exception is the person's own: no group on it.
	res = Resolve(Input{
		UserID: 1, Role: model.RoleUser, Defaults: Standard,
		CustomRoleID: 5, ViaGroup: &GroupRef{ID: 3, Name: "Contractors"},
		Rules: []*model.PermissionRule{rule}, Overrides: map[string]string{"files.delete": model.PermDeny},
	})
	assert.Equal(t, SourceOverride, res.Why(FilesDelete).Kind)
	assert.Empty(t, res.Why(FilesDelete).GroupName)

	// A switched-off role through a group gives nothing, and says so.
	off := *rule
	off.Enabled = false
	res = Resolve(Input{
		UserID: 1, Role: model.RoleUser, Defaults: Standard,
		CustomRoleID: 5, ViaGroup: &GroupRef{ID: 3, Name: "Contractors"},
		Rules: []*model.PermissionRule{&off},
	})
	assert.False(t, res.Can(FilesDownload))
	assert.Equal(t, SourceRoleOff, res.Why(FilesDownload).Kind)
	assert.Equal(t, "Contractors", res.Why(FilesDownload).GroupName)
}
