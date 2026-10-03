package handlers_test

// A folder's mosaic in the listing (`preview` on a folder row): the first
// pictures inside it that have a thumbnail, at most four, never a text file,
// and a picture without a thumbnail yet is sent to be drawn.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

func TestListing_FolderPreviews(t *testing.T) {
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
				return drv, drv.Init(context.Background(), cfg)
			}
			d.Thumbs = thumb.New(d.Store, cacheDir, thumb.Capabilities{Image: true, SVG: true})
			d.Thumbs.AttachSettings(d.Store)
			refresh = thumb.NewRefresher(d.Thumbs, d.Store, 128)
			d.ThumbRefresh = refresh
		})
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)

	root := t.TempDir()
	cfg, _ := json.Marshal(map[string]any{"root": root})
	synced := time.Now().Add(-time.Hour)
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "foto", Driver: "local", MountPath: "/foto", ConfigJSON: cfg,
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true, LastSyncAt: &synced})
	require.NoError(t, err)
	now := time.Now().Add(-time.Minute)
	dir := func(name string) *model.Node {
		require.NoError(t, os.MkdirAll(filepath.Join(root, name), 0o755))
		n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, Name: name, Path: "/" + name,
			PathHash: mutTestPathHash(st.ID, "/"+name), Type: model.NodeTypeDirectory, SeenAt: time.Now()})
		require.NoError(t, err)
		return n
	}
	// base: every file's own time is an hour after this, plus its step in
	// minutes, so the newest is the one with the largest step.
	base := time.Now().Add(time.Hour)
	file := func(parent *model.Node, name string, step int, ready bool) *model.Node {
		p := parent.Path + "/" + name
		require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(parent.Path, "/")), name), []byte("x"), 0o644))
		mt := base.Add(time.Duration(step) * time.Minute)
		n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, ParentID: &parent.ID, Name: name, Path: p,
			PathHash: mutTestPathHash(st.ID, p), Type: model.NodeTypeFile, Size: 1, Etag: "e-" + name, SeenAt: time.Now(), BackendMtime: &mt})
		require.NoError(t, err)
		if ready {
			require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{NodeID: n.ID, State: "ready", StorageKey: "k",
				GeneratedAt: &now, AttemptedAt: &now, SourceSig: n.ContentFingerprint()}))
		}
		return n
	}
	sub := func(parent *model.Node, name string) *model.Node {
		p := parent.Path + "/" + name
		require.NoError(t, os.MkdirAll(filepath.Join(root, strings.TrimPrefix(parent.Path, "/"), name), 0o755))
		n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, ParentID: &parent.ID, Name: name, Path: p,
			PathHash: mutTestPathHash(st.ID, p), Type: model.NodeTypeDirectory, SeenAt: time.Now()})
		require.NoError(t, err)
		return n
	}
	tatil := dir("Tatil")
	file(tatil, "a.jpg", 1, true)
	file(tatil, "c.svg", 0, true)
	file(tatil, "b.png", 2, true)
	file(tatil, "notlar.txt", 3, true)
	docs := dir("Belgeler")
	file(docs, "rapor.txt", 1, true)
	many := dir("Karisik")
	karisik := map[int]*model.Node{}
	for i := 1; i <= 6; i++ {
		// 05 is shown with no thumbnail; 02, not drawn either, is not shown.
		n := file(many, fmt.Sprintf("%02d.jpg", i), i, i != 5 && i != 2)
		karisik[i] = n
	}
	fresh := dir("Yeni")
	waiting := file(fresh, "ilk.jpg", 1, false)
	// A folder that holds only a folder: its subfolder's newest file is not
	// its own.
	ust := dir("Ust")
	file(sub(ust, "Alt"), "derin.jpg", 9, true)

	type shown struct{ name, url string }
	listed := func() map[string][]shown {
		status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/files/manager?action=index&path=foto://", nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
		previews := map[string][]shown{}
		for _, raw := range body["files"].([]any) {
			f := raw.(map[string]any)
			var items []shown
			if p, ok := f["preview"].([]any); ok {
				for _, x := range p {
					item := x.(map[string]any)
					u, _ := item["thumb_url"].(string)
					items = append(items, shown{item["name"].(string), u})
				}
			}
			previews[f["basename"].(string)] = items
		}
		return previews
	}
	names := func(items []shown) []string {
		var out []string
		for _, it := range items {
			out = append(out, it.name)
		}
		return out
	}
	previews := listed()
	require.Equal(t, []string{"notlar.txt", "b.png", "a.jpg"}, names(previews["Tatil"]), "the three newest, newest first, a text among them")
	require.Equal(t, []string{"rapor.txt"}, names(previews["Belgeler"]), "a folder of text shows its text")
	require.Equal(t, []string{"06.jpg", "05.jpg", "04.jpg"}, names(previews["Karisik"]), "the newest three, drawn or not")
	require.NotEmpty(t, previews["Karisik"][0].url)
	require.Empty(t, previews["Karisik"][1].url, "a file with no thumbnail yet is shown as its type icon")
	require.NotEmpty(t, previews["Karisik"][2].url)
	require.Equal(t, []string{"ilk.jpg"}, names(previews["Yeni"]))
	require.True(t, refresh.Queued(waiting.ID), "a shown file with no thumbnail yet is sent to be drawn")
	require.True(t, refresh.Queued(karisik[5].ID))
	require.False(t, refresh.Queued(karisik[2].ID), "only the files shown are asked for")
	require.Empty(t, previews["Ust"], "a folder of folders shows nothing: its subfolders are not looked into")

	capsOn := func() any {
		status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/files/capabilities", nil)
		require.Equal(t, http.StatusOK, status)
		return body["folder_previews"]
	}
	require.Equal(t, true, capsOn(), "on by default, and the explorer is told")

	// The administrator turns folder previews off: listings send no pictures
	// at once (the pipeline's cached switch is dropped), and an explorer that
	// loads is told, so it does not peek either.
	status, body := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/tools/thumbnails/settings",
		map[string]any{"folder_previews": false})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Equal(t, false, body["folder_previews"])
	for name, p := range listed() {
		require.Empty(t, p, "%s still has pictures with folder previews off", name)
	}
	require.Equal(t, false, capsOn())

	status, _ = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/tools/thumbnails/settings",
		map[string]any{"folder_previews": true})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []string{"notlar.txt", "b.png", "a.jpg"}, names(listed()["Tatil"]), "and back on")
}
