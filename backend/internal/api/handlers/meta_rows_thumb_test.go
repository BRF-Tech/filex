package handlers_test

// Recent / Starred / tag rows carry a `thumb_url` exactly when the thumbnail
// endpoint would serve an image — the folder listing's rule — and never
// otherwise.
//
// ⚠⚠ Measured 2026-09-21: the client built `/api/files/thumb/<id>` for EVERY
// file on these rows, so opening Recent with ten files (docx, markdown,
// drawio, images the pipeline had not rendered yet) fired ten thumbnail
// requests and all ten came back `404 "not ready"`. The server now says which
// rows have an image; a pending or failed one, and a file type nobody renders,
// is not asked about.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestMetaLists_ThumbURLOnlyWhereThereIsAnImage(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	ctx := context.Background()
	admin, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)

	cfg, err := json.Marshal(map[string]any{"root": t.TempDir()})
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "thumbs", Driver: "local", MountPath: "/thumbs",
		ConfigJSON: cfg, SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)

	mk := func(name, mime string) *model.Node {
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: name, Path: name, Type: model.NodeTypeFile,
			Size: 3, Etag: name, Mime: mime,
			PathHash: mutTestPathHash(st.ID, name), SeenAt: time.Now(),
		})
		require.NoError(t, err)
		require.NoError(t, store.SetUserNodeMeta(ctx, admin.ID, n.ID, "starred", "1"))
		require.NoError(t, store.SetUserNodeMeta(ctx, admin.ID, n.ID, "last_opened", "1758240000"))
		testutil.TagNode(t, store, n.ID, 0, "thumbs")
		return n
	}
	ready := mk("ready.jpg", "image/jpeg")
	pending := mk("pending.jpg", "image/jpeg")
	failed := mk("failed.jpg", "image/jpeg")
	mk("letter.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{NodeID: ready.ID, State: "ready", StorageKey: "k/ready.jpg"}))
	require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{NodeID: pending.ID, State: "pending"}))
	require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{NodeID: failed.ID, State: "failed", Error: "boom"}))

	for _, path := range []string{
		"/api/files/manager/star/list?limit=10",
		"/api/files/manager/recent?limit=10",
		"/api/files/manager/tagged?tag=thumbs",
	} {
		t.Run(path, func(t *testing.T) {
			resp, err := client.Get(srv.URL + path)
			require.NoError(t, err)
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
			var body struct {
				Nodes []struct {
					Name     string `json:"name"`
					ThumbURL string `json:"thumb_url"`
				} `json:"nodes"`
			}
			require.NoError(t, json.Unmarshal(raw, &body))
			got := map[string]string{}
			for _, n := range body.Nodes {
				got[n.Name] = n.ThumbURL
			}
			require.Len(t, got, 4, string(raw))
			assert.Contains(t, got["ready.jpg"], "/api/files/thumb/", "a rendered thumbnail is offered")
			for _, name := range []string{"pending.jpg", "failed.jpg", "letter.docx"} {
				assert.Empty(t, got[name], "%s: no image behind it, so no URL to ask", name)
			}
			// ⚠ The internal record stays internal: no storage key on the wire.
			assert.NotContains(t, string(raw), "k/ready.jpg")
		})
	}
}
