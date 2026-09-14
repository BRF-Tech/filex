package ops_test

// A same-storage copy of a FOLDER is one driver call that writes the whole
// subtree, and the DB mirror recorded only the top row. Listings are served
// from the node cache once a storage has synced, so the copied folder OPENED
// EMPTY, and stayed that way until the next sync pass. And a source the cache
// had never seen (a storage mid-first-walk) was not mirrored at all.
//
// Found by @alfatm in his fork (dbf94c23, a4241577); the tests are his,
// ported to this tree's fixture.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// manager wires a fresh manager over store as the worker's DBSync and returns
// it, so a test can attach what it needs (a search index, a rigged store).
func (f *opsFixture) manager(store db.Store) *handlers.Manager {
	mgr := handlers.NewManager(store, func(int64) (storage.Driver, error) { return f.drv, nil })
	f.svc.SetSync(mgr)
	return mgr
}

func (f *opsFixture) nodeAt(t *testing.T, rel string) *model.Node {
	t.Helper()
	n, _ := f.store.GetNodeByPath(context.Background(), f.st.ID, opsPathHash(f.st.ID, rel))
	return n
}

func TestOpsWorker_CopyDir_MirrorsTheWholeSubtree(t *testing.T) {
	ctx := context.Background()
	f := newOpsFixture(t)
	f.seedDir(t, "dest")
	f.seedDir(t, "proje")
	f.seedDir(t, "proje/src")
	f.seedFile(t, "proje/README.md", "# proje")
	f.seedFile(t, "proje/src/main.go", "package main")

	op := f.runOp(t, ops.OpCopy, []string{"proje"}, "dest/")
	require.Equal(t, ops.StatusOK, op.Status, "copy op error: %s", op.Error)

	for _, want := range []struct {
		rel  string
		typ  model.NodeType
		size int64
	}{
		{"dest/proje", model.NodeTypeDirectory, 0},
		{"dest/proje/README.md", model.NodeTypeFile, int64(len("# proje"))},
		{"dest/proje/src", model.NodeTypeDirectory, 0},
		{"dest/proje/src/main.go", model.NodeTypeFile, int64(len("package main"))},
	} {
		n := f.nodeAt(t, want.rel)
		require.NotNilf(t, n, "DB node must exist for the copied %s", want.rel)
		assert.Equalf(t, want.typ, n.Type, "node type for %s", want.rel)
		// A folder row's size is a cached recursive total, never the
		// directory entry's own few kilobytes.
		assert.Equalf(t, want.size, n.Size, "node size for %s", want.rel)
		if want.typ == model.NodeTypeFile {
			assert.Equalf(t, n.Path, n.StorageKey, "storage_key for %s", want.rel)
		}
	}

	// The listing the explorer actually reads for the copied folder.
	top := f.nodeAt(t, "dest/proje")
	require.NotNil(t, top)
	kids, err := f.store.ListNodesByParent(ctx, f.st.ID, &top.ID)
	require.NoError(t, err)
	assert.Len(t, kids, 2, "opening the copied folder must list its contents, not nothing")
}

// The ops worker is restart-safe: the mirror step can run a second time on the
// same source and destination and must not duplicate a single row.
func TestOpsWorker_CopyDir_MirrorReplaysCleanly(t *testing.T) {
	ctx := context.Background()
	f := newOpsFixture(t)
	mgr := f.manager(f.store)
	f.seedDir(t, "dest")
	f.seedDir(t, "proje")
	f.seedFile(t, "proje/README.md", "# proje")

	op := f.runOp(t, ops.OpCopy, []string{"proje"}, "dest/")
	require.Equal(t, ops.StatusOK, op.Status, "copy op error: %s", op.Error)

	mgr.SyncCopy(ctx, f.st.ID, "proje", "dest/proje")

	top := f.nodeAt(t, "dest/proje")
	require.NotNil(t, top)
	kids, err := f.store.ListNodesByParent(ctx, f.st.ID, &top.ID)
	require.NoError(t, err)
	assert.Len(t, kids, 1, "a replayed sync must not duplicate the subtree rows")
}

// A source the node cache has never heard of must still be mirrored: a storage
// that has not finished its first sync walk serves listings from the driver,
// so a user can copy a folder that has no row.
func TestOpsWorker_Copy_SourceNotInTheCache_StillMirrorsTheCopy(t *testing.T) {
	ctx := context.Background()
	f := newOpsFixture(t)
	f.seedDir(t, "dest")
	require.NoError(t, f.drv.Mkdir(ctx, "yeni"))
	require.NoError(t, f.drv.Write(ctx, "yeni/a.txt", strings.NewReader("abc"), 3))
	require.NoError(t, f.drv.Write(ctx, "tek.txt", strings.NewReader("tekil"), 5))

	op := f.runOp(t, ops.OpCopy, []string{"yeni", "tek.txt"}, "dest/")
	require.Equal(t, ops.StatusOK, op.Status, "copy op error: %s", op.Error)

	dir := f.nodeAt(t, "dest/yeni")
	require.NotNil(t, dir, "a copied folder with no source row must still be listed")
	assert.Equal(t, model.NodeTypeDirectory, dir.Type)
	child := f.nodeAt(t, "dest/yeni/a.txt")
	require.NotNil(t, child, "and so must what is inside it")
	assert.EqualValues(t, 3, child.Size)
	file := f.nodeAt(t, "dest/tek.txt")
	require.NotNil(t, file, "a copied file with no source row must still be listed")
	assert.Equal(t, model.NodeTypeFile, file.Type)
	assert.EqualValues(t, 5, file.Size, "the size comes from what landed, not a guess")
}

// wiring:e2 — the marker must be catalogued BEFORE its siblings, not merely
// somewhere in the pass. The index's content hook runs the extraction job
// INLINE, at the moment each row is indexed, so it asks UnderEncrypted against
// the rows that exist AT THAT MOMENT; and the plaintext sibling sorts before
// `.filex-e2e.json`, which is the order os.ReadDir hands them over in.
func TestOpsWorker_CopyDir_MirrorsTheMarkerBeforeAnySibling(t *testing.T) {
	ctx := context.Background()
	f := newOpsFixture(t)
	f.seedDir(t, "dest")
	f.seedDir(t, "kasa")
	f.seedFile(t, "kasa/"+e2e.MarkerName, `{"v":1}`)
	f.seedFile(t, "kasa/-not.txt", "çokgizlisözcük")

	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	ci := queue.NewContentIndexer(f.store, func(int64) (storage.Driver, error) { return f.drv, nil }, idx, 0)
	idx.SetContentHook(func(ctx context.Context, n *model.Node) {
		_ = ci.Handle(ctx, queue.Op{Type: queue.TypeContentIndex, Payload: map[string]any{"node_id": n.ID}})
	})
	f.manager(f.store).AttachSearchIndex(idx)

	op := f.runOp(t, ops.OpCopy, []string{"kasa"}, "dest/")
	require.Equal(t, ops.StatusOK, op.Status, "copy op error: %s", op.Error)

	marker := f.nodeAt(t, "dest/kasa/"+e2e.MarkerName)
	require.NotNil(t, marker, "the copy needs its OWN marker row — the folder is encrypted only if one exists")
	copied := f.nodeAt(t, "dest/kasa/-not.txt")
	require.NotNil(t, copied, "the sibling must still be mirrored — the point is the ORDER")
	require.True(t, ci.Eligible(copied), "the guard only means something while the file is otherwise extractable")
	assert.True(t, e2e.UnderEncrypted(ctx, f.store, f.st.ID, "dest/kasa/-not.txt"))

	assert.Less(t, marker.ID, copied.ID, "the marker row must be written before any sibling's")
	assert.Empty(t, idx.SafeSearchScoped(ctx, "çokgizlisözcük", 10, search.ScopeContent),
		"content from inside an encrypted folder reached the index — and that job never retries")
}

// failCreateStore is the real store with CreateNode rigged to fail for the
// rows `fail` picks out.
type failCreateStore struct {
	db.Store
	fail func(*model.Node) bool
}

func (s *failCreateStore) CreateNode(ctx context.Context, n *model.Node) (*model.Node, error) {
	if s.fail(n) {
		return nil, fmt.Errorf("simulated insert failure for %q", n.Path)
	}
	return s.Store.CreateNode(ctx, n)
}

// wiring:e2 — a marker that WAS listed but whose row could not be written
// abandons the directory: "no marker row" and "the marker row failed to land"
// look identical downstream, and the damage (plaintext indexed, fingerprint
// recorded, never retried) is the same.
func TestOpsWorker_CopyDir_MarkerRowFails_LeavesSiblingsUnmirrored(t *testing.T) {
	ctx := context.Background()
	f := newOpsFixture(t)
	f.seedDir(t, "dest")
	f.seedDir(t, "kasa")
	f.seedFile(t, "kasa/"+e2e.MarkerName, `{"v":1}`)
	f.seedFile(t, "kasa/not.txt", "çokgizlisözcük")
	f.manager(&failCreateStore{Store: f.store, fail: func(n *model.Node) bool { return n.Name == e2e.MarkerName }})

	op := f.runOp(t, ops.OpCopy, []string{"kasa"}, "dest/")
	require.Equal(t, ops.StatusOK, op.Status, "the BYTES still copy — this is about the index only")
	_, err := f.drv.Stat(ctx, "dest/kasa/not.txt")
	require.NoError(t, err)

	require.Nil(t, f.nodeAt(t, "dest/kasa/"+e2e.MarkerName), "fixture check: the marker row is the one that must fail")
	assert.Nil(t, f.nodeAt(t, "dest/kasa/not.txt"),
		"a sibling mirrored under a folder with no marker row indexes plaintext and never retries")
	assert.NotNil(t, f.nodeAt(t, "dest/kasa"), "the copied folder itself must still appear in the listing")
}
