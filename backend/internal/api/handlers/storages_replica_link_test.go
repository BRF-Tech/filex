package handlers_test

// Linking a storage to a replication target, and editing a target, take
// effect in the running process (#186).
//
// ⚠ What happened before: a save that changed only `replica_target_id` left
// the resolver's cached driver alone ("the replica pairing is read from the
// row where it is used" - nothing read it), a target edited or deleted kept
// its storages' drivers as they were, a PATCH of a target replaced the whole
// row with the body (`{"enabled": false}` wiped its name, driver and
// configuration), and "Fix all" ran on a Service with no wrapper.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/replica"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// linkRecorder is a handlers.ReplicaLinks that writes down what it is told.
type linkRecorder struct {
	mu       sync.Mutex
	storages []int64
	targets  []targetCall
	folders  []int64
}

type targetCall struct {
	target   int64
	storages []int64
	restart  bool
}

func (l *linkRecorder) StorageLinkChanged(_ context.Context, id int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.storages = append(l.storages, id)
}

func (l *linkRecorder) TargetChanged(_ context.Context, id int64, storages []int64, restart bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.targets = append(l.targets, targetCall{target: id, storages: storages, restart: restart})
}

func (l *linkRecorder) FolderChanged(_ context.Context, id int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.folders = append(l.folders, id)
}

func (l *linkRecorder) lastTarget(t *testing.T) targetCall {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	require.NotEmpty(t, l.targets, "the target edit told the running process nothing")
	return l.targets[len(l.targets)-1]
}

// okQueue takes every op.
type okQueue struct{ queue.Driver }

func (okQueue) Enqueue(context.Context, queue.Op) (string, error) { return "op", nil }

type linkFixture struct {
	srv       string
	client    *http.Client
	store     db.Store
	worker    *syncpkg.Worker
	links     *linkRecorder
	forgotten func() []int64
}

func newLinkFixture(t *testing.T) *linkFixture {
	t.Helper()
	f := &linkFixture{links: &linkRecorder{}}
	var mu sync.Mutex
	var forgotten []int64
	f.forgotten = func() []int64 {
		mu.Lock()
		defer mu.Unlock()
		return append([]int64(nil), forgotten...)
	}
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		f.worker = d.Worker
		d.ForgetStorage = func(id int64) {
			mu.Lock()
			forgotten = append(forgotten, id)
			mu.Unlock()
		}
		d.ReplicaLinks = f.links
		d.ReplicaService = replica.New(d.Store, nil, okQueue{}, nil)
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	f.srv, f.client, f.store = srv.URL, client, store
	return f
}

func (f *linkFixture) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, f.srv+path, rd)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (f *linkFixture) target(t *testing.T, name string) *model.ReplicationTarget {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"path": t.TempDir()})
	rt, err := f.store.CreateReplicationTarget(context.Background(), &model.ReplicationTarget{
		Name: name, Driver: "local", ConfigJSON: cfg, Mode: "async", Enabled: true,
	})
	require.NoError(t, err)
	return rt
}

func (f *linkFixture) storage(t *testing.T, name string, target *int64) *model.Storage {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"path": t.TempDir()})
	st, err := f.store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: "local", MountPath: "/" + name, ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: true, ReplicaTargetID: target,
	})
	require.NoError(t, err)
	return st
}

func TestStorageUpdate_LinkingATargetRebuildsTheDriverAndStartsTheCopy(t *testing.T) {
	f := newLinkFixture(t)
	rt := f.target(t, "storage-box")
	st := f.storage(t, "arsiv", nil)
	require.NoError(t, f.worker.AddStorage(context.Background(), st))
	t.Cleanup(f.worker.Stop)

	code, _ := f.do(t, http.MethodPatch, "/api/admin/storages/"+strconv.FormatInt(st.ID, 10),
		map[string]any{"replica_target_id": rt.ID})
	require.Equal(t, http.StatusOK, code)

	assert.Contains(t, f.forgotten(), st.ID,
		"linking a target kept the bare driver: nothing the storage is sent reaches the target")
	assert.Contains(t, f.links.storages, st.ID, "linking a target started no initial copy")
	assert.True(t, f.worker.Known(st.ID), "linking a target took the storage's syncer away")

	// Relinking to another target is a change too - the body's id lands in
	// the same int64 the stored one is read into, which once made a relink
	// look like no change at all.
	other := f.target(t, "storage-box-2")
	before := len(f.forgotten())
	code, _ = f.do(t, http.MethodPatch, "/api/admin/storages/"+strconv.FormatInt(st.ID, 10),
		map[string]any{"replica_target_id": other.ID})
	require.Equal(t, http.StatusOK, code)
	assert.Greater(t, len(f.forgotten()), before, "a relink kept fanning out to the old target")

	// Unlinking is a change too.
	before = len(f.forgotten())
	code, _ = f.do(t, http.MethodPatch, "/api/admin/storages/"+strconv.FormatInt(st.ID, 10),
		map[string]any{"replica_target_id": nil})
	require.Equal(t, http.StatusOK, code)
	assert.Greater(t, len(f.forgotten()), before, "unlinking kept the wrapper: writes still fan out")
	got, err := f.store.GetStorage(context.Background(), st.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ReplicaTargetID)
}

// A storage created already linked stays linked. The store's create wrote
// every column but replica_target_id, so the answer, the row and the running
// process all saw an unlinked storage: no wrapper, no initial copy, nothing
// written to it ever reached the target.
func TestStorageCreate_ALinkGivenAtCreateIsKept(t *testing.T) {
	f := newLinkFixture(t)
	rt := f.target(t, "storage-box")
	code, body := f.do(t, http.MethodPost, "/api/admin/storages", map[string]any{
		"name": "arsiv", "driver": "local", "mount_path": "/arsiv", "enabled": false, "sync_mode": "ondemand",
		"config": map[string]any{"path": t.TempDir()}, "replica_target_id": rt.ID,
	})
	require.Equal(t, http.StatusOK, code, "%v", body)
	assert.EqualValues(t, rt.ID, body["replica_target_id"], "the create answer dropped the link")
	raw, ok := body["id"].(float64)
	require.True(t, ok, "%v", body)
	id := int64(raw)

	got, err := f.store.GetStorage(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, got.ReplicaTargetID, "the storage was saved unlinked")
	assert.Equal(t, rt.ID, *got.ReplicaTargetID)
	assert.Contains(t, f.links.storages, id, "a storage created linked started no initial copy")
}

func TestStorageUpdate_ALinkToNoTargetIsRefused(t *testing.T) {
	f := newLinkFixture(t)
	st := f.storage(t, "arsiv", nil)
	code, body := f.do(t, http.MethodPatch, "/api/admin/storages/"+strconv.FormatInt(st.ID, 10),
		map[string]any{"replica_target_id": 9999})
	assert.Equal(t, http.StatusBadRequest, code, "a link to nothing was saved: it replicates nothing, silently")
	assert.Contains(t, body["error"], "does not exist")
}

func TestReplicationTargetUpdate_APartialPatchKeepsTheRestAndRebuildsItsStorages(t *testing.T) {
	f := newLinkFixture(t)
	rt := f.target(t, "storage-box")
	tid := rt.ID
	st := f.storage(t, "arsiv", &tid)
	path := "/api/admin/replication-targets/" + strconv.FormatInt(rt.ID, 10)

	code, body := f.do(t, http.MethodPatch, path, map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "storage-box", body["name"], "switching a target off wiped its name")
	assert.Equal(t, "local", body["driver"], "switching a target off wiped its driver")
	assert.NotEmpty(t, body["config"], "switching a target off wiped its configuration")
	call := f.links.lastTarget(t)
	assert.Equal(t, rt.ID, call.target)
	assert.Contains(t, call.storages, st.ID, "the linked storage keeps fanning out to a disabled target")
	assert.False(t, call.restart)

	_, _ = f.do(t, http.MethodPatch, path, map[string]any{"enabled": true})
	assert.True(t, f.links.lastTarget(t).restart, "switched back on: what changed meanwhile went nowhere, the copy must run again")

	_, _ = f.do(t, http.MethodPatch, path, map[string]any{"config": map[string]any{"path": t.TempDir()}})
	assert.True(t, f.links.lastTarget(t).restart, "a target moved elsewhere starts empty: the copy must run again")

	_, _ = f.do(t, http.MethodPatch, path, map[string]any{"name": "storage-box-2"})
	assert.False(t, f.links.lastTarget(t).restart, "a rename is not a reason to copy everything again")
}

func TestReplicationTargetDelete_RebuildsTheStoragesThatPointedAtIt(t *testing.T) {
	f := newLinkFixture(t)
	rt := f.target(t, "storage-box")
	tid := rt.ID
	st := f.storage(t, "arsiv", &tid)

	req, _ := http.NewRequest(http.MethodDelete, f.srv+"/api/admin/replication-targets/"+strconv.FormatInt(rt.ID, 10), nil)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	call := f.links.lastTarget(t)
	assert.Contains(t, call.storages, st.ID, "the storages linked before the delete were not named")
	got, err := f.store.GetStorage(context.Background(), st.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ReplicaTargetID)
}

func TestReplicaFix_NoReplicaConfiguredOnlyWhenNothingReplicates(t *testing.T) {
	f := newLinkFixture(t)
	code, body := f.do(t, http.MethodPost, "/api/admin/replica/fix", nil)
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Equal(t, "no replica configured", body["error"])

	rt := f.target(t, "storage-box")
	tid := rt.ID
	st := f.storage(t, "arsiv", &tid)
	code, _ = f.do(t, http.MethodPost, "/api/admin/replica/fix", nil)
	assert.Equal(t, http.StatusOK, code, "Fix all refused while a storage replicates")

	// fix-one names the storage; an older caller that does not is matched
	// to the one failure at that path.
	require.NoError(t, f.store.UpsertReplicaFailure(context.Background(), st.ID, "/a.txt", "write", "E", "x"))
	code, body = f.do(t, http.MethodPost, "/api/admin/replica/fix-one", map[string]any{"path": "/a.txt", "op": "write"})
	assert.Equal(t, http.StatusOK, code, "%v", body)
	code, _ = f.do(t, http.MethodPost, "/api/admin/replica/fix-one",
		map[string]any{"storage_id": st.ID, "path": "/a.txt", "op": "write"})
	assert.Equal(t, http.StatusOK, code)

	code, body = f.do(t, http.MethodGet, "/api/admin/replica/initial-copies", nil)
	require.Equal(t, http.StatusOK, code)
	_, has := body["items"]
	assert.True(t, has)
}

// The folder each storage writes into on its target is listed, and changed
// only on purpose: the change rebuilds the storage's driver and begins its
// copy again (FolderChanged); another storage's folder is refused.
func TestReplicaLinks_TheFolderIsShownAndChangedOnPurpose(t *testing.T) {
	ctx := context.Background()
	f := newLinkFixture(t)
	rt := f.target(t, "storage-box")
	tid := rt.ID
	a := f.storage(t, "arsiv", &tid)
	b := f.storage(t, "ikinci", &tid)
	_, err := replica.EnsureFolder(ctx, f.store, a, rt.ID)
	require.NoError(t, err)
	_, err = replica.EnsureFolder(ctx, f.store, b, rt.ID)
	require.NoError(t, err)

	code, body := f.do(t, http.MethodGet, "/api/admin/replica/links", nil)
	require.Equal(t, http.StatusOK, code)
	items, _ := body["items"].([]any)
	require.Len(t, items, 2)
	first, _ := items[0].(map[string]any)
	assert.Equal(t, "arsiv", first["folder"])
	assert.Equal(t, "storage-box", first["target_name"])

	code, body = f.do(t, http.MethodPut, "/api/admin/replica/links/"+strconv.FormatInt(a.ID, 10), map[string]any{"folder": "yeni/ad"})
	require.Equal(t, http.StatusOK, code, "%v", body)
	assert.Equal(t, "yeni-ad", body["folder"])
	assert.Contains(t, f.links.folders, a.ID, "the folder changed and the running process was not told")

	code, _ = f.do(t, http.MethodPut, "/api/admin/replica/links/"+strconv.FormatInt(b.ID, 10), map[string]any{"folder": "Yeni-Ad"})
	assert.Equal(t, http.StatusConflict, code, "a storage was moved into another storage's folder")
}
