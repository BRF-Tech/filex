package handlers_test

// A public link hands out only what its creator may download. The folder a
// link names is asked at the mint and on every visit (publicLinkRefusal,
// linkCreatorAllows), and every file inside a folder link is asked as well
// (handlers.LinkMembers, the member rule archives use): a role's part that
// refuses files.download on one file inside the folder keeps that file out of
// the link's ZIP - streamed or cached -, its listing and its /f/ door.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// shareLinkFix is permFix with a folder Docs holding open.txt and
// kept-back.txt, uploaded by the administrator.
func shareLinkFix(t *testing.T) *permFix {
	t.Helper()
	pf := newPermFix(t)
	status, body := fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": "alpha://", "name": "Docs"})
	require.Equal(t, http.StatusOK, status, body)
	fxUpload(t, pf.URL, pf.adminTok, "alpha://Docs", "open.txt", "open")
	fxUpload(t, pf.URL, pf.adminTok, "alpha://Docs", "kept-back.txt", "kept back")
	return pf
}

// keepBackFromMember gives the member a role that refuses files.download on
// Docs/kept-back.txt alone, and is the standard user everywhere else.
func keepBackFromMember(t *testing.T, pf *permFix) {
	t.Helper()
	pf.memberRole(t, &model.PermissionRule{Name: "Kept back",
		Permissions: perm.Standard.Strings(),
		Conditions:  model.PermRuleConditions{StorageIDs: []int64{pf.StA.ID}, Paths: []string{"Docs/kept-back.txt"}},
		Effects:     map[string]string{"files.download": model.PermDeny}})
}

// mintPublicLink makes a public link as tok and returns its token.
func mintPublicLink(t *testing.T, pf *permFix, tok string, payload map[string]any) string {
	t.Helper()
	status, body := fxPost(t, pf.URL+"/api/files/share", tok, payload)
	require.Less(t, status, 300, "precondition: the link is made: %s", body)
	return decode(t, body)["share"].(map[string]any)["token"].(string)
}

func TestShareLink_FolderZipMembersAnswerTheCreatorsDownloadRule(t *testing.T) {
	pf := shareLinkFix(t)
	keepBackFromMember(t, pf)
	// The folder itself may be downloaded, so the folder link is made.
	token := mintPublicLink(t, pf, pf.memberTok, map[string]any{"path": "alpha://Docs"})

	status, body := anonStatus(t, pf.URL+"/s/"+token+"?zip=wait")
	require.Equal(t, http.StatusOK, status, body)
	got := openZip(t, []byte(body))
	assert.Equal(t, "open", got["open.txt"], "a member the creator may download is in the archive: %v", keys(got))
	assert.NotContains(t, got, "kept-back.txt", "a member the creator may not download stays out of the archive")
}

// The cached archive (internal/sharezip, the router's folder-link ZIP cache
// and its warmer) is built from the same kept list.
func TestShareLink_CachedFolderZipAnswersTheCreatorsDownloadRule(t *testing.T) {
	pf := shareLinkFix(t)
	keepBackFromMember(t, pf)
	token := mintPublicLink(t, pf, pf.memberTok, map[string]any{"path": "alpha://Docs"})

	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": pf.RootA}))
	h := handlers.NewShare(share.NewService(pf.Store), pf.Store,
		func(int64) (storage.Driver, error) { return drv, nil }, "", sharezip.New(t.TempDir()))
	h.AttachACL(acl.New(pf.Store))

	req := httptest.NewRequest(http.MethodGet, "/s/"+token+"?zip=wait", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.HandleDownload(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := openZip(t, rec.Body.Bytes())
	assert.Equal(t, "open", got["open.txt"], "members: %v", keys(got))
	assert.NotContains(t, got, "kept-back.txt", "the cached archive leaves the refused member out too")
}

func TestShareLink_BrowseFileAnswersTheCreatorsDownloadRule(t *testing.T) {
	pf := shareLinkFix(t)
	keepBackFromMember(t, pf)
	token := mintPublicLink(t, pf, pf.memberTok, map[string]any{"path": "alpha://Docs"})

	status, body := anonStatus(t, pf.URL+"/s/"+token+"/f/open.txt")
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "open", body)

	status, body = anonStatus(t, pf.URL+"/s/"+token+"/f/kept-back.txt")
	assert.Equal(t, http.StatusNotFound, status, "a member the creator may not download answers like a missing one: %s", body)
	assert.NotContains(t, body, "kept back")
}

func TestShareLink_EntriesLeaveOutWhatTheCreatorMayNotDownload(t *testing.T) {
	pf := shareLinkFix(t)
	keepBackFromMember(t, pf)
	token := mintPublicLink(t, pf, pf.memberTok, map[string]any{"path": "alpha://Docs"})

	status, body := anonStatus(t, pf.URL+"/api/public/s/"+token+"/entries")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "open.txt")
	assert.NotContains(t, body, "kept-back.txt", "the shell's listing")

	// The no-JavaScript page lists the same.
	status, body = anonStatus(t, pf.URL+"/s/"+token)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "open.txt")
	assert.NotContains(t, body, "kept-back.txt", "the browse page's listing")
}

func TestShareLink_MintAsksDownload(t *testing.T) {
	pf := shareLinkFix(t)
	keepBackFromMember(t, pf)

	status, body := fxPost(t, pf.URL+"/api/files/share", pf.memberTok, map[string]any{"path": "alpha://Docs/kept-back.txt"})
	requireDenied(t, "a download link to a file its creator may not download", status, body, perm.FilesDownload, perm.SourceRule)

	status, body = fxPost(t, pf.URL+"/api/ai/share", pf.memberTok, map[string]any{"path": "alpha://Docs/kept-back.txt"})
	assert.Equal(t, http.StatusForbidden, status, "the agent API asks the same rule: %s", body)

	// What may be downloaded is linked as before, and a file request asks
	// nothing about downloads: it hands nothing out.
	mintPublicLink(t, pf, pf.memberTok, map[string]any{"path": "alpha://Docs/open.txt"})
	mintPublicLink(t, pf, pf.memberTok, map[string]any{"path": "alpha://Docs", "kind": "drop"})
}

func TestShareLink_ClosesWhenTheCreatorLosesDownload(t *testing.T) {
	pf := shareLinkFix(t)
	fileTok := mintPublicLink(t, pf, pf.memberTok, map[string]any{"path": "alpha://Docs/kept-back.txt"})
	folderTok := mintPublicLink(t, pf, pf.memberTok, map[string]any{"path": "alpha://Docs"})

	status, body := anonStatus(t, pf.URL+"/api/files/share/"+fileTok)
	require.Equal(t, http.StatusOK, status, "the link answers while its creator may download the file: %s", body)

	keepBackFromMember(t, pf)
	status, body = anonStatus(t, pf.URL+"/api/files/share/"+fileTok)
	assert.Equal(t, http.StatusNotFound, status, "metadata of the closed link: %s", body)
	status, body = anonStatus(t, pf.URL+"/s/"+fileTok)
	assert.Equal(t, http.StatusNotFound, status, "the /s/ door of the closed link: %s", body)
	assert.NotContains(t, body, "kept back")

	// The folder link stays open, without the file.
	status, body = anonStatus(t, pf.URL+"/s/"+folderTok+"?zip=wait")
	require.Equal(t, http.StatusOK, status, body)
	got := openZip(t, []byte(body))
	assert.Contains(t, got, "open.txt")
	assert.NotContains(t, got, "kept-back.txt")
	assert.FileExists(t, filepath.Join(pf.RootA, "Docs", "kept-back.txt"), "nothing is deleted")
}
