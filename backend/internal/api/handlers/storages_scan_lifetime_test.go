package handlers_test

// A storage's scan belongs to its syncer: "Sync now" says what it did, a save
// that changes nothing the scan reads leaves the scan alone, and a save that
// does change it stops every scan walking the old settings — the ones "Sync
// now" started included.
//
// ⚠ What happened before:
//   - every Save restarted the syncer, so renaming a storage (or pairing a
//     replica, which saves the row) cut a running scan off as "aborted";
//   - a scan started by "Sync now" ran on its own context, which the restart
//     could not reach: it went on walking the OLD root and rules for up to
//     six hours, invisible to Running(id), beside the new syncer's own walk;
//   - "Sync now" answered "started" before the scan held the storage's run
//     lock, so a second press straight after was answered "started" too.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// holdListDriver is a local storage whose listing of its root waits until the
// test lets it go (or the scan's context ends), and says when a walk is in it.
type holdListDriver struct {
	storage.Driver
	root string
}

type listHold struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	// inside counts the walks waiting in the listing right now.
	inside atomic.Int32
}

var listHolds sync.Map // root → *listHold

func init() {
	storage.Register("holdlist-scan-test", func() storage.Driver {
		d, _ := storage.Get("local")
		return &holdListDriver{Driver: d}
	})
}

func (d *holdListDriver) Name() string { return "holdlist-scan-test" }
func (d *holdListDriver) Init(ctx context.Context, cfg map[string]any) error {
	d.root, _ = cfg["root"].(string)
	return d.Driver.Init(ctx, cfg)
}
func (d *holdListDriver) List(ctx context.Context, p string) ([]storage.Object, error) {
	if h, ok := listHolds.Load(d.root); ok && filepath.ToSlash(filepath.Clean("/"+p)) == "/" {
		hold := h.(*listHold)
		hold.inside.Add(1)
		defer hold.inside.Add(-1)
		select {
		case hold.entered <- struct{}{}:
		default:
		}
		select {
		case <-hold.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return d.Driver.List(ctx, p)
}

type scanFixture struct {
	srv    string
	client *http.Client
	store  db.Store
	worker *syncpkg.Worker
	st     *model.Storage
	hold   *listHold
}

func newScanFixture(t *testing.T, mode model.SyncMode) *scanFixture {
	t.Helper()
	var worker *syncpkg.Worker
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) { worker = d.Worker })
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	root := t.TempDir()
	hold := &listHold{entered: make(chan struct{}, 8), release: make(chan struct{})}
	listHolds.Store(root, hold)
	t.Cleanup(func() {
		hold.once.Do(func() { close(hold.release) })
		listHolds.Delete(root)
	})
	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "arsiv", Driver: "holdlist-scan-test", MountPath: "/arsiv", ConfigJSON: cfg,
		SyncMode: mode, SyncIntervalS: 3600, Enabled: true,
	})
	require.NoError(t, err)
	require.NoError(t, worker.AddStorage(context.Background(), st))
	t.Cleanup(worker.Stop)
	return &scanFixture{srv: srv.URL, client: client, store: store, worker: worker, st: st, hold: hold}
}

func (f *scanFixture) waitInside(t *testing.T) {
	t.Helper()
	select {
	case <-f.hold.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no scan reached the storage")
	}
}

func (f *scanFixture) syncNow(t *testing.T) map[string]any {
	t.Helper()
	resp, err := f.client.Post(f.srv+"/api/admin/storages/"+itoa64(f.st.ID)+"/sync", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	out := map[string]any{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func (f *scanFixture) save(t *testing.T, body map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPatch, f.srv+"/api/admin/storages/"+itoa64(f.st.ID), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (f *scanFixture) runs(t *testing.T) []*model.SyncRun {
	t.Helper()
	runs, _, err := f.store.ListSyncRunsAcrossAll(context.Background(), f.st.ID, "", 10, 0)
	require.NoError(t, err)
	return runs
}

func TestTriggerSync_ASecondPressStraightAfterTheFirstSaysRunning(t *testing.T) {
	f := newScanFixture(t, model.SyncModeOnDemand)

	first := f.syncNow(t)
	second := f.syncNow(t)
	assert.Equal(t, "started", first["status"])
	assert.Equal(t, "running", second["status"], "the second press was told a scan started")
	assert.True(t, f.worker.Running(f.st.ID), "the answer came before the scan held the storage")

	f.waitInside(t)
	f.hold.once.Do(func() { close(f.hold.release) })
	require.Eventually(t, func() bool { return !f.worker.Running(f.st.ID) }, 5*time.Second, 10*time.Millisecond)
	assert.Len(t, f.runs(t), 1, "one scan, one row")
}

func TestStorageUpdate_ARenameLeavesTheScanAlone(t *testing.T) {
	f := newScanFixture(t, model.SyncModePoll) // the loop starts a scan at once
	f.waitInside(t)

	f.save(t, map[string]any{"name": "arsiv-yeni"})
	assert.True(t, f.worker.Running(f.st.ID), "renaming the storage stopped its scan")

	f.hold.once.Do(func() { close(f.hold.release) })
	require.Eventually(t, func() bool { return !f.worker.Running(f.st.ID) }, 5*time.Second, 10*time.Millisecond)
	runs := f.runs(t)
	require.Len(t, runs, 1, "a second scan was started")
	assert.Equal(t, "ok", runs[0].Status)
}

func TestStorageUpdate_ANewScanSettingStopsTheScanSyncNowStarted(t *testing.T) {
	f := newScanFixture(t, model.SyncModeOnDemand)
	assert.Equal(t, "started", f.syncNow(t)["status"])
	f.waitInside(t)

	cfg := map[string]any{}
	require.NoError(t, json.Unmarshal(f.st.ConfigJSON, &cfg))
	cfg["scan_exclude"] = []string{"tmp"}
	f.save(t, map[string]any{"config": cfg})

	require.Eventually(t, func() bool {
		runs := f.runs(t)
		return len(runs) == 1 && runs[0].Status == "aborted"
	}, 5*time.Second, 20*time.Millisecond, "the scan went on walking the old settings")
	assert.True(t, f.worker.Known(f.st.ID), "the storage's syncer is back with the new settings")
}

func TestStorageDelete_StopsTheScanSyncNowStarted(t *testing.T) {
	f := newScanFixture(t, model.SyncModeOnDemand)
	assert.Equal(t, "started", f.syncNow(t)["status"])
	f.waitInside(t)

	req, _ := http.NewRequest(http.MethodDelete, f.srv+"/api/admin/storages/"+itoa64(f.st.ID), nil)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// The hold stays shut: only the scan's own context can get it out.
	require.Eventually(t, func() bool { return f.hold.inside.Load() == 0 }, 5*time.Second, 20*time.Millisecond,
		"a scan of the deleted storage is still walking")
}
