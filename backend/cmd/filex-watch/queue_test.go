package main

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// A burst is sent once it has been quiet for `quiet`, or once it has lasted
// `max_wait` - whichever comes first.
func TestQueue_DueAfterQuietOrMaxWait(t *testing.T) {
	q := newQueue(100)
	assert.False(t, q.due(t0, time.Second, 10*time.Second), "nothing waiting")

	q.addFolder("/a", t0)
	assert.False(t, q.due(t0.Add(500*time.Millisecond), time.Second, 10*time.Second))
	assert.True(t, q.due(t0.Add(time.Second), time.Second, 10*time.Second), "quiet for a second")

	// A burst that keeps going is still sent at max_wait.
	q2 := newQueue(100)
	for i := 0; i < 12; i++ {
		q2.addFolder(fmt.Sprintf("/f%d", i), t0.Add(time.Duration(i)*900*time.Millisecond))
	}
	now := t0.Add(10 * time.Second)
	assert.False(t, q2.due(now.Add(-time.Millisecond), time.Second, 10*time.Second), "never quiet, not yet max_wait")
	assert.True(t, q2.due(now, time.Second, 10*time.Second))
}

// One report carries at most the limit; the rest stays, sorted, for the next.
func TestQueue_TakeIsBoundedAndSorted(t *testing.T) {
	q := newQueue(100)
	for _, p := range []string{"/c", "/a", "/b"} {
		q.addFolder(p, t0)
	}
	q.addSubtree("/big", t0)
	b := q.take(3)
	assert.Equal(t, []string{"/big"}, b.subtrees)
	assert.Equal(t, []string{"/a", "/b"}, b.folders)
	assert.False(t, b.resync)
	rest := q.take(3)
	assert.Equal(t, []string{"/c"}, rest.folders)
	assert.True(t, q.take(3).empty())
}

// Past its bound the queue gives the folders up for one resync: memory stays
// flat however much of the disk is rewritten.
func TestQueue_OverflowBecomesAResync(t *testing.T) {
	q := newQueue(16)
	for i := 0; i < 17; i++ {
		q.addFolder(fmt.Sprintf("/f%02d", i), t0)
	}
	assert.Equal(t, 1, q.takeOverflows())
	assert.Equal(t, 0, q.takeOverflows(), "said once")
	b := q.take(1000)
	assert.True(t, b.resync)
	assert.Empty(t, b.folders)
}

// A batch filex did not take goes back, merged with what came meanwhile, and
// still within the bound.
func TestQueue_PutBackMergesWithinTheBound(t *testing.T) {
	q := newQueue(16)
	q.addFolder("/a", t0)
	b := q.take(1000)
	q.addFolder("/b", t0)
	q.putBack(b, t0)
	again := q.take(1000)
	assert.Equal(t, []string{"/a", "/b"}, again.folders)

	q.markResync(t0)
	r := q.take(1000)
	q.putBack(r, t0)
	assert.True(t, q.take(1000).resync, "a resync that was not delivered is not forgotten")

	big := batch{}
	for i := 0; i < 20; i++ {
		big.folders = append(big.folders, fmt.Sprintf("/x%02d", i))
	}
	q.putBack(big, t0)
	got := q.take(1000)
	assert.True(t, got.resync)
	assert.Empty(t, got.folders)
}

// The watcher adds while the sender takes, from two goroutines.
func TestQueue_ConcurrentUse(t *testing.T) {
	q := newQueue(1_000_000)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				q.addFolder(fmt.Sprintf("/g%d/%d", g, i), time.Now())
			}
		}(g)
	}
	seen := map[string]bool{}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	for {
		for _, p := range q.take(100).folders {
			seen[p] = true
		}
		select {
		case <-done:
			for _, p := range q.take(1_000_000).folders {
				seen[p] = true
			}
			require.Len(t, seen, 2000)
			return
		default:
		}
	}
}
