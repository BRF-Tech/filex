package watchin

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/watchproto"
)

// fakeRescanner records what the inbox hands the catalogue, and can say "a
// scan holds the storage" (later) or "the storage is gone" a number of times.
type fakeRescanner struct {
	mu      sync.Mutex
	changes [][]string
	resyncs [][]string
	busy    int // passes that answer every folder as "later"
	err     error
}

func (f *fakeRescanner) ExternalChanges(_ context.Context, _ int64, dirs []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.busy > 0 {
		f.busy--
		return dirs, nil
	}
	f.changes = append(f.changes, dirs)
	return nil, nil
}

func (f *fakeRescanner) ExternalResync(_ context.Context, _ int64, dirs []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.resyncs = append(f.resyncs, dirs)
	return nil, nil
}

func (f *fakeRescanner) seen() (changes, resyncs [][]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.changes...), append([][]string(nil), f.resyncs...)
}

func fastInbox(r Rescanner) *Inbox {
	in := New(r)
	in.Quiet = 30 * time.Millisecond
	in.MaxWait = 300 * time.Millisecond
	in.RetryAfter = 40 * time.Millisecond
	return in
}

// A burst of reports for one storage becomes ONE pass once it goes quiet,
// each folder once.
func TestInbox_ABurstBecomesOnePass(t *testing.T) {
	f := &fakeRescanner{}
	in := fastInbox(f)
	in.Add(7, []string{"/a", "/b"}, nil)
	in.Add(7, []string{"/b", "/c"}, nil)
	require.Eventually(t, func() bool { c, _ := f.seen(); return len(c) == 1 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	changes, resyncs := f.seen()
	require.Len(t, changes, 1, "one pass, not one per report")
	assert.Equal(t, []string{"/a", "/b", "/c"}, changes[0])
	assert.Empty(t, resyncs)
	assert.Zero(t, in.Pending(7))
}

// A burst that never goes quiet is still handed on after MaxWait.
func TestInbox_ABurstThatNeverStopsIsHandedOnAtMaxWait(t *testing.T) {
	f := &fakeRescanner{}
	in := fastInbox(f)
	stop := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(stop) {
		in.Add(7, []string{"/hot"}, nil)
		time.Sleep(10 * time.Millisecond)
		if c, _ := f.seen(); len(c) > 0 {
			break
		}
	}
	c, _ := f.seen()
	assert.NotEmpty(t, c, "the pass came while reports were still arriving")
}

// More folders than one storage may hold waiting become one resync of the
// whole storage.
func TestInbox_OverflowBecomesAWholeResync(t *testing.T) {
	f := &fakeRescanner{}
	in := fastInbox(f)
	in.MaxPending = 3
	in.Add(7, []string{"/a", "/b", "/c", "/d"}, []string{"/sub"})
	require.Eventually(t, func() bool { _, r := f.seen(); return len(r) == 1 }, 2*time.Second, 5*time.Millisecond)
	changes, resyncs := f.seen()
	assert.Equal(t, []string{"/"}, resyncs[0])
	assert.Empty(t, changes, "the single folders were dropped: the resync covers them")
}

// A resync of the whole storage makes the other resyncs moot.
func TestInbox_AWholeResyncSwallowsSubtreeResyncs(t *testing.T) {
	f := &fakeRescanner{}
	in := fastInbox(f)
	in.Add(7, nil, []string{"/big", "/"})
	require.Eventually(t, func() bool { _, r := f.seen(); return len(r) == 1 }, 2*time.Second, 5*time.Millisecond)
	_, resyncs := f.seen()
	assert.Equal(t, []string{"/"}, resyncs[0])
}

// While a scan holds the storage the folders come back, and the inbox asks
// again after RetryAfter.
func TestInbox_FoldersAScanHeldBackAreAskedAgain(t *testing.T) {
	f := &fakeRescanner{busy: 2}
	in := fastInbox(f)
	in.Add(7, []string{"/a"}, nil)
	require.Eventually(t, func() bool { c, _ := f.seen(); return len(c) == 1 }, 3*time.Second, 5*time.Millisecond)
	changes, _ := f.seen()
	assert.Equal(t, []string{"/a"}, changes[0])
}

// A storage nothing will look at (gone, switched off) is dropped, not retried
// for ever.
func TestInbox_AGoneStorageIsDropped(t *testing.T) {
	gone := errors.New("no syncer")
	f := &fakeRescanner{err: gone}
	in := fastInbox(f)
	in.Gone = func(err error) bool { return errors.Is(err, gone) }
	in.Add(9, []string{"/a"}, []string{"/"})
	require.Eventually(t, func() bool { return in.Pending(9) == 0 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, in.Pending(9))
}

// Two storages drain on their own: one does not wait for the other.
func TestInbox_StoragesDrainApart(t *testing.T) {
	block := make(chan struct{})
	slow := &blockingRescanner{block: block}
	in := fastInbox(slow)
	in.Add(1, []string{"/slow"}, nil)
	require.Eventually(t, func() bool { return slow.started() }, 2*time.Second, 5*time.Millisecond)
	in.Add(2, []string{"/fast"}, nil)
	require.Eventually(t, func() bool { return slow.got(2) }, 2*time.Second, 5*time.Millisecond)
	close(block)
}

type blockingRescanner struct {
	block chan struct{}
	mu    sync.Mutex
	seen  map[int64]bool
}

func (b *blockingRescanner) ExternalChanges(_ context.Context, id int64, _ []string) ([]string, error) {
	b.mu.Lock()
	if b.seen == nil {
		b.seen = map[int64]bool{}
	}
	b.seen[id] = true
	b.mu.Unlock()
	if id == 1 {
		<-b.block
	}
	return nil, nil
}

func (b *blockingRescanner) ExternalResync(context.Context, int64, []string) ([]string, error) {
	return nil, nil
}

func (b *blockingRescanner) started() bool { return b.got(1) }

func (b *blockingRescanner) got(id int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seen[id]
}

func TestAgents_ARetryOfTheSameBatchIsNotQueuedTwice(t *testing.T) {
	var a Agents
	rep := watchproto.Report{Protocol: 1, Seq: 4, Agent: watchproto.Agent{ID: "x", Version: "0.1.0", Host: "nas01"}, Folders: []string{"/a"}, Resync: true}

	assert.False(t, a.Note(12, rep))
	assert.True(t, a.Note(12, rep), "the same batch again: its folders are already queued")

	rep.Seq, rep.Resync = 5, false
	assert.False(t, a.Note(12, rep))

	// Another agent (a restart) whose numbers happen to meet is not a duplicate.
	assert.False(t, a.Note(12, watchproto.Report{Protocol: 1, Seq: 5, Agent: watchproto.Agent{ID: "y"}}))

	st, ok := a.Get(12)
	require.True(t, ok)
	assert.Equal(t, "y", st.Agent.ID)
	assert.EqualValues(t, 4, st.Reports)
	assert.EqualValues(t, 2, st.Folders, "a duplicate's folders are not counted again")
	assert.EqualValues(t, 1, st.Resyncs)
	require.NotNil(t, st.LastResyncAt)

	a.Forget(12)
	_, ok = a.Get(12)
	assert.False(t, ok)
}
