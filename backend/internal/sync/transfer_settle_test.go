package sync_test

// A staged upload publishes its row as transfer_state="staged", writes the
// bytes to the storage in a background op, and only then flips the row to
// "stored" and releases its staging. When that flip did not happen — a failed
// catalogue write while cleanup went ahead — the row said "staged" for ever
// with no staging behind it: listed, but every read answered 503 STAGING_GONE,
// every overwrite was refused (the version snapshot reads the same way), and
// the antivirus scan never ran. A full scan saw the object, updated the row's
// metadata and left transfer_state alone. These pin the scan's half of the
// repair, and the evidence it must have before it takes the object as the
// upload's bytes.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// seedUnstored writes a row the way publishStagedNode leaves it: the committed
// size, the staged ETag, and — for an overwrite — backend_mtime stamped with
// the commit time.
func seedUnstored(t *testing.T, store db.Store, st *model.Storage, p, state string, size int64, committedAt *time.Time) *model.Node {
	t.Helper()
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID, Name: filepath.Base(p), Path: p, PathHash: pathkey.Hash(st.ID, p),
		StorageKey: p, Type: model.NodeTypeFile, Size: size, Mime: "application/pdf",
		Etag: "0b7c1f6e3c2d4a5b8e9f001122334455-2", BackendMtime: committedAt,
		SyncState: model.SyncStateSynced, TransferState: state,
	})
	require.NoError(t, err)
	return n
}

func transferState(t *testing.T, store db.Store, id int64) string {
	t.Helper()
	n, err := store.GetNode(context.Background(), id)
	require.NoError(t, err)
	return n.TransferState
}

func TestScanMarksAStagedUploadStoredWhenItsBytesAreOnTheStorage(t *testing.T) {
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	committed := time.Now().Add(-time.Hour)
	body := "the whole uploaded file"
	writeUnder(t, root, "teklif.pdf", body) // written by the transfer, after the commit
	writeUnder(t, root, "yarim.pdf", body)
	staged := seedUnstored(t, store, st, "/teklif.pdf", model.TransferStateStaged, int64(len(body)), &committed)
	failed := seedUnstored(t, store, st, "/yarim.pdf", model.TransferStateFailed, int64(len(body)), &committed)

	job := avJob(store, st, drv, &cleanScanner{})
	run := syncWithAV(t, store, st, job, qd)

	assert.Equal(t, model.TransferStateStored, transferState(t, store, staged.ID),
		"no session holds the bytes and the object is the committed file: the row must say stored")
	assert.Equal(t, model.TransferStateStored, transferState(t, store, failed.ID),
		"a failed transfer whose session is gone and whose bytes did land is stored too")
	assert.GreaterOrEqual(t, run.Updated, 2, "a settled row is an updated row")
	assert.ElementsMatch(t, []int64{staged.ID, failed.ID}, pendingScans(t, qd))
}

// The shape on the deployment that found this: a full scan had ALREADY seen
// the ETag difference and written the backend's metadata onto the row, so
// there is no drift left to notice — only the transfer_state it never
// touched. The settle alone must flip it, and hand the bytes to the scanner:
// the post-transfer hooks never ran, so they were never scanned.
func TestScanSettlesAStagedRowAnEarlierScanAlreadyUpdated(t *testing.T) {
	conn, store := dbtest.NewTestDB(t)
	bucket := newTreeBucket(t, "docs/b.pdf")
	st := treeStorage(t, store, "objtree-test", map[string]any{"bucket": bucket})
	qd := avQueue(t, conn)
	size := treeBucketOf(bucket).objs["docs/b.pdf"]
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID, Name: "b.pdf", Path: "/docs/b.pdf", PathHash: pathkey.Hash(st.ID, "/docs/b.pdf"),
		StorageKey: "/docs/b.pdf", Type: model.NodeTypeFile, Size: size, Mime: "application/pdf",
		Etag:      fmt.Sprint(size), // what the backend reports: nothing left to drift
		SyncState: model.SyncStateSynced, TransferState: model.TransferStateStaged,
	})
	require.NoError(t, err)

	job := avJob(store, st, nil, &cleanScanner{})
	syncWithAV(t, store, st, job, qd)

	assert.Equal(t, model.TransferStateStored, transferState(t, store, n.ID))
	assert.Contains(t, pendingScans(t, qd), n.ID, "the settled file must be handed to the antivirus scan")
}

func TestScanLeavesAStagedUploadAloneUnlessTheObjectIsItsBytes(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	committed := time.Now().Add(-time.Minute)
	body := "the whole uploaded file"

	// 1. A session still owns the bytes: in flight, or failed and retryable.
	writeUnder(t, root, "surecte.pdf", body)
	inFlight := seedUnstored(t, store, st, "/surecte.pdf", model.TransferStateStaged, int64(len(body)), &committed)
	nodeID := inFlight.ID
	const sessionID = "11111111-2222-3333-4444-555555555555"
	require.NoError(t, store.CreateStagedUpload(ctx, &model.StagedUpload{
		ID: sessionID, StorageID: st.ID, StorageKey: "/surecte.pdf",
		TotalSize: int64(len(body)), ChunkSize: 8 << 20, State: model.StagedUploadCommitting,
		ExpiresAt: time.Now().Add(time.Hour),
	}))
	// What Commit does once the row is published.
	require.NoError(t, store.AttachStagedUploadTarget(ctx, sessionID, nodeID, 0))

	// 2. Not the committed size: a partial write, or another file.
	writeUnder(t, root, "kisa.pdf", body[:5])
	short := seedUnstored(t, store, st, "/kisa.pdf", model.TransferStateStaged, int64(len(body)), &committed)

	// 3. Older than the commit: the version this upload was replacing, still
	//    at the key because the new bytes never arrived.
	writeUnder(t, root, "eski.pdf", body)
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(root, "eski.pdf"), old, old))
	stale := seedUnstored(t, store, st, "/eski.pdf", model.TransferStateStaged, int64(len(body)), &committed)

	// Twice: the first pass must not rewrite the row's committed size and
	// commit time with the wrong object's, or the second would take that
	// object for the upload.
	for pass := 1; pass <= 2; pass++ {
		_, run := runSync(t, store, st)
		require.Equal(t, "ok", run.Status, run.Error)

		assert.Equal(t, model.TransferStateStaged, transferState(t, store, inFlight.ID), "pass %d: a live session wins", pass)
		assert.Equal(t, model.TransferStateStaged, transferState(t, store, short.ID), "pass %d: a size mismatch is not the upload", pass)
		assert.Equal(t, model.TransferStateStaged, transferState(t, store, stale.ID), "pass %d: an object older than the commit is not the upload", pass)
		waitPastSecondBoundary()
	}
	got, err := store.GetNode(ctx, short.ID)
	require.NoError(t, err)
	assert.EqualValues(t, len(body), got.Size, "an unstored row keeps the size that was committed, not the object's")
}

// The drift update used to write the listing's mime over the row's, and an
// object store's listing carries none: every drifted S3 file lost the type
// sniffed from its bytes at upload.
func TestDriftKeepsTheMimeWhenTheListingHasNone(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	bucket := newTreeBucket(t, "docs/a.pdf")
	st := treeStorage(t, store, "objtree-test", map[string]any{"bucket": bucket})
	runSync(t, store, st)
	n := node(t, store, st.ID, "/docs/a.pdf")
	require.NotNil(t, n)
	require.NoError(t, store.UpdateNodeMeta(ctx, n.ID, n.Size, "application/pdf", n.Etag, time.Now()))

	treeBucketOf(bucket).objs["docs/a.pdf"] = 4242 // replaced on the backend: new size, new etag
	waitPastSecondBoundary()
	runSync(t, store, st)

	got, err := store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 4242, got.Size, "fixture check: the drift was applied")
	assert.Equal(t, "application/pdf", got.Mime, "a listing with no mime must not erase the row's")
}
