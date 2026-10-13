package search

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

func openBatchIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := Open(filepath.Join(t.TempDir(), "idx.bleve"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func contentNode(id int64) *model.Node {
	return &model.Node{ID: id, StorageID: 1, Name: fmt.Sprintf("n%d.txt", id), Path: fmt.Sprintf("/n%d.txt", id),
		Type: model.NodeTypeFile, Mime: "text/plain", Size: 10, Etag: fmt.Sprintf("e%d", id)}
}

// batches is how many batches the live index has applied so far.
func batches(t *testing.T, idx *Index) uint64 {
	t.Helper()
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	stats, ok := idx.bleve.StatsMap()["index"].(map[string]interface{})
	if !ok {
		t.Fatal("the index reports no scorch stats")
	}
	n, ok := stats["TotBatches"].(uint64)
	if !ok {
		t.Fatalf("TotBatches is %T", stats["TotBatches"])
	}
	return n
}

// Issue #115: one Bleve write per extracted text was one segment per file,
// and the merger rewrote those segments until the host ran out of memory.
// Texts that arrive together are written together.
func TestContentWrites_ThatArriveTogetherShareOneBatch(t *testing.T) {
	idx := openBatchIndex(t)
	ctx := context.Background()
	before := batches(t, idx)

	const writers = 32
	// The writer takes its first batch only once every writer has queued: on
	// a loaded machine goroutines start late, and a clock-based linger would
	// be a guess about how late.
	var once sync.Once
	idx.cq.beforeDrain = func() {
		once.Do(func() {
			deadline := time.Now().Add(time.Minute)
			for time.Now().Before(deadline) {
				idx.cq.mu.Lock()
				n := len(idx.cq.pending)
				idx.cq.mu.Unlock()
				if n >= writers {
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		})
	}
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for k := 1; k <= writers; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- idx.IndexNodeContent(ctx, contentNode(int64(k)), fmt.Sprintf("ortak kelime belge%d", k))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if got := batches(t, idx) - before; got > 2 {
		t.Errorf("%d texts queued together took %d batches; they are meant to share one", writers, got)
	}
	if hits := idx.SafeSearchScoped(ctx, "ortak", 100, ScopeContent); len(hits) != writers {
		t.Errorf("%d documents found by their text, want %d", len(hits), writers)
	}
}

// A write returns only once its text is in the index: a queue job is
// acknowledged after the write, as it always was.
func TestContentWrite_ReturnsOnceSearchable(t *testing.T) {
	idx := openBatchIndex(t)
	ctx := context.Background()
	if err := idx.IndexNodeContent(ctx, contentNode(7), "toplantı kararı kapasite"); err != nil {
		t.Fatal(err)
	}
	if hits := idx.SafeSearchScoped(ctx, "kapasite", 10, ScopeContent); len(hits) != 1 {
		t.Fatalf("%d hits right after the write returned, want 1", len(hits))
	}
}

// fakeMemory is the process's memory as a test sets it, with the gate's hooks
// reporting what it did.
type fakeMemory struct {
	held      atomic.Uint64
	collected atomic.Int32
	holds     atomic.Int32
	// onHold and onRelease receive one value each time a write is held and
	// let go.
	onHold, onRelease chan struct{}
}

func (f *fakeMemory) install(idx *Index) {
	f.onHold, f.onRelease = make(chan struct{}, 8), make(chan struct{}, 8)
	idx.mem.inUse = f.held.Load
	idx.mem.collect = func() { f.collected.Add(1) }
	idx.mem.onHold = func() { f.holds.Add(1); f.onHold <- struct{}{} }
	idx.mem.onRelease = func() { f.onRelease <- struct{}{} }
}

// within waits for ch, failing the test with what after a generous minute:
// the step is an event, the minute only stops a hung test.
func within(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Minute):
		t.Fatal(what)
	}
}

// Over the budget a write waits; when the memory comes back it goes through.
func TestContentWrite_WaitsWhileOverTheMemoryBudget(t *testing.T) {
	idx := openBatchIndex(t)
	mem := &fakeMemory{}
	mem.held.Store(100 << 20)
	mem.install(idx)
	idx.SetMemoryBudget(50 << 20) // limit = 150 MiB
	mem.held.Store(400 << 20)
	// No write is let through on time alone while this test runs.
	prev := roomMaxWait
	roomMaxWait = time.Hour
	t.Cleanup(func() { roomMaxWait = prev })

	done := make(chan error, 1)
	go func() { done <- idx.IndexNodeContent(context.Background(), contentNode(1), "bekleyen metin") }()

	within(t, mem.onHold, "the write was not held over the budget")
	if mem.collected.Load() == 0 {
		t.Error("nothing collected the garbage while writes were held")
	}
	select {
	case err := <-done:
		t.Fatalf("the write went through over the budget (err %v)", err)
	default:
	}

	mem.held.Store(120 << 20)
	within(t, mem.onRelease, "the write was not let go once the memory came back")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write: %v", err)
		}
	case <-time.After(time.Minute):
		t.Fatal("the write was let go but never landed")
	}
	if hits := idx.SafeSearchScoped(context.Background(), "bekleyen", 10, ScopeContent); len(hits) != 1 {
		t.Errorf("%d hits, want 1", len(hits))
	}
}

func TestContentWrite_UnderTheBudgetDoesNotWait(t *testing.T) {
	idx := openBatchIndex(t)
	mem := &fakeMemory{}
	mem.held.Store(100 << 20)
	mem.install(idx)
	idx.SetMemoryBudget(50 << 20)
	mem.held.Store(140 << 20)

	if err := idx.IndexNodeContent(context.Background(), contentNode(2), "serbest metin"); err != nil {
		t.Fatal(err)
	}
	// What the gate did, not how long the write took: the write itself may be
	// slow on a loaded disk, and that is not waiting.
	if n := mem.holds.Load(); n != 0 {
		t.Errorf("a write under the budget was held %d times", n)
	}
	if mem.collected.Load() != 0 {
		t.Error("the garbage was collected for a write that did not wait")
	}
}

// A caller that gives up while held is let go.
func TestContentWrite_HeldWriteLeavesWithItsCaller(t *testing.T) {
	idx := openBatchIndex(t)
	mem := &fakeMemory{}
	mem.install(idx)
	idx.SetMemoryBudget(1 << 20)
	mem.held.Store(1 << 40)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := idx.IndexNodeContent(ctx, contentNode(3), "x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the caller's deadline", err)
	}
}

// Memory something else holds slows indexing to a trickle, never to a halt.
func TestContentWrite_IsLetThroughAfterTheLongestWait(t *testing.T) {
	idx := openBatchIndex(t)
	mem := &fakeMemory{}
	mem.install(idx)
	idx.SetMemoryBudget(1 << 20)
	mem.held.Store(1 << 40)
	prev := roomMaxWait
	roomMaxWait = 400 * time.Millisecond
	t.Cleanup(func() { roomMaxWait = prev })

	if err := idx.IndexNodeContent(context.Background(), contentNode(4), "sonunda"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if hits := idx.SafeSearchScoped(context.Background(), "sonunda", 10, ScopeContent); len(hits) != 1 {
		t.Errorf("%d hits, want 1", len(hits))
	}
}

// No budget set (0): nothing waits, whatever the memory.
func TestContentWrite_NoBudgetNeverWaits(t *testing.T) {
	idx := openBatchIndex(t)
	mem := &fakeMemory{}
	mem.install(idx)
	mem.held.Store(1 << 40)
	if err := idx.IndexNodeContent(context.Background(), contentNode(5), "bütçesiz"); err != nil {
		t.Fatal(err)
	}
	if mem.collected.Load() != 0 {
		t.Error("collected without a budget")
	}
}
