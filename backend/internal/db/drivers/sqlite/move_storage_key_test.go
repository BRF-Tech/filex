package sqlite_test

// storage_key must follow a node when it moves.
//
// It is not a display field: internal/versioning (Snapshot AND Restore), the
// antivirus quarantine, the id-addressed download in Manager.Read and the sync
// tombstone pass all hand it to the storage driver IN PREFERENCE TO `path`.
// Every one of them fails silently on a miss — Snapshot returns "nothing to
// snapshot" and lets the overwrite through, quarantine reports success while
// the infected bytes stay live at the real path, confirmGone tombstones a file
// that is perfectly fine — so a stale value is invisible until one of those
// four does the wrong thing.
//
// Nothing else repairs it either: the periodic storage walk only touches
// seen_at and UpdateNodeMeta, and neither statement writes this column.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestMoveNode_StorageKeyFollowsThePath is the regression that would have
// caught it: on main the moved row kept "/eski/rapor.txt".
func TestMoveNode_StorageKeyFollowsThePath(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	stg, err := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)

	n, err := store.CreateNode(ctx, &model.Node{
		StorageID:  stg.ID,
		Name:       "rapor.txt",
		Path:       "/eski/rapor.txt",
		PathHash:   pathkey.Hash(stg.ID, "/eski/rapor.txt"),
		StorageKey: "/eski/rapor.txt",
		Type:       model.NodeTypeFile,
		Size:       12,
	})
	require.NoError(t, err)

	const dst = "/yeni/rapor-2.txt"
	require.NoError(t, store.MoveNode(ctx, n.ID, nil, "rapor-2.txt", dst, pathkey.Hash(stg.ID, dst)))

	moved, err := store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, dst, moved.Path)
	assert.Equal(t, dst, moved.StorageKey,
		"a moved node's storage_key must address where the file now IS")
}

// The trash exception, from the other side: on a trashed row storage_key
// deliberately holds the ORIGINAL path while path points into `.filex-trash/`.
// That is the only record of where Restore has to put the file back, and
// sync.reconcileTrash reads it to tell a restorable deletion from a row that
// has to be hard-deleted. A move must not overwrite it.
func TestMoveNode_LeavesATrashedRowsOriginAlone(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	stg, err := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)

	n, err := store.CreateNode(ctx, &model.Node{
		StorageID:  stg.ID,
		Name:       "sozlesme.pdf",
		Path:       "/belgeler/sozlesme.pdf",
		PathHash:   pathkey.Hash(stg.ID, "/belgeler/sozlesme.pdf"),
		StorageKey: "/belgeler/sozlesme.pdf",
		Type:       model.NodeTypeFile,
	})
	require.NoError(t, err)

	const trashPath = "/.filex-trash/1__sozlesme.pdf"
	require.NoError(t, store.SoftDeleteAndRetag(ctx, n.ID, trashPath,
		pathkey.Hash(stg.ID, trashPath), "/belgeler/sozlesme.pdf"))

	const renamed = "/.filex-trash/2__sozlesme.pdf"
	require.NoError(t, store.MoveNode(ctx, n.ID, nil, "2__sozlesme.pdf", renamed,
		pathkey.Hash(stg.ID, renamed)))

	got, err := store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, renamed, got.Path)
	assert.Equal(t, "/belgeler/sozlesme.pdf", got.StorageKey,
		"a trashed row's storage_key is where Restore puts it back; a move must not clobber it")
}
