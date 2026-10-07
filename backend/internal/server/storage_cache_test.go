package server

// The storage resolver wraps a storage that is linked to a replication target
// (#186, GitHub Discussion #91).
//
// ⚠ What happened before: the resolver built the bare driver whatever the row
// said - storage.NewReplicated was called only by tests - so a storage linked
// to a target (a Hetzner Storage Box over SMB, in the report) sent nothing to
// it, wrote no failure and raised no notification, in every version since the
// feature was added. And Fix all ran on a Service built with a nil wrapper, so
// it answered "no replica configured" whatever was linked.
//
// Measured here on real local disks: a write through the resolver reaches the
// target; linking and unlinking apply without a restart; a target switched off
// is not written to; dropping a storage retires its old wrapper; Fix all goes
// through the live one; a target that cannot be opened says why on every
// change.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/replica"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type cacheFixture struct {
	store      db.Store
	cache      *storageCache
	st         *model.Storage
	target     *model.ReplicationTarget
	primaryDir string
	targetDir  string
}

func newCacheFixture(t *testing.T, linked bool) *cacheFixture {
	t.Helper()
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	f := &cacheFixture{store: store, primaryDir: t.TempDir(), targetDir: t.TempDir()}
	tcfg, _ := json.Marshal(map[string]any{"path": f.targetDir})
	target, err := store.CreateReplicationTarget(ctx, &model.ReplicationTarget{
		Name: "storage-box", Driver: "local", ConfigJSON: tcfg, Mode: "async", Enabled: true,
	})
	require.NoError(t, err)
	f.target = target
	scfg, _ := json.Marshal(map[string]any{"path": f.primaryDir})
	row := &model.Storage{
		Name: "arsiv", Driver: "local", MountPath: "/arsiv", ConfigJSON: scfg,
		SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: true,
	}
	if linked {
		tid := target.ID
		row.ReplicaTargetID = &tid
	}
	f.st, err = store.CreateStorage(ctx, row)
	require.NoError(t, err)
	rules, _ := replica.NewRulesEngine(store)
	f.cache = newStorageCache(ctx, store, rules, nil, nil)
	t.Cleanup(func() { f.cache.stopAll(5 * time.Second) })
	return f
}

// write puts a file through the resolver - the way every upload, save and
// copy reaches a storage - and waits for the wrapper's fan-out.
func (f *cacheFixture) write(t *testing.T, name, body string) {
	t.Helper()
	drv, err := f.cache.resolve(f.st.ID)
	require.NoError(t, err)
	w, ok := drv.(storage.Writer)
	require.True(t, ok)
	require.NoError(t, w.Write(context.Background(), name, strings.NewReader(body), int64(len(body))))
	if _, ok := storage.AsReplicated(drv); ok {
		require.Eventually(t, func() bool { return f.onTarget(name) },
			5*time.Second, 10*time.Millisecond, "%s never reached the target", name)
	}
}

// onTarget reports whether name is on the target, inside the storage's own
// folder there when it has one (it has once it was wrapped).
func (f *cacheFixture) onTarget(name string) bool {
	_, err := os.Stat(filepath.Join(f.targetDir, f.folder(), name))
	return err == nil
}

// folder is the storage's folder on the target, "" before it has one.
func (f *cacheFixture) folder() string {
	l, err := f.store.GetReplicaLink(context.Background(), f.st.ID)
	if err != nil || l == nil {
		return ""
	}
	return l.Folder
}

func (f *cacheFixture) setLink(t *testing.T, targetID *int64) {
	t.Helper()
	f.st.ReplicaTargetID = targetID
	require.NoError(t, f.store.UpdateStorage(context.Background(), f.st))
	f.cache.forget(f.st.ID)
}

func TestResolver_ALinkedStorageIsWrappedAndItsWritesReachTheTarget(t *testing.T) {
	f := newCacheFixture(t, true)
	drv, err := f.cache.resolve(f.st.ID)
	require.NoError(t, err)
	_, wrapped := storage.AsReplicated(drv)
	require.True(t, wrapped, "the resolver handed out the bare driver of a linked storage")

	f.write(t, "rapor.docx", "hello")
	assert.Equal(t, "arsiv", f.folder(), "the storage writes into a folder of its own, named after it")
	body, err := os.ReadFile(filepath.Join(f.targetDir, "arsiv", "rapor.docx"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(body))
	_, err = os.Stat(filepath.Join(f.targetDir, "rapor.docx"))
	assert.True(t, os.IsNotExist(err), "a storage wrote into the target's root, where another storage's files are")

	// The local disk's own abilities are kept: SFTP/FTP/NFS/S3 clients send
	// modification times through Toucher.
	_, keepsTimes := drv.(storage.Toucher)
	assert.True(t, keepsTimes, "wrapping a disk lost SetMtime")
}

func TestResolver_AStorageWithoutALinkIsNotWrapped(t *testing.T) {
	f := newCacheFixture(t, false)
	drv, err := f.cache.resolve(f.st.ID)
	require.NoError(t, err)
	_, wrapped := storage.AsReplicated(drv)
	assert.False(t, wrapped)
}

func TestResolver_ASwitchedOffTargetIsNotWrittenTo(t *testing.T) {
	f := newCacheFixture(t, true)
	f.target.Enabled = false
	require.NoError(t, f.store.UpdateReplicationTarget(context.Background(), f.target))

	drv, err := f.cache.resolve(f.st.ID)
	require.NoError(t, err)
	_, wrapped := storage.AsReplicated(drv)
	assert.False(t, wrapped, "a storage linked to a disabled target was wrapped")
	f.write(t, "a.txt", "x")
	assert.False(t, f.onTarget("a.txt"), "a disabled target was written to")
}

func TestResolver_LinkingAndUnlinkingApplyWithoutARestart(t *testing.T) {
	f := newCacheFixture(t, false)
	f.write(t, "before.txt", "x")
	assert.False(t, f.onTarget("before.txt"))

	tid := f.target.ID
	f.setLink(t, &tid)
	f.write(t, "linked.txt", "y")
	assert.True(t, f.onTarget("linked.txt"), "a link saved without a restart replicated nothing")

	drv, err := f.cache.resolve(f.st.ID)
	require.NoError(t, err)
	old, ok := storage.AsReplicated(drv)
	require.True(t, ok)

	f.setLink(t, nil)
	f.write(t, "unlinked.txt", "z")
	assert.False(t, f.onTarget("unlinked.txt"), "an unlinked storage still fanned out")
	assert.True(t, old.Halted(), "the dropped wrapper was left running")
}

func TestResolver_ForgetStopsTheOldWrapper(t *testing.T) {
	f := newCacheFixture(t, true)
	drv, err := f.cache.resolve(f.st.ID)
	require.NoError(t, err)
	old, ok := storage.AsReplicated(drv)
	require.True(t, ok)

	f.cache.forget(f.st.ID)
	assert.True(t, old.Halted(), "forget left the old wrapper taking fan-outs")
	select {
	case <-old.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the old wrapper never released its drivers: one leaked per edit")
	}
	again, err := f.cache.resolve(f.st.ID)
	require.NoError(t, err)
	fresh, ok := storage.AsReplicated(again)
	require.True(t, ok)
	assert.NotSame(t, old, fresh)
}

func TestResolver_ATargetThatCannotBeOpenedSaysWhyOnEveryChange(t *testing.T) {
	f := newCacheFixture(t, true)
	f.target.Driver = "no-such-driver"
	require.NoError(t, f.store.UpdateReplicationTarget(context.Background(), f.target))

	drv, err := f.cache.resolve(f.st.ID)
	require.NoError(t, err, "a broken target must not take the storage down")
	w := drv.(storage.Writer)
	require.NoError(t, w.Write(context.Background(), "a.txt", strings.NewReader("x"), 1))
	rd, ok := storage.AsReplicated(drv)
	require.True(t, ok)
	rd.Stop()

	rows, _, err := f.store.ListReplicaFailures(context.Background(), true, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1, "a change that could not reach the target left no trace")
	assert.Equal(t, "REPLICA_UNAVAILABLE", rows[0].ErrorCode)
	assert.Equal(t, f.st.ID, rows[0].StorageID)
	assert.Contains(t, rows[0].ErrorMsg, "storage-box")
}

// memOps is a queue.Driver that keeps ops in memory.
type memOps struct {
	queue.Driver
	ops []queue.Op
}

func (q *memOps) Enqueue(_ context.Context, op queue.Op) (string, error) {
	raw, _ := json.Marshal(op.Payload)
	var back map[string]any
	_ = json.Unmarshal(raw, &back)
	op.Payload = back
	q.ops = append(q.ops, op)
	return "op", nil
}

func TestFixAll_RunsOnTheResolversWrapper(t *testing.T) {
	ctx := context.Background()
	f := newCacheFixture(t, true)
	require.NoError(t, os.WriteFile(filepath.Join(f.primaryDir, "lost.txt"), []byte("v1"), 0o644))
	require.NoError(t, f.store.UpsertReplicaFailure(ctx, f.st.ID, "/lost.txt", "write", "REPLICA_WRITE_FAIL", "timeout"))

	q := &memOps{}
	svc := replica.New(f.store, f.cache, q, nil)
	got, err := svc.ReconcileAll(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, got.Queued)
	require.Len(t, q.ops, 1)
	require.NoError(t, svc.HandleRetry(ctx, q.ops[0]), "Fix all answered without the real driver")

	assert.True(t, f.onTarget("lost.txt"), "Fix all did not copy the file")
	n, err := f.store.CountUnresolvedReplicaFailures(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)
}

func TestReplicaLinks_ATargetEditRebuildsItsStoragesAndRestartsTheirCopies(t *testing.T) {
	ctx := context.Background()
	f := newCacheFixture(t, true)
	q := &memOps{}
	svc := replica.New(f.store, f.cache, q, nil)
	links := replicaLinks{cache: f.cache, svc: svc}

	drv, err := f.cache.resolve(f.st.ID)
	require.NoError(t, err)
	old, _ := storage.AsReplicated(drv)
	links.StorageLinkChanged(ctx, f.st.ID)
	c, err := f.store.GetReplicaInitialCopy(ctx, f.st.ID)
	require.NoError(t, err)
	require.NotNil(t, c, "linking started no initial copy")

	// The target moves to another directory: the storage's wrapper is
	// rebuilt and its copy begins again.
	newDir := t.TempDir()
	cfg, _ := json.Marshal(map[string]any{"path": newDir})
	f.target.ConfigJSON = cfg
	require.NoError(t, f.store.UpdateReplicationTarget(ctx, f.target))
	links.TargetChanged(ctx, f.target.ID, []int64{f.st.ID}, true)
	assert.True(t, old.Halted(), "the wrapper of the old configuration is still in use")

	f.targetDir = newDir
	f.write(t, "after-edit.txt", "x")
	assert.True(t, f.onTarget("after-edit.txt"), "writes still go to the old target")
	restarted, err := f.store.GetReplicaInitialCopy(ctx, f.st.ID)
	require.NoError(t, err)
	require.NotNil(t, restarted)
	assert.Greater(t, restarted.Revision, c.Revision, "the copy to the old place was not begun again")
}
