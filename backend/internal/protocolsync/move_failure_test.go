package protocolsync

// A folder move over a protocol surface (WebDAV MOVE, S3 CopyObject+Delete,
// SFTP/FTPS/NFS rename, the AI/MCP move) re-homes the whole cached subtree
// through MoveRows. This file pins what happens to a DESCENDANT whose row
// cannot be re-homed.
//
// Found by @alfatm in his fork (32e8935e, a4241577); the first two tests are
// his, ported.

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// failMoveStore is the real store with MoveNode rigged to fail for the rows
// `fail` picks out — the collision the live-only path_hash unique index
// (migration 00032) produces when something already holds the destination.
type failMoveStore struct {
	db.Store
	fail func(path string) bool
}

func (s *failMoveStore) MoveNode(ctx context.Context, id int64, parentID *int64, name, p, hash string) error {
	if s.fail(p) {
		return fmt.Errorf("simulated move failure for %q", p)
	}
	return s.Store.MoveNode(ctx, id, parentID, name, p, hash)
}

// newRiggedSyncer is newSyncer with MoveNode failing for the paths `fail`
// picks out.
func newRiggedSyncer(t *testing.T, fail func(path string) bool) (*Syncer, *model.Storage) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "Local",
		Driver:     "local",
		MountPath:  "/local",
		ConfigJSON: []byte(`{"path":"/tmp/does-not-matter"}`),
		Enabled:    true,
	})
	require.NoError(t, err)
	return New(&failMoveStore{Store: store, fail: fail}, nil, nil, "test"), st
}

// A DESCENDANT whose row cannot be re-homed must not be soft-deleted.
//
// Soft-deleting it takes the file out of its folder AND puts a row in the
// trash listing whose bytes were never retagged into `.filex-trash` — so
// Restore reads the untouched old path out of storage_key, finds nothing to
// take back, swallows the miss and reports success having delivered nothing.
// Nothing reaps that row either: sync.reconcileTrash only looks at LIVE rows
// under the trash prefix.
func TestMoveRowsLeavesAFailedDescendantInPlace(t *testing.T) {
	installEmitter(t)
	s, st := newRiggedSyncer(t, func(p string) bool { return p == "/Archive/Alpha/stuck.txt" })
	ctx := context.Background()

	require.True(t, s.Write(ctx, st, "Docs/Alpha/stuck.txt", 4, "text/plain"))
	require.True(t, s.Write(ctx, st, "Docs/Alpha/fine.txt", 4, "text/plain"))
	_, err := s.EnsureDirChain(ctx, st, "Archive")
	require.NoError(t, err)

	alpha, err := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, "/Docs/Alpha"))
	require.NoError(t, err)
	require.NotNil(t, alpha)
	stuck, err := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, "/Docs/Alpha/stuck.txt"))
	require.NoError(t, err)
	require.NotNil(t, stuck)

	require.True(t, s.MoveRows(ctx, st, "Docs/Alpha", "Archive/Alpha"))

	got, err := s.Store.GetNode(ctx, stuck.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Nil(t, got.DeletedAt, "a descendant that could not be re-homed must not vanish into the trash")
	require.NotNil(t, got.ParentID)
	assert.Equal(t, alpha.ID, *got.ParentID, "it still hangs under the folder that moved, so it still lists")

	// The user-facing trash lists every soft-deleted row, so a soft-deleted
	// descendant lands in the listing the user actually sees.
	trashed, total, err := s.Store.ListTrashed(ctx, &st.ID, 50, 0)
	require.NoError(t, err)
	for _, n := range trashed {
		assert.NotEqual(t, stuck.ID, n.ID,
			"a phantom trash entry: its bytes were never retagged into .filex-trash, so Restore reports success and delivers nothing")
	}
	assert.Equal(t, 0, total, "the move put %d row(s) in the trash", total)

	// One stuck row does not abandon the pass.
	sib, err := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, "/Archive/Alpha/fine.txt"))
	require.NoError(t, err)
	assert.NotNil(t, sib, "the sibling that could move still moved")
}

// When the TOP row cannot be re-homed the pass stops there, and the row is
// left LIVE where it stands.
//
// The top row used to degrade to a soft-delete. That is not a deletion: the
// bytes are at the destination, never retagged into `.filex-trash`, and
// storage_key still names the path they left — so the row lands in the
// user-facing trash listing behind a Restore that takes nothing back and
// reports success, and nothing reaps it (sync.reconcileTrash only looks at
// LIVE rows under the trash prefix). Here the destination is not merely
// occupied — ReclaimDestination finds no row to drop — so there is nothing to
// repair, and a live row with a stale path is what the next sync walk can see.
//
// Carrying on past the failure re-homed every descendant to the DESTINATION
// while their parent stayed at the source: rows whose paths claim a folder
// that is not their parent, in a listing nobody reaches.
func TestMoveRowsStopsWhenTheTopRowCannotMove(t *testing.T) {
	installEmitter(t)
	s, st := newRiggedSyncer(t, func(p string) bool { return p == "/Archive/Alpha" })
	ctx := context.Background()

	require.True(t, s.Write(ctx, st, "Docs/Alpha/child.txt", 4, "text/plain"))
	_, err := s.EnsureDirChain(ctx, st, "Archive")
	require.NoError(t, err)

	child, err := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, "/Docs/Alpha/child.txt"))
	require.NoError(t, err)
	require.NotNil(t, child)
	top, err := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, "/Docs/Alpha"))
	require.NoError(t, err)
	require.NotNil(t, top)

	assert.False(t, s.MoveRows(ctx, st, "Docs/Alpha", "Archive/Alpha"),
		"the top row did not land at the destination, and the answer says so")

	stayed, err := s.Store.GetNode(ctx, top.ID)
	require.NoError(t, err)
	require.NotNil(t, stayed)
	assert.Nil(t, stayed.DeletedAt,
		"a move that could not be recorded is not a deletion, and a row whose bytes are at the destination cannot be restored from the trash")
	assert.Equal(t, "/Docs/Alpha", stayed.Path, "left where the walk can still see it")

	_, total, err := s.Store.ListTrashed(ctx, &st.ID, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, total,
		"a phantom trash entry: its bytes were never retagged into .filex-trash, so Restore reports success and delivers nothing")

	got, err := s.Store.GetNode(ctx, child.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "/Docs/Alpha/child.txt", got.Path,
		"the child was re-homed under a parent that had just been trashed")
	assert.Equal(t, "/Docs/Alpha/child.txt", got.StorageKey)
	assert.Nil(t, got.DeletedAt)
}

// A descendant whose stored path was never canonical must move too.
//
// CollectSubtree walks parent_id, not paths, so it reaches rows spelled the way
// whatever wrote them spelled it — EnsureDirChain stored directory rows
// verbatim ("dav/davdir") before it canonicalised them, and no migration
// rewrites `path` (00033 repairs storage_key alone). Trimming the canonical
// source prefix off the RAW spelling is a no-op on such a row, and the new path
// comes out as the destination with the whole old path glued on.
func TestMoveRowsReHomesADescendantStoredWithoutTheLeadingSlash(t *testing.T) {
	installEmitter(t)
	s, st := newSyncer(t)
	ctx := context.Background()

	_, err := s.EnsureDirChain(ctx, st, "Archive")
	require.NoError(t, err)
	alphaParent, err := s.EnsureDirChain(ctx, st, "Docs/Alpha")
	require.NoError(t, err)
	// The legacy spelling: same node, no leading slash.
	sub, err := s.Store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, ParentID: alphaParent, Name: "sub",
		Path: "Docs/Alpha/sub", PathHash: hashOf(st.ID, "Docs/Alpha/sub"),
		StorageKey: "Docs/Alpha/sub", Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	leaf, err := s.Store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, ParentID: &sub.ID, Name: "b.txt",
		Path: "/Docs/Alpha/sub/b.txt", PathHash: hashOf(st.ID, "/Docs/Alpha/sub/b.txt"),
		StorageKey: "/Docs/Alpha/sub/b.txt", Type: model.NodeTypeFile,
	})
	require.NoError(t, err)

	require.True(t, s.MoveRows(ctx, st, "Docs/Alpha", "Archive/Alpha"))

	got, err := s.Store.GetNode(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, "/Archive/Alpha/sub", got.Path,
		"a non-canonical spelling is not a reason to write a doubled path")
	assert.Equal(t, "/Archive/Alpha/sub", got.StorageKey)
	assert.Equal(t, hashOf(st.ID, "/Archive/Alpha/sub"), got.PathHash)

	child, err := s.Store.GetNode(ctx, leaf.ID)
	require.NoError(t, err)
	assert.Equal(t, "/Archive/Alpha/sub/b.txt", child.Path)
}
