package handlers_test

// Every file inside a folder archive answers the rule a download of that one
// file answers (memberRule, pack_source_rule.go): a role's part that refuses
// files.download on one file inside a folder the account may download keeps
// that file out of every archive of the folder - the selection download and
// its /z/ ticket, archive/create, the agent zip and the MCP file_zip tool -
// and the rest of the folder still goes in.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// memberRuleFixture is newVerbFixture's RBAC storage (an editor grant on
// docs/, docs/gizli.txt) with a second file in docs/, and an API token of the
// editor.
func memberRuleFixture(t *testing.T) (*verbFixture, string) {
	t.Helper()
	f := newVerbFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.rbacRoot, "docs", "acik.txt"), []byte("acik"), 0o644))
	t.Cleanup(perm.Invalidate)
	return f, testutil.NewAPIToken(t, f.store, f.editorID, "read,write,delete,mcp")
}

// denyDownloadOn gives the editor a role that is the standard list everywhere
// and refuses files.download on these paths of the RBAC storage.
func (f *verbFixture) denyDownloadOn(t *testing.T, paths ...string) {
	t.Helper()
	ctx := context.Background()
	rule, err := f.store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "No download there", Enabled: true,
		Permissions: perm.Standard.Strings(),
		Effects:     map[string]string{string(perm.FilesDownload): model.PermDeny},
		Conditions:  model.PermRuleConditions{StorageIDs: []int64{f.rbac.ID}, Paths: paths},
	})
	require.NoError(t, err)
	require.NoError(t, f.store.SetUserCustomRole(ctx, f.editorID, rule.ID))
	perm.Invalidate()
}

// mintFolderArchive asks POST /api/files/archive/download for paths and
// returns the /z/ url.
func (f *verbFixture) mintFolderArchive(t *testing.T, tok string, paths ...string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"paths": paths})
	require.NoError(t, err)
	code, raw := f.call(t, nil, tok, http.MethodPost, "/api/files/archive/download", string(body))
	require.Equal(t, http.StatusOK, code, raw)
	var info struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &info))
	require.NotEmpty(t, info.URL, raw)
	return info.URL
}

// storedZip opens an archive written into the RBAC storage.
func (f *verbFixture) storedZip(t *testing.T, rel string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.rbacRoot, filepath.FromSlash(rel)))
	require.NoError(t, err, "the archive %s was not written", rel)
	return openZip(t, raw)
}

// assertFolderMembers: the file the rule names is not in the archive, the
// other one is.
func assertFolderMembers(t *testing.T, door string, members map[string]string) {
	t.Helper()
	assert.NotContains(t, members, "docs/gizli.txt", "%s: a file the rule keeps from a download is not packed", door)
	assert.Equal(t, "acik", members["docs/acik.txt"], "%s: the rest of the folder still goes in", door)
}

func TestArchive_MembersAnswerTheDownloadRule(t *testing.T) {
	f, tok := memberRuleFixture(t)
	f.denyDownloadOn(t, "docs/gizli.txt")

	// The rule as a download sees it: this file no, its neighbour yes.
	code, body := f.call(t, nil, tok, http.MethodGet, "/api/ai/download?path=rbac://docs/gizli.txt", "")
	require.Equal(t, http.StatusForbidden, code, body)
	code, body = f.call(t, nil, tok, http.MethodGet, "/api/ai/download?path=rbac://docs/acik.txt", "")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "acik", body)

	// Named on its own it is refused, as before.
	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/archive/download", `{"paths":["rbac://docs/gizli.txt"]}`)
	assert.Equal(t, http.StatusForbidden, code, body)

	// The selection download of the folder, through its ticket.
	resp, raw := fetchArchive(t, f.srv.URL, f.mintFolderArchive(t, tok, "rbac://docs"))
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	assertFolderMembers(t, "archive/download", openZip(t, raw))

	// The agent zip, written into the storage.
	code, body = f.call(t, nil, tok, http.MethodPost, "/api/ai/zip", `{"sources":["rbac://docs"],"dest":"rbac://docs/out.zip"}`)
	require.Equal(t, http.StatusOK, code, body)
	assertFolderMembers(t, "ai/zip", f.storedZip(t, "docs/out.zip"))

	// The MCP twin.
	isErr, text, _ := parityTool(t, f.srv.URL, tok, "file_zip", map[string]any{"sources": []string{"rbac://docs"}, "dest": "rbac://docs/out-mcp.zip"})
	require.False(t, isErr, text)
	assertFolderMembers(t, "file_zip", f.storedZip(t, "docs/out-mcp.zip"))

	// The explorer's Compress (this harness has no operations queue, so it
	// answers when the archive is written).
	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/archive/create", `{"sources":["rbac://docs"],"dest":"rbac://docs/out-create.zip","format":"zip"}`)
	require.Equal(t, http.StatusOK, code, body)
	assertFolderMembers(t, "archive/create", f.storedZip(t, "docs/out-create.zip"))
}

// A rule that names a whole subfolder keeps all of it out, and the folder
// around it still downloads.
func TestArchive_ASubfolderTheRuleNamesStaysOut(t *testing.T) {
	f, tok := memberRuleFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(f.rbacRoot, "docs", "kapali"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.rbacRoot, "docs", "kapali", "rapor.txt"), []byte("rapor"), 0o644))
	f.denyDownloadOn(t, "docs/kapali")

	resp, raw := fetchArchive(t, f.srv.URL, f.mintFolderArchive(t, tok, "rbac://docs"))
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	members := openZip(t, raw)
	assert.NotContains(t, members, "docs/kapali/rapor.txt", "a file under a folder the rule names is not packed")
	assert.Equal(t, "acik", members["docs/acik.txt"])
	assert.Equal(t, "gizli", members["docs/gizli.txt"], "the rule names one subfolder, not the rest")
}

// The ticket is redeemed without a credential, minutes after the mint: the
// redeem asks every member again, as the account that minted it.
func TestArchive_ATicketAsksTheRuleAgainWhenRedeemed(t *testing.T) {
	f, tok := memberRuleFixture(t)
	url := f.mintFolderArchive(t, tok, "rbac://docs")

	f.denyDownloadOn(t, "docs/gizli.txt")

	resp, raw := fetchArchive(t, f.srv.URL, url)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	members := openZip(t, raw)
	assertFolderMembers(t, "a ticket minted before the rule", members)
	assert.Contains(t, members["_FILEX-INCOMPLETE.txt"], "docs/gizli.txt", "the archive says what it left out")
}

// A ticket minted with an API token ends with the token, as a one-file link
// does.
func TestArchive_ATicketEndsWithItsToken(t *testing.T) {
	f, tok := memberRuleFixture(t)
	url := f.mintFolderArchive(t, tok, "rbac://docs")

	toks, err := f.store.ListAPITokensByUser(context.Background(), f.editorID)
	require.NoError(t, err)
	require.Len(t, toks, 1)
	require.NoError(t, f.store.DeleteAPIToken(context.Background(), toks[0].ID))

	resp, raw := fetchArchive(t, f.srv.URL, url)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "a revoked token's ticket serves nothing: %s", raw)
}

// archive/add packs each file it is given: each asks what packing asks.
func TestArchive_AddAsksTheDownloadRuleOfEachFile(t *testing.T) {
	f, tok := memberRuleFixture(t)
	f.denyDownloadOn(t, "docs/gizli.txt")

	code, body := f.call(t, nil, tok, http.MethodPost, "/api/files/archive/add",
		`{"path":"rbac://docs/pack.zip","files":[{"source":"rbac://docs/gizli.txt","name":"gizli.txt"}]}`)
	require.Equal(t, http.StatusForbidden, code, body)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &got), body)
	assert.Equal(t, "permission_denied", got["error"], body)
	assert.Equal(t, string(perm.FilesDownload), got["permission"], body)
	assert.NoFileExists(t, filepath.Join(f.rbacRoot, "docs", "pack.zip"))

	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/archive/add",
		`{"path":"rbac://docs/pack.zip","files":[{"source":"rbac://docs/acik.txt","name":"acik.txt"}]}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "acik", f.storedZip(t, "docs/pack.zip")["acik.txt"])
}

// roleForEditor gives the editor a role built from rule, limited to the RBAC
// storage.
func (f *verbFixture) roleForEditor(t *testing.T, rule *model.PermissionRule) {
	t.Helper()
	ctx := context.Background()
	rule.Enabled = true
	rule.Conditions.StorageIDs = []int64{f.rbac.ID}
	created, err := f.store.CreatePermissionRule(ctx, rule)
	require.NoError(t, err)
	require.NoError(t, f.store.SetUserCustomRole(ctx, f.editorID, created.ID))
	perm.Invalidate()
}

// What is inside an archive the account may not download does not come out
// where it could: an extraction into a folder that allows the download is
// refused, and one that stays where download is refused goes ahead.
func TestArchive_ExtractKeepsTheArchivesDownloadRule(t *testing.T) {
	f, tok := memberRuleFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.rbacRoot, "docs", "paket.zip"), craftedZip(t, map[string]string{"ic.txt": "ic"}), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(f.rbacRoot, "docs", "kapali"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.rbacRoot, "docs", "kapali", "paket.zip"), craftedZip(t, map[string]string{"ic.txt": "ic"}), 0o644))
	f.denyDownloadOn(t, "docs/paket.zip", "docs/kapali")

	code, body := f.call(t, nil, tok, http.MethodPost, "/api/files/archive/extract", `{"path":"rbac://docs/paket.zip","dest":"rbac://docs/acilan"}`)
	require.Equal(t, http.StatusForbidden, code, body)
	assert.Contains(t, body, string(perm.FilesDownload), body)
	assert.NoFileExists(t, filepath.Join(f.rbacRoot, "docs", "acilan", "ic.txt"))

	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/archive/extract", `{"path":"rbac://docs/kapali/paket.zip","dest":"rbac://docs/kapali/acilan"}`)
	require.Equal(t, http.StatusOK, code, body)
	got, err := os.ReadFile(filepath.Join(f.rbacRoot, "docs", "kapali", "acilan", "ic.txt"))
	require.NoError(t, err, "an extraction that stays where download is refused goes ahead")
	assert.Equal(t, "ic", string(got))
}

// A role that allows the download of some names only: the folder an archive
// unpacks into refuses it, a member under an allowed name would not - that one
// member stays in the archive, the rest are extracted.
func TestArchive_ExtractWithholdsAMemberThatWouldBeDownloadable(t *testing.T) {
	f, tok := memberRuleFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.rbacRoot, "docs", "paket.zip"),
		craftedZip(t, map[string]string{"not.txt": "not", "veri.bin": "veri"}), 0o644))
	f.roleForEditor(t, &model.PermissionRule{
		Name:        "Text files only",
		Permissions: perm.Standard.Without(perm.FilesDownload).Strings(),
		Effects:     map[string]string{string(perm.FilesDownload): model.PermAllow},
		Conditions:  model.PermRuleConditions{Paths: []string{"**/*.txt"}},
	})

	code, body := f.call(t, nil, tok, http.MethodPost, "/api/files/archive/extract", `{"path":"rbac://docs/paket.zip","dest":"rbac://docs/acilan"}`)
	require.Equal(t, http.StatusOK, code, body)
	var res struct {
		Count    int `json:"count"`
		Withheld int `json:"withheld"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &res), body)
	assert.Equal(t, 1, res.Count, body)
	assert.Equal(t, 1, res.Withheld, body)
	assert.NoFileExists(t, filepath.Join(f.rbacRoot, "docs", "acilan", "not.txt"), "a member that would be downloadable where it lands stays in the archive")
	assert.FileExists(t, filepath.Join(f.rbacRoot, "docs", "acilan", "veri.bin"))
}
