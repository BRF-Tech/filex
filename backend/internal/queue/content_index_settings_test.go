package queue_test

// The content job reads its settings per file (task #261): the switch and the
// size cap on the Search page apply to the next file, without a restart.

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
)

func TestContentIndexer_TheSizeCapIsLive(t *testing.T) {
	var limit atomic.Int64
	limit.Store(1024)
	ci := queue.NewContentIndexer(fakeNodes{}, nil, nil, 1<<30)
	ci.AttachSettings(limit.Load, nil)

	n := textNode(1, "a.txt", "/a.txt", 2048)
	assert.False(t, ci.Eligible(n), "over the live cap of 1 KiB")
	limit.Store(4096)
	assert.True(t, ci.Eligible(n), "the raised cap did not apply to the next check")
	// A cap that cannot be read (0) falls back to the constructor's.
	limit.Store(0)
	assert.True(t, ci.Eligible(n))
}

func TestContentIndexer_SwitchedOffEnqueuesNothing(t *testing.T) {
	ctx := context.Background()
	drv := setupSQLite(t)
	var on atomic.Bool
	ci := queue.NewContentIndexer(fakeNodes{}, nil, nil, 1024)
	ci.AttachSettings(nil, on.Load)

	ci.Enqueue(ctx, drv, textNode(1, "a.txt", "/a.txt", 100))
	_, total, err := drv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 0, total, "content search is off and a job was queued")

	on.Store(true)
	ci.Enqueue(ctx, drv, textNode(2, "b.txt", "/b.txt", 100))
	_, total, err = drv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total, "switched back on, the next file was not queued")
}

// A job queued while the switch was on does nothing when it comes up after
// the switch went off: no read, no index write.
func TestContentIndexer_AQueuedJobDoesNothingOnceSwitchedOff(t *testing.T) {
	ctx := context.Background()
	idx := newContentTestIndex(t)
	n := textNode(7, "notes.txt", "/notes.txt", 64)
	require.NoError(t, idx.IndexNode(ctx, n))
	reads := 0
	ci := queue.NewContentIndexer(fakeNodes{7: n}, func(int64) (storage.Driver, error) {
		reads++
		return &fakeStorage{files: map[string][]byte{"/notes.txt": []byte("harbour minutes")}}, nil
	}, idx, 0)
	ci.AttachSettings(nil, func() bool { return false })
	require.NoError(t, ci.Handle(ctx, queue.Op{Type: queue.TypeContentIndex, Payload: map[string]any{"node_id": int64(7)}}))
	assert.Zero(t, reads, "a switched-off job read the file")
	assert.Empty(t, idx.SafeSearchScoped(ctx, "harbour", 10, search.ScopeContent), "a switched-off job indexed text")
}
