package handlers_test

// Two more doors that hand out what a file holds answer the account's
// files.download on that file: an app's result that lands as a new file, and
// the document editor's own Download as.

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

// denyDownloadForUser gives one account a role that is the standard list
// everywhere and refuses files.download on these paths of one storage.
func denyDownloadForUser(t *testing.T, store interface {
	CreatePermissionRule(context.Context, *model.PermissionRule) (*model.PermissionRule, error)
	SetUserCustomRole(context.Context, int64, int64) error
}, userID, storageID int64, paths ...string) {
	t.Helper()
	ctx := context.Background()
	rule, err := store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "No download there", Enabled: true,
		Permissions: perm.Standard.Strings(),
		Effects:     map[string]string{string(perm.FilesDownload): model.PermDeny},
		Conditions:  model.PermRuleConditions{StorageIDs: []int64{storageID}, Paths: paths},
	})
	require.NoError(t, err)
	require.NoError(t, store.SetUserCustomRole(ctx, userID, rule.ID))
	perm.Invalidate()
	t.Cleanup(perm.Invalidate)
}

// An app's result written beside its input is a new file under a new name:
// for an input the person may not download it is refused, and the same
// action on a file they may download runs as before.
func TestAppAction_AResultKeepsTheInputsDownloadRule(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEchoWith(t, func(m map[string]any) {
		for _, a := range m["actions"].([]any) {
			a := a.(map[string]any)
			if a["id"] == "upper" {
				a["view"] = "hello"
				a["output"] = map[string]any{"mode": "sibling", "name": "{stem}-upper{ext}"}
			}
		}
	})
	f.writeFile(t, "docs/gizli.txt", "gizli")
	f.writeFile(t, "docs/acik.txt", "acik")
	client, u, _ := f.loginRegular(t, "ada", model.GrantEditor)
	denyDownloadForUser(t, f.store, u.ID, f.st.ID, "docs/gizli.txt")

	status, raw := f.helloSubmit(t, client, "main://docs/gizli.txt", map[string]any{"output_mode": "sibling"})
	require.Equal(t, http.StatusForbidden, status, string(raw))
	assert.Contains(t, string(raw), string(perm.FilesDownload))
	_, err := os.Stat(filepath.Join(f.root, "docs", "gizli-upper.txt"))
	assert.True(t, os.IsNotExist(err), "no result was written")

	status, raw = f.helloSubmit(t, client, "main://docs/acik.txt", map[string]any{"output_mode": "sibling"})
	require.Equal(t, http.StatusAccepted, status, string(raw))
	op := f.drain(t, opID(t, raw))
	require.Equal(t, "ok", op["status"], op)
	got, err := os.ReadFile(filepath.Join(f.root, "docs", "acik-upper.txt"))
	require.NoError(t, err)
	assert.Equal(t, "ACIK", string(got))
}

// The editor opens a document the person may not download (a preview stays
// open) without its own Download as; a document they may download keeps it.
func TestOnlyOfficeConfig_DownloadFollowsTheDownloadRule(t *testing.T) {
	root := t.TempDir()
	srv, store := editorFixtureWith(t, root, nil)
	ctx := context.Background()
	seedServerFile(t, store, "oo", "Documents/Gizli.docx", "PK not really a docx")
	seedServerFile(t, store, "oo", "Documents/Acik.docx", "PK not really a docx")
	oo, err := store.GetStorageByName(ctx, "oo")
	require.NoError(t, err)

	const email, pw = "editor-oo@test.local", "EditorPass!123"
	testutil.SeedRegularUser(t, store, email, pw)
	u, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)
	_, err = store.CreateFileGrant(ctx, &model.FileGrant{StorageID: oo.ID, PathPrefix: "Documents", IsDir: true, UserID: u.ID, Level: model.GrantEditor})
	require.NoError(t, err)
	denyDownloadForUser(t, store, u.ID, oo.ID, "Documents/Gizli.docx")
	tok := issueToken(t, store, u.ID, fullScopes, nil)

	download := func(p string) any {
		t.Helper()
		status, body := fxPost(t, srv.URL+"/api/files/onlyoffice/config", tok, map[string]any{"path": p, "mode": "view"})
		require.Equal(t, http.StatusOK, status, body)
		var got editorAnswer
		require.NoError(t, json.Unmarshal([]byte(body), &got), body)
		doc, _ := got.Config["document"].(map[string]any)
		perms, _ := doc["permissions"].(map[string]any)
		require.NotNil(t, perms, body)
		return perms["download"]
	}
	assert.Equal(t, false, download("oo://Documents/Gizli.docx"), "no Download as where files.download is refused")
	assert.Equal(t, true, download("oo://Documents/Acik.docx"))
}
