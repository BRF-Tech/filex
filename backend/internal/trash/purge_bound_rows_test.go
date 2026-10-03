package trash_test

// Issue #104, the purge's half.
//
//   - A folder deleted where it stood (the queue's "already missing" branches
//     did that up to 0.49) could still have LIVE rows under it: the branches
//     touched the folder's row alone. Purging the folder hard-deleted its row,
//     and the parent_id cascade took those live rows with it - files nobody
//     deleted, gone from every listing, their quota never released.
//   - A purged file's snapshots under `.versions/<id>/` stayed on the storage:
//     their rows cascade with the file's, and nothing reached the bytes again.

import (
	"context"
	"path"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

func (r *purgeRig) child(t *testing.T, parent *model.Node, p string, kind model.NodeType) *model.Node {
	t.Helper()
	n, err := r.store.CreateNode(context.Background(), &model.Node{
		StorageID: r.sid, ParentID: &parent.ID, Name: path.Base(p), Path: p, StorageKey: p,
		PathHash: pathkey.Hash(r.sid, p), Type: kind, Size: 5,
	})
	require.NoError(t, err)
	return n
}

// history gives n one snapshot, on the disk and as its node_versions row.
func (r *purgeRig) history(t *testing.T, n *model.Node) string {
	t.Helper()
	key := ".versions/" + strconv.FormatInt(n.ID, 10) + "/1"
	r.write(t, key, "the bytes before the last save")
	_, err := r.store.CreateNodeVersion(context.Background(), &model.NodeVersion{
		NodeID: n.ID, VersionN: 1, StorageKey: key, Size: 30,
	})
	require.NoError(t, err)
	return key
}

// The purge never takes a live row through a folder's cascade. The folder
// stays (the run counts it as failed) until nothing live is under it, and goes
// on the run after that.
func TestPurge_AFolderDeletedWhereItStoodNeverTakesTheLiveRowsUnderIt(t *testing.T) {
	r := newPurgeRig(t)
	ctx := context.Background()
	dir := r.row(t, "/Proje", model.NodeTypeDirectory)
	live := r.child(t, dir, "/Proje/a.txt", model.NodeTypeFile)
	r.write(t, "Proje/a.txt", "nobody deleted me")
	require.NoError(t, r.store.SoftDeleteNode(ctx, dir.ID))

	res, err := r.svc.EmptyOlderThan(ctx, 0, r.sid)
	require.NoError(t, err)

	got, err := r.store.GetNode(ctx, live.ID)
	require.NoError(t, err, "the purge took a live row with the folder it was under")
	assert.Nil(t, got.DeletedAt)
	_, err = r.store.GetNode(ctx, dir.ID)
	assert.NoError(t, err, "the folder must stay while a live row is under it")
	assert.Equal(t, 1, res.Failed, "the folder it could not purge is counted as failed")
	assert.True(t, r.exists("Proje/a.txt"))

	// The live row goes (the storage sync found its file gone); the next run
	// purges the folder.
	require.NoError(t, r.store.HardDeleteNode(ctx, live.ID))
	res, err = r.svc.EmptyOlderThan(ctx, 0, r.sid)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Failed)
	_, err = r.store.GetNode(ctx, dir.ID)
	assert.Error(t, err, "with nothing live under it, the folder is purged")
}

// PurgeOne answers an error for such a folder, and touches nothing.
func TestPurge_PurgeOneRefusesAFolderWithLiveRowsUnderIt(t *testing.T) {
	r := newPurgeRig(t)
	ctx := context.Background()
	dir := r.row(t, "/Arsiv", model.NodeTypeDirectory)
	live := r.child(t, dir, "/Arsiv/b.txt", model.NodeTypeFile)
	require.NoError(t, r.store.SoftDeleteNode(ctx, dir.ID))

	assert.Error(t, r.svc.PurgeOne(ctx, dir.ID))
	_, err := r.store.GetNode(ctx, live.ID)
	assert.NoError(t, err, "the live row went with the folder")
}

// A purged trash entry takes its version history with it. The history of a
// file that is not purged stays.
func TestPurge_ATrashedFilesVersionHistoryGoesWithIt(t *testing.T) {
	r := newPurgeRig(t)
	ctx := context.Background()
	key := "/.filex-trash/1790000000-abc123__old.txt"
	r.write(t, key[1:], "trashed bytes")
	n := r.row(t, "/old.txt", model.NodeTypeFile)
	snap := r.history(t, n)
	kept := r.row(t, "/kept.txt", model.NodeTypeFile)
	keptSnap := r.history(t, kept)
	require.NoError(t, r.store.SoftDeleteAndRetag(ctx, n.ID, key, pathkey.Hash(r.sid, key), "/old.txt"))

	require.NoError(t, r.svc.PurgeOne(ctx, n.ID))

	assert.False(t, r.exists(snap), "a purged file's snapshot is left on the storage")
	assert.False(t, r.exists(path.Dir(snap)), "a purged file's folder under .versions/ is left behind")
	assert.True(t, r.exists(keptSnap), "the purge deleted another file's history")
}

// A row deleted where it stood owns no bytes at its path, but its snapshots
// are keyed by the row, not the path: they go with it.
func TestPurge_ATombstonesVersionHistoryGoesWithIt(t *testing.T) {
	r := newPurgeRig(t)
	ctx := context.Background()
	n := r.tombstone(t, "/notlar.txt", model.NodeTypeFile)
	snap := r.history(t, n)

	require.NoError(t, r.svc.PurgeOne(ctx, n.ID))
	assert.False(t, r.exists(snap))
}

// A trashed folder's files lose their history when the folder is purged.
func TestPurge_ATrashedFoldersFilesLoseTheirHistory(t *testing.T) {
	r := newPurgeRig(t)
	ctx := context.Background()
	dirKey := "/.filex-trash/1790000000-def456__Proje"
	r.write(t, dirKey[1:]+"/a.txt", "a")
	dir := r.row(t, "/Proje", model.NodeTypeDirectory)
	f := r.child(t, dir, "/Proje/a.txt", model.NodeTypeFile)
	snap := r.history(t, f)
	require.NoError(t, r.store.SoftDeleteAndRetag(ctx, dir.ID, dirKey, pathkey.Hash(r.sid, dirKey), "/Proje"))

	require.NoError(t, r.svc.PurgeOne(ctx, dir.ID))

	_, err := r.store.GetNode(ctx, f.ID)
	assert.Error(t, err, "fixture check: the folder's file is purged with it")
	assert.False(t, r.exists(snap), "the history of a file purged with its folder is left on the storage")
}
