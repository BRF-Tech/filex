package writehook

// Unit tests for the post-write side-effect gate: every hook must fan
// out to the configured AV enqueue + notify sink with the documented
// event shape, and stay a safe no-op when unconfigured.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// fakeSink captures Send calls. Only Send is implemented — the embedded
// nil interface panics on anything else, which is exactly what we want
// (the writehook must never touch other Service methods).
type fakeSink struct {
	notify.Service
	mu     sync.Mutex
	events []notify.Event
	ch     chan notify.Event
}

func newFakeSink() *fakeSink { return &fakeSink{ch: make(chan notify.Event, 8)} }

func (f *fakeSink) Send(_ context.Context, e notify.Event) (int64, error) {
	f.mu.Lock()
	f.events = append(f.events, e)
	f.mu.Unlock()
	f.ch <- e
	return 1, nil
}

// waitEvent blocks until the sink observed one event (emission is async).
func (f *fakeSink) waitEvent(t *testing.T) notify.Event {
	t.Helper()
	select {
	case e := <-f.ch:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no event emitted within 2s")
		return notify.Event{}
	}
}

// install wires fakes and restores the unconfigured state afterwards.
func install(t *testing.T) (*fakeSink, *[]*model.Node) {
	t.Helper()
	sink := newFakeSink()
	var scanned []*model.Node
	Configure(func(_ context.Context, n *model.Node) { scanned = append(scanned, n) }, sink)
	t.Cleanup(func() { Configure(nil, nil) })
	return sink, &scanned
}

func TestOnFileWritten_EmitsEventAndEnqueuesAV(t *testing.T) {
	sink, scanned := install(t)
	node := &model.Node{ID: 7, StorageID: 3, Name: "a.txt", Path: "/dir/a.txt", Size: 12, Type: model.NodeTypeFile}

	OnFileWritten(context.Background(), 3, node, OriginManager, map[string]any{"chunked": true})

	// AV enqueue is synchronous.
	require.Len(t, *scanned, 1)
	assert.Equal(t, int64(7), (*scanned)[0].ID)

	e := sink.waitEvent(t)
	assert.Equal(t, notify.EventFileUploaded, e.Event)
	assert.Equal(t, "/dir/a.txt", e.Body)
	require.NotNil(t, e.Node)
	assert.Equal(t, int64(3), e.Node.StorageID)
	assert.Equal(t, "a.txt", e.Node.Name)
	assert.Equal(t, int64(12), e.Node.Size)
	assert.Equal(t, OriginManager, e.Meta["origin"])
	assert.Equal(t, true, e.Meta["chunked"])
	assert.Equal(t, notify.SeverityInfo, e.Severity)
}

func TestOnFileWritten_TransientNodeSkipsAV(t *testing.T) {
	sink, scanned := install(t)
	// ID == 0 → the scan job could never re-fetch it; event still fires.
	node := &model.Node{StorageID: 1, Name: "b.txt", Path: "/b.txt", Size: 5, Type: model.NodeTypeFile}

	OnFileWritten(context.Background(), 1, node, OriginAI)

	assert.Empty(t, *scanned, "transient (unsaved) node must not be AV-enqueued")
	e := sink.waitEvent(t)
	assert.Equal(t, notify.EventFileUploaded, e.Event)
	assert.Equal(t, OriginAI, e.Meta["origin"])
}

func TestOnFileWritten_NilAndDirectoryNoop(t *testing.T) {
	sink, scanned := install(t)

	OnFileWritten(context.Background(), 1, nil, OriginManager)
	OnFileWritten(context.Background(), 1, &model.Node{ID: 2, Type: model.NodeTypeDirectory, Path: "/d"}, OriginManager)

	assert.Empty(t, *scanned)
	select {
	case e := <-sink.ch:
		t.Fatalf("unexpected event emitted: %v", e.Event)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestOnFileDeleted_EmitsEvent(t *testing.T) {
	sink, scanned := install(t)

	OnFileDeleted(context.Background(), 4, "/x/y.pdf", "y.pdf", OriginOps, map[string]any{"purged": true})

	assert.Empty(t, *scanned, "delete must not enqueue a scan")
	e := sink.waitEvent(t)
	assert.Equal(t, notify.EventFileDeleted, e.Event)
	assert.Equal(t, "/x/y.pdf", e.Body)
	require.NotNil(t, e.Node)
	assert.Equal(t, int64(4), e.Node.StorageID)
	assert.Equal(t, "y.pdf", e.Node.Name)
	assert.Equal(t, OriginOps, e.Meta["origin"])
	assert.Equal(t, true, e.Meta["purged"])
}

func TestOnFileMoved_EmitsEventWithFromTo(t *testing.T) {
	sink, _ := install(t)

	OnFileMoved(context.Background(), 2, "/old/a.txt", "/new/a.txt", "a.txt", OriginDAV, map[string]any{"rename": true})

	e := sink.waitEvent(t)
	assert.Equal(t, notify.EventFileMoved, e.Event)
	assert.Equal(t, "/new/a.txt", e.Body)
	require.NotNil(t, e.Node)
	assert.Equal(t, "/new/a.txt", e.Node.Path)
	assert.Equal(t, "/old/a.txt", e.Meta["from"])
	assert.Equal(t, "/new/a.txt", e.Meta["to"])
	assert.Equal(t, true, e.Meta["rename"])
	assert.Equal(t, OriginDAV, e.Meta["origin"])
}

func TestOnFileTrashed_EmitsEventWithTrashPath(t *testing.T) {
	sink, _ := install(t)

	OnFileTrashed(context.Background(), 9, "/doc.txt", "doc.txt", "/.filex-trash/1__doc.txt", OriginShareX)

	e := sink.waitEvent(t)
	assert.Equal(t, notify.EventFileTrashed, e.Event)
	assert.Equal(t, "/doc.txt", e.Body)
	assert.Equal(t, "/.filex-trash/1__doc.txt", e.Meta["trash_path"])
	assert.Equal(t, OriginShareX, e.Meta["origin"])
}

func TestUnconfigured_Noop(t *testing.T) {
	Configure(nil, nil)
	// Must not panic anywhere.
	OnFileWritten(context.Background(), 1, &model.Node{ID: 1, Path: "/a", Type: model.NodeTypeFile}, OriginManager)
	OnFileDeleted(context.Background(), 1, "/a", "a", OriginManager)
	OnFileMoved(context.Background(), 1, "/a", "/b", "b", OriginManager)
	OnFileTrashed(context.Background(), 1, "/a", "a", "/.filex-trash/a", OriginManager)
}
