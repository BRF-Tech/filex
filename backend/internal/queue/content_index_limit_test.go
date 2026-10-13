package queue_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// gateStorage counts the reads in flight and holds each one until released.
type gateStorage struct {
	fakeStorage
	inFlight, most atomic.Int32
	release        chan struct{}
}

func (g *gateStorage) Read(ctx context.Context, path string) (io.ReadCloser, error) {
	n := g.inFlight.Add(1)
	for {
		m := g.most.Load()
		if n <= m || g.most.CompareAndSwap(m, n) {
			break
		}
	}
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	g.inFlight.Add(-1)
	return io.NopCloser(bytes.NewReader([]byte("ortak metin " + path))), nil
}

// The content job has a pool of its own with more workers than the shared one
// (issue #115), so that their texts can share one index write; reading and
// extracting must still happen only as many at a time as before.
func TestContentIndexer_LimitExtractionBoundsTheReadsInFlight(t *testing.T) {
	ctx := context.Background()
	idx := newContentTestIndex(t)
	nodes := fakeNodes{}
	for k := int64(1); k <= 12; k++ {
		n := textNode(k, fmt.Sprintf("n%d.txt", k), fmt.Sprintf("/n%d.txt", k), 20)
		nodes[k] = n
		require.NoError(t, idx.IndexNode(ctx, n))
	}
	drv := &gateStorage{release: make(chan struct{})}
	ci := queue.NewContentIndexer(nodes, func(int64) (storage.Driver, error) { return drv, nil }, idx, 0)
	ci.LimitExtraction(3)

	var wg sync.WaitGroup
	errs := make(chan error, len(nodes))
	for id := range nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- ci.Handle(ctx, queue.Op{Type: queue.TypeContentIndex, Payload: map[string]any{"node_id": id}})
		}()
	}
	// Let the jobs pile up at the gate, then let them all through.
	time.Sleep(300 * time.Millisecond)
	if got := drv.inFlight.Load(); got != 3 {
		t.Errorf("%d reads in flight with 12 jobs and 3 slots, want 3", got)
	}
	close(drv.release)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	if most := drv.most.Load(); most > 3 {
		t.Errorf("%d reads ran at once, the limit is 3", most)
	}
	require.Len(t, idx.SafeSearchScoped(ctx, "ortak", 100, search.ScopeContent), 12)
}

func TestContentIndexer_NoLimitByDefault(t *testing.T) {
	ci := queue.NewContentIndexer(fakeNodes{}, nil, nil, 0)
	// Nothing to read: the job returns at once, with or without a limit.
	require.NoError(t, ci.Handle(context.Background(), queue.Op{Type: queue.TypeContentIndex}))
	ci.LimitExtraction(0)
	require.NoError(t, ci.Handle(context.Background(), queue.Op{Type: queue.TypeContentIndex}))
}
