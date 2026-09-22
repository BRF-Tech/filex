package handlers_test

// After a staged upload's bytes reach the storage, two catalogue writes turn
// the row into a readable file: the backend's metadata, and transfer_state
// "stored". Both were fire-and-forget — the metadata error discarded, the
// flip's merely logged — and the staging directory and session row were
// deleted right after, whatever had happened. One failed write (a database
// busy under a large scan) therefore left a file that was listed, fully on
// the storage, and unreadable for ever: "staged", nothing staged, 503
// STAGING_GONE on every read, while the op told the client "ok".

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// flakyTransferStore fails the "stored" flip the given number of times — the
// transient database error the transfer's bookkeeping has to survive.
type flakyTransferStore struct {
	db.Store
	failStored atomic.Int32
}

func (s *flakyTransferStore) SetNodeTransferState(ctx context.Context, id int64, state string) error {
	if state == model.TransferStateStored && s.failStored.Add(-1) >= 0 {
		return errors.New("database is locked (simulated)")
	}
	return s.Store.SetNodeTransferState(ctx, id, state)
}

func newFlakyStagedFixture(t *testing.T, failures int32) *stagedFixture {
	t.Helper()
	return newStagedFixtureWith(t, func(d *api.Deps) {
		flaky := &flakyTransferStore{Store: d.Store}
		flaky.failStored.Store(failures)
		d.Store = flaky
	})
}

// stageAndCommitOnce pushes body through begin → put → commit and returns the
// session, node and op ids. The transfer runs in the fixture's ops worker.
func (f *stagedFixture) stageAndCommitOnce(t *testing.T, name string, body []byte) (sessionID string, nodeID, opID int64) {
	t.Helper()
	total := int64(len(body))
	code, begun := f.begin(t, map[string]any{"path": "main://", "name": name, "size": total, "chunk_size": 4096})
	require.Equal(t, http.StatusOK, code, "begin: %v", begun)
	sessionID, _ = begun["id"].(string)
	for off := int64(0); off < total; off += 4096 {
		end := min(off+4096, total)
		code, put := f.putChunk(t, sessionID, off, end-off, total, body[off:end])
		require.Equal(t, http.StatusOK, code, "put at %d: %v", off, put)
	}
	code, committed := f.commit(t, sessionID)
	require.Equal(t, http.StatusAccepted, code, "commit: %v", committed)
	return sessionID, num(committed["node_id"]), num(committed["op_id"])
}

func (f *stagedFixture) download(t *testing.T, rel string) (int, []byte) {
	t.Helper()
	resp, err := f.client.Get(fmt.Sprintf("%s/api/files/manager?action=download&path=main://%s", f.srv.URL, rel))
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, b
}

func (f *stagedFixture) nodeState(t *testing.T, id int64) string {
	t.Helper()
	n, err := f.store.GetNode(context.Background(), id)
	require.NoError(t, err)
	return n.TransferState
}

func TestStagedTransfer_RetriesTheStoredFlipAndOnlyThenReleasesStaging(t *testing.T) {
	f := newFlakyStagedFixture(t, 1)
	body := randomBytes(10_000)

	sessionID, nodeID, opID := f.stageAndCommitOnce(t, "sozlesme.bin", body)
	require.Equal(t, "ok", f.waitForOp(t, opID))

	assert.Equal(t, model.TransferStateStored, f.nodeState(t, nodeID),
		"one failed catalogue write must be retried, not turned into a file that says staged for ever")
	code, got := f.download(t, "sozlesme.bin")
	require.Equal(t, http.StatusOK, code, "%s", got)
	assert.Equal(t, body, got)
	_, err := f.store.GetStagedUpload(context.Background(), sessionID)
	assert.Error(t, err, "the session goes once the row says stored")
	assert.Empty(t, stagingDirsOnDisk(t, f.dataDir), "and so does its staging")
}

func TestStagedTransfer_KeepsStagingWhenTheStoredFlipCannotBeWritten(t *testing.T) {
	f := newFlakyStagedFixture(t, 1000)
	body := randomBytes(10_000)

	sessionID, nodeID, opID := f.stageAndCommitOnce(t, "rapor.bin", body)
	assert.Equal(t, "failed", f.waitForOp(t, opID),
		"the client must not be told ok while the row cannot say where the bytes are")

	sess, err := f.store.GetStagedUpload(context.Background(), sessionID)
	require.NoError(t, err, "the session must survive: it is what makes the file readable and the commit retryable")
	assert.Equal(t, model.StagedUploadFailed, sess.State)
	assert.NotEmpty(t, stagingDirsOnDisk(t, f.dataDir), "the staging must survive with it")
	assert.Equal(t, model.TransferStateFailed, f.nodeState(t, nodeID))

	code, got := f.download(t, "rapor.bin")
	require.Equal(t, http.StatusOK, code, "the file stays readable from staging: %s", got)
	assert.Equal(t, body, got)
}

// ── the boot pass ───────────────────────────────────────────────────────────

// seedUnstoredFile writes a row as publishStagedNode leaves it and, unless
// content is nil, a file at its key on the local storage.
func (f *stagedFixture) seedUnstoredFile(t *testing.T, rel, state string, size int64, committedAt time.Time, content []byte, fileMtime *time.Time) *model.Node {
	t.Helper()
	if content != nil {
		abs := filepath.Join(f.rootDir, rel)
		require.NoError(t, os.WriteFile(abs, content, 0o644))
		if fileMtime != nil {
			require.NoError(t, os.Chtimes(abs, *fileMtime, *fileMtime))
		}
	}
	n, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.storage.ID, Name: rel, Path: "/" + rel, PathHash: pathkey.Hash(f.storage.ID, rel),
		StorageKey: "/" + rel, Type: model.NodeTypeFile, Size: size, Mime: "application/octet-stream",
		Etag: "4ae71336e44bf9bf79d2752e234818a5-3", BackendMtime: &committedAt,
		SyncState: model.SyncStateSynced, TransferState: state,
	})
	require.NoError(t, err)
	return n
}

func TestStagedUploadBootRecoverySettlesRowsWhoseBytesLanded(t *testing.T) {
	ctx := context.Background()
	f := newStagedFixture(t)
	body := randomBytes(3000)
	committed := time.Now().Add(-time.Hour)
	old := time.Now().Add(-2 * time.Hour)

	landed := f.seedUnstoredFile(t, "landed.bin", model.TransferStateStaged, 3000, committed, body, nil)
	landedFailed := f.seedUnstoredFile(t, "landed-failed.bin", model.TransferStateFailed, 3000, committed, body, nil)
	wrongSize := f.seedUnstoredFile(t, "wrong-size.bin", model.TransferStateStaged, 3000, committed, body[:10], nil)
	older := f.seedUnstoredFile(t, "older.bin", model.TransferStateStaged, 3000, committed, body, &old)
	missing := f.seedUnstoredFile(t, "missing.bin", model.TransferStateStaged, 3000, committed, nil, nil)
	inSession := f.seedUnstoredFile(t, "in-session.bin", model.TransferStateStaged, 3000, committed, body, nil)
	const sessionID = "99999999-8888-7777-6666-555555555555"
	require.NoError(t, f.store.CreateStagedUpload(ctx, &model.StagedUpload{
		ID: sessionID, StorageID: f.storage.ID, StorageKey: "/in-session.bin", UserID: f.userID,
		TotalSize: 3000, ChunkSize: 4096, State: model.StagedUploadFailed, ExpiresAt: time.Now().Add(time.Hour),
	}))
	require.NoError(t, f.store.AttachStagedUploadTarget(ctx, sessionID, inSession.ID, 0))

	settled := f.deps.StagedUploads.RecoverUnstored(ctx)
	assert.Equal(t, 2, settled)

	assert.Equal(t, model.TransferStateStored, f.nodeState(t, landed.ID))
	assert.Equal(t, model.TransferStateStored, f.nodeState(t, landedFailed.ID))
	assert.Equal(t, model.TransferStateStaged, f.nodeState(t, wrongSize.ID), "not the committed size")
	assert.Equal(t, model.TransferStateStaged, f.nodeState(t, older.ID), "older than the commit: the version it was replacing")
	assert.Equal(t, model.TransferStateStaged, f.nodeState(t, missing.ID), "nothing on the storage")
	assert.Equal(t, model.TransferStateStaged, f.nodeState(t, inSession.ID), "a session still owns the bytes")

	code, got := f.download(t, "landed.bin")
	require.Equal(t, http.StatusOK, code, "%s", got)
	assert.Equal(t, body, got, "a settled row reads from the storage")
}

// The pass runs by itself: RunSweeper does it once at boot, after the first
// sweep (which may just have removed the session that was blocking it).
func TestStagedUploadSweeperRunsTheRecoveryAtBoot(t *testing.T) {
	f := newStagedFixture(t)
	body := randomBytes(2000)
	n := f.seedUnstoredFile(t, "boot.bin", model.TransferStateStaged, 2000, time.Now().Add(-time.Hour), body, nil)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go f.deps.StagedUploads.RunSweeper(ctx, time.Hour)

	require.Eventually(t, func() bool {
		got, err := f.store.GetNode(context.Background(), n.ID)
		return err == nil && got.TransferState == model.TransferStateStored
	}, 5*time.Second, 20*time.Millisecond, "the boot pass must settle a row whose bytes landed")
}
