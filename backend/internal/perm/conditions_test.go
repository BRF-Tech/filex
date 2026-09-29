package perm

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func TestPathPatternMatch(t *testing.T) {
	cases := []struct {
		pattern, rel string
		want         bool
	}{
		{"Archive", "Archive", true},
		{"Archive", "Archive/2024/q1.pdf", true},
		{"Archive", "Archived/x", false},
		{"Archive/**", "Archive/2024/q1.pdf", true},
		{"Archive/**", "Archive", true},
		{"Clients/*/Contracts", "Clients/acme/Contracts/nda.pdf", true},
		{"Clients/*/Contracts", "Clients/acme/Invoices/1.pdf", false},
		{"*.psd", "cover.psd", true},
		{"*.psd", "art/cover.psd", false},
		{"**/*.psd", "art/2024/cover.psd", true},
		{"**/*.psd", "cover.psd", true},
		{"**/*.psd", "cover.png", false},
		{"HR", "hr/x", false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, pathPatternMatch(c.pattern, c.rel), "%q ~ %q", c.pattern, c.rel)
	}
}

func TestConditionedRuleAppliesOnlyWhereItMatches(t *testing.T) {
	archive := &model.PermissionRule{
		ID: 5, Name: "Archive is read-only", Enabled: true,
		Targets:     everyone,
		Permissions: Standard.Strings(),
		Effects:     map[string]string{"files.delete": "deny", "files.modify": "deny"},
		Conditions:  model.PermRuleConditions{StorageIDs: []int64{1}, Paths: []string{"Archive/**"}},
	}
	res := Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 5, Rules: []*model.PermissionRule{archive}})

	require.True(t, res.Can(FilesDelete), "account-wide, a folder part applies nowhere")
	require.Equal(t, []int64{5}, res.Rules, "the role itself is held")
	require.Equal(t, []int64{5}, res.ConditionalRules())

	require.False(t, res.CanAt(1, "Archive/2024/q1.pdf", FilesDelete))
	require.Equal(t, Source{Kind: SourceRule, RuleID: 5, RuleName: "Archive is read-only"}, res.WhyAt(1, "Archive/x", FilesDelete))
	require.True(t, res.CanAt(1, "Projects/a.txt", FilesDelete), "outside the path")
	require.True(t, res.CanAt(2, "Archive/x", FilesDelete), "another storage")
	require.True(t, res.CanAt(1, "Archive/x", FilesCreate), "a permission the rule does not name")
}

func TestConditionedAllowAndOverride(t *testing.T) {
	// Deleting is off for the account, but allowed in the Scratch folder.
	scratch := &model.PermissionRule{
		ID: 1, Name: "Scratch", Enabled: true, Targets: everyone,
		Permissions: Standard.Without(FilesDelete).Strings(),
		Effects:     map[string]string{"files.delete": "allow"},
		Conditions:  model.PermRuleConditions{Paths: []string{"Scratch"}},
	}
	in := Input{UserID: 1, Role: model.RoleUser, Defaults: Standard.Without(FilesDelete), CustomRoleID: 1, Rules: []*model.PermissionRule{scratch}}
	res := Resolve(in)
	require.False(t, res.CanAt(9, "Docs/a", FilesDelete))
	require.True(t, res.CanAt(9, "Scratch/a", FilesDelete))

	// The user's own exception beats the role.
	in.Overrides = map[string]string{"files.delete": "allow"}
	require.True(t, Resolve(in).CanAt(9, "Docs/a", FilesDelete))
}

func TestConditionedRuleCannotTouchAViewerCeilingOrAnAdmin(t *testing.T) {
	allowDelete := &model.PermissionRule{
		ID: 1, Name: "x", Enabled: true, Targets: everyone,
		Effects:    map[string]string{"files.delete": "allow"},
		Conditions: model.PermRuleConditions{Paths: []string{"Tmp"}},
	}
	viewer := Resolve(Input{UserID: 1, Role: model.RoleViewer, CustomRoleID: 1, Rules: []*model.PermissionRule{allowDelete}})
	require.False(t, viewer.CanAt(1, "Tmp/a", FilesDelete))
	denyDelete := &model.PermissionRule{
		ID: 2, Name: "y", Enabled: true, Targets: everyone,
		Effects:    map[string]string{"files.delete": "deny"},
		Conditions: model.PermRuleConditions{Paths: []string{"Tmp"}},
	}
	admin := Resolve(Input{UserID: 2, Role: model.RoleAdmin, CustomRoleID: 2, Rules: []*model.PermissionRule{denyDelete}})
	require.True(t, admin.CanAt(1, "Tmp/a", FilesDelete))
}

func TestNormalizeConditions(t *testing.T) {
	r := &model.PermissionRule{
		Name: "x", Targets: everyone, Effects: map[string]string{"files.delete": "deny"},
		Conditions: model.PermRuleConditions{StorageIDs: []int64{3, 3}, Paths: []string{` \Archive\2024\ `, "Archive/2024", ""}},
	}
	require.NoError(t, NormalizeRule(r))
	require.Equal(t, []int64{3}, r.Conditions.StorageIDs)
	require.Equal(t, []string{"Archive/2024"}, r.Conditions.Paths)

	for name, bad := range map[string]*model.PermissionRule{
		"non-file permission": {Name: "x", Targets: everyone, Effects: map[string]string{"access.sftp": "deny"}, Conditions: model.PermRuleConditions{Paths: []string{"A"}}},
		"dotdot":              {Name: "x", Targets: everyone, Effects: map[string]string{"files.delete": "deny"}, Conditions: model.PermRuleConditions{Paths: []string{"A/../B"}}},
		"bad pattern":         {Name: "x", Targets: everyone, Effects: map[string]string{"files.delete": "deny"}, Conditions: model.PermRuleConditions{Paths: []string{"A/[x"}}},
		"bad storage":         {Name: "x", Targets: everyone, Effects: map[string]string{"files.delete": "deny"}, Conditions: model.PermRuleConditions{StorageIDs: []int64{0}}},
	} {
		t.Run(name, func(t *testing.T) { require.ErrorIs(t, NormalizeRule(bad), ErrInvalid) })
	}
}

// A person's own exception beats their role INSIDE the role's folders too,
// not only outside them (PR #75 review: a mutation that let the folder part
// win over the exception passed every test — the only exception case was
// asserted on a path the folder part does not match).
func TestExceptionBeatsTheRolesFolderPart(t *testing.T) {
	scratch := &model.PermissionRule{
		ID: 1, Name: "Scratch", Enabled: true, Targets: everyone,
		Permissions: Standard.Without(FilesDelete).Strings(),
		Effects:     map[string]string{"files.delete": "allow"},
		Conditions:  model.PermRuleConditions{Paths: []string{"Scratch"}},
	}
	in := Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 1, Rules: []*model.PermissionRule{scratch}}
	in.Overrides = map[string]string{"files.delete": "deny"}
	res := Resolve(in)
	require.False(t, res.CanAt(9, "Scratch/a", FilesDelete), "their own Deny beats the role's folder Allow")
	require.Equal(t, SourceOverride, res.WhyAt(9, "Scratch/a", FilesDelete).Kind)

	archive := &model.PermissionRule{
		ID: 2, Name: "Archive is read-only", Enabled: true, Targets: everyone,
		Permissions: Standard.Strings(),
		Effects:     map[string]string{"files.delete": "deny"},
		Conditions:  model.PermRuleConditions{Paths: []string{"Archive"}},
	}
	in = Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 2, Rules: []*model.PermissionRule{archive},
		Overrides: map[string]string{"files.delete": "allow"}}
	res = Resolve(in)
	require.True(t, res.CanAt(9, "Archive/2024/a.pdf", FilesDelete), "their own Allow beats the role's folder Deny")
	require.Equal(t, SourceOverride, res.WhyAt(9, "Archive/2024/a.pdf", FilesDelete).Kind)
}
