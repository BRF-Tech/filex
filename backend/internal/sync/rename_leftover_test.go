package sync_test

// Issue #21, the recovery half.
//
// A folder rename used to move exactly one row and leave every descendant
// pointing at the old path. The next sync then could not create a row for the
// file it found at the new path — the descendant still held
// (storage, parent, name) — so it logged
// `duplicate key value violates unique constraint idx_nodes_storage_parent_name`
// on every pass, forever, while the tombstone pass moved the stale rows into
// the trash.
//
// The write path is fixed. This is what heals the installs that already have
// those rows: the walk repairs them in place, keeping the node's id — and with
// it its shares, comments and version history — rather than binning it and
// cataloguing a stranger.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestSyncRepairsRowsLeftBehindByAFolderRename(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "Leon", "Belgeler"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Leon", "not.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Leon", "Belgeler", "rapor.pages"), []byte("b"), 0o644))

	runSync(t, store, st)

	before, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/Leon/not.txt"))
	require.NoError(t, err)
	require.NotNil(t, before)
	deepBefore, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/Leon/Belgeler/rapor.pages"))
	require.NoError(t, err)

	// Reproduce exactly what the old rename did: the folder moves on the
	// storage AND in the catalogue, and the descendants are left behind.
	require.NoError(t, os.Rename(filepath.Join(root, "Leon"), filepath.Join(root, "Leonid")))
	folder, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/Leon"))
	require.NoError(t, err)
	require.NoError(t, store.MoveNode(ctx, folder.ID, nil, "Leonid", "/Leonid", pathkey.Hash(st.ID, "/Leonid")))

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	// The file is catalogued at the path the storage really has…
	after, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/Leonid/not.txt"))
	require.NoError(t, err, "the walk did not repair the row; it is still at the old path")
	require.NotNil(t, after)
	assert.Nil(t, after.DeletedAt, "the file must not be in the trash")

	// …and it is the SAME row. A new id would mean the shares, comments and
	// version history of that file were silently left on a tombstone.
	assert.Equal(t, before.ID, after.ID,
		"the repair must keep the node's identity, not catalogue a stranger")

	// The same one level deeper, which is where a one-row repair would stop.
	deepAfter, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/Leonid/Belgeler/rapor.pages"))
	require.NoError(t, err)
	assert.Equal(t, deepBefore.ID, deepAfter.ID)
	assert.Nil(t, deepAfter.DeletedAt)

	// And nothing is left at the old prefix to collide with on the next pass.
	stale, _ := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/Leon/not.txt"))
	assert.Nil(t, stale)
}
