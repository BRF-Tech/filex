package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// Issue #21: renaming a folder from the web UI moved its CONTENTS to the
// trash, and the next storage sync then filled the log with
// `duplicate key value violates unique constraint idx_nodes_storage_parent_name`.
//
// The cause is one row versus a subtree. The rename updated the folder's own
// node and left every descendant row carrying the OLD path: the sync worker
// then walks the storage, cannot find those paths any more and tombstones them
// (they appear in trash), while the files it DOES find at the new path have no
// row — so it tries to create one, and collides with the live descendant row
// that is still sitting under the same parent with the same name.
//
// ⚠ Every other write surface already does this correctly:
// protocolsync.Syncer.MoveRows re-homes the subtree, and its own comment says
// why. WebDAV, SFTP, S3, NFS and the AI/MCP tools were pointed at it; the HTTP
// manager — the surface a person actually clicks — was not.
func renameFixture(t *testing.T) (*httptest.Server, *http.Client, db.Store, int64, string) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Leon", "Belgeler"), 0o755))
	for _, f := range []string{
		filepath.Join(root, "Leon", "not.txt"),
		filepath.Join(root, "Leon", "Belgeler", "rapor.pages"),
	} {
		require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	}

	drv, err := storage.Get("local")
	require.NoError(t, err)
	require.NoError(t, drv.Init(context.Background(), map[string]any{"path": root}))

	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.StorageResolver = func(int64) (storage.Driver, error) { return drv, nil }
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "files", Driver: "local", MountPath: "/",
		ConfigJSON: json.RawMessage(`{"path":"` + filepath.ToSlash(root) + `"}`),
		SyncMode:   model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)

	// The rows a sync would have produced: the folder, a file in it, a
	// subfolder and a file in that.
	mk := func(p string, kind model.NodeType, parent *int64) int64 {
		n, err := store.CreateNode(context.Background(), &model.Node{
			StorageID: st.ID, ParentID: parent, Name: p[strings.LastIndex(p, "/")+1:],
			Path: p, PathHash: pathkey.Hash(st.ID, p), Type: kind,
		})
		require.NoError(t, err)
		return n.ID
	}
	folder := mk("/Leon", model.NodeTypeDirectory, nil)
	mk("/Leon/not.txt", model.NodeTypeFile, &folder)
	sub := mk("/Leon/Belgeler", model.NodeTypeDirectory, &folder)
	mk("/Leon/Belgeler/rapor.pages", model.NodeTypeFile, &sub)

	return srv, client, store, st.ID, root
}

func TestManagerRename_MovesTheWholeSubtree(t *testing.T) {
	srv, client, store, storageID, _ := renameFixture(t)
	ctx := context.Background()

	body, _ := json.Marshal(map[string]any{
		"path": "files://",
		"item": "files:///Leon",
		"name": "Leonid",
	})
	resp, err := client.Post(srv.URL+"/api/files/manager?action=rename", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// The folder itself moved — that part always worked.
	moved, err := store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, "/Leonid"))
	require.NoError(t, err)
	require.Equal(t, "Leonid", moved.Name)

	// …and so did everything under it. Before the fix these three rows still
	// said /Leon/…, which is what sent them to the trash on the next sync.
	for _, p := range []string{"/Leonid/not.txt", "/Leonid/Belgeler", "/Leonid/Belgeler/rapor.pages"} {
		n, err := store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, p))
		require.NoError(t, err, "no row at %s — the descendant was left behind", p)
		require.NotNil(t, n)
		require.Nil(t, n.DeletedAt, "%s must not be in the trash", p)
	}

	// And nothing is left pointing at the old prefix, which is the row a
	// re-sync would collide with.
	for _, p := range []string{"/Leon/not.txt", "/Leon/Belgeler", "/Leon/Belgeler/rapor.pages"} {
		n, _ := store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, p))
		require.Nil(t, n, "a live row still sits at the old path %s", p)
	}
}

// The same rename, one level down: a move that changes parent must carry the
// subtree too, and must not leave the old rows behind.
func TestManagerMove_CarriesTheSubtreeAcrossFolders(t *testing.T) {
	srv, client, store, storageID, root := renameFixture(t)
	ctx := context.Background()

	require.NoError(t, os.MkdirAll(filepath.Join(root, "Arsiv"), 0o755))
	_, err := store.CreateNode(ctx, &model.Node{
		StorageID: storageID, Name: "Arsiv", Path: "/Arsiv",
		PathHash: pathkey.Hash(storageID, "/Arsiv"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)

	// `path` is the DESTINATION directory; `items` are the sources.
	body, _ := json.Marshal(map[string]any{
		"path":  "files:///Arsiv",
		"items": []map[string]string{{"path": "files:///Leon"}},
	})
	resp, err := client.Post(srv.URL+"/api/files/manager?action=move", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	for _, p := range []string{"/Arsiv/Leon", "/Arsiv/Leon/not.txt", "/Arsiv/Leon/Belgeler/rapor.pages"} {
		n, err := store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, p))
		require.NoError(t, err, "no row at %s", p)
		require.Nil(t, n.DeletedAt, "%s must not be in the trash", p)
	}
	stale, _ := store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, "/Leon/not.txt"))
	require.Nil(t, stale, "a live row still sits at the old path")
}

// The second half of issue #21's report: pressing Sync on a large storage
// showed "30000 milliseconds exceeded". The run used to happen inside the HTTP
// request, so the browser's own timeout both reported a failure for a sync
// that was fine AND cancelled the context the walk was using, stopping it
// halfway. The endpoint answers immediately now.
func TestTriggerSync_AnswersImmediatelyAndRunsInTheBackground(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "rapor.txt"), []byte("x"), 0o644))

	drv, err := storage.Get("local")
	require.NoError(t, err)
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))

	var worker *filexsync.Worker
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.StorageResolver = func(int64) (storage.Driver, error) { return drv, nil }
		worker = d.Worker
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "files", Driver: "local", MountPath: "/",
		ConfigJSON: json.RawMessage(`{"path":"` + filepath.ToSlash(root) + `"}`),
		SyncMode:   model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	require.NotNil(t, worker, "the harness must expose the sync worker")
	require.NoError(t, worker.AddStorage(ctx, st))

	resp, err := client.Post(
		srv.URL+"/api/admin/storages/"+strconv.FormatInt(st.ID, 10)+"/sync",
		"application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()

	// 202: accepted and running, not "finished".
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, "started", out["status"])

	// …and it really ran, after the request was over.
	require.Eventually(t, func() bool {
		run, err := store.GetLastSyncRun(ctx, st.ID)
		return err == nil && run != nil && run.Status == "ok"
	}, 10*time.Second, 100*time.Millisecond, "the detached run never finished")

	// A storage the worker does not know is still an immediate 404 rather than
	// a goroutine nobody is waiting for.
	missing, err := client.Post(srv.URL+"/api/admin/storages/99999/sync", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer missing.Body.Close()
	require.Equal(t, http.StatusNotFound, missing.StatusCode)
}
