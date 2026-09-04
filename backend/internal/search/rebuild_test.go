package search

// Tests for the self-repairing index (the v0.29.0 upgrade problem).
//
// v0.29.0 added name_norm/path_norm and shipped WITHOUT a rebuild, so the
// default experience on every existing installation was "nothing changed":
// the reporter of issue #15 upgraded, measured, and correctly said so. The
// contract these tests pin down is that an index written by an older schema
// repairs itself in the background, and that it does so without the search
// outage that made an automatic rebuild unacceptable the first time round.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"

	"github.com/brf-tech/filex/backend/internal/model"
)

// legacyDoc is the v0.28.0 document shape, copied verbatim from
// `git show v0.28.0:backend/internal/search/index.go`. Writing it by hand
// is how these tests get a pre-#15 index without checking out the old
// build: no name_norm, no path_norm, and no schema stamp at all.
type legacyDoc struct {
	StorageID  int64  `json:"storage_id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Mime       string `json:"mime,omitempty"`
	Type       string `json:"type"`
	Content    string `json:"content,omitempty"`
	ContentSig string `json:"content_sig,omitempty"`
}

// writeLegacyIndex builds an index at path exactly the way v0.28.0 would
// have: legacy documents, and no version marker.
func writeLegacyIndex(t *testing.T, path string, nodes []*model.Node, content map[int64]string) {
	t.Helper()
	bx, err := bleve.New(path, bleve.NewIndexMapping())
	if err != nil {
		t.Fatalf("create legacy index: %v", err)
	}
	for _, n := range nodes {
		d := legacyDoc{
			StorageID: n.StorageID,
			Name:      n.Name,
			Path:      n.Path,
			Mime:      n.Mime,
			Type:      string(n.Type),
		}
		if c, ok := content[n.ID]; ok {
			d.Content = c
			d.ContentSig = ContentFingerprint(n)
		}
		if err := bx.Index(strconv.FormatInt(n.ID, 10), d); err != nil {
			t.Fatalf("index legacy doc: %v", err)
		}
	}
	if err := bx.Close(); err != nil {
		t.Fatalf("close legacy index: %v", err)
	}
}

// issueNodes is the corpus from issue #15's reproduction, plus a folder so
// path matching has something to hit.
func issueNodes() []*model.Node {
	return []*model.Node{
		{ID: 1, StorageID: 1, Name: "main.go", Path: "/Code/main.go", Mime: "text/plain", Type: model.NodeTypeFile, Size: 120, Etag: "e-main"},
		{ID: 2, StorageID: 1, Name: "Code", Path: "/Code", Type: model.NodeTypeDirectory},
		{ID: 3, StorageID: 1, Name: "README.md", Path: "/README.md", Mime: "text/markdown", Type: model.NodeTypeFile, Size: 60, Etag: "e-readme"},
	}
}

// waitRebuild blocks until no rebuild is running (or the test fails).
func waitRebuild(t *testing.T, idx *Index) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for idx.Rebuilding() {
		if time.Now().After(deadline) {
			t.Fatal("rebuild did not finish within 30s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func hitIDs(hits []Hit) []int64 {
	out := make([]int64, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.NodeID)
	}
	return out
}

// TestAutoRebuild_RepairsStaleSchemaWithoutOperatorAction is the whole
// complaint in one test: an index built by v0.28.0 answers none of the
// issue #15 queries, and after nothing but a restart it answers all of
// them — including the content search that was already in it.
func TestAutoRebuild_RepairsStaleSchemaWithoutOperatorAction(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "search.bleve")
	nodes := issueNodes()
	writeLegacyIndex(t, dir, nodes, map[int64]string{3: "the quarterly budget lives here"})

	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	if !idx.NeedsRebuild() {
		t.Fatal("a v0.28.0 index must be detected as stale")
	}
	// The measured "before" column: none of these reach main.go on a
	// v0.28.0 index. (`Code main` does return the `Code` FOLDER even
	// there — the legacy match query ORs its terms — which is why the
	// assertion is about the file the reporter was looking for, not
	// about an empty result list.)
	for _, q := range []string{"main go", "Code main", "main code", "mian.go"} {
		hits, err := idx.SearchScoped(ctx, q, 10, ScopeName)
		if err != nil {
			t.Fatalf("precondition %q: %v", q, err)
		}
		for _, h := range hits {
			if h.NodeID == 1 {
				t.Fatalf("precondition: %q already finds main.go on a legacy index; the test measures nothing", q)
			}
		}
	}

	if started := idx.AutoRebuildIfStale(stubLister{nodes: nodes}, true); !started {
		t.Fatal("a stale index must start a background rebuild")
	}
	waitRebuild(t, idx)

	for _, q := range []string{"main go", "main code", "mian.go"} {
		hits, err := idx.SearchScoped(ctx, q, 10, ScopeName)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		found := false
		for _, h := range hits {
			if h.NodeID == 1 {
				found = true
			}
		}
		if !found {
			t.Errorf("after the self-repair %q must find main.go, got %v", q, hitIDs(hits))
		}
	}
	// Content that was already extracted must survive the rebuild — the
	// blackout is the reason auto-rebuild was rejected the first time.
	hits, err := idx.SearchScoped(ctx, "quarterly", 10, ScopeContent)
	if err != nil || len(hits) != 1 || hits[0].NodeID != 3 {
		t.Fatalf("content must survive the rebuild, got %v (err %v)", hitIDs(hits), err)
	}
	if idx.NeedsRebuild() {
		t.Error("a completed rebuild must clear needs_rebuild")
	}
	if idx.Stats().Rebuilding {
		t.Error("stats must not report a rebuild that finished")
	}
	// Nothing left behind on disk.
	assertNoLeftovers(t, dir)
}

func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	for _, p := range []string{pendingPath(dir), retiredPath(dir)} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("leftover directory %s", p)
		}
	}
}

// TestAutoRebuild_KillSwitch: FILEX_SEARCH_AUTO_REBUILD=0 reaches this as
// enabled=false and must be honoured before any work starts.
func TestAutoRebuild_KillSwitch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "search.bleve")
	nodes := issueNodes()
	writeLegacyIndex(t, dir, nodes, nil)

	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	if started := idx.AutoRebuildIfStale(stubLister{nodes: nodes}, false); started {
		t.Fatal("the kill switch must stop the rebuild before it starts")
	}
	if idx.Rebuilding() {
		t.Fatal("no rebuild may be running with the kill switch set")
	}
	if !idx.NeedsRebuild() {
		t.Error("the index is still stale and must keep saying so")
	}
	if _, err := os.Stat(pendingPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("the kill switch must stop the rebuild before it touches the disk")
	}
	// And the degraded behaviour is unchanged: still no separator-blind hit.
	if hits, _ := idx.SearchScoped(context.Background(), "main go", 10, ScopeName); len(hits) != 0 {
		t.Errorf("nothing should have been reindexed, got %v", hitIDs(hits))
	}
}

// TestAutoRebuild_CurrentIndexIsLeftAlone: the check runs on every start,
// so it has to be a no-op on an index this build wrote.
func TestAutoRebuild_CurrentIndexIsLeftAlone(t *testing.T) {
	idx := newTestIndex(t)
	if idx.AutoRebuildIfStale(stubLister{nodes: issueNodes()}, true) {
		t.Fatal("a current index must not be rebuilt")
	}
}

// blockingLister holds the rebuild open until release() is called, so a
// test can measure what search does WHILE a rebuild is in flight.
type blockingLister struct {
	nodes   []*model.Node
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingLister(nodes []*model.Node) *blockingLister {
	return &blockingLister{nodes: nodes, entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingLister) AllNodesForIndex(_ context.Context) ([]*model.Node, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.nodes, nil
}

// TestRebuild_OldIndexAnswersThroughout is the requirement that made the
// side-by-side design necessary: the pre-v0.30 rebuild deleted the index
// directory before reindexing, so every query during a rebuild came back
// empty. Here the live index must keep answering until the swap.
func TestRebuild_OldIndexAnswersThroughout(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "search.bleve")
	nodes := issueNodes()
	writeLegacyIndex(t, dir, nodes, map[int64]string{3: "the quarterly budget lives here"})

	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	lister := newBlockingLister(nodes)
	if !idx.AutoRebuildIfStale(lister, true) {
		t.Fatal("rebuild did not start")
	}
	<-lister.entered

	// A rebuild is in flight. The legacy queries must still work, exactly
	// as they did before it started.
	for i := 0; i < 20; i++ {
		hits, err := idx.SearchScoped(ctx, "main.go", 10, ScopeName)
		if err != nil {
			t.Fatalf("search errored during a rebuild: %v", err)
		}
		if len(hits) == 0 {
			t.Fatalf("the live index stopped answering during a rebuild (iteration %d)", i)
		}
		hits, err = idx.SearchScoped(ctx, "quarterly", 10, ScopeContent)
		if err != nil {
			t.Fatalf("content search errored during a rebuild: %v", err)
		}
		if len(hits) == 0 {
			t.Fatalf("content search went dark during a rebuild (iteration %d)", i)
		}
		time.Sleep(time.Millisecond)
	}
	if !idx.Rebuilding() {
		t.Fatal("the rebuild should still be running; the test measured nothing")
	}
	if !idx.Stats().Rebuilding {
		t.Error("stats must surface an in-progress rebuild so the admin UI can say so")
	}
	if !idx.Stats().NeedsRebuild {
		t.Error("needs_rebuild must stay true until the new index is live")
	}
	close(lister.release)
	waitRebuild(t, idx)
	if hits, _ := idx.SearchScoped(ctx, "main go", 10, ScopeName); len(hits) == 0 {
		t.Error("after the swap the new behaviour must be live")
	}
}

// TestRebuild_SwapIsAtomic hammers search across the whole rebuild,
// including the moment the new index replaces the old one. Every single
// query must return the document — there is no window in which the index
// is empty, closed, or erroring.
func TestRebuild_SwapIsAtomic(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "search.bleve")

	nodes := issueNodes()
	for i := int64(10); i < 1010; i++ {
		nodes = append(nodes, &model.Node{
			ID: i, StorageID: 1, Name: fmt.Sprintf("file-%d.txt", i),
			Path: fmt.Sprintf("/bulk/file-%d.txt", i), Mime: "text/plain",
			Type: model.NodeTypeFile, Size: 10, Etag: fmt.Sprintf("e-%d", i),
		})
	}
	writeLegacyIndex(t, dir, nodes, nil)

	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	var queries, during, misses, failures int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			inFlight := idx.Rebuilding()
			hits, err := idx.SearchScoped(ctx, "main.go", 5, ScopeName)
			atomic.AddInt64(&queries, 1)
			if inFlight {
				atomic.AddInt64(&during, 1)
			}
			if err != nil {
				atomic.AddInt64(&failures, 1)
				continue
			}
			if len(hits) == 0 {
				atomic.AddInt64(&misses, 1)
			}
		}
	}()

	if !idx.AutoRebuildIfStale(stubLister{nodes: nodes}, true) {
		t.Fatal("rebuild did not start")
	}
	waitRebuild(t, idx)
	close(stop)
	wg.Wait()

	// The count is not the contract — every query coming back with the
	// document is. But a run where nothing overlapped the rebuild would
	// pass while measuring nothing, so the overlap is asserted too. (A
	// wildcard query under the race detector costs enough that the
	// numbers here are small: measured 17 queries over an 8.2s rebuild
	// without -race, 7 with it.)
	if during < 3 {
		t.Fatalf("only %d of %d queries overlapped the rebuild; the test measured nothing", during, queries)
	}
	if failures != 0 || misses != 0 {
		t.Fatalf("search was interrupted by the swap: %d queries (%d during the rebuild), %d errors, %d empty results",
			queries, during, failures, misses)
	}
	t.Logf("swap survived %d concurrent queries (%d of them during the rebuild) with 0 errors and 0 empty results", queries, during)
}

// TestRebuild_InterruptedIsDiscardedAndRetried: a container killed
// mid-rebuild leaves a half-built index behind. It must never be swapped
// in, it must not survive the next start, and the index must still ask to
// be rebuilt.
func TestRebuild_InterruptedIsDiscardedAndRetried(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "search.bleve")
	nodes := issueNodes()
	writeLegacyIndex(t, dir, nodes, nil)

	// A half-built index, as a crash would leave it: a real Bleve
	// directory holding SOME of the corpus.
	writeLegacyIndex(t, pendingPath(dir), nodes[:1], nil)

	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	if _, err := os.Stat(pendingPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a half-built index must be discarded on start, not left behind forever")
	}
	if !idx.NeedsRebuild() {
		t.Error("after discarding a partial rebuild the index must ask to be rebuilt again")
	}
	if got := idx.Stats().DocCount; got != uint64(len(nodes)) {
		t.Errorf("the live index must be untouched: doc count %d, want %d", got, len(nodes))
	}
}

// TestRebuild_CrashDuringSwapRestoresTheOldIndex covers the other crash
// window: the swap renames the live index aside before moving the new one
// into place. A container killed between those two renames leaves no
// index at all under the configured path.
func TestRebuild_CrashDuringSwapRestoresTheOldIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "search.bleve")
	nodes := issueNodes()
	writeLegacyIndex(t, retiredPath(dir), nodes, nil) // live index, already moved aside
	writeLegacyIndex(t, pendingPath(dir), nil, nil)   // unverifiable replacement

	idx, err := Open(dir)
	if err != nil {
		t.Fatalf("open after a crashed swap must recover, got: %v", err)
	}
	defer func() { _ = idx.Close() }()

	if got := idx.Stats().DocCount; got != uint64(len(nodes)) {
		t.Errorf("the old index must be restored: doc count %d, want %d", got, len(nodes))
	}
	assertNoLeftovers(t, dir)
	if !idx.NeedsRebuild() {
		t.Error("the restored index is still the old schema and must say so")
	}
}

// slowLister blocks on every call so two rebuilds can be caught racing.
type slowLister struct {
	nodes []*model.Node
	gate  chan struct{}
}

func (s *slowLister) AllNodesForIndex(_ context.Context) ([]*model.Node, error) {
	<-s.gate
	return s.nodes, nil
}

// TestRebuild_NoOverlap: the manual endpoint and the automatic repair
// share one guard, in both orders, and neither leaves it stuck.
func TestRebuild_NoOverlap(t *testing.T) {
	ctx := context.Background()
	nodes := issueNodes()

	t.Run("manual blocks automatic", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "search.bleve")
		writeLegacyIndex(t, dir, nodes, nil)
		idx, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = idx.Close() }()

		gate := make(chan struct{})
		lister := &slowLister{nodes: nodes, gate: gate}
		if err := idx.StartRebuild(lister, RebuildOptions{Reason: "admin"}); err != nil {
			t.Fatal(err)
		}
		for !idx.Rebuilding() {
			time.Sleep(time.Millisecond)
		}
		if started := idx.AutoRebuildIfStale(stubLister{nodes: nodes}, true); started {
			t.Error("the automatic rebuild must not run alongside a manual one")
		}
		if err := idx.StartRebuild(lister, RebuildOptions{}); !errors.Is(err, ErrRebuildInProgress) {
			t.Errorf("a second manual rebuild must be refused, got %v", err)
		}
		close(gate)
		waitRebuild(t, idx)
		if idx.Rebuilding() {
			t.Error("the guard is stuck after a finished rebuild")
		}
	})

	t.Run("automatic blocks manual", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "search.bleve")
		writeLegacyIndex(t, dir, nodes, nil)
		idx, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = idx.Close() }()

		lister := newBlockingLister(nodes)
		if !idx.AutoRebuildIfStale(lister, true) {
			t.Fatal("rebuild did not start")
		}
		<-lister.entered
		if err := idx.StartRebuild(stubLister{nodes: nodes}, RebuildOptions{Reason: "admin"}); !errors.Is(err, ErrRebuildInProgress) {
			t.Errorf("the manual endpoint must be refused while the automatic rebuild runs, got %v", err)
		}
		if err := idx.Rebuild(ctx, stubLister{nodes: nodes}, RebuildOptions{}); !errors.Is(err, ErrRebuildInProgress) {
			t.Errorf("the synchronous entry point must be refused too, got %v", err)
		}
		close(lister.release)
		waitRebuild(t, idx)
	})
}

// failingLister stands in for a database that goes away mid-rebuild.
type failingLister struct{}

func (failingLister) AllNodesForIndex(_ context.Context) ([]*model.Node, error) {
	return nil, errors.New("database is gone")
}

// TestRebuild_FailureKeepsTheOldIndex: a rebuild that cannot finish must
// leave the live index serving, must clear its own flag, must not leave
// the half-built directory behind, and must keep telling the operator a
// rebuild is still needed.
func TestRebuild_FailureKeepsTheOldIndex(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "search.bleve")
	nodes := issueNodes()
	writeLegacyIndex(t, dir, nodes, map[int64]string{3: "the quarterly budget lives here"})

	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	if err := idx.Rebuild(ctx, failingLister{}, RebuildOptions{}); err == nil {
		t.Fatal("a rebuild whose node listing fails must return an error")
	}
	if idx.Rebuilding() {
		t.Error("the rebuild flag is stuck after a failure")
	}
	if !idx.NeedsRebuild() {
		t.Error("a failed rebuild must keep needs_rebuild true")
	}
	assertNoLeftovers(t, dir)
	if hits, err := idx.SearchScoped(ctx, "main.go", 10, ScopeName); err != nil || len(hits) == 0 {
		t.Fatalf("the live index must still answer after a failed rebuild: %v (err %v)", hitIDs(hits), err)
	}
	if hits, err := idx.SearchScoped(ctx, "quarterly", 10, ScopeContent); err != nil || len(hits) != 1 {
		t.Fatalf("content must be intact after a failed rebuild: %v (err %v)", hitIDs(hits), err)
	}
}

// TestRebuild_WritesDuringARebuildSurviveTheSwap: a rebuild reads a
// snapshot of the node table. Anything written while it runs would be
// invisible after the swap unless the write also lands in the index being
// built — and anything deleted would come back from the dead.
func TestRebuild_WritesDuringARebuildSurviveTheSwap(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "search.bleve")
	nodes := issueNodes()
	writeLegacyIndex(t, dir, nodes, nil)

	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	lister := newBlockingLister(nodes)
	if !idx.AutoRebuildIfStale(lister, true) {
		t.Fatal("rebuild did not start")
	}
	<-lister.entered

	// Uploaded while the rebuild runs — after the snapshot the rebuild
	// is working from.
	fresh := &model.Node{ID: 42, StorageID: 1, Name: "uploaded_during.txt", Path: "/uploaded_during.txt",
		Mime: "text/plain", Type: model.NodeTypeFile, Size: 5, Etag: "e-42"}
	if err := idx.IndexNode(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexNodeContent(ctx, fresh, "written while the rebuild was running"); err != nil {
		t.Fatal(err)
	}
	// Deleted while the rebuild runs — it is still in the snapshot.
	if err := idx.DeleteNode(ctx, 3); err != nil {
		t.Fatal(err)
	}

	close(lister.release)
	waitRebuild(t, idx)

	if hits, err := idx.SearchScoped(ctx, "uploaded during", 10, ScopeName); err != nil || len(hits) != 1 {
		t.Errorf("a file indexed during the rebuild vanished at the swap: %v (err %v)", hitIDs(hits), err)
	}
	if hits, err := idx.SearchScoped(ctx, "running", 10, ScopeContent); err != nil || len(hits) != 1 {
		t.Errorf("content indexed during the rebuild vanished at the swap: %v (err %v)", hitIDs(hits), err)
	}
	if hits, err := idx.SearchScoped(ctx, "README.md", 10, ScopeName); err != nil || len(hits) != 0 {
		t.Errorf("a file deleted during the rebuild came back at the swap: %v (err %v)", hitIDs(hits), err)
	}
}

// TestRebuild_RefusesWhenTheDiskCannotHoldTwoIndexes: two indexes exist at
// once during a rebuild. When the disk cannot take that, the rebuild must
// fail loudly and keep serving the old index rather than filling the disk.
func TestRebuild_RefusesWhenTheDiskCannotHoldTwoIndexes(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "search.bleve")
	nodes := issueNodes()
	writeLegacyIndex(t, dir, nodes, nil)

	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	idx.FreeBytes = func(string) (uint64, error) { return 1024, nil }

	err = idx.Rebuild(ctx, stubLister{nodes: nodes}, RebuildOptions{})
	if !errors.Is(err, ErrNoDiskSpace) {
		t.Fatalf("want ErrNoDiskSpace, got %v", err)
	}
	assertNoLeftovers(t, dir)
	if hits, err := idx.SearchScoped(ctx, "main.go", 10, ScopeName); err != nil || len(hits) == 0 {
		t.Fatalf("the old index must keep serving: %v (err %v)", hitIDs(hits), err)
	}
	if !idx.NeedsRebuild() {
		t.Error("a refused rebuild must keep needs_rebuild true")
	}

	// A probe that cannot answer must not block the rebuild — see
	// internal/diskfree.
	idx.FreeBytes = func(string) (uint64, error) { return 0, errors.New("unsupported") }
	if err := idx.Rebuild(ctx, stubLister{nodes: nodes}, RebuildOptions{}); err != nil {
		t.Fatalf("an unmeasurable disk must not block the rebuild: %v", err)
	}
}

// TestRebuild_ReExtractOnlyOnTheManualPath records the difference between
// the two callers: the manual `?content=1` rebuild re-enqueues extraction
// for every eligible file (an operator asking for it usually just added an
// extractor), while the automatic schema repair carries the text it
// already has and only re-enqueues what actually drifted. Both end up with
// a searchable content field; only one costs a full re-extraction.
func TestRebuild_ReExtractOnlyOnTheManualPath(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "search.bleve")
	nodes := issueNodes()
	writeLegacyIndex(t, dir, nodes, map[int64]string{1: "package main", 3: "the quarterly budget"})

	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	var fired []int64
	idx.SetContentHook(func(_ context.Context, n *model.Node) { fired = append(fired, n.ID) })

	if !idx.AutoRebuildIfStale(stubLister{nodes: nodes}, true) {
		t.Fatal("rebuild did not start")
	}
	waitRebuild(t, idx)
	if len(fired) != 0 {
		t.Errorf("the automatic repair must not re-extract text it already has, fired=%v", fired)
	}
	if hits, _ := idx.SearchScoped(ctx, "quarterly", 10, ScopeContent); len(hits) != 1 {
		t.Fatal("content did not survive the automatic repair")
	}

	fired = nil
	if err := idx.Rebuild(ctx, stubLister{nodes: nodes}, RebuildOptions{ReExtract: true, Reason: "admin"}); err != nil {
		t.Fatal(err)
	}
	if len(fired) != 2 {
		t.Errorf("the manual content rebuild must re-enqueue both files, fired=%v", fired)
	}
	if hits, _ := idx.SearchScoped(ctx, "quarterly", 10, ScopeContent); len(hits) != 1 {
		t.Error("even a re-extracting rebuild must not blank content while extraction is queued")
	}
}
