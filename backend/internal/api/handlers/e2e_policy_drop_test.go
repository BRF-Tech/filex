package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// dropFile posts one file named name to the file request token, as an
// anonymous visitor does.
func dropFile(t *testing.T, f *mtFix, token, name, content string) (int, string) {
	t.Helper()
	var drop bytes.Buffer
	dw := multipart.NewWriter(&drop)
	part, err := dw.CreateFormFile("file[]", name)
	require.NoError(t, err)
	_, _ = part.Write([]byte(content))
	require.NoError(t, dw.Close())
	return fxReq(t, http.MethodPost, f.URL+"/d/"+token, "", &drop, dw.FormDataContentType())
}

// A file request's upload is judged as the link creator's (refusesE2E), but
// it never spends the creator's approval (operator decision 2026-10-03): the
// visitor did not ask for it, and the creator would find it gone. Under the
// approval policy a dropped file that would create an encryption is refused,
// as any file the link does not take, and the creator's approval stays
// unused. A plain file goes through, and the other policies are as they were:
// permitted lets the creator's own right through, off refuses.
func TestE2EPolicy_AFileRequestNeverSpendsTheCreatorsApproval(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	tok := issueToken(t, f.Store, f.UserA, "read,write", nil)
	status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://", "name": "Gelen"})
	require.Equal(t, http.StatusOK, status, body)
	gelen, err := f.Store.GetNodeByPath(ctx, f.StA.ID, pathkey.Hash(f.StA.ID, "/Gelen"))
	require.NoError(t, err)
	link, err := share.NewService(f.Store).Create(ctx, share.CreateOpts{NodeID: gelen.ID, Kind: model.ShareKindDrop, CreatedBy: &f.UserA})
	require.NoError(t, err)
	landed := func(name string) bool {
		got, _ := filepath.Glob(filepath.Join(f.RootA, "Gelen", "*", name))
		return len(got) > 0
	}

	// The creator holds an approval for Gelen: one new encrypted folder in it.
	require.NoError(t, f.Store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyApproval))
	r := dbtest.ApproveE2E(t, f.Store, f.UserA, f.StA.ID, "Gelen", model.E2ERequestFolder)
	for _, name := range []string{e2eKeyFile, "gonderi.fxe"} {
		status, body := dropFile(t, f, link.Token, name, kfOne)
		assert.Equal(t, http.StatusForbidden, status, "%s was taken: %s", name, body)
		var got struct{ Error, Reason, Message string }
		_ = json.Unmarshal([]byte(body), &got)
		assert.Equal(t, "e2e_not_allowed", got.Error, "%s: %s", name, body)
		assert.Equal(t, string(e2epolicy.ReasonApprovalRequired), got.Reason, "%s: %s", name, body)
		assert.NotEmpty(t, got.Message, "%s: the visitor is told in words", name)
		assert.False(t, landed(name), "%s landed", name)
		assert.Equal(t, model.E2ERequestApproved, dbtest.E2EStatus(t, f.Store, r.ID), "a drop of %s spent the creator's approval", name)
	}
	status, body = dropFile(t, f, link.Token, "notlar.txt", "plain")
	assert.Less(t, status, 300, "a plain file under the approval policy: %s", body)
	assert.True(t, landed("notlar.txt"), "the plain file did not land")

	// permitted: the creator may encrypt, and so the link takes the file.
	require.NoError(t, f.Store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyPermitted))
	status, body = dropFile(t, f, link.Token, "izinli.fxe", fxeBody)
	assert.Less(t, status, 300, "a .fxe under permitted: %s", body)
	assert.True(t, landed("izinli.fxe"), "the .fxe did not land under permitted")

	// off: nobody may.
	encryptionOff(t, f.Store)
	status, body = dropFile(t, f, link.Token, "kapali.fxe", fxeBody)
	assertE2ERefused(t, "a .fxe under off", status, body)
	assert.False(t, landed("kapali.fxe"), "the .fxe landed under off")
	assert.Equal(t, model.E2ERequestApproved, dbtest.E2EStatus(t, f.Store, r.ID), "the approval was spent")
}
