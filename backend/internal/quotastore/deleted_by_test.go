package quotastore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
)

// Putting a thing in the trash names who did it, the way a move and a restore
// already name who did them. The trash listing reads the name.

func TestTrash_RecordsWhoDeletedIt(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, asUser(e.alice), "t.txt", 10)

	require.NoError(t, e.store.SoftDeleteNode(asUser(e.bob), n.ID))

	row := e.node(t, n.ID)
	require.NotNil(t, row.DeletedBy, "the trash does not say who put it there")
	assert.Equal(t, e.bob, *row.DeletedBy)
	require.NotNil(t, row.OwnerID)
	assert.Equal(t, e.alice, *row.OwnerID, "deleting a file does not make it the deleter's")
}

// The ops worker runs a queued delete long after the request is gone; it names
// the person who asked with WithActor.
func TestTrash_TheOpsWorkerNamesWhoAsked(t *testing.T) {
	e := newEnv(t)
	ctx := asUser(e.alice)
	dir, err := e.store.CreateNode(ctx, &model.Node{
		StorageID: e.storageID, Name: "Proje", Path: "/Proje",
		PathHash: pathkey.Hash(e.storageID, "/Proje"), StorageKey: "/Proje", Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	child, err := e.store.CreateNode(ctx, &model.Node{
		StorageID: e.storageID, ParentID: &dir.ID, Name: "plan.txt", Path: "/Proje/plan.txt",
		PathHash: pathkey.Hash(e.storageID, "/Proje/plan.txt"), StorageKey: "/Proje/plan.txt",
		Type: model.NodeTypeFile, Size: 5,
	})
	require.NoError(t, err)

	worker := quotastore.WithActor(context.Background(), e.bob)
	key := "/.filex-trash/1700000000-cd34__Proje"
	require.NoError(t, e.store.SoftDeleteAndRetag(worker, dir.ID, key, pathkey.Hash(e.storageID, key), "/Proje"))

	for _, id := range []int64{dir.ID, child.ID} {
		row := e.node(t, id)
		require.NotNil(t, row.DeletedBy, "row %d names nobody", id)
		assert.Equal(t, e.bob, *row.DeletedBy)
	}
}

// The scanner's tombstone pass trashes a row whose object vanished from the
// storage. Nobody in filex did that, and no name is written.
func TestTrash_BySystem_NamesNobody(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, asUser(e.alice), "t.txt", 10)

	system := quotastore.WithActor(quotastore.WithOwner(context.Background(), 0), 0)
	require.NoError(t, e.store.SoftDeleteNode(system, n.ID))

	assert.Nil(t, e.node(t, n.ID).DeletedBy)
}

// A restore forgets who deleted it, so a later delete by nobody is not
// credited to the person who deleted it the first time.
func TestTrash_RestoreForgetsWhoDeletedIt(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, asUser(e.alice), "t.txt", 10)
	require.NoError(t, e.store.SoftDeleteNode(asUser(e.bob), n.ID))

	require.NoError(t, e.store.RestoreNode(asUser(e.alice), n.ID))
	assert.Nil(t, e.node(t, n.ID).DeletedBy)

	system := quotastore.WithActor(quotastore.WithOwner(context.Background(), 0), 0)
	require.NoError(t, e.store.SoftDeleteNode(system, n.ID))
	assert.Nil(t, e.node(t, n.ID).DeletedBy, "the first deleter was named for the second delete")
}
