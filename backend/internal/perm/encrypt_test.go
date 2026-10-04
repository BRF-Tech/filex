package perm_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// files.encrypt is a file action like the others: checked on a path, able to
// differ by folder, beyond a read-only account — and part of every preset that
// can add files (Full admin, Standard, Upload-only), so the upgrade takes
// nobody's encryption away (docs/PERMISSIONS.md → The permissions).
func TestFilesEncryptIsAFileAction(t *testing.T) {
	d, ok := perm.Lookup(perm.FilesEncrypt)
	require.True(t, ok, "files.encrypt is in the catalogue")
	require.Equal(t, perm.GroupFiles, d.Group)
	require.True(t, d.ViewerCapped, "a read-only account never encrypts")
	require.False(t, d.RoleOnly, "roles and exceptions decide it")
	require.True(t, perm.Conditionable(perm.FilesEncrypt), "a role can allow or deny it in some folders")
	require.Equal(t, acl.LevelEditor, acl.NeedLevel(perm.FilesEncrypt), "encrypting writes: it needs the editor level where it happens")

	var files []perm.Perm
	for _, d := range perm.All() {
		if d.Group == perm.GroupFiles {
			files = append(files, d.Key)
		}
	}
	require.Equal(t, []perm.Perm{
		perm.FilesDownload, perm.FilesCreate, perm.FilesModify, perm.FilesRename, perm.FilesMove,
		perm.FilesDelete, perm.FilesPurge, perm.FilesEncrypt, perm.FilesTag,
	}, files, "UI order: after the actions it narrows, before tagging")

	presets := map[string]perm.Set{}
	for _, p := range perm.Presets() {
		presets[p.Name] = p.Set
	}
	require.True(t, presets[perm.PresetFullAdmin].Has(perm.FilesEncrypt))
	require.True(t, presets[perm.PresetStandard].Has(perm.FilesEncrypt), "the built-in User role holds it until an administrator narrows it")
	require.True(t, presets[perm.PresetUploadOnly].Has(perm.FilesEncrypt), "an Upload-only account could encrypt before the permission existed: files.create was enough")
	for _, name := range []string{perm.PresetReadOnly, perm.PresetGuest} {
		require.False(t, presets[name].Has(perm.FilesEncrypt), name)
	}
}

// A refusal names the action it refused ("… does not allow you to encrypt
// files"): every permission needs its phrase in the languages filex ships, or
// the sentence carries the bare key.
func TestEveryPermissionNamesItsAction(t *testing.T) {
	for _, lang := range srvtext.BuiltinLanguages() {
		table := srvtext.Builtin(lang)
		for _, d := range perm.All() {
			require.NotEmpty(t, table["server.perm.action."+string(d.Key)], "%s: server.perm.action.%s", lang, d.Key)
		}
	}
}
