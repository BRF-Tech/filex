package handlers_test

// Who may encrypt (internal/e2epolicy), at a COPY (operator decision
// 2026-10-03). A copy of an encrypted folder or of a `.fxe` makes a new
// encrypted item where it lands, exactly as creating one would, so it is asked
// as that encryption: with the policy off nobody may, under the approval
// policy it needs an approval of the matching kind at the destination. A move
// and a rename of what is encrypted stay free: they make nothing new.

import (
	"context"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// With the policy off, the queue's copy of an encrypted folder, of a `.fxe`
// (under its own name or another `.fxe` name) and of a plain folder that holds
// an encrypted folder further down is refused, and nothing lands. A plain
// folder still copies, and the encrypted folder and the `.fxe` still move.
func TestE2ECopy_ACopyOfWhatIsEncryptedIsANewEncryption(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	admin, err := f.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	tok := issueToken(t, f.Store, admin.ID, fullScopes, nil)
	for _, dir := range []string{"Kasa", "Arsiv", "Ust", "Ust/Ic", "Duz", "Tasinan"} {
		status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://" + path.Dir(dir), "name": path.Base(dir)})
		require.Equal(t, http.StatusOK, status, "%s: %s", dir, body)
	}
	// Encrypted before the policy was switched off.
	fxUpload(t, f.URL, tok, "alpha://Kasa", e2eKeyFile, kfOne)
	fxUpload(t, f.URL, tok, "alpha://Ust/Ic", e2eKeyFile, kfOne)
	fxUpload(t, f.URL, tok, "alpha://", "rapor.pdf.fxe", fxeBody)
	fxUpload(t, f.URL, tok, "alpha://Duz", "notlar.txt", "plain")
	encryptionOff(t, f.Store)

	copyTo := func(src, target, name string) (int, string) {
		t.Helper()
		body := map[string]any{"source": []string{src}, "target": target}
		if name != "" {
			body["name"] = name
		}
		return fxPost(t, f.URL+"/api/files/copy", tok, body)
	}
	for _, c := range []struct{ what, src, name string }{
		{"an encrypted folder", "alpha://Kasa", ""},
		{"a .fxe under its own name", "alpha://rapor.pdf.fxe", ""},
		{"a .fxe under another .fxe name", "alpha://rapor.pdf.fxe", "kopya.pdf.fxe"},
		{"a folder holding an encrypted folder below it", "alpha://Ust", ""},
	} {
		status, body := copyTo(c.src, "alpha://Arsiv", c.name)
		assertE2ERefused(t, "copying "+c.what, status, body)
	}
	status, body := fxPost(t, f.URL+"/api/files/ops", tok, map[string]any{
		"kind": "copy", "storage_id": f.StA.ID, "sources": []string{"Kasa"}, "dest": "Arsiv/",
	})
	assertE2ERefused(t, "copying an encrypted folder through /api/files/ops", status, body)

	// What makes nothing new still goes through.
	status, body = copyTo("alpha://Duz", "alpha://Arsiv", "")
	require.Equal(t, http.StatusAccepted, status, "a plain folder: %s", body)
	status, body = fxPost(t, f.URL+"/api/files/move", tok, map[string]any{"source": []string{"alpha://Kasa", "alpha://rapor.pdf.fxe"}, "target": "alpha://Tasinan"})
	require.Equal(t, http.StatusAccepted, status, "moving an encrypted folder and a .fxe: %s", body)
	f.drainOps(t)

	for _, rel := range []string{"Arsiv/Kasa", "Arsiv/rapor.pdf.fxe", "Arsiv/kopya.pdf.fxe", "Arsiv/Ust"} {
		assert.NoFileExists(t, inAlpha(f, rel), "%s landed", rel)
		assert.NoDirExists(t, inAlpha(f, rel), "%s landed", rel)
	}
	assert.FileExists(t, inAlpha(f, "Arsiv/Duz/notlar.txt"))
	assert.FileExists(t, inAlpha(f, "Tasinan/Kasa/"+e2eKeyFile))
	assert.FileExists(t, inAlpha(f, "Tasinan/rapor.pdf.fxe"))
}

// Under the approval policy a copied encrypted folder is a NEW encrypted
// folder where it lands: a new-folder approval of the destination opens it,
// once; an in-place approval of the destination does not (operator decisions
// 2026-10-03: copies are asked, and the kinds are separate).
func TestE2ECopy_UnderApprovalACopiedEncryptedFolderSpendsANewFolderApproval(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	tok := issueToken(t, f.Store, f.UserA, "read,write", nil)
	for _, dir := range []string{"Kasa", "Hedef"} {
		status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://", "name": dir})
		require.Equal(t, http.StatusOK, status, "%s: %s", dir, body)
	}
	fxUpload(t, f.URL, tok, "alpha://Kasa", e2eKeyFile, kfOne)
	fxUpload(t, f.URL, tok, "alpha://Hedef", "liste.txt", "plain")
	require.NoError(t, f.Store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyApproval))
	copyKasa := func() (int, string) {
		t.Helper()
		return fxPost(t, f.URL+"/api/files/copy", tok, map[string]any{"source": []string{"alpha://Kasa"}, "target": "alpha://Hedef"})
	}
	refusedFor := func(what string, status int, body string) {
		t.Helper()
		require.Equal(t, http.StatusForbidden, status, "%s: %s", what, body)
		assert.Contains(t, body, `"reason":"`+string(e2epolicy.ReasonApprovalRequired)+`"`, "%s: %s", what, body)
	}

	status, body := copyKasa()
	refusedFor("no approval", status, body)
	inPlace := dbtest.ApproveE2E(t, f.Store, f.UserA, f.StA.ID, "Hedef", "folder")
	status, body = copyKasa()
	refusedFor("an in-place approval of the destination", status, body)
	assert.Equal(t, model.E2ERequestApproved, dbtest.E2EStatus(t, f.Store, inPlace.ID))

	newFolder := dbtest.ApproveE2E(t, f.Store, f.UserA, f.StA.ID, "Hedef", "new_folder")
	status, body = copyKasa()
	require.Equal(t, http.StatusAccepted, status, "a new-folder approval of the destination: %s", body)
	f.drainOps(t)
	assert.FileExists(t, inAlpha(f, "Hedef/Kasa/"+e2eKeyFile))
	assert.Equal(t, model.E2ERequestUsed, dbtest.E2EStatus(t, f.Store, newFolder.ID), "the copy spent it")
	assert.Equal(t, model.E2ERequestApproved, dbtest.E2EStatus(t, f.Store, inPlace.ID), "the other kind is not touched")

	status, body = copyKasa()
	refusedFor("the approval is spent", status, body)
}

// A WebDAV COPY of an encrypted folder is asked too (protoperm
// CopyEncryptionAllowed, through the router's own WebDAV): with the policy off
// it is refused and nothing lands, while a plain folder copies and the
// encrypted folder still moves.
func TestE2ECopy_AWebDAVCopyOfAnEncryptedFolderIsAsked(t *testing.T) {
	f := newStagedFixture(t)
	ctx := context.Background()
	seed := func(rel, content string, dir bool) {
		t.Helper()
		typ := model.NodeTypeFile
		if dir {
			typ = model.NodeTypeDirectory
			require.NoError(t, os.MkdirAll(filepath.Join(f.rootDir, filepath.FromSlash(rel)), 0o755))
		} else {
			require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, filepath.FromSlash(rel)), []byte(content), 0o644))
		}
		_, err := f.store.CreateNode(ctx, &model.Node{
			StorageID: f.storage.ID, Name: path.Base(rel), Path: "/" + rel, PathHash: pathkey.Hash(f.storage.ID, "/"+rel),
			Type: typ, Size: int64(len(content)),
		})
		require.NoError(t, err)
	}
	seed("Kasa", "", true)
	seed("Kasa/"+e2eKeyFile, kfOne, false)
	seed("Duz", "", true)
	seed("Duz/notlar.txt", "plain", false)
	encryptionOff(t, f.store)

	dav := func(method, from, to string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, f.srv.URL+"/dav/main/"+from, nil)
		require.NoError(t, err)
		req.Header.Set("Destination", f.srv.URL+"/dav/main/"+to)
		req.SetBasicAuth(f.adminEml, f.adminPw)
		resp, err := f.client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	status, body := dav("COPY", "Kasa", "Kopya")
	assert.Equal(t, http.StatusForbidden, status, "copying an encrypted folder: %s", body)
	assert.True(t, strings.Contains(body, "encrypted"), "the refusal is the rule's: %s", body)
	assert.NoDirExists(t, filepath.Join(f.rootDir, "Kopya"))

	status, body = dav("COPY", "Duz", "Duz2")
	assert.Contains(t, []int{http.StatusCreated, http.StatusNoContent}, status, "a plain folder: %s", body)
	assert.FileExists(t, filepath.Join(f.rootDir, "Duz2", "notlar.txt"))
	status, body = dav("MOVE", "Kasa", "Kasa2")
	assert.Contains(t, []int{http.StatusCreated, http.StatusNoContent}, status, "moving an encrypted folder: %s", body)
	assert.FileExists(t, filepath.Join(f.rootDir, "Kasa2", e2eKeyFile))
}
