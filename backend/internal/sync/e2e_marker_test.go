package sync_test

// An encrypted folder the walk sees for the FIRST time.
//
// "Is this folder encrypted?" is answered by a DB-ROW lookup: e2e.FindRoot
// asks for the marker's node at `<dir>/.filex-e2e.json`. Until that row
// exists the folder is not encrypted as far as the server is concerned, and
// every sibling row the walk creates before it starts a content-extraction
// job that reads UnderEncrypted as false, indexes the plaintext it finds and
// records the content fingerprint — so it never retries. That junk in the
// search index is permanent.
//
// The walk is exactly where this is reachable: a storage pointed at an
// existing tree, or an encrypted folder whose contents arrived over DAV,
// rclone or the CLI. Ordering is a property of the driver's List, not of the
// walk — os.ReadDir is sorted and `-` (0x2D) sorts before `.` (0x2E), and the
// object stores promise no order at all.
//
// Found by @alfatm in his fork (dbf94c23); the tests are his, ported.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// seedEncryptedFolder writes an encrypted folder whose plaintext sibling
// sorts BEFORE the marker, which is the order os.ReadDir hands them over in.
func seedEncryptedFolder(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "kasa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "kasa", e2e.MarkerName), []byte(`{"v":1}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "kasa", "-not.txt"), []byte("çokgizlisözcük"), 0o644))
}

func TestSyncCataloguesTheMarkerBeforeAnySibling(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	seedEncryptedFolder(t, root)

	// The real content job, run INLINE from the index's content hook, so it
	// asks UnderEncrypted against the rows that exist at that moment — which
	// is the whole question here.
	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	ci := queue.NewContentIndexer(store, func(int64) (storage.Driver, error) { return drv, nil }, idx, 0)
	idx.SetContentHook(func(ctx context.Context, n *model.Node) {
		_ = ci.Handle(ctx, queue.Op{Type: queue.TypeContentIndex, Payload: map[string]any{"node_id": n.ID}})
	})

	w := filexsync.New(store)
	w.AttachIndex(idx)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID))

	marker, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/kasa/"+e2e.MarkerName))
	require.NoError(t, err)
	require.NotNil(t, marker, "the marker is the folder's own content and must be catalogued")
	sibling, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/kasa/-not.txt"))
	require.NoError(t, err)
	require.NotNil(t, sibling, "the sibling must still be catalogued — the point is the ORDER")
	require.True(t, ci.Eligible(sibling),
		"the guard only means something while the file is otherwise extractable")

	// sqlite hands out rowids in insertion order, so this states the ordering
	// directly; the index assertion below is what the ordering is FOR.
	assert.Less(t, marker.ID, sibling.ID, "the marker row must be written before any sibling's")
	assert.Empty(t, idx.SafeSearchScoped(ctx, "çokgizlisözcük", 10, search.ScopeContent),
		"the content job that ran WHILE the walk was writing rows must have seen the marker already there")
}

// failMarkerStore is the real store with CreateNode rigged to fail for the
// encrypted-folder marker — the transient DB error the walk has to survive.
type failMarkerStore struct {
	db.Store
	fail bool
}

func (s *failMarkerStore) CreateNode(ctx context.Context, n *model.Node) (*model.Node, error) {
	if s.fail && n.Name == e2e.MarkerName {
		return nil, assert.AnError
	}
	return s.Store.CreateNode(ctx, n)
}

// A marker that was LISTED but whose row could not be written abandons the
// directory: catalogue nothing under it this pass.
//
// "No marker row" and "the marker row failed to land" look identical to
// everything downstream, and the damage is the same. Carrying on would index
// plaintext and record a fingerprint that is never revisited — permanent.
// Abandoning costs one pass: the walk is the reconciling path, it runs again
// on the next interval, and the folder is catalogued correctly then.
func TestSyncMarkerRowFails_LeavesTheDirectoryUncatalogued(t *testing.T) {
	ctx := context.Background()
	_, real := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, real)
	seedEncryptedFolder(t, root)

	store := &failMarkerStore{Store: real, fail: true}
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID), "one directory's DB error must not fail the whole pass")

	marker, _ := real.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/kasa/"+e2e.MarkerName))
	require.Nil(t, marker, "fixture check: the marker row is the one that must fail")
	sibling, _ := real.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/kasa/-not.txt"))
	assert.Nil(t, sibling,
		"a sibling catalogued under a folder with no marker row indexes plaintext and never retries")
	// The folder itself is catalogued by its PARENT's listing, before the
	// failing one, and a directory row carries no content of its own.
	dir, _ := real.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/kasa"))
	assert.NotNil(t, dir, "the folder itself must still appear in the listing")

	// And the abandon is a retry, not a hole: the very next pass catalogues
	// the subtree, marker first.
	store.fail = false
	waitPastSecondBoundary()
	require.NoError(t, w.Trigger(ctx, st.ID))
	marker, _ = real.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/kasa/"+e2e.MarkerName))
	require.NotNil(t, marker, "the next pass must catalogue what the failed one left")
	sibling, _ = real.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/kasa/-not.txt"))
	require.NotNil(t, sibling)
	assert.Less(t, marker.ID, sibling.ID, "the marker row must be written before any sibling's")
}
