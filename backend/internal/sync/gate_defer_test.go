package sync_test

// sec055: a scan steps back for a change that holds the storage's row gate,
// without keeping any other change out, and is deferred once the change has
// held the gate for all of filexsync.GateWait. Up to 0.55's first cut the
// gate was a sync.RWMutex: the scan queued as a writer behind the long change
// (a folder moved or deleted object by object over WebDAV or SFTP, which can
// take hours), and every change on the storage that came after it - and the
// operations queue's worker with them - waited behind the scan.
//
// Break: in listDir, take the gate with rowgate.Judge again (no budget, a
// writer's place in the queue) - the scan waits for the long change, and the
// second change waits for the scan.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/rowgate"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestAScanIsDeferredByALongChangeAndKeepsNoChangeOut(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	_, st, root := besideStorage(t, store)
	writeUnder(t, root, "klasor/icinde.txt", "icinde")
	_, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)

	was := filexsync.GateWait
	filexsync.GateWait = 300 * time.Millisecond
	t.Cleanup(func() { filexsync.GateWait = was })

	// A long change holds the gate (a folder moved object by object). The
	// cleanup lets go of it whatever happens: the gate is process-wide.
	long := rowgate.Move(st.ID)
	t.Cleanup(long)

	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	scanned := make(chan error, 1)
	go func() { scanned <- w.Trigger(ctx, st.ID) }()

	// While the scan waits for the gate, another change on the storage gets
	// it at once: a waiting scan keeps no change out.
	time.Sleep(100 * time.Millisecond)
	other := make(chan struct{})
	go func() {
		rowgate.Move(st.ID)()
		close(other)
	}()
	select {
	case <-other:
	case <-time.After(2 * time.Second):
		long()
		t.Fatal("a change waited behind a scan that was only waiting for the row gate")
	}

	// The scan does not wait for the long change: it is deferred.
	select {
	case err := <-scanned:
		require.ErrorIs(t, err, rowgate.ErrBusy)
	case <-time.After(10 * time.Second):
		long()
		t.Fatal("the scan waited for the long change instead of being deferred")
	}
	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, "aborted", run.Status, "a deferred scan is not a failed one: %s", run.Error)
	require.Contains(t, run.Error, "deferred")
	require.Zero(t, run.Deleted, "a deferred scan judged rows")

	// Once the change lets go, the next scan runs.
	long()
	require.NoError(t, w.Trigger(ctx, st.ID))
	run, err = store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", run.Status, run.Error)
}
