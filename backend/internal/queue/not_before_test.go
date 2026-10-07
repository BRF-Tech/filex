package queue_test

// Regression: the delayed-dispatch contract on the SQLite driver.
//
// `not_before` is compared in SQL as a STRING (SQLite has no date type), so
// what Go writes into that column has to be spelled the way CURRENT_TIMESTAMP
// is spelled. Binding a time.Time did not: the driver wrote its own rendering,
// complete with offset and monotonic suffix, and the comparison then depended
// on the server's timezone — three hours late at UTC+3, and no delay at all
// west of UTC. Nothing caught it because the existing tests overrode
// not_before instead of waiting for it, and CI runs at UTC where the bug is
// invisible.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/queue"
)

// The same instant, written from three different zones, must schedule
// identically. If the stored value carried the local offset, the UTC+3 and
// UTC-5 spellings would land hours apart.
func TestNotBefore_IsZoneIndependent(t *testing.T) {
	ctx := context.Background()
	drv := setupSQLite(t)

	base := time.Now().Add(2 * time.Hour)
	for _, loc := range []*time.Location{
		time.UTC,
		time.FixedZone("UTC+3", 3*60*60),
		time.FixedZone("UTC-5", -5*60*60),
	} {
		at := base.In(loc)
		id, err := drv.Enqueue(ctx, queue.Op{Type: "zonecheck", NotBefore: &at})
		require.NoError(t, err)
		got, err := drv.Get(ctx, id)
		require.NoError(t, err)
		require.NotNil(t, got.NotBefore)
		assert.WithinDuration(t, base, *got.NotBefore, time.Second,
			"stored not_before must be the same instant regardless of the zone it was written in")
	}

	// And none of them is runnable, in any zone.
	_, err := drv.Dequeue(ctx, []string{"zonecheck"})
	assert.ErrorIs(t, err, queue.ErrEmpty)
}

// A delay in the future really holds the op back, and it really is released
// once the delay passes. This is the property the whole debounced save-scan
// rests on. TestDriverContract_NotBeforeHoldsThenReleases asks the same of
// every configured driver.
func TestNotBefore_HoldsThenReleases(t *testing.T) {
	assertHoldsThenReleases(t, setupSQLite(t))
}

// assertHoldsThenReleases checks a delayed op against the CLOCK, never
// against how fast the test itself runs.
//
// ⚠⚠ The first version enqueued an op two seconds out and asked "is it
// runnable right after Enqueue?". In the 0.53 full chain run (2026-10-07, on
// a build host) the disk answered a write in 130-440 ms on average while that test
// ran, enqueuing took more than the delay, the op HAD reached its time, and
// the queue rightly gave it out: red for the host, not for the queue. A busy
// host can only make Dequeue LATER, never earlier, so three things are asked
// that hold however slow it is. The hold: an op due an hour from now is never
// given out. The release: an op due in a moment is given out. Never early:
// when it is given out, the clock read after Dequeue returned is already past
// its time. That last check is the one that found the SQL and Redis drivers
// rounding the deadline DOWN to its second.
func assertHoldsThenReleases(t *testing.T, drv queue.Driver) {
	t.Helper()
	ctx := context.Background()

	far := time.Now().Add(time.Hour)
	_, err := drv.Enqueue(ctx, queue.Op{Type: "delayed", NotBefore: &far})
	require.NoError(t, err)

	at := time.Now().Add(1500 * time.Millisecond)
	id, err := drv.Enqueue(ctx, queue.Op{Type: "delayed", NotBefore: &at})
	require.NoError(t, err)

	// Wall clocks on both sides: the database compares wall-clock time, and
	// Round(0) drops the monotonic reading a time.Now() carries.
	wallAt := at.Round(0)
	deadline := time.Now().Add(30 * time.Second)
	for {
		op, derr := drv.Dequeue(ctx, []string{"delayed"})
		returned := time.Now().Round(0)
		if derr == nil {
			require.Equal(t, id, op.ID, "the op due in an hour must not be given out")
			require.False(t, returned.Before(wallAt),
				"must not be runnable before its time: given out %v early", wallAt.Sub(returned))
			break
		}
		require.ErrorIs(t, derr, queue.ErrEmpty)
		require.True(t, time.Now().Before(deadline), "must become runnable once the delay passes")
		time.Sleep(50 * time.Millisecond)
	}

	_, err = drv.Dequeue(ctx, []string{"delayed"})
	assert.ErrorIs(t, err, queue.ErrEmpty, "the op due in an hour is still held")
}
