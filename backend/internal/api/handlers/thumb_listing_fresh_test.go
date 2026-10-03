package handlers_test

// A listing hands every file's thumbnail row to the refresher, which draws
// what is missing or stale in the background (docs/thumbnails.md, Design
// notes). What the listing itself must do: carry the old picture of a stale
// file (with a version the browser cache cannot confuse with the next one),
// queue exactly the files that need drawing, and never wait for them.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

func TestListing_HandsMissingAndStaleThumbnailsToTheRefresher(t *testing.T) {
	ctx := context.Background()
	cacheDir := t.TempDir()
	var refresh *thumb.Refresher
	srv, client, store := testutil.NewTestServerWith(t,
		func(c *config.Config) { c.Thumbs.CacheDir = cacheDir },
		func(d *api.Deps) {
			st := d.Store
			d.StorageResolver = func(id int64) (storage.Driver, error) {
				row, err := st.GetStorage(context.Background(), id)
				if err != nil || row == nil {
					return nil, fmt.Errorf("unknown storage %d", id)
				}
				var cfg map[string]any
				if err := json.Unmarshal(row.ConfigJSON, &cfg); err != nil {
					return nil, err
				}
				drv := &local.Driver{}
				if err := drv.Init(context.Background(), cfg); err != nil {
					return nil, err
				}
				return drv, nil
			}
			d.Thumbs = thumb.New(d.Store, cacheDir, thumb.Capabilities{Image: true})
			// Not running: what is queued stays queued, so the test reads the
			// listing's decisions and nothing else.
			refresh = thumb.NewRefresher(d.Thumbs, d.Store, 64)
			d.ThumbRefresh = refresh
		})
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)

	root := t.TempDir()
	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "fresh", Driver: "local", MountPath: "/fresh", ConfigJSON: cfg,
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	now := time.Now().Add(-time.Hour)
	mk := func(name, etag string, row *model.Thumbnail) *model.Node {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644))
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: name, Path: name, Type: model.NodeTypeFile,
			Size: 1, Etag: etag, Mime: "image/png", PathHash: mutTestPathHash(st.ID, name), SeenAt: time.Now(),
		})
		require.NoError(t, err)
		if row != nil {
			row.NodeID = n.ID
			require.NoError(t, os.WriteFile(filepath.Join(cacheDir, fmt.Sprint(n.ID)+".jpg"), []byte("\xff\xd8jpeg"), 0o644))
			require.NoError(t, store.UpsertThumbnail(ctx, row))
		}
		return n
	}
	fresh := mk("taze.png", "e1", &model.Thumbnail{State: "ready", StorageKey: "k", GeneratedAt: &now, SourceSig: "e1", AttemptedAt: &now})
	stale := mk("bayat.png", "e2", &model.Thumbnail{State: "ready", StorageKey: "k", GeneratedAt: &now, SourceSig: "old", AttemptedAt: &now})
	missing := mk("yok.png", "e3", nil)
	failedHere := mk("bozuk.png", "e4", &model.Thumbnail{State: "failed", SourceSig: "e4", AttemptedAt: &now})

	status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/files/manager?action=index&path=fresh://", nil)
	require.Equal(t, http.StatusOK, status, "%s", body)
	files, _ := body["files"].([]any)
	urls := map[string]string{}
	for _, raw := range files {
		f, _ := raw.(map[string]any)
		if u, ok := f["thumb_url"].(string); ok {
			urls[f["basename"].(string)] = u
		}
	}

	// The fresh file and the stale one both carry a picture: a stale file
	// keeps its old one on screen until the new one is drawn.
	require.Contains(t, urls, "taze.png")
	require.Contains(t, urls, "bayat.png")
	require.NotContains(t, urls, "yok.png")
	require.NotContains(t, urls, "bozuk.png")
	u, err := url.Parse(urls["taze.png"])
	require.NoError(t, err)
	require.Equal(t, fmt.Sprint(now.UnixMilli()), u.Query().Get("v"),
		"the URL names the render, so a new render is a new URL for the browser cache")

	require.False(t, refresh.Queued(fresh.ID), "a fresh thumbnail is not drawn again")
	require.True(t, refresh.Queued(stale.ID), "a stale thumbnail is drawn again")
	require.True(t, refresh.Queued(missing.ID), "a file never drawn is drawn (issue #79)")
	require.False(t, refresh.Queued(failedHere.ID), "a failure on this very content is not retried by every listing")
}
