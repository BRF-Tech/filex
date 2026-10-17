package handlers_test

// A copy, a move, a rename or an extraction puts a file's bytes at another
// path. A role whose files.download differs by path (a part limited to some
// folders or names) keeps its answer for those bytes: what may not be
// downloaded where it is does not travel to where it could be. Moving or
// copying inside the refused place, and everything the role leaves alone,
// works as before (download_lift.go).

import (
	"archive/zip"
	"bytes"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// newKeptFix is permFix with three folders the administrator filled - Kept
// (plan.txt), Notes (kept.txt, other.txt) and Open (empty) - and a member
// whose role refuses files.download on alpha://Kept and on
// alpha://Notes/kept.txt, and nowhere else.
func newKeptFix(t *testing.T) *permFix {
	t.Helper()
	pf := newPermFix(t)
	for _, dir := range []string{"Kept", "Notes", "Open"} {
		status, body := fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": "alpha://", "name": dir})
		require.Equal(t, http.StatusOK, status, body)
	}
	fxUpload(t, pf.URL, pf.adminTok, "alpha://Kept", "plan.txt", "plan")
	fxUpload(t, pf.URL, pf.adminTok, "alpha://Notes", "kept.txt", "kept")
	fxUpload(t, pf.URL, pf.adminTok, "alpha://Notes", "other.txt", "other")
	pf.memberRole(t, &model.PermissionRule{Name: "Kept in place",
		Conditions: model.PermRuleConditions{StorageIDs: []int64{pf.StA.ID}, Paths: []string{"Kept", "Notes/kept.txt"}},
		Effects:    map[string]string{"files.download": model.PermDeny}})
	return pf
}

func TestOpsCopy_KeepsTheDownloadRuleOfWhatItCopies(t *testing.T) {
	pf := newKeptFix(t)

	status, body := fxPost(t, pf.URL+"/api/files/copy", pf.memberTok, map[string]any{"source": []string{"alpha://Kept/plan.txt"}, "target": "alpha://Open"})
	requireDenied(t, "copy out of Kept", status, body, perm.FilesDownload, perm.SourceRule)
	assert.NoFileExists(t, filepath.Join(pf.RootA, "Open", "plan.txt"))

	status, body = fxPost(t, pf.URL+"/api/files/ops", pf.memberTok, map[string]any{
		"kind": "copy", "storage_id": pf.StA.ID, "sources": []string{"Kept/plan.txt"}, "dest": "Open/",
	})
	requireDenied(t, "queued copy out of Kept", status, body, perm.FilesDownload, perm.SourceRule)

	// One file the rule names, inside a folder it does not: the folder's copy
	// would carry it out.
	status, body = fxPost(t, pf.URL+"/api/files/copy", pf.memberTok, map[string]any{"source": []string{"alpha://Notes"}, "target": "alpha://Open"})
	requireDenied(t, "copy of a folder holding a kept file", status, body, perm.FilesDownload, perm.SourceRule)
	assert.NoDirExists(t, filepath.Join(pf.RootA, "Open", "Notes"))

	// Inside the refused place, and for what the rule leaves alone, a copy is
	// a copy.
	status, body = fxPost(t, pf.URL+"/api/files/copy", pf.memberTok, map[string]any{"source": []string{"alpha://Kept/plan.txt"}, "target": "alpha://Kept", "name": "plan-2.txt"})
	assert.Less(t, status, 300, "a copy that stays in Kept: %s", body)
	status, body = fxPost(t, pf.URL+"/api/files/copy", pf.memberTok, map[string]any{"source": []string{"alpha://Notes/other.txt"}, "target": "alpha://Open"})
	assert.Less(t, status, 300, "a file the rule leaves alone: %s", body)
}

func TestOpsMove_KeepsTheDownloadRuleOfWhatItMoves(t *testing.T) {
	pf := newKeptFix(t)

	status, body := fxPost(t, pf.URL+"/api/files/move", pf.memberTok, map[string]any{"source": []string{"alpha://Kept/plan.txt"}, "target": "alpha://Open"})
	requireDenied(t, "queued move out of Kept", status, body, perm.FilesDownload, perm.SourceRule)
	assert.FileExists(t, filepath.Join(pf.RootA, "Kept", "plan.txt"))

	status, body = fxPost(t, pf.URL+"/api/files/ops", pf.memberTok, map[string]any{
		"kind": "move", "storage_id": pf.StA.ID, "sources": []string{"Notes/kept.txt"}, "dest": "Open/",
	})
	requireDenied(t, "queued move of a kept file", status, body, perm.FilesDownload, perm.SourceRule)

	status, body = fxPost(t, pf.URL+"/api/files/move", pf.memberTok, map[string]any{"source": []string{"alpha://Notes/other.txt"}, "target": "alpha://Open"})
	assert.Less(t, status, 300, "a file the rule leaves alone moves: %s", body)
}

func TestManagerRenameAndMove_KeepTheDownloadRule(t *testing.T) {
	pf := newKeptFix(t)

	// The rule names the file: under another name it would answer yes.
	status, body := fxMutate(t, pf.URL, pf.memberTok, "rename", map[string]any{"path": "alpha://Notes", "item": "alpha://Notes/kept.txt", "name": "renamed.txt"})
	requireDenied(t, "rename of a kept file", status, body, perm.FilesDownload, perm.SourceRule)
	assert.FileExists(t, filepath.Join(pf.RootA, "Notes", "kept.txt"))
	assert.NoFileExists(t, filepath.Join(pf.RootA, "Notes", "renamed.txt"))

	status, body = fxMutate(t, pf.URL, pf.memberTok, "move", map[string]any{"path": "alpha://Open", "items": []map[string]string{{"path": "alpha://Kept/plan.txt"}}})
	requireDenied(t, "move out of Kept", status, body, perm.FilesDownload, perm.SourceRule)
	assert.FileExists(t, filepath.Join(pf.RootA, "Kept", "plan.txt"))

	// A new name inside Kept stays inside Kept: nothing is carried out.
	status, body = fxMutate(t, pf.URL, pf.memberTok, "rename", map[string]any{"path": "alpha://Kept", "item": "alpha://Kept/plan.txt", "name": "plan-v2.txt"})
	require.Equal(t, http.StatusOK, status, "a rename that stays in Kept: %s", body)
	assert.FileExists(t, filepath.Join(pf.RootA, "Kept", "plan-v2.txt"))

	status, body = fxMutate(t, pf.URL, pf.memberTok, "rename", map[string]any{"path": "alpha://Notes", "item": "alpha://Notes/other.txt", "name": "other-2.txt"})
	require.Equal(t, http.StatusOK, status, "a file the rule leaves alone: %s", body)
}

func TestAIMoveAndCopy_KeepTheDownloadRule(t *testing.T) {
	pf := newKeptFix(t)

	status, body := fxPost(t, pf.URL+"/api/ai/move", pf.memberTok, map[string]any{"src": "alpha://Notes/kept.txt", "dst": "alpha://Notes/renamed.txt"})
	assert.Equal(t, http.StatusForbidden, status, body)
	assert.Contains(t, body, "files.download", body)
	assert.FileExists(t, filepath.Join(pf.RootA, "Notes", "kept.txt"))

	isErr, text, _ := parityTool(t, pf.URL, pf.memberTok, "file_move", map[string]any{"src": "alpha://Kept/plan.txt", "dst": "alpha://Open/plan.txt"})
	assert.True(t, isErr, text)
	assert.Contains(t, text, "files.download", text)
	assert.FileExists(t, filepath.Join(pf.RootA, "Kept", "plan.txt"))

	// file_copy goes through the explorer's queued copy, and answers its refusal.
	status, body = fxPost(t, pf.URL+"/api/ai/copy", pf.memberTok, map[string]any{"src": "alpha://Kept/plan.txt", "dst": "alpha://Open/plan.txt"})
	assert.Equal(t, http.StatusForbidden, status, body)
	assert.Contains(t, body, "files.download", body)

	status, body = fxPost(t, pf.URL+"/api/ai/move", pf.memberTok, map[string]any{"src": "alpha://Notes/other.txt", "dst": "alpha://Open/other.txt"})
	assert.Equal(t, http.StatusOK, status, "a file the rule leaves alone moves: %s", body)
}

// keptZip is a zip archive holding one member, inside.txt.
func keptZip(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("inside.txt")
	require.NoError(t, err)
	_, err = fw.Write([]byte("inside"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.String()
}

func TestAIUnzip_KeepsTheDownloadRuleOfTheArchive(t *testing.T) {
	pf := newKeptFix(t)
	fxUpload(t, pf.URL, pf.adminTok, "alpha://Kept", "bundle.zip", keptZip(t))

	status, body := fxPost(t, pf.URL+"/api/ai/unzip", pf.memberTok, map[string]any{"src": "alpha://Kept/bundle.zip", "dest": "alpha://Open/out"})
	assert.Equal(t, http.StatusForbidden, status, body)
	assert.Contains(t, body, "files.download", body)
	assert.NoFileExists(t, filepath.Join(pf.RootA, "Open", "out", "inside.txt"))

	// Extracted where download is refused too, the contents keep the answer.
	status, body = fxPost(t, pf.URL+"/api/ai/unzip", pf.memberTok, map[string]any{"src": "alpha://Kept/bundle.zip", "dest": "alpha://Kept/out"})
	require.Equal(t, http.StatusOK, status, body)
	assert.FileExists(t, filepath.Join(pf.RootA, "Kept", "out", "inside.txt"))

	// An archive the member may download extracts anywhere.
	fxUpload(t, pf.URL, pf.adminTok, "alpha://Open", "free.zip", keptZip(t))
	status, body = fxPost(t, pf.URL+"/api/ai/unzip", pf.memberTok, map[string]any{"src": "alpha://Open/free.zip", "dest": "alpha://Open/free"})
	require.Equal(t, http.StatusOK, status, "an archive the rule leaves alone: %s", body)
}
