package queue_test

// The debounced save-scan: EnqueueAfterSave plus the queue's not_before and
// coalescing key. Each test states the property it pins, because every one of
// them is a way this feature could silently stop protecting anything.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// sqliteQueueAt opens the queue on a NAMED file so the same database can be
// reopened by a second driver. That is how the restart proof is expressed.
func sqliteQueueAt(t *testing.T, path string) (queue.Driver, func()) {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	_, err = conn.Exec(`
CREATE TABLE IF NOT EXISTS ops_queue (
    id            TEXT PRIMARY KEY,
    type          TEXT NOT NULL,
    payload       TEXT NOT NULL DEFAULT '{}',
    status        TEXT NOT NULL DEFAULT 'pending',
    priority      INTEGER NOT NULL DEFAULT 0,
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 3,
    last_error    TEXT,
    enqueued_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at    DATETIME,
    finished_at   DATETIME,
    not_before    DATETIME,
    dedup_key     TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ops_queue_dedup_pending
    ON ops_queue (dedup_key)
    WHERE dedup_key IS NOT NULL AND status = 'pending';`)
	require.NoError(t, err)

	drv, err := queue.Get("sqlite")
	require.NoError(t, err)
	require.NoError(t, drv.Init(context.Background(), map[string]any{"db": conn}))
	return drv, func() { _ = conn.Close() }
}

// avJobFor wires a scanner job over fixed bytes. A nil captureNotify is passed
// as an untyped nil so the notify.Service interface really is nil inside.
func avJobFor(files map[string][]byte, nodes map[int64]*model.Node, n *captureNotify) (*queue.AntivirusScanner, *fakeAVDriver, *fakeAVStore) {
	sdrv := &fakeAVDriver{files: files}
	store := &fakeAVStore{nodes: nodes}
	var sink notify.Service
	if n != nil {
		sink = n
	}
	job := queue.NewAntivirusScanner(store,
		func(int64) (storage.Driver, error) { return sdrv, nil },
		&fakeAVScanner{}, sink, nil, 0)
	return job, sdrv, store
}

// The headline property: a burst of Ctrl+S costs exactly one scan, scheduled
// one window out, and the scan reads the LAST content, not the bytes of the
// save that scheduled it.
func TestEnqueueAfterSave_OneScanPerWindow_ReadsFinalContent(t *testing.T) {
	ctx := context.Background()
	qdrv := setupSQLite(t)

	n := avFileNode(1, "/notes.txt", 40)
	files := map[string][]byte{n.StorageKey: []byte("save 1 clean")}
	notif := &captureNotify{}
	job, sdrv, store := avJobFor(files, map[int64]*model.Node{1: n}, notif)

	const window = 30 * time.Minute
	before := time.Now()

	// Five saves, each leaving different bytes on the driver. Only the last is
	// infected, so "the scan read the last content" is provable rather than
	// asserted.
	for i, body := range []string{
		"save 1 clean", "save 2 clean", "save 3 clean",
		"save 4 clean", "save 5 VIRUS",
	} {
		sdrv.files[n.StorageKey] = []byte(body)
		job.EnqueueAfterSave(ctx, qdrv, n, window)
		_, total, err := qdrv.List(ctx, queue.StatusPending, 10, 0)
		require.NoError(t, err)
		require.EqualValues(t, 1, total,
			"after save %d there must still be exactly one pending scan", i+1)
	}

	ops, _, err := qdrv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	require.Len(t, ops, 1)
	op := ops[0]
	assert.Equal(t, queue.TypeAntivirusScan, op.Type)
	assert.EqualValues(t, 1, op.Payload["node_id"])
	require.NotNil(t, op.NotBefore,
		"the delay must be the queue's, not a timer inside this process")
	assert.WithinDuration(t, before.Add(window), *op.NotBefore, time.Minute)

	// The window elapses; the worker runs the one op it finds.
	require.NoError(t, job.Handle(ctx, op))

	require.Len(t, store.retags, 1, "the infected FINAL content must be quarantined")
	require.Len(t, sdrv.moves, 1)
	assert.Equal(t, n.StorageKey, sdrv.moves[0][0])
	assert.Contains(t, sdrv.moves[0][1], ".filex-trash/")
}

// An infected verdict reached through the DELAYED path must behave exactly
// like one reached from an upload: quarantine plus a file.infected event.
func TestEnqueueAfterSave_InfectedBehavesLikeAnUpload(t *testing.T) {
	ctx := context.Background()
	qdrv := setupSQLite(t)

	n := avFileNode(7, "/evil.txt", 20)
	notif := &captureNotify{}
	job, _, store := avJobFor(map[string][]byte{n.StorageKey: []byte("VIRUS")},
		map[int64]*model.Node{7: n}, notif)

	job.EnqueueAfterSave(ctx, qdrv, n, 30*time.Minute)
	ops, _, err := qdrv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	require.Len(t, ops, 1)
	require.NoError(t, job.Handle(ctx, ops[0]))

	require.Len(t, store.retags, 1)
	require.Len(t, notif.events, 1)
	assert.Equal(t, notify.EventFileInfected, notif.events[0].Event)
	assert.Equal(t, true, notif.events[0].Meta["quarantined"])
}

// Once the pending op is gone, the next save schedules a new scan. Without
// this a file would be scanned once and then never again, however much it
// changed afterwards.
func TestEnqueueAfterSave_NewScanAfterTheWindow(t *testing.T) {
	ctx := context.Background()
	qdrv := setupSQLite(t)

	n := avFileNode(1, "/a.txt", 10)
	job, _, _ := avJobFor(map[string][]byte{n.StorageKey: []byte("x")},
		map[int64]*model.Node{1: n}, nil)

	job.EnqueueAfterSave(ctx, qdrv, n, 10*time.Millisecond)
	job.EnqueueAfterSave(ctx, qdrv, n, 10*time.Millisecond)
	_, total, err := qdrv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total, "second save inside the window is absorbed")

	// The window passes and a worker claims the scan.
	require.Eventually(t, func() bool {
		_, err := qdrv.Dequeue(ctx, []string{queue.TypeAntivirusScan})
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)

	job.EnqueueAfterSave(ctx, qdrv, n, 10*time.Millisecond)
	_, total, err = qdrv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total, "a save after the window schedules a fresh scan")
}

// The restart proof. A time.AfterFunc would die with the process and take the
// pending scan with it, on every deploy, for every file mid-window. The delay
// is a row in the queue, so a brand-new driver over the same database finds it
// and runs it.
func TestEnqueueAfterSave_SurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "queue.db")

	n := avFileNode(1, "/notes.txt", 30)
	files := map[string][]byte{n.StorageKey: []byte("VIRUS")}
	nodes := map[int64]*model.Node{1: n}

	// Process #1: the save happens, the scan is scheduled, the process dies.
	q1, close1 := sqliteQueueAt(t, dbPath)
	job1, _, _ := avJobFor(files, nodes, nil)
	job1.EnqueueAfterSave(ctx, q1, n, 150*time.Millisecond)
	ops, total, err := q1.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	scheduledID := ops[0].ID
	close1()

	// Process #2: a fresh driver, nothing in memory carried over.
	q2, close2 := sqliteQueueAt(t, dbPath)
	defer close2()
	notif := &captureNotify{}
	job2, _, store2 := avJobFor(files, nodes, notif)

	got, total, err := q2.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total, "the pending scan must survive the restart")
	assert.Equal(t, scheduledID, got[0].ID)
	require.NotNil(t, got[0].NotBefore)

	var claimed queue.Op
	require.Eventually(t, func() bool {
		op, err := q2.Dequeue(ctx, []string{queue.TypeAntivirusScan})
		if err != nil {
			return false
		}
		claimed = op
		return true
	}, 5*time.Second, 20*time.Millisecond)

	require.NoError(t, job2.Handle(ctx, claimed))
	assert.Len(t, store2.retags, 1, "the scan the dead process scheduled still ran")
}

// A nonsense window must not be read as "never". The safe reading is "now".
func TestEnqueueAfterSave_NonPositiveWindowScansImmediately(t *testing.T) {
	ctx := context.Background()
	qdrv := setupSQLite(t)
	n := avFileNode(1, "/a.txt", 10)
	job, _, _ := avJobFor(map[string][]byte{n.StorageKey: []byte("x")},
		map[int64]*model.Node{1: n}, nil)

	job.EnqueueAfterSave(ctx, qdrv, n, 0)

	ops, total, err := qdrv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	assert.Nil(t, ops[0].NotBefore, "no delay, and no coalescing key left behind")
}

// Ineligible nodes are skipped here exactly as they are on the immediate path.
func TestEnqueueAfterSave_RespectsEligibility(t *testing.T) {
	ctx := context.Background()
	qdrv := setupSQLite(t)
	job := queue.NewAntivirusScanner(&fakeAVStore{}, nil, &fakeAVScanner{}, nil, nil, 1024)

	job.EnqueueAfterSave(ctx, qdrv, avFileNode(2, "/big.bin", 4096), time.Minute)
	job.EnqueueAfterSave(ctx, qdrv, avFileNode(3, "/empty.bin", 0), time.Minute)
	job.EnqueueAfterSave(ctx, qdrv, avFileNode(4, "/.filex-trash/1-x__a.bin", 10), time.Minute)
	job.EnqueueAfterSave(ctx, qdrv, nil, time.Minute)

	_, total, err := qdrv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)
}

func TestAVDedupKey_IsPerNode(t *testing.T) {
	assert.Equal(t, "antivirus_scan:42", queue.AVDedupKey(42))
	assert.NotEqual(t, queue.AVDedupKey(1), queue.AVDedupKey(2))
}
