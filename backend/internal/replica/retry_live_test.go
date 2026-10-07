package replica

// Fix all and Fix one replay a failure through the storage's LIVE wrapper.
//
// ⚠ The Service was built with a nil wrapper ("v0.1 skips that"): every retry
// answered "replica retry: no replica configured", whatever was linked, and
// the failure stayed open forever (#186). With several storages replicating
// to different targets, a failure also has to say whose it is: the same path
// on two storages is two failures (migration 00094).

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/queue"
)

func TestFixAll_RepairsThroughTheLiveWrapper(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	l := newLinked(t, store)
	writeFile(t, l.primaryDir, "rapor.docx", "v2")
	require.NoError(t, store.UpsertReplicaFailure(ctx, l.st.ID, "/rapor.docx", "write", "REPLICA_WRITE_FAIL", "connection reset"))
	q := newMemQueue()
	svc := New(store, staticWrappers{l.st.ID: l.wrapper(t)}, q, nil)

	got, err := svc.ReconcileAll(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Queued)
	op, ok := q.next()
	require.True(t, ok)
	assert.Equal(t, queue.TypeReplicaRetry, op.Type)
	require.NoError(t, svc.HandleRetry(ctx, op), "the retry did not go through the storage's wrapper")

	body, ok := readFile(t, l.targetDir, "rapor.docx")
	require.True(t, ok, "Fix all left the target without the file")
	assert.Equal(t, "v2", body)
	n, err := store.CountUnresolvedReplicaFailures(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "a repaired failure stayed open")
}

func TestFixOne_TheSamePathOnTwoStoragesIsTwoFailures(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	a := newLinked(t, store)
	b := newLinked(t, store)
	writeFile(t, a.primaryDir, "x.txt", "from a")
	writeFile(t, b.primaryDir, "x.txt", "from b")
	require.NoError(t, store.UpsertReplicaFailure(ctx, a.st.ID, "/x.txt", "write", "E", "a"))
	require.NoError(t, store.UpsertReplicaFailure(ctx, b.st.ID, "/x.txt", "write", "E", "b"))
	rows, total, err := store.ListReplicaFailures(ctx, true, 50, 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, total, "one storage's failure overwrote the other's")

	q := newMemQueue()
	svc := New(store, staticWrappers{a.st.ID: a.wrapper(t), b.st.ID: b.wrapper(t)}, q, nil)
	queued, err := svc.FixOne(ctx, b.st.ID, "/x.txt", "write")
	require.NoError(t, err)
	require.True(t, queued)
	drain(t, svc, q)

	body, ok := readFile(t, b.targetDir, "x.txt")
	require.True(t, ok)
	assert.Equal(t, "from b", body)
	_, ok = readFile(t, a.targetDir, "x.txt")
	assert.False(t, ok, "storage a's target got storage b's repair")

	rows, _, err = store.ListReplicaFailures(ctx, true, 50, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, a.st.ID, rows[0].StorageID, "the failure left open is storage a's")
}

func TestHandleRetry_AStorageThatNoLongerReplicatesHasNothingToRepair(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	l := newLinked(t, store)
	require.NoError(t, store.UpsertReplicaFailure(ctx, l.st.ID, "/a.txt", "delete", "E", "x"))
	q := newMemQueue()
	svc := New(store, staticWrappers{}, q, nil) // unlinked since

	_, err := svc.ReconcileAll(ctx)
	require.NoError(t, err)
	drain(t, svc, q)
	n, err := store.CountUnresolvedReplicaFailures(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "a failure nobody can repair any more stays in the list for ever")
}

func TestHasLinked(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	svc := New(store, nil, nil, nil)
	got, err := svc.HasLinked(ctx)
	require.NoError(t, err)
	assert.False(t, got, "no storage, nothing replicates")

	newLinked(t, store)
	got, err = svc.HasLinked(ctx)
	require.NoError(t, err)
	assert.True(t, got)
}

// Fix all replays EVERY unresolved failure. It asked the store for 10,000
// rows in one page, the store clamps a page above 1,000 to 100, and so it
// replayed the newest hundred and said nothing of the rest - an initial copy
// into a target that refused one kind of file leaves far more than that.
func TestFixAll_ReplaysMoreThanOnePage(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	l := newLinked(t, store)
	const n = 1150
	for i := 0; i < n; i++ {
		require.NoError(t, store.UpsertReplicaFailure(ctx, l.st.ID, "/f"+strconv.Itoa(i)+".txt", "write", "E", "x"))
	}
	q := newMemQueue()
	svc := New(store, staticWrappers{l.st.ID: l.wrapper(t)}, q, nil)
	got, err := svc.ReconcileAll(ctx)
	require.NoError(t, err)
	assert.Equal(t, n, got.Queued, "Fix all stopped at one page of failures")
	assert.Equal(t, n, q.waiting())
}
