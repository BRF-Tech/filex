package handlers_test

// POST /api/admin/storages/{id}/sync?path=<folder> rescans one catalogued
// folder instead of the whole storage. Without it the only way to make the
// catalogue look at a storage again was a full scan (169k rows, 23 minutes,
// seen_at rewritten on every one of them) to learn about one folder.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// rescanFixture is the admin API over one local storage the worker knows.
type rescanFixture struct {
	srv    string
	client *http.Client
	store  db.Store
	worker *syncpkg.Worker
	st     *model.Storage
	root   string
}

func newRescanFixture(t *testing.T, driver string) *rescanFixture {
	t.Helper()
	var worker *syncpkg.Worker
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) { worker = d.Worker })
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	root := t.TempDir()
	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "arsiv", Driver: driver, MountPath: "/arsiv", ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	require.NoError(t, worker.AddStorage(context.Background(), st))
	t.Cleanup(worker.Stop)
	return &rescanFixture{srv: srv.URL, client: client, store: store, worker: worker, st: st, root: root}
}

func (f *rescanFixture) write(t *testing.T, rel, body string) {
	t.Helper()
	abs := filepath.Join(f.root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
}

func (f *rescanFixture) sync(t *testing.T, folder string) (int, map[string]any) {
	t.Helper()
	u := f.srv + "/api/admin/storages/" + itoa64(f.st.ID) + "/sync"
	if folder != "" {
		u += "?path=" + url.QueryEscape(folder)
	}
	return doJSON(t, f.client, http.MethodPost, u, nil)
}

func (f *rescanFixture) row(t *testing.T, p string) *model.Node {
	t.Helper()
	n, err := f.store.GetNodeByPath(context.Background(), f.st.ID, pathkey.Hash(f.st.ID, p))
	if err != nil {
		return nil
	}
	return n
}

func TestTriggerSync_RescansOneFolder(t *testing.T) {
	f := newRescanFixture(t, "local")
	f.write(t, "docs/a.txt", "a")
	f.write(t, "docs/b.txt", "b")
	f.write(t, "docs/c.txt", "c")
	f.write(t, "other/x.txt", "x")
	require.NoError(t, f.worker.Trigger(context.Background(), f.st.ID)) // the full scan that built the catalogue
	runs, err := f.store.ListSyncRuns(context.Background(), f.st.ID, 50)
	require.NoError(t, err)

	f.write(t, "docs/new.txt", "new")
	f.write(t, "other/new.txt", "new")
	code, body := f.sync(t, "/docs/")
	require.Equal(t, http.StatusOK, code, "%v", body)
	assert.Equal(t, "/docs", body["path"])
	assert.EqualValues(t, 4, body["scanned"])
	assert.EqualValues(t, 1, body["added"])
	assert.EqualValues(t, 0, body["updated"])
	assert.EqualValues(t, 0, body["removed"])
	assert.EqualValues(t, 0, body["reconciled"])

	assert.NotNil(t, f.row(t, "/docs/new.txt"))
	assert.Nil(t, f.row(t, "/other/new.txt"), "only the folder asked for is rescanned")
	after, err := f.store.ListSyncRuns(context.Background(), f.st.ID, 50)
	require.NoError(t, err)
	assert.Len(t, after, len(runs), "a folder rescan records no sync run")
}

func TestTriggerSync_FolderRescanAnswersWhatItCannotDo(t *testing.T) {
	f := newRescanFixture(t, "local")
	f.write(t, "docs/a.txt", "a")
	require.NoError(t, f.worker.Trigger(context.Background(), f.st.ID))

	for _, c := range []struct {
		folder string
		code   int
	}{
		{"../etc", http.StatusBadRequest},
		{"docs/../../x", http.StatusBadRequest},
		{".versions", http.StatusBadRequest},
		{"/.filex-trash/x", http.StatusBadRequest},
		{"docs/a.txt", http.StatusBadRequest}, // not a folder
		{"yok", http.StatusNotFound},          // not catalogued
		{"docs/yok/alt", http.StatusNotFound}, // neither
	} {
		code, body := f.sync(t, c.folder)
		assert.Equal(t, c.code, code, "path=%q: %v", c.folder, body)
		assert.NotEmpty(t, body["error"], "path=%q must say why", c.folder)
	}

	// "/" is the whole storage: exactly the full scan it always was.
	code, body := f.sync(t, "/")
	assert.Equal(t, http.StatusAccepted, code, "%v", body)
	assert.Equal(t, "started", body["status"])
	require.Eventually(t, func() bool { return !f.worker.Running(f.st.ID) }, 5*time.Second, 10*time.Millisecond)
}

// ── a folder rescan that runs out of time ───────────────────────────────────

// slowListDriver is a local storage whose listing of /yavas/ic never answers.
type slowListDriver struct{ storage.Driver }

func init() {
	storage.Register("slowlist-rescan-test", func() storage.Driver {
		d, _ := storage.Get("local")
		return &slowListDriver{Driver: d}
	})
}

func (d *slowListDriver) Name() string { return "slowlist-rescan-test" }
func (d *slowListDriver) List(ctx context.Context, p string) ([]storage.Object, error) {
	if filepath.ToSlash(filepath.Clean("/"+p)) == "/yavas/ic" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return d.Driver.List(ctx, p)
}

func TestTriggerSync_FolderRescanOverItsTimeLimitAnswers504(t *testing.T) {
	f := newRescanFixture(t, "slowlist-rescan-test")
	f.write(t, "yavas/a.txt", "a")
	f.write(t, "yavas/ic/b.txt", "b")
	seed := func(p string, typ model.NodeType, parent *int64) *model.Node {
		n, err := f.store.CreateNode(context.Background(), &model.Node{
			StorageID: f.st.ID, ParentID: parent, Name: filepath.Base(p), Path: p,
			PathHash: pathkey.Hash(f.st.ID, p), StorageKey: p, Type: typ,
		})
		require.NoError(t, err)
		return n
	}
	dir := seed("/yavas", model.NodeTypeDirectory, nil)
	seed("/yavas/ic", model.NodeTypeDirectory, &dir.ID)

	was := handlers.ScopedRescanTimeout
	handlers.ScopedRescanTimeout = 200 * time.Millisecond
	t.Cleanup(func() { handlers.ScopedRescanTimeout = was })

	code, body := f.sync(t, "yavas")
	require.Equal(t, http.StatusGatewayTimeout, code, "%v", body)
	assert.NotEmpty(t, body["error"])
	assert.EqualValues(t, 1, body["added"], "the counts so far: a.txt was catalogued before the time ran out")
	assert.EqualValues(t, 0, body["removed"], "nothing is removed on a partial view")
	assert.NotNil(t, f.row(t, "/yavas/a.txt"))
	assert.False(t, f.worker.Running(f.st.ID), "the run lock is released")
}
