package perm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// A custom role in other languages (migration 00071): what NormalizeRule
// keeps of the translations, and who reads which name.

func TestRoleNames_Normalized(t *testing.T) {
	r := &model.PermissionRule{
		Name:         "Accounting",
		Names:        map[string]string{"TR": "  Muhasebe ", "en": "   "},
		Descriptions: map[string]string{"tr_TR": "", "tr": " Faturalar ve ödemeler "},
	}
	require.NoError(t, NormalizeRule(r))
	require.Equal(t, map[string]string{"tr": "Muhasebe"}, r.Names, "trimmed, keyed lower-case, blank dropped")
	require.Equal(t, map[string]string{"tr": "Faturalar ve ödemeler"}, r.Descriptions, "a blank entry is dropped before its language is judged")

	none := &model.PermissionRule{Name: "x", Names: map[string]string{"tr": " "}, Descriptions: map[string]string{}}
	require.NoError(t, NormalizeRule(none))
	require.Nil(t, none.Names, "nothing left is no translations")
	require.Nil(t, none.Descriptions)
}

func TestRoleNames_Refused(t *testing.T) {
	long := strings.Repeat("ş", MaxRuleNameLen+1)
	for name, bad := range map[string]*model.PermissionRule{
		"unknown language":        {Name: "x", Names: map[string]string{"xx": "X"}},
		"a region nobody offers":  {Name: "x", Names: map[string]string{"tr-tr": "X"}},
		"no language at all":      {Name: "x", Names: map[string]string{"": "X"}},
		"unknown description":     {Name: "x", Descriptions: map[string]string{"de": "Buchhaltung"}},
		"name longer than a name": {Name: "x", Names: map[string]string{"tr": long}},
		"language given twice":    {Name: "x", Names: map[string]string{"tr": "A", "TR": "B"}},
		"description too long":    {Name: "x", Descriptions: map[string]string{"tr": strings.Repeat("a", MaxRuleDescriptionLen+1)}},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, NormalizeRule(bad), ErrInvalid)
		})
	}
	// Exactly the limit is fine, counted in characters, not bytes.
	ok := &model.PermissionRule{Name: "x", Names: map[string]string{"tr": strings.Repeat("ş", MaxRuleNameLen)}}
	require.NoError(t, NormalizeRule(ok))
}

func TestRoleNames_PackLanguages(t *testing.T) {
	srvtext.SetPacks(srvtext.StaticPacks{"es": {"server.mail.greeting": "Hola:"}})
	t.Cleanup(func() { srvtext.SetPacks(nil) })

	r := &model.PermissionRule{Name: "Accounting", Names: map[string]string{"es": "Contabilidad"}}
	require.NoError(t, NormalizeRule(r), "a running language pack's language is a language")

	// The pack goes. A new role cannot name its language any more — but the
	// role that already carries it can still be saved, entry and all.
	srvtext.SetPacks(nil)
	require.ErrorIs(t, NormalizeRule(&model.PermissionRule{Name: "y", Names: map[string]string{"es": "Y"}}), ErrInvalid)
	edit := &model.PermissionRule{Name: "Accounting (EU)", Names: map[string]string{"es": "Contabilidad (UE)", "tr": "Muhasebe"}}
	require.NoError(t, NormalizeRuleEdit(edit, r))
	require.Equal(t, "Contabilidad (UE)", edit.Names["es"])
	require.ErrorIs(t, NormalizeRuleEdit(&model.PermissionRule{Name: "z", Names: map[string]string{"fr": "Z"}}, r), ErrInvalid,
		"only the languages the role already had are kept")
}

func TestRoleNames_NameFor(t *testing.T) {
	r := &model.PermissionRule{
		Name: "Accounting", Description: "Invoices",
		Names:        map[string]string{"tr": "Muhasebe", "pt": "Contabilidade"},
		Descriptions: map[string]string{"tr": "Faturalar"},
	}
	for lang, want := range map[string]string{
		"tr": "Muhasebe", "TR": "Muhasebe", "tr_TR": "Muhasebe", "tr-TR": "Muhasebe",
		"pt-br": "Contabilidade", "en": "Accounting", "": "Accounting", "de": "Accounting",
	} {
		require.Equal(t, want, r.NameFor(lang), "lang %q", lang)
	}
	require.Equal(t, "Faturalar", r.DescriptionFor("tr"))
	require.Equal(t, "Invoices", r.DescriptionFor("en"))
	var none *model.PermissionRule
	require.Empty(t, none.NameFor("tr"))
}

func TestRoleNames_RefusalSourceCarriesThem(t *testing.T) {
	r := &model.PermissionRule{ID: 4, Name: "Uploader", Names: map[string]string{"tr": "Yükleyici"}, Enabled: true,
		Permissions: []string{"files.create"}}
	res := Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 4, Rules: []*model.PermissionRule{r}})
	src := res.Why(FilesDelete)
	require.Equal(t, SourceRule, src.Kind)
	require.Equal(t, "Uploader", src.RuleName, "the wire keeps the role's own name")
	require.Equal(t, "Yükleyici", src.RuleNameFor("tr"))
	require.Equal(t, "Uploader", src.RuleNameFor("en"))

	r.Enabled = false
	res = Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 4, Rules: []*model.PermissionRule{r}})
	off := res.Why(FilesDelete)
	require.Equal(t, SourceRoleOff, off.Kind)
	require.Equal(t, "Yükleyici", off.RuleNameFor("tr"), "a switched-off role is named in the reader's language too")

	// The folder part names itself the same way.
	r.Enabled = true
	r.Effects = map[string]string{"files.delete": model.PermAllow}
	r.Conditions = model.PermRuleConditions{Paths: []string{"Scratch/**"}}
	res = Resolve(Input{UserID: 1, Role: model.RoleUser, Defaults: Standard, CustomRoleID: 4, Rules: []*model.PermissionRule{r}})
	require.True(t, res.CanAt(1, "Scratch/a.txt", FilesDelete))
	require.Equal(t, "Yükleyici", res.WhyAt(1, "Scratch/a.txt", FilesDelete).RuleNameFor("tr"))
}
