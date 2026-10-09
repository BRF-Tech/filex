package queue_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/gatetest"
)

// rowgateAVStore is fakeAVStore with a lock: the quarantine writes its retag
// on the worker's goroutine while the test reads it from a storage scan's.
type rowgateAVStore struct {
	mu      sync.Mutex
	nodes   map[int64]*model.Node
	trashed map[int64]string
}

func (f *rowgateAVStore) GetNode(_ context.Context, id int64) (*model.Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.nodes[id]
	if !ok {
		return nil, nil
	}
	return n, nil
}

func (f *rowgateAVStore) SoftDeleteAndRetag(_ context.Context, id int64, trashPath, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trashed[id] = trashPath
	return nil
}

func (f *rowgateAVStore) retagged(id int64) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.trashed[id]
	return p, ok
}

// Issue #201: the antivirus quarantine is a two-step change - the infected
// bytes move into `.filex-trash/`, then the row is retagged there - and it
// holds the storage's row gate (internal/rowgate) from the one to the other.
// Judged half way, a storage scan saw a live row whose bytes had left, confirmed
// it gone by a Stat of its old path and dropped it: the quarantined file stayed
// in the trash with no entry to list, restore or purge it by.
//
// Break: take rowgate.Move out of AntivirusScanner.quarantine - the scan's
// judgement gets the gate while the row still names the live path.
func TestAntivirusScan_Quarantine_HoldsTheRowGateUntilTheRowFollows(t *testing.T) {
	const storageID = 9_201_101
	drv := gatetest.New(t)
	drv.Arm(false)
	ctx := context.Background()
	require.NoError(t, drv.Write(ctx, "inbox/malware.exe", strings.NewReader("VIRUS payload"), 13))
	drv.Arm(true)

	n := avFileNode(11, "/inbox/malware.exe", 13)
	n.StorageID = storageID
	st := &rowgateAVStore{nodes: map[int64]*model.Node{11: n}, trashed: map[int64]string{}}
	job := queue.NewAntivirusScanner(st, func(int64) (storage.Driver, error) { return drv, nil },
		&fakeAVScanner{}, nil, nil, 0)

	done := make(chan error, 1)
	go func() {
		done <- job.Handle(ctx, queue.Op{
			Type: queue.TypeAntivirusScan, Payload: map[string]any{"node_id": int64(11)}})
	}()

	gatetest.HeldHalfWay(t, storageID, drv, func() bool {
		p, ok := st.retagged(11)
		return ok && strings.HasPrefix(p, "/.filex-trash/")
	})
	require.NoError(t, <-done)
	_, err := drv.Stat(ctx, "inbox/malware.exe")
	assert.ErrorIs(t, err, storage.ErrNotFound, "the infected file left its live path")
}

// rename re-homes row id as a rename does: a new row value at the new path
// (the scanner holds the old one).
func (f *rowgateAVStore) rename(id int64, to string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *f.nodes[id]
	cp.Path = to
	cp.Name = to[strings.LastIndex(to, "/")+1:]
	cp.StorageKey = strings.TrimPrefix(to, "/")
	f.nodes[id] = &cp
}

// pausingAVScanner reads the bytes, then holds its verdict until letGo: a
// scan of a large file, which takes its time. Only the first scan pauses.
type pausingAVScanner struct {
	reached, letGo chan struct{}
	once           sync.Once
}

func (s *pausingAVScanner) Supports() bool { return true }

func (s *pausingAVScanner) Scan(_ context.Context, r io.Reader) (bool, string, error) {
	b, _ := io.ReadAll(r)
	s.once.Do(func() { close(s.reached) })
	<-s.letGo
	return bytes.Contains(b, []byte("VIRUS")), "Test-Signature", nil
}

// sec055: the quarantine reads the row again once it holds the row gate. A
// scan takes its time; a file renamed while it ran is no longer where the
// verdict found it. The quarantine used to move the OLD path into the trash,
// take "not found" for success, report the file quarantined and retag its row
// into the trash - while the infected bytes stayed live at the new name.
//
// Break: drop the re-read in AntivirusScanner.quarantine, or count
// storage.ErrNotFound from the move as quarantined again - the first Handle
// answers nil and the row is retagged with the bytes still live.
func TestAntivirusScan_Quarantine_DoesNotMissAFileRenamedWhileItWasScanned(t *testing.T) {
	const storageID = 9_201_102
	drv := gatetest.New(t)
	drv.Arm(false)
	ctx := context.Background()
	require.NoError(t, drv.Write(ctx, "inbox/malware.exe", strings.NewReader("VIRUS payload"), 13))

	n := avFileNode(12, "/inbox/malware.exe", 13)
	n.StorageID = storageID
	st := &rowgateAVStore{nodes: map[int64]*model.Node{12: n}, trashed: map[int64]string{}}
	sc := &pausingAVScanner{reached: make(chan struct{}), letGo: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-sc.letGo:
		default:
			close(sc.letGo)
		}
	})
	job := queue.NewAntivirusScanner(st, func(int64) (storage.Driver, error) { return drv, nil },
		sc, nil, nil, 0)
	op := queue.Op{Type: queue.TypeAntivirusScan, Payload: map[string]any{"node_id": int64(12)}}

	done := make(chan error, 1)
	go func() { done <- job.Handle(ctx, op) }()
	select {
	case <-sc.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the scan never read the file")
	}
	// Renamed while the scan runs - the bytes and the row, under the gate,
	// the way every filex surface renames.
	require.NoError(t, rowgate.Change(storageID,
		func() error { return drv.Move(ctx, "inbox/malware.exe", "inbox/renamed.exe") },
		func() { st.rename(12, "/inbox/renamed.exe") },
	))
	close(sc.letGo)

	select {
	case err := <-done:
		require.Error(t, err, "the quarantine reported a file it never moved: the infected bytes are live at the new name")
	case <-time.After(10 * time.Second):
		t.Fatal("the quarantine never finished")
	}
	_, retagged := st.retagged(12)
	require.False(t, retagged, "the row went to the trash while its infected bytes stayed live")
	_, err := drv.Stat(ctx, "inbox/renamed.exe")
	require.NoError(t, err)

	// The queue's retry scans the row where it is now, and quarantines it.
	require.NoError(t, job.Handle(ctx, op))
	p, ok := st.retagged(12)
	require.True(t, ok, "the retry did not quarantine the renamed file")
	require.True(t, strings.HasPrefix(p, "/.filex-trash/"), "retagged to %q", p)
	_, err = drv.Stat(ctx, "inbox/renamed.exe")
	require.ErrorIs(t, err, storage.ErrNotFound, "the infected file stayed at its new name")
}

// sec055 follow-up: a file whose folder is being moved or trashed object by
// object (a fence on the folder's prefix, rowgate.FenceCtx) is that change's.
// The quarantine does not take it out from under the move: the move may not
// have reached it yet (taken away, it breaks the move half way), or may have
// moved it already while the row still names the old path. It fails, and the
// queue's retry quarantines the file once the fence has opened.
//
// Break: take the rowgate.Fences check out of AntivirusScanner.quarantine -
// the first Handle moves the file into the trash while its folder is fenced.
func TestAntivirusScan_Quarantine_WaitsOutAFenceOverItsFolder(t *testing.T) {
	const storageID = 9_201_103
	drv := gatetest.New(t)
	drv.Arm(false)
	ctx := context.Background()
	require.NoError(t, drv.Write(ctx, "inbox/malware.exe", strings.NewReader("VIRUS payload"), 13))

	n := avFileNode(13, "/inbox/malware.exe", 13)
	n.StorageID = storageID
	st := &rowgateAVStore{nodes: map[int64]*model.Node{13: n}, trashed: map[int64]string{}}
	job := queue.NewAntivirusScanner(st, func(int64) (storage.Driver, error) { return drv.ObjectStore(), nil },
		&fakeAVScanner{}, nil, nil, 0)
	op := queue.Op{Type: queue.TypeAntivirusScan, Payload: map[string]any{"node_id": int64(13)}}

	release, err := rowgate.FenceCtx(ctx, storageID, "/inbox", "/arsiv/inbox")
	require.NoError(t, err)
	// Process-wide: let go however the test ends.
	t.Cleanup(release)

	require.Error(t, job.Handle(ctx, op), "the quarantine took a file out from under its folder's move")
	_, retagged := st.retagged(13)
	require.False(t, retagged, "the row went to the trash while its folder was fenced")
	_, err = drv.Stat(ctx, "inbox/malware.exe")
	require.NoError(t, err, "the file left its folder while the folder was being moved")

	release()
	require.NoError(t, job.Handle(ctx, op))
	p, ok := st.retagged(13)
	require.True(t, ok, "the retry did not quarantine the file once the fence opened")
	require.True(t, strings.HasPrefix(p, "/.filex-trash/"), "retagged to %q", p)
	_, err = drv.Stat(ctx, "inbox/malware.exe")
	require.ErrorIs(t, err, storage.ErrNotFound, "the infected file stayed live")
}
