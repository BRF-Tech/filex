package perm

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func TestCatalogueIs28(t *testing.T) {
	require.Len(t, All(), 28, "the agreed permission list is 28 entries; change this test on purpose")
	require.Equal(t, 28, allSet.Len())
}

func TestSetRoundTripsThroughStrings(t *testing.T) {
	s := Of(FilesDelete, AccessSFTP, AdminAudit)
	require.Equal(t, s, FromStrings(s.Strings()))
	require.Equal(t, []string{"files.delete", "access.sftp", "admin.audit"}, s.Strings(), "catalogue order")
}

func TestUnknownKeysAreIgnoredNotFatal(t *testing.T) {
	s := FromStrings([]string{"files.download", "files.teleport"})
	require.Equal(t, Of(FilesDownload), s)
	require.False(t, s.Has("files.teleport"))
	require.Equal(t, s, s.With("files.teleport"))
}

func TestPresets(t *testing.T) {
	require.False(t, Standard.Has(AdminUsers), "standard has no admin area")
	require.True(t, Standard.Has(FilesPurge))
	require.True(t, Standard.Has(AccessSFTP), "today's users can use every protocol")
	require.Equal(t, 22, Standard.Len())

	// ReadOnly is today's viewer: no file mutation, no sharing, the rest kept.
	for _, p := range []Perm{FilesCreate, FilesModify, FilesRename, FilesMove, FilesDelete, FilesPurge, ShareLinks, ShareUploadLinks, ShareUsers} {
		require.False(t, ReadOnly.Has(p), p)
	}
	for _, p := range []Perm{FilesDownload, FilesTag, CommentsWrite, AccessWebDAV, AccessAPI, AccessDesktop, AccountEdit} {
		require.True(t, ReadOnly.Has(p), p)
	}

	require.Equal(t, PresetStandard, MatchPreset(Standard))
	require.Equal(t, PresetGuest, MatchPreset(Of(FilesDownload)))
	require.Equal(t, "", MatchPreset(Of(FilesDownload, FilesTag)))
}

// rule is a role whose list is the Standard preset with these changes.
func rule(id int64, name string, targets []model.PermRuleTarget, changes map[string]string) *model.PermissionRule {
	set := Standard
	for k, eff := range changes {
		if eff == model.PermAllow {
			set = set.With(Perm(k))
		} else {
			set = set.Without(Perm(k))
		}
	}
	return &model.PermissionRule{ID: id, Name: name, Enabled: true, Targets: targets, Permissions: set.Strings()}
}

// everyone is "no targets": a person holds a role through Input.CustomRoleID.
var everyone []model.PermRuleTarget

func TestResolveUpgradeChangesNobody(t *testing.T) {
	// No rules, no overrides, defaults unset (Standard): each role gets
	// exactly what it had before permissions existed.
	u := Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard})
	require.Equal(t, Standard, u.Allowed)
	v := Resolve(Input{UserID: 2, Role: model.RoleViewer, Defaults: Standard})
	require.Equal(t, ReadOnly, v.Allowed)
	a := Resolve(Input{UserID: 3, Role: model.RoleAdmin})
	require.Equal(t, allSet, a.Allowed)
	require.Equal(t, Source{Kind: SourceBase}, u.Why(FilesDelete))
}

func TestResolvePrecedence(t *testing.T) {
	tests := []struct {
		name     string
		in       Input
		p        Perm
		want     bool
		wantKind SourceKind
		wantRule int64
	}{
		{
			name: "rule deny beats base",
			in:   Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 7, Rules: []*model.PermissionRule{rule(7, "Contractors", everyone, map[string]string{"files.delete": "deny"})}},
			p:    FilesDelete, want: false, wantKind: SourceRule, wantRule: 7,
		},
		{
			name: "rule allow adds to base",
			in:   Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 3, Rules: []*model.PermissionRule{rule(3, "Auditors", everyone, map[string]string{"admin.audit": "allow"})}},
			p:    AdminAudit, want: true, wantKind: SourceRule, wantRule: 3,
		},
		{
			name: "only the person's own role counts",
			in: Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 2, Rules: []*model.PermissionRule{
				rule(1, "Finance", everyone, map[string]string{"files.purge": "allow"}),
				rule(2, "Contractors", everyone, map[string]string{"files.purge": "deny"}),
			}},
			p: FilesPurge, want: false, wantKind: SourceRule, wantRule: 2,
		},
		{
			name: "override allow beats rule deny",
			in: Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 7,
				Rules:     []*model.PermissionRule{rule(7, "Contractors", everyone, map[string]string{"files.delete": "deny"})},
				Overrides: map[string]string{"files.delete": "allow"}},
			p: FilesDelete, want: true, wantKind: SourceOverride,
		},
		{
			name: "override deny beats base",
			in:   Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, Overrides: map[string]string{"share.links": "deny"}},
			p:    ShareLinks, want: false, wantKind: SourceOverride,
		},
		{
			name: "admin.full cannot be granted by override",
			in:   Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, Overrides: map[string]string{"admin.full": "allow"}},
			p:    AdminFull, want: false, wantKind: SourceRole,
		},
		{
			name: "admin.full cannot be granted by rule",
			in:   Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 1, Rules: []*model.PermissionRule{rule(1, "x", everyone, map[string]string{"admin.full": "allow"})}},
			p:    AdminFull, want: false, wantKind: SourceRole,
		},
		{
			name: "viewer ceiling beats override allow",
			in:   Input{UserID: 1, Role: model.RoleViewer, Overrides: map[string]string{"files.delete": "allow"}},
			p:    FilesDelete, want: false, wantKind: SourceViewerCeiling,
		},
		{
			name: "viewer can be given a non-capped admin permission (auditor)",
			in:   Input{UserID: 1, Role: model.RoleViewer, Overrides: map[string]string{"admin.audit": "allow"}},
			p:    AdminAudit, want: true, wantKind: SourceOverride,
		},
		{
			name: "admin is bound by no rule",
			in:   Input{UserID: 1, Role: model.RoleAdmin, CustomRoleID: 1, Rules: []*model.PermissionRule{rule(1, "x", everyone, map[string]string{"files.delete": "deny"})}},
			p:    FilesDelete, want: true, wantKind: SourceRole,
		},
		{
			name: "a switched-off role gives nothing — never the built-in role instead",
			in: Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 1, Rules: []*model.PermissionRule{
				{ID: 1, Name: "off", Enabled: false, Permissions: Standard.Without(FilesDelete).Strings()},
			}},
			p: FilesDelete, want: false, wantKind: SourceRoleOff, wantRule: 1,
		},
		{
			name: "a switched-off role's holder can still be given an exception",
			in: Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 1, Rules: []*model.PermissionRule{
				{ID: 1, Name: "off", Enabled: false, Permissions: Standard.Strings()},
			}, Overrides: map[string]string{"files.download": "allow"}},
			p: FilesDownload, want: true, wantKind: SourceOverride,
		},
		{
			name: "unknown role holds nothing",
			in:   Input{UserID: 1, Role: "wizard", Defaults: Standard},
			p:    FilesDownload, want: false, wantKind: SourceBase,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := Resolve(tc.in)
			require.Equal(t, tc.want, res.Can(tc.p))
			src := res.Why(tc.p)
			require.Equal(t, tc.wantKind, src.Kind)
			require.Equal(t, tc.wantRule, src.RuleID)
		})
	}
}

func TestRoleHolding(t *testing.T) {
	deny := map[string]string{"files.delete": "deny"}
	pid := func(v int64) *int64 { return &v }

	cases := []struct {
		name  string
		rule  *model.PermissionRule
		in    Input
		binds bool
		// outOfScope: the account holds the role, but the role is another
		// tenant's — it binds nothing and, like a switched-off role, gives
		// nothing (never the built-in role instead).
		outOfScope bool
	}{
		{"the person's role binds", rule(1, "r", nil, deny), Input{UserID: 42, CustomRoleID: 1}, true, false},
		{"another role does not", rule(1, "r", nil, deny), Input{UserID: 42, CustomRoleID: 2}, false, false},
		{"no role, nothing binds", rule(1, "r", nil, deny), Input{UserID: 42}, false, false},
		{"an SSO-group target alone binds nobody (it is a starting role)", rule(1, "r", []model.PermRuleTarget{{Kind: "sso_group", Value: "contractors"}}, deny), Input{UserID: 1}, false, false},
		{"tenant role binds its tenant", &model.PermissionRule{ID: 1, Enabled: true, ProviderID: pid(5), Effects: deny}, Input{UserID: 1, ProviderID: pid(5), CustomRoleID: 1}, true, false},
		{"tenant role skips other tenants", &model.PermissionRule{ID: 1, Enabled: true, ProviderID: pid(5), Effects: deny}, Input{UserID: 1, ProviderID: pid(6), CustomRoleID: 1}, false, true},
		{"tenant role skips tenantless users", &model.PermissionRule{ID: 1, Enabled: true, ProviderID: pid(5), Effects: deny}, Input{UserID: 1, CustomRoleID: 1}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Role = model.RoleUser
			tc.in.Defaults = Standard
			tc.in.Rules = []*model.PermissionRule{tc.rule}
			res := Resolve(tc.in)
			require.Equal(t, !tc.binds && !tc.outOfScope, res.Can(FilesDelete))
			require.Equal(t, tc.binds, len(res.Rules) == 1)
		})
	}
}

func TestStartingRole(t *testing.T) {
	pid := func(v int64) *int64 { return &v }
	group := func(g string) []model.PermRuleTarget { return []model.PermRuleTarget{{Kind: "sso_group", Value: g}} }
	off := &model.PermissionRule{ID: 1, Name: "off", Enabled: false, Targets: group("staff")}
	other := &model.PermissionRule{ID: 2, Name: "tenant 9", Enabled: true, ProviderID: pid(9), Targets: group("staff")}
	staff := &model.PermissionRule{ID: 3, Name: "Staff", Enabled: true, Targets: group("staff")}
	later := &model.PermissionRule{ID: 4, Name: "Later", Enabled: true, Targets: group("staff")}
	rules := []*model.PermissionRule{off, other, staff, later}

	require.Equal(t, staff, StartingRole(rules, []string{"x", "staff"}, nil), "lowest enabled role in scope")
	require.Equal(t, other, StartingRole(rules, []string{"staff"}, pid(9)), "a tenant's own role counts for its people")
	require.Nil(t, StartingRole(rules, []string{"Staff"}, nil), "groups compare exactly")
	require.Nil(t, StartingRole(rules, nil, nil))
}

func TestRoleSettingsAreCopied(t *testing.T) {
	d7 := 7
	b5 := int64(5)
	rules := []*model.PermissionRule{
		{ID: 2, Enabled: true, Settings: model.PermRuleSettings{ShareLinkMaxDays: &d7, MaxUploadBytes: &b5, Require2FA: true, BlockedExtensions: []string{"exe", "bat"}}},
	}
	res := Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 2, Rules: rules})
	require.Equal(t, 7, *res.Settings.ShareLinkMaxDays)
	require.Equal(t, int64(5), *res.Settings.MaxUploadBytes)
	require.True(t, res.Settings.Require2FA)
	require.False(t, res.Settings.ShareLinkPasswordRequired)
	require.Equal(t, []string{"exe", "bat"}, res.Settings.BlockedExtensions)
	// The result must not alias the role's own values.
	*res.Settings.ShareLinkMaxDays = 1
	require.Equal(t, 7, *rules[0].Settings.ShareLinkMaxDays)
}

func TestValidateEffects(t *testing.T) {
	require.NoError(t, ValidateEffects(map[string]string{"files.delete": "deny", "admin.audit": "allow"}))
	for _, bad := range []map[string]string{
		{"files.teleport": "allow"},
		{"admin.full": "allow"},
		{"files.delete": "maybe"},
		{"files.delete": ""},
	} {
		err := ValidateEffects(bad)
		require.Error(t, err, bad)
		require.True(t, errors.Is(err, ErrInvalid))
	}
}

func TestNormalizeRule(t *testing.T) {
	r := &model.PermissionRule{
		Name: "  Contractors ",
		Targets: []model.PermRuleTarget{
			{Kind: "sso_group", Value: " contractors "},
			{Kind: "sso_group", Value: "contractors"},
		},
		Permissions: []string{"files.delete", "files.download", "files.download"},
		Settings:    model.PermRuleSettings{BlockedExtensions: []string{".EXE", "exe", " bat ", ""}},
	}
	require.NoError(t, NormalizeRule(r))
	require.Equal(t, "Contractors", r.Name)
	require.Equal(t, []string{"files.download", "files.delete"}, r.Permissions, "deduplicated, in catalogue order")
	require.Equal(t, []model.PermRuleTarget{{Kind: "sso_group", Value: "contractors"}}, r.Targets)
	require.Equal(t, []string{"exe", "bat"}, r.Settings.BlockedExtensions)

	zero := 0
	nothing := &model.PermissionRule{Name: "No access"}
	require.NoError(t, NormalizeRule(nothing), "a role that may do nothing is a role")
	require.Equal(t, []string{}, nothing.Permissions)

	for name, bad := range map[string]*model.PermissionRule{
		"no name":         {Permissions: []string{"files.download"}},
		"person target":   {Name: "x", Targets: []model.PermRuleTarget{{Kind: "user", Value: "7"}}, Effects: map[string]string{"files.delete": "deny"}},
		"everyone":        {Name: "x", Targets: []model.PermRuleTarget{{Kind: "everyone"}}, Effects: map[string]string{"files.delete": "deny"}},
		"unknown perm":    {Name: "x", Permissions: []string{"files.teleport"}},
		"role-only list":  {Name: "x", Permissions: []string{"admin.full"}},
		"allow w/o place": {Name: "x", Effects: map[string]string{"files.delete": "deny"}},
		"empty group":     {Name: "x", Targets: []model.PermRuleTarget{{Kind: "sso_group"}}, Effects: map[string]string{"files.delete": "deny"}},
		"bad kind":        {Name: "x", Targets: []model.PermRuleTarget{{Kind: "planet"}}, Effects: map[string]string{"files.delete": "deny"}},
		"zero days":       {Name: "x", Targets: everyone, Settings: model.PermRuleSettings{ShareLinkMaxDays: &zero}},
		"path extension":  {Name: "x", Targets: everyone, Settings: model.PermRuleSettings{BlockedExtensions: []string{"a/b"}}},
		"role-only":       {Name: "x", Targets: everyone, Effects: map[string]string{"admin.full": "allow"}},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, NormalizeRule(bad), ErrInvalid)
		})
	}
}

func TestRoleWithNoMembersBindsNobody(t *testing.T) {
	r := &model.PermissionRule{Name: "Contractor", Enabled: true, Permissions: []string{"files.download"}}
	require.NoError(t, NormalizeRule(r), "a role may be created before anybody holds it")
	res := Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, Rules: []*model.PermissionRule{r}})
	require.True(t, res.Can(FilesDelete))
}

func TestStandAloneRole(t *testing.T) {
	// The role's list IS what its holder may do — not the User role changed.
	r := &model.PermissionRule{ID: 4, Name: "Uploader", Enabled: true, Permissions: []string{"files.create", "account.edit"}}
	res := Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 4, Rules: []*model.PermissionRule{r}})
	require.Equal(t, Of(FilesCreate, AccountEdit), res.Allowed)
	require.Equal(t, Source{Kind: SourceRule, RuleID: 4, RuleName: "Uploader"}, res.Why(FilesDownload), "a permission the role leaves off is the role's answer too")

	// An exception still beats it.
	res = Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 4, Rules: []*model.PermissionRule{r},
		Overrides: map[string]string{"files.download": "allow"}})
	require.True(t, res.Can(FilesDownload))

	// Different in some folders: delete, but only in Scratch.
	r.Conditions = model.PermRuleConditions{Paths: []string{"Scratch"}}
	r.Effects = map[string]string{"files.delete": "allow"}
	res = Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 4, Rules: []*model.PermissionRule{r}})
	require.False(t, res.Can(FilesDelete))
	require.True(t, res.CanAt(1, "Scratch/a", FilesDelete))
	require.False(t, res.CanAt(1, "Docs/a", FilesDelete))
	require.Equal(t, Of(FilesDelete), res.AllowedInFolders(), "offered in the web app; decided per path")
	require.Equal(t, Of(FilesDelete), res.VariesByFolder())

	// A folder that denies what the role allows everywhere varies too.
	r.Effects = map[string]string{"files.delete": "allow", "files.create": "deny"}
	res = Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 4, Rules: []*model.PermissionRule{r}})
	require.Equal(t, Of(FilesDelete, FilesCreate), res.VariesByFolder())
	require.Equal(t, Of(FilesDelete), res.AllowedInFolders())
	r.Effects = map[string]string{"files.delete": "allow"}

	// An exception denying it, or a viewer, takes it off the list.
	res = Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 4, Rules: []*model.PermissionRule{r},
		Overrides: map[string]string{"files.delete": "deny"}})
	require.Zero(t, res.AllowedInFolders())
	require.Zero(t, res.VariesByFolder(), "an exception fixes the answer everywhere")
	res = Resolve(Input{UserID: 1, Role: model.RoleViewer, CustomRoleID: 4, Rules: []*model.PermissionRule{r}})
	require.Zero(t, res.AllowedInFolders())
}

func TestRoleSetIsTheRolesOwnList(t *testing.T) {
	require.Zero(t, RoleSet(nil))
	require.Zero(t, RoleSet(&model.PermissionRule{}), "a role without a list may do nothing")
	r := &model.PermissionRule{Permissions: []string{"files.download", "admin.full"}, Effects: map[string]string{"files.delete": "deny"},
		Conditions: model.PermRuleConditions{Paths: []string{"Archive"}}}
	require.Equal(t, Of(FilesDownload), RoleSet(r), "admin.full is never a role's; a folder part does not change the list")
	require.Equal(t, model.RoleViewer, RoleHolder(r))
}

func TestHolderRole(t *testing.T) {
	require.Equal(t, model.RoleUser, HolderRole(nil, Of(FilesDownload, FilesDelete)), "a role that can delete needs the User level")
	require.Equal(t, model.RoleViewer, HolderRole(nil, Of(FilesDownload, AdminAudit)), "a read-only role stays read-only")
	scratch := &model.PermissionRule{Effects: map[string]string{"files.create": "allow"}, Conditions: model.PermRuleConditions{Paths: []string{"Drop"}}}
	require.Equal(t, model.RoleUser, HolderRole(scratch, Of(FilesDownload)), "writing in some folders needs it too")
}

// The role editor's answer (POST /api/admin/roles/preview) is RoleHolder on
// the body as a save would store it, for a body that is not valid yet too.
func TestPreviewHolderRole(t *testing.T) {
	require.Equal(t, model.RoleViewer, PreviewHolderRole(model.PermissionRule{}), "no name, no list: may do nothing")
	require.Equal(t, model.RoleUser, PreviewHolderRole(model.PermissionRule{Permissions: []string{"files.download", "files.delete"}}))
	require.Equal(t, model.RoleViewer, PreviewHolderRole(model.PermissionRule{Permissions: []string{"files.download", "no.such"}}), "an unknown key is nothing")
	scratch := model.PermissionRule{Permissions: []string{"files.download"}, Effects: map[string]string{"files.create": "allow"},
		Conditions: model.PermRuleConditions{Paths: []string{"Drop"}}}
	require.Equal(t, RoleHolder(&scratch), PreviewHolderRole(scratch))
	require.Equal(t, model.RoleUser, PreviewHolderRole(scratch), "writing in some folders needs the User level")
	deny := scratch
	deny.Effects = map[string]string{"files.create": "deny"}
	require.Equal(t, model.RoleViewer, PreviewHolderRole(deny), "a Deny in some folders asks nothing of the level")

	// A path a save would drop names no folder, so the folder part is not
	// there: what the stored role would be, not what the raw body says.
	blank := scratch
	blank.Conditions = model.PermRuleConditions{Paths: []string{"  ", "/"}}
	require.Equal(t, model.RoleUser, RoleHolder(&blank), "the raw body looks place-scoped")
	require.Equal(t, model.RoleViewer, PreviewHolderRole(blank))
	require.Equal(t, []string{"  ", "/"}, blank.Conditions.Paths, "the caller's body is left as it was")
}

func TestViewerBaseIsEditableButCapped(t *testing.T) {
	custom := Of(FilesDownload, FilesDelete) // delete is beyond a viewer
	res := Resolve(Input{UserID: 1, Role: model.RoleViewer, ViewerBase: &custom})
	require.True(t, res.Can(FilesDownload))
	require.False(t, res.Can(FilesDelete), "the viewer ceiling holds whatever the base says")
	require.False(t, res.Can(CommentsWrite), "the edited base replaces Read-only")
}

func TestRelocateNeeds(t *testing.T) {
	cases := []struct {
		src, dst string
		cross    bool
		want     []Perm
	}{
		{"docs/a.txt", "docs/b.txt", false, []Perm{FilesRename}},
		{"docs/a.txt", "archive/a.txt", false, []Perm{FilesMove}},
		{"docs/a.txt", "archive/b.txt", false, []Perm{FilesMove, FilesRename}},
		{"a.txt", "sub/a.txt", false, []Perm{FilesMove}},
		{"/docs/a.txt/", "docs/a.txt", false, []Perm{FilesRename}}, // unchanged: still checked
		{"docs/a.txt", "docs/a.txt", true, []Perm{FilesMove}},      // another storage
	}
	for _, c := range cases {
		dst := int64(1)
		if c.cross {
			dst = 2
		}
		require.Equal(t, c.want, RelocateNeeds(1, c.src, dst, c.dst), "%s → %s", c.src, c.dst)
	}
}

// A custom role the account holds but whose tenant is not the account's (a
// supertenant administrator gave it across tenants, or the account was moved
// to another tenant afterwards) gives nothing — like a switched-off role —
// never the built-in role instead: for a role that takes things away, the
// built-in User role is MORE access (PR #75 review).
func TestHeldRoleOutsideTheAccountsTenantGivesNothing(t *testing.T) {
	tenantA, tenantB := int64(7), int64(9)
	rule := &model.PermissionRule{ID: 3, Name: "No delete", Enabled: true, ProviderID: &tenantA,
		Permissions: Standard.Without(FilesDelete).Strings()}
	in := Input{UserID: 1, Role: model.RoleUser, ProviderID: &tenantB, Defaults: Standard,
		CustomRoleID: rule.ID, Rules: []*model.PermissionRule{rule}}
	res := Resolve(in)
	require.False(t, res.Can(FilesDelete), "the held role took delete away; its tenant does not change that")
	require.False(t, res.Can(FilesDownload), "an out-of-scope role gives nothing, like a switched-off one")
	require.Equal(t, SourceRoleOff, res.Why(FilesDownload).Kind)

	in.ProviderID = &tenantA
	res = Resolve(in)
	require.True(t, res.Can(FilesDownload), "in its own tenant the role is the role")
	require.False(t, res.Can(FilesDelete))
}
