package handlers_test

// An app's public page hands over copies made from its document; they answer
// what a download link of the document answers - its creator may still
// download it there - at every visit (appLinkCopiesServed). The page itself,
// the app's own content, stays.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
)

func TestAppLink_CopiesAnswerTheCreatorsDownloadRule(t *testing.T) {
	f := newAppFixture(t, nil)
	pluginID := f.installEcho(t)
	f.writeFile(t, "docs/gizli.txt", "gizli")
	_, u, _ := f.loginRegular(t, "ada", model.GrantEditor)
	ctx := context.Background()
	node, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.st.ID, Name: "gizli.txt", Path: "/docs/gizli.txt",
		PathHash: pathkey.Hash(f.st.ID, "/docs/gizli.txt"), Type: model.NodeTypeFile,
		Size: 5, Mime: "text/plain", SeenAt: time.Now(),
	})
	require.NoError(t, err)

	svc := share.NewService(f.store)
	uid := u.ID
	withCopy, err := svc.Create(ctx, share.CreateOpts{
		NodeID: node.ID, PluginID: pluginID, PageID: "signer", Subject: "Please sign", CreatedBy: &uid,
		FilesJSON: `[{"ref":"pub:0","name":"gizli.txt","file":"0-gizli.txt","size":5}]`,
	})
	require.NoError(t, err)
	pageOnly, err := svc.Create(ctx, share.CreateOpts{
		NodeID: node.ID, PluginID: pluginID, PageID: "signer", Subject: "Just the page", CreatedBy: &uid,
	})
	require.NoError(t, err)

	state := func(token string) (bool, []any) {
		t.Helper()
		status, raw := doReq(t, freshClient(t), http.MethodGet, f.srv.URL+"/api/public/s/"+token, nil)
		require.Equal(t, http.StatusOK, status, string(raw))
		var got struct {
			Revoked bool `json:"revoked"`
			App     *struct {
				Files []any `json:"files"`
			} `json:"app"`
		}
		require.NoError(t, json.Unmarshal(raw, &got), string(raw))
		require.NotNil(t, got.App, string(raw))
		return got.Revoked, got.App.Files
	}

	revoked, files := state(withCopy.Token)
	require.False(t, revoked)
	require.Len(t, files, 1, "the copy is listed while its creator may download the document")

	denyDownloadForUser(t, f.store, u.ID, f.st.ID, "docs/gizli.txt")

	revoked, files = state(withCopy.Token)
	assert.False(t, revoked, "the page itself stays")
	assert.Empty(t, files, "a copy of a document its creator may not download is not listed")
	status, raw := doReq(t, freshClient(t), http.MethodGet, f.srv.URL+"/api/public/s/"+withCopy.Token+"/file/pub:0", nil)
	assert.Equal(t, http.StatusNotFound, status, string(raw))

	revoked, files = state(pageOnly.Token)
	assert.False(t, revoked, "a page that hands over no copy is the app's own content")
	assert.Empty(t, files)
}
