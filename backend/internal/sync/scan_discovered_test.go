package sync_test

// Files that arrive ON a storage were catalogued and content-indexed, and
// never scanned.
//
// Every write THROUGH filex is scanned — uploads, the AI/MCP surface, ShareX,
// drop links, the editor, a restore. A file that turns up on the backend
// instead (`aws s3 cp` into the bucket, another process writing on a mounted
// disk, everything that was already there when the storage was pointed at the
// folder) reaches the catalogue only through the sync walk. The walk created
// the row, fed the search index, enqueued content extraction — and never once
// handed the bytes to ClamAV.
//
// That is the one place a reader most expects a scan: an operator who turned
// scanning on believes the files in filex are scanned.
//
// These tests are the red proof. They fail on the code as it was, and they run
// the REAL enqueue (queue.AntivirusScanner.Enqueue) against a REAL sqlite queue
// driver, so what they assert is the op that would actually be persisted, not a
// hook that was called.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/storage"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/trash"

	_ "github.com/brf-tech/filex/backend/internal/queue/drivers/sqlite"
)

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// avQueue opens the sqlite queue driver over the SAME connection dbtest
// migrated, so `ops_queue` is the table migration 00006 created rather than a
// hand-written copy that could drift from it.
func avQueue(t *testing.T, conn *sql.DB) queue.Driver {
	t.Helper()
	qd, err := queue.Get("sqlite")
	require.NoError(t, err)
	require.NoError(t, qd.Init(context.Background(), map[string]any{"db": conn}))
	return qd
}

// cleanScanner is ClamAV finding nothing — the ordinary case, and the one the
// "do not re-scan" tests need, since a verdict must not change the catalogue.
type cleanScanner struct{ scanned int }

func (c *cleanScanner) Supports() bool { return true }
func (c *cleanScanner) Scan(_ context.Context, r io.Reader) (bool, string, error) {
	_, _ = io.Copy(io.Discard, r)
	c.scanned++
	return false, "", nil
}

// avJob builds the real antivirus job over one storage driver.
func avJob(store db.Store, st *model.Storage, drv storage.Driver, sc queue.AVScanner) *queue.AntivirusScanner {
	return queue.NewAntivirusScanner(store, func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, errors.New("unknown storage")
		}
		return drv, nil
	}, sc, nil, nil, 0)
}

// syncWithAV runs one sync pass with the antivirus hook wired exactly as
// server bootstrap wires it: AttachAntivirus over
// AntivirusScanner.EnqueueDiscovered bound to the queue driver.
func syncWithAV(t *testing.T, store db.Store, st *model.Storage, job *queue.AntivirusScanner, qd queue.Driver) *model.SyncRun {
	t.Helper()
	ctx := context.Background()
	w := filexsync.New(store)
	w.AttachAntivirus(func(ctx context.Context, n *model.Node) { job.EnqueueDiscovered(ctx, qd, n) })
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID))
	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", run.Status, run.Error)
	return run
}

// pendingScans returns the node ids of every pending antivirus_scan op.
func pendingScans(t *testing.T, qd queue.Driver) []int64 {
	t.Helper()
	ops, _, err := qd.List(context.Background(), queue.StatusPending, 100000, 0)
	require.NoError(t, err)
	var ids []int64
	for _, op := range ops {
		if op.Type != queue.TypeAntivirusScan {
			continue
		}
		switch v := op.Payload["node_id"].(type) {
		case float64:
			ids = append(ids, int64(v))
		case int64:
			ids = append(ids, v)
		case json.Number:
			n, _ := v.Int64()
			ids = append(ids, n)
		default:
			t.Fatalf("antivirus_scan op with an unusable node_id %T (%v)", v, v)
		}
	}
	return ids
}

// drainScans dequeues and runs every pending antivirus_scan op through the
// real handler, the way the worker pool would.
func drainScans(t *testing.T, qd queue.Driver, job *queue.AntivirusScanner) int {
	t.Helper()
	ctx := context.Background()
	n := 0
	for {
		op, err := qd.Dequeue(ctx, []string{queue.TypeAntivirusScan})
		if errors.Is(err, queue.ErrEmpty) {
			return n
		}
		require.NoError(t, err)
		require.NoError(t, job.Handle(ctx, op))
		require.NoError(t, qd.Ack(ctx, op.ID))
		n++
	}
}

// ---------------------------------------------------------------------------
// 1. the gap itself
// ---------------------------------------------------------------------------

// The red proof. A file nobody wrote through filex is catalogued by the walk;
// on the old code that was the end of it.
func TestSyncScansAFileThatArrivedOutOfBand(t *testing.T) {
	ctx := context.Background()
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	// Written straight onto the backend — no upload, no drop link, no editor.
	require.NoError(t, os.WriteFile(filepath.Join(root, "fatura.exe"), []byte("virus payload"), 0o644))

	sc := &cleanScanner{}
	job := avJob(store, st, drv, sc)
	syncWithAV(t, store, st, job, qd)

	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/fatura.exe"))
	require.NoError(t, err)
	require.NotNil(t, n, "precondition: the walk must catalogue the file")

	assert.Equal(t, []int64{n.ID}, pendingScans(t, qd),
		"the sync catalogued a file and never handed it to the scanner")

	require.Equal(t, 1, drainScans(t, qd, job))
	assert.Equal(t, 1, sc.scanned, "the queued op must actually read and scan the bytes")
}

// An infected file the sync DISCOVERS must be quarantined exactly as an
// uploaded one is: bytes renamed into `.filex-trash/`, row soft-deleted and
// retagged, and the whole thing surviving the next pass.
func TestSyncQuarantinesAnInfectedFileItDiscovered(t *testing.T) {
	ctx := context.Background()
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	require.NoError(t, os.WriteFile(filepath.Join(root, "fatura.exe"), []byte("virus payload"), 0o644))

	job := avJob(store, st, drv, alwaysInfected{})
	syncWithAV(t, store, st, job, qd)

	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/fatura.exe"))
	require.NoError(t, err)
	require.NotNil(t, n)

	require.Equal(t, 1, drainScans(t, qd, job), "exactly one scan op for the discovered file")

	after, err := store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	require.NotNil(t, after.DeletedAt, "the infected file was left live on the storage")
	assert.True(t, strings.HasPrefix(after.Path, "/"+trash.Prefix+"/"), after.Path)
	assert.Equal(t, "/fatura.exe", after.StorageKey, "quarantine must record where it came from")
	assert.Equal(t, 1, trashCount(t, store, st.ID))

	// The bytes moved; nothing is left at the original key.
	_, err = os.Stat(filepath.Join(root, "fatura.exe"))
	assert.True(t, os.IsNotExist(err), "the infected bytes are still at the path they were found")
	quarantined, err := filepath.Glob(filepath.Join(root, trash.Prefix, "*__fatura.exe"))
	require.NoError(t, err)
	assert.Len(t, quarantined, 1, "the bytes must be parked in the trash bucket")

	// And the next pass does not let it out again, nor scan it a second time.
	waitPastSecondBoundary()
	syncWithAV(t, store, st, job, qd)
	assert.Empty(t, pendingScans(t, qd), "a quarantined file must not be re-enqueued")
	still, err := store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	assert.NotNil(t, still.DeletedAt)
}

// ---------------------------------------------------------------------------
// 2. the way this is most likely to go wrong
// ---------------------------------------------------------------------------

// The walk sees the same objects on every pass, forever. Hanging the scan off
// "the walk saw it" would re-scan the entire storage every sync interval — on
// the 15-minute default, 96 full scans of every file a day.
//
// Three passes over an unchanged storage: scans enqueued on the first, ZERO on
// the second and third.
func TestSyncScansOnceThenNeverAgain(t *testing.T) {
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	require.NoError(t, os.WriteFile(filepath.Join(root, "rapor.txt"), []byte("kullanicinin dosyasi"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "belgeler"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "belgeler", "notlar.txt"), []byte("on bytes"), 0o644))

	sc := &cleanScanner{}
	job := avJob(store, st, drv, sc)

	syncWithAV(t, store, st, job, qd)
	first := pendingScans(t, qd)
	assert.Len(t, first, 2, "pass 1 must scan both newly catalogued files")
	require.Equal(t, 2, drainScans(t, qd, job))

	for pass := 2; pass <= 3; pass++ {
		waitPastSecondBoundary()
		syncWithAV(t, store, st, job, qd)
		assert.Emptyf(t, pendingScans(t, qd),
			"pass %d re-scanned an unchanged storage: every sync interval would re-scan everything", pass)
	}
	assert.Equal(t, 2, sc.scanned, "the bytes must have been read exactly twice in three passes")
}

// Content that DID change is scanned again — the file was replaced on the
// backend and the bytes now there have never been looked at.
//
// On an object store, which is the shape fm.example.com runs: drift there is the
// bucket's etag. The drivers that report no etag take the size + mtime path
// instead, and are covered by local_drift_test.go.
func TestSyncRescansAFileWhoseContentDrifted(t *testing.T) {
	ctx := context.Background()
	conn, store := dbtest.NewTestDB(t)
	st, drv, bucket := objStorage(t, store)
	qd := avQueue(t, conn)

	bucket.objs["gelen/rapor.txt"] = []byte("ilk surum")

	sc := &cleanScanner{}
	job := avJob(store, st, drv, sc)
	syncWithAV(t, store, st, job, qd)
	require.Equal(t, 1, drainScans(t, qd, job))

	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/gelen/rapor.txt"))
	require.NoError(t, err)
	require.NotNil(t, n)

	// A pass in between, with nothing changed: still no second scan.
	waitPastSecondBoundary()
	syncWithAV(t, store, st, job, qd)
	require.Empty(t, pendingScans(t, qd), "an unchanged object must not be re-scanned")

	// Now someone replaces the object out of band, with something longer — so
	// the row the scanner is judged against must be the fresh one.
	waitPastSecondBoundary()
	bucket.objs["gelen/rapor.txt"] = []byte("ikinci surum, epeyce daha uzun")

	syncWithAV(t, store, st, job, qd)
	assert.Equal(t, []int64{n.ID}, pendingScans(t, qd),
		"content changed on the backend and was never re-scanned")

	fresh, err := store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(len("ikinci surum, epeyce daha uzun")), fresh.Size,
		"the drift branch must judge the scan against the bytes that are there now")
}

// ---------------------------------------------------------------------------
// 3. what the walk must NOT hand to the scanner
// ---------------------------------------------------------------------------

// A sync pass walks a lot of things. Only files may reach the scanner:
// directories carry no bytes, `.versions/` snapshots are deliberately not
// scanned when taken (they are scanned on restore), and `.filex-trash/` is
// where quarantine PUTS things.
func TestSyncDoesNotScanDirectoriesVersionsOrTrash(t *testing.T) {
	ctx := context.Background()
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "belgeler"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".versions", "belgeler"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, trash.Prefix), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "belgeler", "rapor.txt"), []byte("gercek dosya"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".versions", "belgeler", "rapor.txt.1"), []byte("eski surum"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, trash.Prefix, "1700000000-abc123__virus.exe"), []byte("virus payload"), 0o644))
	// A zero-byte file: nothing to scan, and clamd would be handed an empty
	// temp file for every one of them.
	require.NoError(t, os.WriteFile(filepath.Join(root, "bos.txt"), nil, 0o644))

	sc := &cleanScanner{}
	job := avJob(store, st, drv, sc)
	syncWithAV(t, store, st, job, qd)

	real, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/belgeler/rapor.txt"))
	require.NoError(t, err)
	require.NotNil(t, real)

	assert.Equal(t, []int64{real.ID}, pendingScans(t, qd),
		"only the real file may be scanned: no directories, no .versions/, no .filex-trash/")
}

// ---------------------------------------------------------------------------
// 4. the first import must not push a person to the back of the queue
// ---------------------------------------------------------------------------

// Pointing filex at a folder that already holds files enqueues one scan per
// file, and at equal priority the queue's `priority DESC, enqueued_at ASC`
// puts every one of them AHEAD of whatever arrives next. Measured on a 20 000
// file import: an upload's scan enqueued ten seconds in waited 41 seconds to
// be picked up — most of the whole import, not most of a scan.
//
// So the sweep sits one step below everything else, and an interactive scan
// overtakes it.
func TestAnInteractiveScanOvertakesTheSyncBacklog(t *testing.T) {
	ctx := context.Background()
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "eski"), 0o755))
	for i := 0; i < 50; i++ {
		require.NoError(t, os.WriteFile(
			filepath.Join(root, "eski", fmt.Sprintf("dosya-%02d.txt", i)), []byte("eski icerik"), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "yeni.txt"), []byte("az once yuklendi"), 0o644))

	job := avJob(store, st, drv, &cleanScanner{})
	syncWithAV(t, store, st, job, qd)
	require.Len(t, pendingScans(t, qd), 51, "the import must enqueue one scan per file")

	// Every one of them is marked as the sweep it is.
	ops, _, err := qd.List(ctx, queue.StatusPending, 1000, 0)
	require.NoError(t, err)
	for _, op := range ops {
		require.Equal(t, queue.PriorityDiscovered, op.Priority,
			"a scan the sync asked for must not queue level with a person's")
	}

	// A person uploads a file: the ordinary Enqueue, after all 51 of those.
	fresh, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/yeni.txt"))
	require.NoError(t, err)
	require.NotNil(t, fresh)
	job.Enqueue(ctx, qd, fresh)

	// Enqueued last, dequeued first.
	op, err := qd.Dequeue(ctx, []string{queue.TypeAntivirusScan})
	require.NoError(t, err)
	assert.Equal(t, float64(fresh.ID), op.Payload["node_id"],
		"a person's upload waited behind the whole first import")
	assert.Equal(t, 0, op.Priority, "and it is the interactive op that came out")
}
