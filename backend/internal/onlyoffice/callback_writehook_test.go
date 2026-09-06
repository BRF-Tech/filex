package onlyoffice

// The save-back gate.
//
// Every write surface in filex fans out to the same four side effects — a
// change frame for open explorers, a re-index, the canonical webhook event,
// and an antivirus scan. The OnlyOffice callback wrote the bytes and did none
// of them, which made the office-document editor the one write ClamAV never
// saw. These tests pin all four, and they pin the split between the two kinds
// of save the document server sends: status 2 (the editing session ended — the
// bytes are final) scans immediately like an upload, status 6 (a force-save
// mid-session, which repeats) takes the debounced window the browser's text
// editor takes, for the same reason.
//
// ⚠ Written to FAIL against the pre-fix callback: on that code the file is
// written and the node row is refreshed, and every assertion below it fails.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// ---------------------------------------------------------------- test doubles

// captureSink records the canonical write events. Only Send is implemented;
// anything else panics on the embedded nil, which is the point — the gate must
// not reach for another method.
type captureSink struct {
	notify.Service
	mu     sync.Mutex
	events []notify.Event
	ch     chan notify.Event
}

func newCaptureSink() *captureSink { return &captureSink{ch: make(chan notify.Event, 8)} }

func (c *captureSink) Send(_ context.Context, e notify.Event) (int64, error) {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
	c.ch <- e
	return 1, nil
}

// wait blocks for one event; emission is fire-and-forget on a goroutine.
func (c *captureSink) wait(t *testing.T) notify.Event {
	t.Helper()
	select {
	case e := <-c.ch:
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("no write event emitted within 3s — the callback announced nothing")
		return notify.Event{}
	}
}

// captureFrames records realtime change frames.
type captureFrames struct {
	mu     sync.Mutex
	frames []realtime.ChangeEvent
	dirs   []string
}

func (c *captureFrames) EmitChange(_ int64, dir string, ev realtime.ChangeEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dirs = append(c.dirs, dir)
	c.frames = append(c.frames, ev)
}

func (c *captureFrames) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.frames)
}

// harness is one wired instance: a storage on disk, a catalogue row for the
// document, a search index, and every side-effect sink captured.
type harness struct {
	svc      *Service
	node     *model.Node
	root     string
	sink     *captureSink
	frames   *captureFrames
	scanNow  *[]*model.Node
	scanSave *[]*model.Node
	// reindexed records the nodes whose content re-extraction was re-queued,
	// which is what keeps a document's text findable after an edit.
	reindexed *[]int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "budget.docx"), []byte("OLD BYTES"), 0o644))

	_, store := dbtest.NewTestDB(t)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "Local",
		Driver:     "local",
		MountPath:  "/local",
		ConfigJSON: []byte(fmt.Sprintf(`{"path":%q}`, filepath.ToSlash(root))),
		Enabled:    true,
	})
	require.NoError(t, err)

	node, err := store.CreateNode(context.Background(), &model.Node{
		StorageID:  st.ID,
		Name:       "budget.docx",
		Path:       "/budget.docx",
		PathHash:   pathkey.Hash(st.ID, "/budget.docx"),
		StorageKey: "/budget.docx",
		Type:       model.NodeTypeFile,
		Size:       int64(len("OLD BYTES")),
		Mime:       "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		SyncState:  model.SyncStateSynced,
	})
	require.NoError(t, err)

	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"path": root}))

	idx, err := search.Open(filepath.Join(t.TempDir(), "index.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	var reindexed []int64
	idx.SetContentHook(func(_ context.Context, n *model.Node) { reindexed = append(reindexed, n.ID) })
	// The document is already indexed at its pre-edit fingerprint, so a hook
	// call afterwards can only mean the edit was re-indexed.
	require.NoError(t, idx.IndexNode(context.Background(), node))
	reindexed = nil

	frames := &captureFrames{}
	protocolsync.SetChangeEmitter(frames)
	t.Cleanup(func() { protocolsync.SetChangeEmitter(nil) })

	sink := newCaptureSink()
	var scanNow, scanSave []*model.Node
	writehook.Configure(func(_ context.Context, n *model.Node) { scanNow = append(scanNow, n) }, sink)
	writehook.ConfigureSaveScan(func(_ context.Context, n *model.Node) { scanSave = append(scanSave, n) })
	t.Cleanup(func() {
		writehook.Configure(nil, nil)
		writehook.ConfigureSaveScan(nil)
	})

	svc := New(store, func(int64) (storage.Driver, error) { return drv, nil },
		"https://docs.example", "shh", "https://filex.example", time.Hour)
	svc.AttachSync(protocolsync.New(store, idx, nil, writehook.OriginOnlyOffice))

	return &harness{
		svc: svc, node: node, root: root,
		sink: sink, frames: frames,
		scanNow: &scanNow, scanSave: &scanSave, reindexed: &reindexed,
	}
}

// save drives one document-server callback end to end: a stand-in document
// server serves `body` at the URL the callback points filex at.
func (h *harness) save(t *testing.T, status int, body string) map[string]any {
	t.Helper()
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ds.Close)

	payload := fmt.Sprintf(`{"key":"k","status":%d,"url":%q}`, status, ds.URL+"/saved.docx")
	req := httptest.NewRequest(http.MethodPost, "/api/files/onlyoffice/callback?node=1",
		strings.NewReader(payload))
	resp, err := h.svc.HandleCallback(req, h.node.ID)
	require.NoError(t, err)
	return resp
}

// ---------------------------------------------------------------- the tests

// TestCallback_FinalSave_RunsThePostWriteGate is the whole defect in one test:
// a document saved out of OnlyOffice must reach every side effect an upload of
// the same bytes would.
func TestCallback_FinalSave_RunsThePostWriteGate(t *testing.T) {
	h := newHarness(t)

	resp := h.save(t, StatusReadyForSaving, "NEW BYTES FROM THE EDITOR")
	assert.Equal(t, 0, resp["error"], "the document server must be told the save succeeded")

	// The bytes landed (this part always worked).
	onDisk, err := os.ReadFile(filepath.Join(h.root, "budget.docx"))
	require.NoError(t, err)
	assert.Equal(t, "NEW BYTES FROM THE EDITOR", string(onDisk))

	// 1. Open explorers are told.
	assert.Equal(t, 1, h.frames.len(), "no realtime frame: an open explorer keeps showing the old listing")

	// 2. The document is re-indexed, so content search stops returning the
	//    pre-edit text.
	assert.Len(t, *h.reindexed, 1, "no re-index: the document keeps its pre-edit text in search")

	// 3. The canonical event fires, and it is file.updated — a save-back
	//    REPLACES a document that was already there.
	e := h.sink.wait(t)
	assert.Equal(t, notify.EventFileUpdated, e.Event)
	assert.Equal(t, "/budget.docx", e.Body)
	assert.Equal(t, writehook.OriginOnlyOffice, e.Meta["origin"])

	// 4. And it is scanned. Immediately: status 2 arrives after the last
	//    editor has closed the document, so these bytes are final.
	require.Len(t, *h.scanNow, 1, "the office editor is still the one write ClamAV never sees")
	assert.Equal(t, h.node.ID, (*h.scanNow)[0].ID)
	assert.Empty(t, *h.scanSave, "a final save must not be deferred into a debounce window")
}

// TestCallback_ForceSave_TakesTheDebouncedWindow pins the other half of the
// split. A force-save is an INTERIM save: the session is still open and more
// will follow, so it is scheduled the way a Ctrl+S is rather than scanned on
// the spot.
func TestCallback_ForceSave_TakesTheDebouncedWindow(t *testing.T) {
	h := newHarness(t)

	h.save(t, StatusForceSave, "HALF-FINISHED BYTES")

	assert.Equal(t, 1, h.frames.len(), "an interim save changed the bytes; watchers still have to hear it")
	assert.Len(t, *h.reindexed, 1, "an interim save must still re-index")

	e := h.sink.wait(t)
	assert.Equal(t, notify.EventFileUpdated, e.Event)
	assert.Equal(t, writehook.OriginOnlyOffice, e.Meta["origin"])

	require.Len(t, *h.scanSave, 1, "a force-save repeats during a session — it takes the debounced window")
	assert.Empty(t, *h.scanNow, "scanning every force-save is the cost the save window exists to avoid")
}

// TestCallback_NoOpStatuses_AnnounceNothing guards the other direction: the
// statuses that are not a save must not produce a write event, a frame or a
// scan. Announcing a write that did not happen is its own bug.
func TestCallback_NoOpStatuses_AnnounceNothing(t *testing.T) {
	for _, status := range []int{StatusBeingEdited, StatusSavingError, StatusClosedNoChange} {
		t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
			h := newHarness(t)
			resp := h.save(t, status, "IGNORED")
			assert.Equal(t, 0, resp["error"])

			onDisk, err := os.ReadFile(filepath.Join(h.root, "budget.docx"))
			require.NoError(t, err)
			assert.Equal(t, "OLD BYTES", string(onDisk))

			assert.Zero(t, h.frames.len())
			assert.Empty(t, *h.reindexed)
			assert.Empty(t, *h.scanNow)
			assert.Empty(t, *h.scanSave)
			assert.Empty(t, h.sink.events)
		})
	}
}

// TestCallback_UnwiredSyncStillWrites keeps the gate optional in the same
// nil-safe way every other sink in this codebase is: an instance with no
// syncer (tests, a build with no index) must still save the document.
func TestCallback_UnwiredSyncStillWrites(t *testing.T) {
	h := newHarness(t)
	h.svc.AttachSync(nil)

	resp := h.save(t, StatusReadyForSaving, "STILL SAVED")
	assert.Equal(t, 0, resp["error"])

	onDisk, err := os.ReadFile(filepath.Join(h.root, "budget.docx"))
	require.NoError(t, err)
	assert.Equal(t, "STILL SAVED", string(onDisk))
}
