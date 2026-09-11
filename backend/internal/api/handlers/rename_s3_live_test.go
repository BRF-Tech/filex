package handlers_test

// Issue #21 as it was reported: on an S3 storage, catalogued by the sync
// worker, renamed from the web UI, synced again.
//
// The unit tests cover the same defect over the local driver, which is where
// the bug lived — the catalogue, not the driver. This one is the end-to-end
// shape the reporter actually had, against a real S3 server, because "we fixed
// the code path" and "we watched it work on S3" are different claims.
//
//	FILEX_TEST_S3_ENDPOINT=http://127.0.0.1:9000 FILEX_TEST_S3_BUCKET=renametest \
//	FILEX_TEST_S3_ACCESS_KEY=… FILEX_TEST_S3_SECRET_KEY=… \
//	  go test ./internal/api/handlers/ -run TestRenameOnS3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/s3"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestRenameOnS3_TheReportersSequence(t *testing.T) {
	bucket := os.Getenv("FILEX_TEST_S3_BUCKET")
	endpoint := os.Getenv("FILEX_TEST_S3_ENDPOINT")
	access := os.Getenv("FILEX_TEST_S3_ACCESS_KEY")
	secret := os.Getenv("FILEX_TEST_S3_SECRET_KEY")
	if bucket == "" || access == "" || secret == "" {
		t.Skip("set FILEX_TEST_S3_* to run the rename against a real S3 server")
	}
	ctx := context.Background()

	drv, err := storage.Get("s3")
	require.NoError(t, err)
	cfg := map[string]any{
		"bucket": bucket, "endpoint": endpoint,
		"access_key": access, "secret_key": secret, "path_style": true,
	}
	require.NoError(t, drv.Init(ctx, cfg))

	// A folder with a file in it and a subfolder with another — under a prefix
	// of this run's own, so repeated runs do not collide.
	prefix := fmt.Sprintf("run-%d", time.Now().UnixNano())
	w, ok := drv.(storage.Writer)
	require.True(t, ok)
	for _, p := range []string{
		prefix + "/Leon/not.txt",
		prefix + "/Leon/Belgeler/rapor.pages",
	} {
		require.NoError(t, w.Write(ctx, p, strings.NewReader("x"), 1))
	}

	var worker *filexsync.Worker
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.StorageResolver = func(int64) (storage.Driver, error) { return drv, nil }
		worker = d.Worker
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	raw, _ := json.Marshal(cfg)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "garage", Driver: "s3", MountPath: "/",
		ConfigJSON: raw, SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	require.NoError(t, worker.AddStorage(ctx, st))

	// 1. Catalogue it, the way an install does.
	require.NoError(t, worker.Trigger(ctx, st.ID))
	before, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/"+prefix+"/Leon/not.txt"))
	require.NoError(t, err, "the sync must have catalogued the file")

	// 2. Rename the folder from the web UI.
	body, _ := json.Marshal(map[string]any{
		"path": "garage:///" + prefix,
		"item": "garage:///" + prefix + "/Leon",
		"name": "Leonid",
	})
	resp, err := client.Post(srv.URL+"/api/files/manager?action=rename", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// 3. Sync again — the pass that used to fill the log with duplicate-key
	//    warnings and leave the contents in the trash.
	require.NoError(t, worker.Trigger(ctx, st.ID))
	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", run.Status, run.Error)

	// The catalogue followed the rename, one row per object, same identities.
	for _, p := range []string{
		"/" + prefix + "/Leonid/not.txt",
		"/" + prefix + "/Leonid/Belgeler",
		"/" + prefix + "/Leonid/Belgeler/rapor.pages",
	} {
		n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, p))
		require.NoError(t, err, "no row at %s", p)
		require.Nil(t, n.DeletedAt, "%s is in the trash", p)
	}
	after, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/"+prefix+"/Leonid/not.txt"))
	require.NoError(t, err)
	require.Equal(t, before.ID, after.ID, "the file kept its identity across the rename")

	// Nothing left at the old prefix — neither in the catalogue…
	stale, _ := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/"+prefix+"/Leon/not.txt"))
	require.Nil(t, stale)

	// …nor in the bucket: the objects really moved.
	_, err = drv.Stat(ctx, prefix+"/Leonid/not.txt")
	require.NoError(t, err, "the object is not at the new key")
	if _, err := drv.Stat(ctx, prefix+"/Leon/not.txt"); err == nil {
		t.Fatal("the object is still at the old key on the storage")
	}

	t.Cleanup(func() {
		if d, ok := drv.(storage.Deleter); ok {
			for _, p := range []string{
				prefix + "/Leonid/not.txt", prefix + "/Leonid/Belgeler/rapor.pages",
			} {
				_ = d.Delete(context.Background(), p)
			}
		}
	})
}
