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
// rests on.
func TestNotBefore_HoldsThenReleases(t *testing.T) {
	ctx := context.Background()
	drv := setupSQLite(t)

	at := time.Now().Add(2 * time.Second)
	id, err := drv.Enqueue(ctx, queue.Op{Type: "delayed", NotBefore: &at})
	require.NoError(t, err)

	_, err = drv.Dequeue(ctx, []string{"delayed"})
	require.ErrorIs(t, err, queue.ErrEmpty, "must not be runnable before its time")

	var got queue.Op
	require.Eventually(t, func() bool {
		op, derr := drv.Dequeue(ctx, []string{"delayed"})
		if derr != nil {
			return false
		}
		got = op
		return true
	}, 10*time.Second, 100*time.Millisecond, "must become runnable once the delay passes")
	assert.Equal(t, id, got.ID)
}
