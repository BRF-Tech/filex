package sync_test

// Issue #16 — the part that turned a broken upload into DATA LOSS.
//
// The reporter's files never reached S3 (a separate bug, fixed in the staged
// commit path). What made it unrecoverable is what happened NEXT: the storage
// sync walked the bucket, did not find them, and moved them to trash. The
// upload was broken; the sync is what deleted the user's file.
//
// So the tombstone pass is not allowed to read "I did not see it" as "the user
// deleted it". It has to be RIGHT about the deletion:
//
//   - a node whose bytes filex never confirmed on the backend (transfer_state
//     other than "stored") is EXPECTED to be absent — it is in staging, or it
//     is a failed upload — and is never a deletion;
//   - an object that a listing missed but Stat can still see is not deleted;
//   - an object we could not check at all (permissions, timeout, 503) is not
//     known to be deleted, and "I could not check" must never read as "gone".

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// tombstoneDriver is an empty bucket that can be told what Stat should answer.
// List always returns nothing, so EVERY node in the catalogue is stale after a
// pass — the reporter's situation exactly.
type tombstoneDriver struct {
	mu       sync.Mutex
	statErr  error // what Stat answers; nil means "the object is there"
	statCall int
}

func (d *tombstoneDriver) Init(context.Context, map[string]any) error { return nil }
func (d *tombstoneDriver) Name() string                               { return "tombstone-fake" }
func (d *tombstoneDriver) List(context.Context, string) ([]storage.Object, error) {
	return nil, nil
}
func (d *tombstoneDriver) Stat(_ context.Context, p string) (storage.Object, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.statCall++
	if d.statErr != nil {
		return storage.Object{}, d.statErr
	}
	return storage.Object{Path: p, Name: p, Kind: storage.KindFile, Size: 1, Mtime: time.Now()}, nil
}
func (d *tombstoneDriver) Read(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrUnsupported
}
func (d *tombstoneDriver) Capabilities() storage.Capabilities {
	return storage.Capabilities{Read: true}
}

func (d *tombstoneDriver) stats() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.statCall
}

// tombstoneStorage registers the driver under a unique name and creates a
// storage row for it. SyncModeOnDemand keeps Loop() from racing the Trigger.
func tombstoneStorage(t *testing.T, store db.Store, d *tombstoneDriver, name string) *model.Storage {
	t.Helper()
	storage.Register(name, func() storage.Driver { return d })
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       name,
		Driver:     name,
		MountPath:  "/" + name,
		ConfigJSON: []byte(`{}`),
		SyncMode:   model.SyncModeOnDemand,
		Enabled:    true,
	})
	require.NoError(t, err)
	return st
}

// seedNode puts a file row in the catalogue with a chosen transfer_state and a
// seen_at old enough that the next pass considers it stale.
func seedNode(t *testing.T, store db.Store, st *model.Storage, path, transferState string) *model.Node {
	t.Helper()
	ctx := context.Background()
	n, err := store.CreateNode(ctx, &model.Node{
		StorageID:  st.ID,
		Name:       path[1:],
		Path:       path,
		PathHash:   pathkey.Hash(st.ID, path),
		StorageKey: path,
		Type:       model.NodeTypeFile,
		Size:       12 << 20,
	})
	require.NoError(t, err)
	if transferState != "" {
		require.NoError(t, store.SetNodeTransferState(ctx, n.ID, transferState))
	}
	return n
}

func liveNode(t *testing.T, store db.Store, storageID int64, path string) *model.Node {
	t.Helper()
	n, _ := store.GetNodeByPath(context.Background(), storageID, pathkey.Hash(storageID, path))
	return n
}

// ⚠⚠ The headline: a node whose bytes are still in staging (or whose transfer
// failed) must survive a sync of an empty bucket. This is the exact sequence
// the reporter hit — upload, listed, sync, trash.
func TestSyncDoesNotTrashANodeWhoseBytesNeverReachedTheBackend(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	drv := &tombstoneDriver{statErr: storage.ErrNotFound}
	st := tombstoneStorage(t, store, drv, "tombstone-staged")

	staged := seedNode(t, store, st, "/soundscape.mp3", model.TransferStateStaged)
	failed := seedNode(t, store, st, "/failed.mp3", model.TransferStateFailed)

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, "sync run error: %s", run.Error)

	assert.NotNil(t, liveNode(t, store, st.ID, "/soundscape.mp3"),
		"a node still in staging is EXPECTED to be missing from the bucket — trashing it deletes the upload the user was just shown")
	assert.NotNil(t, liveNode(t, store, st.ID, "/failed.mp3"),
		"a failed transfer keeps its bytes in staging and stays retryable; trash is not the answer to it")
	assert.Zero(t, run.Deleted)
	assert.Zero(t, drv.stats(),
		"an unstored node is decided from filex's own records — no need to ask the backend")

	_ = staged
	_ = failed
}

// A listing can miss an object that is really there. Stat is the second
// opinion, and it wins.
func TestSyncKeepsANodeTheListingMissedButStatCanSee(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	drv := &tombstoneDriver{statErr: nil} // Stat says: still there
	st := tombstoneStorage(t, store, drv, "tombstone-present")
	seedNode(t, store, st, "/present.bin", model.TransferStateStored)

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status)

	assert.NotNil(t, liveNode(t, store, st.ID, "/present.bin"),
		"the object is there; a listing that failed to mention it is a listing bug, not a deletion")
	assert.Zero(t, run.Deleted)
	assert.Equal(t, 1, drv.stats(), "the candidate is checked exactly once")
}

// "I could not check" is not "it is gone". A permissions change or a 503 must
// never cost the user a file.
func TestSyncKeepsANodeItCannotCheck(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	drv := &tombstoneDriver{statErr: errors.New("AccessDenied: not authorized to perform s3:GetObject")}
	st := tombstoneStorage(t, store, drv, "tombstone-denied")
	seedNode(t, store, st, "/locked.bin", model.TransferStateStored)

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status)

	assert.NotNil(t, liveNode(t, store, st.ID, "/locked.bin"),
		"an object we were refused permission to read has not been deleted")
	assert.Zero(t, run.Deleted)
}

// And the behaviour that must NOT regress: an object that is genuinely gone
// still goes to trash, so a deletion made in the bucket is still reflected.
func TestSyncStillTrashesAnObjectThatIsGenuinelyGone(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	drv := &tombstoneDriver{statErr: storage.ErrNotFound}
	st := tombstoneStorage(t, store, drv, "tombstone-gone")
	seedNode(t, store, st, "/deleted-elsewhere.bin", model.TransferStateStored)

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status)

	assert.Nil(t, liveNode(t, store, st.ID, "/deleted-elsewhere.bin"),
		"a file deleted in the bucket must still disappear from filex — the guard is about certainty, not about never deleting")
	assert.Equal(t, 1, run.Deleted)
	assert.Equal(t, 1, drv.stats())
}
