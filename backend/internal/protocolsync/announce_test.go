package protocolsync

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// These tests pin the half of "keep the rest of the system in step" that was
// missing until the write-announce audit: telling the people who are looking.
//
// Why it matters enough to test rather than eyeball: an explorer with a live
// WebSocket does not poll — packages/core/src/composables/useRealtime.ts turns
// a change frame into a reload and keeps the 12 s timer only as the fallback
// for a socket that has failed. Measured on a running instance with the
// periodic sync disabled, a WebDAV PUT updated the row and the search index in
// ~12 ms and produced no frame at all, so an open explorer went on showing the
// old listing indefinitely. Every assertion below is one of those frames.

// captureEmitter records frames instead of fanning them out.
type captureEmitter struct {
	mu     sync.Mutex
	frames []capturedFrame
}

type capturedFrame struct {
	StorageID int64
	Dir       string
	Ev        realtime.ChangeEvent
}

func (c *captureEmitter) EmitChange(storageID int64, dir string, ev realtime.ChangeEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frames = append(c.frames, capturedFrame{StorageID: storageID, Dir: dir, Ev: ev})
}

func (c *captureEmitter) actions() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.frames))
	for _, f := range c.frames {
		out = append(out, f.Ev.Action)
	}
	return out
}

func (c *captureEmitter) dirsFor(action string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []string{}
	for _, f := range c.frames {
		if f.Ev.Action == action {
			out = append(out, f.Dir)
		}
	}
	return out
}

// installEmitter wires a capture emitter for one test and restores the
// previous sink afterwards. The sink is process-wide (see the doc comment on
// changeEmitter), so leaking one into the next test would make failures
// depend on test order.
func installEmitter(t *testing.T) *captureEmitter {
	t.Helper()
	prev := changeEmitter
	c := &captureEmitter{}
	SetChangeEmitter(c)
	t.Cleanup(func() { SetChangeEmitter(prev) })
	return c
}

func newSyncer(t *testing.T) (*Syncer, *model.Storage) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	st := &model.Storage{
		Name:       "Local",
		Driver:     "local",
		MountPath:  "/local",
		ConfigJSON: []byte(`{"path":"/tmp/does-not-matter"}`),
		Enabled:    true,
	}
	created, err := store.CreateStorage(context.Background(), st)
	require.NoError(t, err)
	// Index and Thumbs stay nil: this file is about the announcement, and both
	// are already nil-safe.
	return New(store, nil, nil, "test"), created
}

func TestWriteAnnouncesTheFolder(t *testing.T) {
	c := installEmitter(t)
	s, st := newSyncer(t)

	require.True(t, s.Write(context.Background(), st, "docs/report.pdf", 12, "application/pdf"))

	// "create" for the docs/ folder EnsureDirChain had to invent, then
	// "upload" for the file itself.
	assert.Equal(t, []string{"create", "upload"}, c.actions())
	assert.Equal(t, []string{"/docs"}, c.dirsFor("upload"))
	assert.Equal(t, []string{""}, c.dirsFor("create"))
	assert.Equal(t, "report.pdf", c.frames[1].Ev.Name)
	assert.Equal(t, st.ID, c.frames[1].StorageID)
}

func TestWriteAnnouncesEvenWhenTheRowCannotBeWritten(t *testing.T) {
	c := installEmitter(t)
	s, st := newSyncer(t)

	// A storage id nothing was ever created under: CreateNode's foreign key
	// fails, so no row lands. The bytes are already on the driver by the time
	// this runs, so the folder still has to be told.
	ghost := &model.Storage{ID: st.ID + 4242, Name: "Ghost"}
	assert.False(t, s.Write(context.Background(), ghost, "orphan.txt", 3, "text/plain"))

	assert.Contains(t, c.actions(), "upload",
		"a write that failed to record its row still changed the folder on disk")
}

func TestOverwriteAnnouncesOnce(t *testing.T) {
	c := installEmitter(t)
	s, st := newSyncer(t)
	ctx := context.Background()

	require.True(t, s.Write(ctx, st, "note.txt", 5, "text/plain"))
	before := len(c.actions())
	require.True(t, s.Write(ctx, st, "note.txt", 9, "text/plain"))

	assert.Equal(t, before+1, len(c.actions()),
		"the second write takes the existing-row branch and must announce exactly once")
	assert.Equal(t, "upload", c.actions()[before])
}

func TestMkdirAnnouncesEachNewSegmentAndNothingForOnesThatExist(t *testing.T) {
	c := installEmitter(t)
	s, st := newSyncer(t)
	ctx := context.Background()

	s.Mkdir(ctx, st, "a/b/c")
	assert.Equal(t, []string{"create", "create", "create"}, c.actions())
	assert.Equal(t, []string{"", "/a", "/a/b"}, c.dirsFor("create"))

	before := len(c.actions())
	s.Mkdir(ctx, st, "a/b")
	assert.Equal(t, before, len(c.actions()),
		"a folder that already had a row did not change; announcing it would be a lie")
}

func TestMoveAnnouncesBothEnds(t *testing.T) {
	c := installEmitter(t)
	s, st := newSyncer(t)
	ctx := context.Background()

	require.True(t, s.Write(ctx, st, "src/a.txt", 4, "text/plain"))
	require.True(t, s.Write(ctx, st, "dst/keep.txt", 4, "text/plain"))
	c.frames = nil

	s.Move(ctx, st, "src/a.txt", "dst/b.txt")

	require.Equal(t, []string{"/src", "/dst"}, c.dirsFor("move"))
	// The two rooms are told different things on purpose: the source hears the
	// name that left and what it became, the destination hears only the name
	// that arrived. Sending the destination the SOURCE basename names a file
	// that is not in that folder, so a UI keying off `name` to highlight the
	// new row would look for something that was never there.
	assert.Equal(t, "a.txt", c.frames[0].Ev.Name)
	assert.Equal(t, "b.txt", c.frames[0].Ev.NewName)
	assert.Equal(t, "b.txt", c.frames[1].Ev.Name)
	assert.Empty(t, c.frames[1].Ev.NewName)
}

func TestRenameInOneFolderAnnouncesOnceWithBothNames(t *testing.T) {
	c := installEmitter(t)
	s, st := newSyncer(t)
	ctx := context.Background()

	require.True(t, s.Write(ctx, st, "docs/old.txt", 4, "text/plain"))
	c.frames = nil

	s.Move(ctx, st, "docs/old.txt", "docs/new.txt")

	require.Equal(t, []string{"/docs"}, c.dirsFor("move"),
		"source and destination are the same room; two frames would be one wasted reload")
	// Both names are what lets the hub follow a viewer's presence focus across
	// the rename rather than leaving it pointing at a file that is gone.
	assert.Equal(t, "old.txt", c.frames[0].Ev.Name)
	assert.Equal(t, "new.txt", c.frames[0].Ev.NewName)
}

func TestMoveAnnouncesEvenWithNoCachedSourceRow(t *testing.T) {
	c := installEmitter(t)
	s, st := newSyncer(t)

	// Nothing was ever written at src/, so there is no row to re-home — but a
	// protocol client just renamed the object on the storage.
	s.Move(context.Background(), st, "src/ghost.txt", "dst/ghost.txt")

	assert.Equal(t, []string{"/src", "/dst"}, c.dirsFor("move"))
}

// TestWriteRowsRecordsWithoutAnnouncing pins the split Write depends on: a
// caller that fires its own event (the AI surface recording a move whose
// source was never catalogued) needs the row and the index WITHOUT a second
// write hook and a second frame for what is still one operation.
func TestWriteRowsRecordsWithoutAnnouncing(t *testing.T) {
	c := installEmitter(t)
	s, st := newSyncer(t)

	node, _, ok := s.WriteRows(context.Background(), st, "quiet/a.txt", 7, "text/plain")
	require.True(t, ok)
	require.NotNil(t, node)
	assert.Equal(t, "/quiet/a.txt", node.Path)

	// EnsureDirChain still announces the folder it had to invent — that folder
	// really did appear — but the file itself is recorded silently.
	assert.NotContains(t, c.actions(), "upload")
}

func TestTrashAndDeleteAnnounceWithTheName(t *testing.T) {
	c := installEmitter(t)
	s, st := newSyncer(t)
	ctx := context.Background()

	require.True(t, s.Write(ctx, st, "docs/gone.txt", 4, "text/plain"))
	c.frames = nil
	s.Trash(ctx, st, "docs/gone.txt", ".filex-trash/123__gone.txt")
	require.Equal(t, []string{"/docs"}, c.dirsFor("delete"))
	// The hub clears any viewer's presence focus on this name.
	assert.Equal(t, "gone.txt", c.frames[0].Ev.Name)

	require.True(t, s.Write(ctx, st, "docs/hard.txt", 4, "text/plain"))
	c.frames = nil
	s.Delete(ctx, st, "docs/hard.txt")
	assert.Equal(t, []string{"/docs"}, c.dirsFor("delete"))
	assert.Equal(t, "hard.txt", c.frames[0].Ev.Name)
}

func TestDeleteAnnouncesEvenWithNoCachedRow(t *testing.T) {
	c := installEmitter(t)
	s, st := newSyncer(t)

	s.Delete(context.Background(), st, "docs/never-catalogued.txt")

	assert.Equal(t, []string{"/docs"}, c.dirsFor("delete"),
		"the bytes are gone whether or not filex had a row for them")
}

func TestNilEmitterIsSafe(t *testing.T) {
	prev := changeEmitter
	SetChangeEmitter(nil)
	t.Cleanup(func() { SetChangeEmitter(prev) })

	s, st := newSyncer(t)
	ctx := context.Background()
	require.True(t, s.Write(ctx, st, "a.txt", 1, "text/plain"))
	s.Mkdir(ctx, st, "d")
	s.Move(ctx, st, "a.txt", "d/a.txt")
	s.Trash(ctx, st, "d/a.txt", ".filex-trash/1__a.txt")
	s.Delete(ctx, st, "d/a.txt")
}

// TestEnsureDirChainStoresCanonicalPaths guards the spelling of the stored
// path, not the announcement.
//
// pathkey.Hash normalizes internally, so a row written as "dav/davdir" keys
// and dedupes correctly and the bug hides — it surfaces only in the string the
// search API hands back, where a protocol-made folder read "dav/davdir" beside
// an HTTP-made "/dav/httpdir". The sharp edge is MoveRows: it re-homes
// descendants with `dstClean + strings.TrimPrefix(n.Path, srcClean)`, and an
// unslashed n.Path matches no canonical srcClean prefix, so the trim is a
// no-op and the new path comes out doubled.
func TestEnsureDirChainStoresCanonicalPaths(t *testing.T) {
	installEmitter(t)
	s, st := newSyncer(t)
	ctx := context.Background()

	_, err := s.EnsureDirChain(ctx, st, "outer/inner")
	require.NoError(t, err)

	for _, want := range []string{"/outer", "/outer/inner"} {
		n, err := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, want))
		require.NoError(t, err)
		require.NotNil(t, n, "expected a row for %s", want)
		assert.Equal(t, want, n.Path)
		assert.Equal(t, want, n.StorageKey)
	}
}

func TestMoveRowsCarriesTheSubtree(t *testing.T) {
	installEmitter(t)
	s, st := newSyncer(t)
	ctx := context.Background()

	require.True(t, s.Write(ctx, st, "src/folder/deep/leaf.txt", 3, "text/plain"))
	require.True(t, s.MoveRows(ctx, st, "src/folder", "dst/folder"))

	// The descendant is the point: re-homing only the top row leaves every
	// child pointing at a path with nothing behind it, and nothing later
	// notices, because the paths still look plausible.
	moved, err := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, "/dst/folder/deep/leaf.txt"))
	require.NoError(t, err)
	require.NotNil(t, moved, "the leaf did not follow its folder")

	stale, _ := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, "/src/folder/deep/leaf.txt"))
	assert.Nil(t, stale, "the leaf is still listed at its old path too")
}

// hashOf is pathkey.Hash under a local name so the test reads as "the row at
// this path" rather than as a hashing exercise.
func hashOf(storageID int64, p string) string { return pathkey.Hash(storageID, p) }
