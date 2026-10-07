package replica

// The initial copy (#186, the maintainers' decision, 2026-10-06): a storage linked to a
// target copies what it ALREADY holds there - in the background, from the
// queue, by the rules (`skip` paths stay out), leaving alone what the target
// already has, and resumable: a copy cut off anywhere goes on from where it
// stopped, without sending a file twice. On an upgrade, every storage that
// was linked before (and so never had one) gets it once.
//
// Red before: there was no initial copy at all - linking a storage copied
// nothing it held, and nothing it was sent afterwards either (the wrapper was
// never built).

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// seed puts the storage's files in place: two to copy in folders, one under a
// `skip` rule, one the target already has (same bytes, same time), one more.
func seed(t *testing.T, l *linked) {
	t.Helper()
	writeFile(t, l.primaryDir, "a/1.txt", "one")
	writeFile(t, l.primaryDir, "a/b/2.txt", "two")
	writeFile(t, l.primaryDir, "tmp/x.tmp", "junk")
	writeFile(t, l.primaryDir, "present.txt", "same")
	writeFile(t, l.primaryDir, "z.txt", "zed")
	writeFile(t, l.targetDir, "present.txt", "same")
	at := time.Date(2025, 5, 5, 10, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(filepath.Join(l.primaryDir, "present.txt"), at, at))
	require.NoError(t, os.Chtimes(filepath.Join(l.targetDir, "present.txt"), at, at))
}

func copyRow(t *testing.T, l *linked) *model.ReplicaInitialCopy {
	t.Helper()
	c, err := l.store.GetReplicaInitialCopy(context.Background(), l.st.ID)
	require.NoError(t, err)
	require.NotNil(t, c, "the storage has no initial copy")
	return c
}

func TestInitialCopy_EverythingGoesTheRulesAndTheTargetAreRespected(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	l := newLinked(t, store, storage.RuleSpec{ID: 1, Pattern: "tmp/**", Mode: storage.ModeSkip, Priority: 1, Enabled: true})
	seed(t, l)
	q := newMemQueue()
	svc := New(store, staticWrappers{l.st.ID: l.wrapper(t)}, q, nil)

	_, err := svc.StartInitialCopy(ctx, l.st.ID, l.target.ID, false)
	require.NoError(t, err)
	require.Equal(t, 1, q.waiting(), "linking queued no copy")
	drain(t, svc, q)

	c := copyRow(t, l)
	assert.Equal(t, model.ReplicaCopyDone, c.Phase)
	assert.Equal(t, int64(5), c.Total)
	assert.Equal(t, int64(3), c.Copied, "a/1.txt, a/b/2.txt and z.txt go")
	assert.Equal(t, int64(1), c.Present, "present.txt is on the target already")
	assert.Equal(t, int64(1), c.Excluded, "tmp/x.tmp is under a skip rule")
	assert.Equal(t, int64(0), c.Failed)
	assert.NotZero(t, c.FinishedUnix)

	for rel, want := range map[string]string{"a/1.txt": "one", "a/b/2.txt": "two", "z.txt": "zed", "present.txt": "same"} {
		got, ok := readFile(t, l.targetDir, rel)
		assert.True(t, ok, "%s is not on the target", rel)
		assert.Equal(t, want, got, rel)
	}
	_, ok := readFile(t, l.targetDir, "tmp/x.tmp")
	assert.False(t, ok, "a skip path was copied")
	assert.Equal(t, 0, l.replica.count("/present.txt"), "a file the target already had was sent again")

	// The copy keeps the file's own time on a target that can.
	src, _ := os.Stat(filepath.Join(l.primaryDir, "a", "1.txt"))
	dst, _ := os.Stat(filepath.Join(l.targetDir, "a", "1.txt"))
	assert.WithinDuration(t, src.ModTime(), dst.ModTime(), 2*time.Second)
}

func TestInitialCopy_CutOffAnywhereItGoesOnWithoutSendingAFileTwice(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	l := newLinked(t, store, storage.RuleSpec{ID: 1, Pattern: "tmp/**", Mode: storage.ModeSkip, Priority: 1, Enabled: true})
	seed(t, l)
	q := newMemQueue()
	clock := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	svc := New(store, staticWrappers{l.st.ID: l.wrapper(t)}, q, nil)
	svc.SetClock(now)
	svc.SetSlice(time.Hour, 2) // two files a slice
	_, err := svc.StartInitialCopy(ctx, l.st.ID, l.target.ID, false)
	require.NoError(t, err)

	// Three slices: the count, and the copy under way.
	for i := 0; i < 4; i++ {
		op, ok := q.next()
		require.True(t, ok, "slice %d did not queue the next one", i)
		require.NoError(t, svc.HandleInitialCopy(ctx, op))
	}
	mid := copyRow(t, l)
	require.Equal(t, model.ReplicaCopyCopying, mid.Phase, "the copy should be under way")
	require.True(t, mid.Counted)
	require.NotEmpty(t, mid.Cursor, "the copy kept no place")
	require.Equal(t, 1, q.waiting())

	// The server stops; the one that starts builds everything anew. A worker
	// of the old process died holding the copy: its claim must run out, not
	// stop the copy for good.
	op, _ := q.next()
	held, err := store.ClaimReplicaInitialCopy(ctx, l.st.ID, l.target.ID, "dead-worker", clock.Unix(), clock.Add(5*time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, held)

	svc2 := New(store, staticWrappers{l.st.ID: l.wrapper(t)}, q, nil)
	svc2.SetClock(now)
	svc2.SetSlice(time.Hour, 2)
	require.NoError(t, svc2.HandleInitialCopy(ctx, op))
	require.Equal(t, 1, q.waiting(), "a copy held by a dead worker was left with nothing queued")
	clock = clock.Add(6 * time.Minute)
	drain(t, svc2, q)

	c := copyRow(t, l)
	assert.Equal(t, model.ReplicaCopyDone, c.Phase)
	assert.Equal(t, int64(3), c.Copied)
	assert.Equal(t, int64(1), c.Present)
	assert.Equal(t, int64(1), c.Excluded)
	for _, p := range []string{"/a/1.txt", "/a/b/2.txt", "/z.txt"} {
		assert.Equal(t, 1, l.replica.count(p), "%s was sent %d times", p, l.replica.count(p))
	}
}

func TestInitialCopy_RunsOnceForALinkFromBeforeTheUpgrade(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	l := newLinked(t, store) // linked on 0.52: a row, no initial copy
	writeFile(t, l.primaryDir, "old.txt", "from before")
	q := newMemQueue()
	svc := New(store, staticWrappers{l.st.ID: l.wrapper(t)}, q, nil)

	require.NoError(t, svc.ResumeInitialCopies(ctx))
	require.Equal(t, 1, q.waiting(), "a storage linked before the upgrade got no initial copy")
	drain(t, svc, q)
	got, ok := readFile(t, l.targetDir, "old.txt")
	require.True(t, ok)
	assert.Equal(t, "from before", got)

	// The next start does not run it again.
	writeFile(t, l.primaryDir, "later.txt", "x")
	require.NoError(t, svc.ResumeInitialCopies(ctx))
	assert.Equal(t, 0, q.waiting(), "a finished initial copy ran again at the next start")
	assert.Equal(t, model.ReplicaCopyDone, copyRow(t, l).Phase)
}

func TestInitialCopy_ATargetThatStopsAnsweringIsWaitedFor(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	l := newLinked(t, store)
	for i := 0; i < failRun+3; i++ {
		writeFile(t, l.primaryDir, "f"+string(rune('a'+i))+".txt", "x")
	}
	q := newMemQueue()
	clock := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	svc := New(store, staticWrappers{l.st.ID: l.wrapper(t)}, q, nil)
	svc.SetClock(func() time.Time { return clock })
	l.replica.down.Store(true)

	_, err := svc.StartInitialCopy(ctx, l.st.ID, l.target.ID, false)
	require.NoError(t, err)
	op, _ := q.next()
	require.NoError(t, svc.HandleInitialCopy(ctx, op))

	c := copyRow(t, l)
	assert.Equal(t, model.ReplicaCopyWaiting, c.Phase, "a target that does not answer failed the copy file by file")
	assert.Contains(t, c.LastError, "connection refused")
	assert.Equal(t, int64(0), c.Failed, "files that failed only because the target was down count as failed")
	assert.Equal(t, int64(0), c.Copied)
	require.Equal(t, 1, q.waiting(), "the waiting copy queued nothing to come back")
	next, _ := q.next()
	require.NotNil(t, next.NotBefore, "the waiting copy comes back at once")
	assert.True(t, next.NotBefore.After(clock))

	// The target answers again: the copy goes on and the failures it
	// recorded on the way are resolved.
	l.replica.down.Store(false)
	clock = clock.Add(defaultRetryAfter)
	require.NoError(t, svc.HandleInitialCopy(ctx, next))
	drain(t, svc, q)
	c = copyRow(t, l)
	assert.Equal(t, model.ReplicaCopyDone, c.Phase)
	assert.Equal(t, int64(failRun+3), c.Copied)
	n, err := store.CountUnresolvedReplicaFailures(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "the failures of the outage stayed open after the files went")
}

func TestInitialCopy_FollowsTheLink(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	l := newLinked(t, store)
	q := newMemQueue()
	svc := New(store, staticWrappers{l.st.ID: l.wrapper(t)}, q, nil)

	require.NoError(t, svc.LinkChanged(ctx, l.st.ID))
	require.NotNil(t, copyRow(t, l), "linking started no copy")

	// Unlinked: the copy goes.
	l.st.ReplicaTargetID = nil
	require.NoError(t, store.UpdateStorage(ctx, l.st))
	require.NoError(t, svc.LinkChanged(ctx, l.st.ID))
	c, err := store.GetReplicaInitialCopy(ctx, l.st.ID)
	require.NoError(t, err)
	assert.Nil(t, c, "an unlinked storage kept its initial copy")

	// A disabled target is no link.
	tid := l.target.ID
	l.st.ReplicaTargetID = &tid
	require.NoError(t, store.UpdateStorage(ctx, l.st))
	l.target.Enabled = false
	require.NoError(t, store.UpdateReplicationTarget(ctx, l.target))
	linked, err := svc.HasLinked(ctx)
	require.NoError(t, err)
	assert.False(t, linked, "a storage linked to a switched-off target replicates")
	require.NoError(t, svc.LinkChanged(ctx, l.st.ID))
	c, err = store.GetReplicaInitialCopy(ctx, l.st.ID)
	require.NoError(t, err)
	assert.Nil(t, c)
}
