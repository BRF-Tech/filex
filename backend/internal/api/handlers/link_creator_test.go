package handlers_test

// A public link answers only while the person who made it may still make it
// (link_creator.go; Burak 2026-09-28: "linkler kapanır"). Nothing is deleted:
// the link comes back when the permission does. A file request also carries its
// creator's blocked file types, because what is dropped lands in the creator's
// storage as the creator's file.

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

func anonStatus(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func anonDrop(t *testing.T, url, name, content string) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	require.NoError(t, err)
	_, _ = fw.Write([]byte(content))
	require.NoError(t, mw.Close())
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestPerm_LinksCloseWhenTheCreatorLosesSharing(t *testing.T) {
	pf := newPermFix(t)

	status, body := fxPost(t, pf.URL+"/api/files/share", pf.memberTok, map[string]any{"path": "alpha://report.txt"})
	require.Less(t, status, 300, "precondition: a member may share: %s", body)
	token := decode(t, body)["share"].(map[string]any)["token"].(string)

	status, body = anonStatus(t, pf.URL+"/api/files/share/"+token)
	require.Equal(t, http.StatusOK, status, "the link answers while its creator may share: %s", body)

	// The creator loses share.links: every door of the link closes.
	pf.override(t, map[string]string{"share.links": model.PermDeny})
	status, body = anonStatus(t, pf.URL+"/api/files/share/"+token)
	assert.Equal(t, http.StatusNotFound, status, "metadata of a closed link: %s", body)
	status, _ = anonStatus(t, pf.URL+"/s/"+token)
	assert.Equal(t, http.StatusNotFound, status, "the /s/ page of a closed link")

	// …and opens again when the permission comes back. Nothing was deleted.
	pf.override(t, map[string]string{})
	status, body = anonStatus(t, pf.URL+"/api/files/share/"+token)
	assert.Equal(t, http.StatusOK, status, "the link is back with the permission: %s", body)
}

func TestPerm_FileRequestsCloseAndCarryTheCreatorsLimits(t *testing.T) {
	pf := newPermFix(t)
	status, body := fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": "alpha://", "name": "Inbox"})
	require.Equal(t, http.StatusOK, status, body)
	status, body = fxPost(t, pf.URL+"/api/files/share", pf.memberTok, map[string]any{"path": "alpha://Inbox", "kind": "drop"})
	require.Less(t, status, 300, "precondition: a member may open a file request: %s", body)
	token := decode(t, body)["share"].(map[string]any)["token"].(string)

	status, body = anonDrop(t, pf.URL+"/d/"+token, "ok.txt", "hello")
	require.Equal(t, http.StatusOK, status, "a drop works while its creator may make one: %s", body)

	// The creator's role blocks .exe: a dropped .exe is refused like the
	// creator's own upload would be.
	pf.ruleForMember(t, model.PermRuleSettings{BlockedExtensions: []string{"exe"}})
	status, body = anonDrop(t, pf.URL+"/d/"+token, "setup.exe", "MZ")
	assert.Equal(t, http.StatusUnsupportedMediaType, status, "the creator's blocked type: %s", body)
	assert.Contains(t, body, "ext_not_allowed")
	status, body = anonDrop(t, pf.URL+"/d/"+token, "ok2.txt", "hello")
	assert.Equal(t, http.StatusOK, status, "other types still land: %s", body)

	// The creator loses share.upload_links: the request closes.
	pf.override(t, map[string]string{"share.upload_links": model.PermDeny})
	status, body = anonDrop(t, pf.URL+"/d/"+token, "late.txt", "x")
	assert.Equal(t, http.StatusNotFound, status, "a closed file request: %s", body)
	status, _ = anonStatus(t, pf.URL+"/d/"+token)
	assert.Equal(t, http.StatusNotFound, status, "the /d/ page of a closed request")
}

// Deleting an account closes the public links it opened — download links and
// file requests — through every door that deletes one (Burak, 2026-09-28:
// "Kapansın"). ⚠ Before, the store kept the rows with created_by cleared, and a
// link with no creator is one linkCreatorAllows leaves open: the deleted
// person's links went on answering. The audit row says how many closed.
func TestPerm_LinksCloseWhenTheCreatorsAccountIsDeleted(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	status, body := fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": "alpha://", "name": "Inbox"})
	require.Equal(t, http.StatusOK, status, body)

	mint := func(tok string, payload map[string]any) string {
		t.Helper()
		status, body := fxPost(t, pf.URL+"/api/files/share", tok, payload)
		require.Less(t, status, 300, "precondition: %s", body)
		return decode(t, body)["share"].(map[string]any)["token"].(string)
	}
	download := mint(pf.memberTok, map[string]any{"path": "alpha://report.txt"})
	request := mint(pf.memberTok, map[string]any{"path": "alpha://Inbox", "kind": "drop"})
	theirs := mint(pf.adminTok, map[string]any{"path": "alpha://report.txt"})

	// An app's own public page the member's action opened (a signer's page).
	opened, err := pf.Store.GetShareByToken(ctx, download)
	require.NoError(t, err)
	member := pf.UserA
	page := &model.Share{NodeID: opened.NodeID, Token: "app-page-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		CreatedBy: &member, PluginID: 9, PageID: "sign"}
	_, err = pf.Store.CreateShare(ctx, page)
	require.NoError(t, err)

	status, _ = anonStatus(t, pf.URL+"/s/"+download)
	require.Equal(t, http.StatusOK, status, "precondition: the link answers while its creator exists")

	status, body = fxReq(t, http.MethodDelete, pf.URL+"/api/admin/users/"+strconv.FormatInt(pf.UserA, 10), pf.adminTok, nil, "")
	require.Equal(t, http.StatusOK, status, "an administrator deletes the member: %s", body)

	status, _ = anonStatus(t, pf.URL+"/s/"+download)
	assert.Equal(t, http.StatusNotFound, status, "the deleted account's download link")
	status, body = anonStatus(t, pf.URL+"/api/files/share/"+download)
	assert.Equal(t, http.StatusNotFound, status, "…and its metadata: %s", body)
	status, _ = anonStatus(t, pf.URL+"/d/"+request)
	assert.Equal(t, http.StatusNotFound, status, "the deleted account's file request")
	status, body = anonDrop(t, pf.URL+"/d/"+request, "late.txt", "x")
	assert.Equal(t, http.StatusNotFound, status, "nothing lands through it: %s", body)

	status, body = anonStatus(t, pf.URL+"/s/"+theirs)
	assert.Equal(t, http.StatusOK, status, "somebody else's link is untouched: %s", body)
	kept, err := pf.Store.GetShareByToken(ctx, page.Token)
	require.NoError(t, err, "the app's own page stays — the app opened it")
	assert.True(t, kept.IsApp())

	// The audit row of the deletion says how many links closed with it.
	var closed any
	require.Eventually(t, func() bool {
		rows, err := pf.Store.ListAuditRecent(ctx, 50)
		if err != nil {
			return false
		}
		for _, e := range rows {
			if v, ok := e.Metadata["links_closed"]; ok {
				closed = v
				return true
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "no audit row recorded the closed links")
	assert.EqualValues(t, 2, closed, "the download link and the file request")
}

// A lock limits what may be done TO a file, not who may hand it out: a link
// made before an app locked the file (a signed document is locked for good)
// keeps answering. Before this, Set.Can capped the locked file at viewer,
// share.links needs editor, and the signing app's own delivery link was a 404
// (v0.49.0 release run, e2e 113).
func TestPerm_ALockedFileKeepsItsLinks(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	status, body := fxPost(t, pf.URL+"/api/files/share", pf.memberTok, map[string]any{"path": "alpha://report.txt"})
	require.Less(t, status, 300, "precondition: a member may share: %s", body)
	token := decode(t, body)["share"].(map[string]any)["token"].(string)

	node, err := pf.Store.GetNodeByPath(ctx, pf.StA.ID, pathkey.Hash(pf.StA.ID, "/report.txt"))
	require.NoError(t, err)
	require.NoError(t, pf.Store.PutAppPluginLock(ctx, &model.AppPluginLock{
		StorageID: pf.StA.ID, PathHash: node.PathHash, Rel: node.Path, PluginID: 1, PluginName: "sign", Reason: "signed",
	}))

	status, body = anonStatus(t, pf.URL+"/api/files/share/"+token)
	assert.Equal(t, http.StatusOK, status, "the link to a locked file still answers: %s", body)

	// …while the creator losing share.links still closes it.
	pf.override(t, map[string]string{"share.links": model.PermDeny})
	status, _ = anonStatus(t, pf.URL+"/api/files/share/"+token)
	assert.Equal(t, http.StatusNotFound, status)
}
