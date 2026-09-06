package handlers_test

// The gap this closes, driven through the real endpoint: a file created in the
// browser's text editor used to reach the storage driver, the catalogue and
// the search index, and never the antivirus scanner. Every uploaded file on
// the same install was scanned.
//
// These tests assert which SINK the handler reaches for, because that is the
// decision this surface owns. What the sinks then do with it — a scan now, or
// one scan per file per editing window — is the queue's contract and is pinned
// in internal/queue.

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
)

// avRecorder captures the nodes a sink was handed.
type avRecorder struct {
	mu    sync.Mutex
	nodes []*model.Node
}

func (r *avRecorder) record(n *model.Node) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes = append(r.nodes, n)
}

func (r *avRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.nodes)
}

func (r *avRecorder) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, n.Path)
	}
	return out
}

// withAVSinks wires both scan sinks into a fixture's Deps. BuildRouter installs
// them into the handlers package.
func withAVSinks(now, afterSave *avRecorder) func(*api.Deps) {
	return func(d *api.Deps) {
		if now != nil {
			d.AVScan = func(_ context.Context, n *model.Node) { now.record(n) }
		}
		if afterSave != nil {
			d.AVScanAfterSave = func(_ context.Context, n *model.Node) { afterSave.record(n) }
		}
	}
}

// A file the editor CREATES is scanned straight away, like an upload. This is
// the red case from the report: on main, neither sink was reached at all.
func TestSaveText_CreateScansImmediately(t *testing.T) {
	now, later := &avRecorder{}, &avRecorder{}
	f := newStagedFixtureWith(t, withAVSinks(now, later))

	require.Equal(t, http.StatusOK, f.saveText(t, "main://fresh.txt", "created in the editor"))

	require.Equal(t, 1, now.count(), "a newly created file must be scanned immediately")
	assert.Equal(t, []string{"/fresh.txt"}, now.paths())
	assert.Equal(t, 0, later.count(), "a create is not a save; nothing to debounce")

	// The node the scanner was handed must be a real, persisted row — the scan
	// job re-fetches by id, so an id-less node would be silently skipped.
	assert.NotZero(t, now.nodes[0].ID)
	assert.Equal(t, model.NodeTypeFile, now.nodes[0].Type)
	assert.EqualValues(t, len("created in the editor"), now.nodes[0].Size)
}

// A save over a file that already exists goes to the debounced sink instead.
// The handler asks once per save; the coalescing is the queue's job.
func TestSaveText_SaveToExistingIsDebounced(t *testing.T) {
	now, later := &avRecorder{}, &avRecorder{}
	f := newStagedFixtureWith(t, withAVSinks(now, later))

	require.Equal(t, http.StatusOK, f.saveText(t, "main://notes.txt", "v1"))
	require.Equal(t, 1, now.count())

	for _, body := range []string{"v2", "v3", "v4"} {
		require.Equal(t, http.StatusOK, f.saveText(t, "main://notes.txt", body))
	}

	assert.Equal(t, 1, now.count(), "only the create went to the immediate sink")
	assert.Equal(t, 3, later.count(), "each save asks the debounced sink")
	assert.Equal(t, []string{"/notes.txt", "/notes.txt", "/notes.txt"}, later.paths())

	// The node carries the size of the save that was just written, so the
	// scanner's eligibility gate sees the current file.
	assert.EqualValues(t, len("v4"), later.nodes[2].Size)
}

// ⚠ A missing debounced sink must never mean "no scan". If only the immediate
// sink is wired, saves fall back to it — noisier, never silent.
func TestSaveText_FallsBackToImmediateWithoutDebouncedSink(t *testing.T) {
	now := &avRecorder{}
	f := newStagedFixtureWith(t, withAVSinks(now, nil))

	require.Equal(t, http.StatusOK, f.saveText(t, "main://notes.txt", "v1"))
	require.Equal(t, http.StatusOK, f.saveText(t, "main://notes.txt", "v2"))

	assert.Equal(t, 2, now.count(), "create and save both scanned, since there is no debounce to use")
}

// Neither sink wired (no ClamAV binary, or no queue) stays a no-op: scanning
// is optional, writes are not.
func TestSaveText_NoSinksIsHarmless(t *testing.T) {
	f := newStagedFixtureWith(t, nil)
	require.Equal(t, http.StatusOK, f.saveText(t, "main://plain.txt", "hello"))
	require.Equal(t, http.StatusOK, f.saveText(t, "main://plain.txt", "hello again"))
}
