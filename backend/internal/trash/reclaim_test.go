package trash_test

// Per-node cache reclamation at purge time (issue #18).
//
// A purge is the ONE moment at which "this node will never come back" is true.
// Everything keyed on the node id — the cached thumbnail JPEG, any staging
// directory still holding its bytes — may be released exactly there and nowhere
// earlier, because a soft delete into the trash is reversible.
//
// These tests pin both halves of that sentence.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/trash"
)

func reclaimFixture(t *testing.T) (db.Store, *trash.Service, *[]int64, int64) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "s", Driver: "local", MountPath: "s", Enabled: true, ConfigJSON: []byte(`{}`),
	})
	require.NoError(t, err)
	var reclaimed []int64
	svc := trash.New(store, nil, nil)
	svc.Reclaim = func(_ context.Context, id int64) { reclaimed = append(reclaimed, id) }
	return store, svc, &reclaimed, st.ID
}

func mkNode(t *testing.T, store db.Store, sid int64, name string) *model.Node {
	t.Helper()
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: sid, Name: name, Path: "/" + name,
		PathHash: pathkey.Hash(sid, "/"+name), Type: model.NodeTypeFile, Size: 7,
	})
	require.NoError(t, err)
	return n
}

func TestPurge_ReclaimsThePerNodeCaches(t *testing.T) {
	ctx := context.Background()
	store, svc, reclaimed, sid := reclaimFixture(t)

	n := mkNode(t, store, sid, "gone.png")
	require.NoError(t, store.SoftDeleteNode(ctx, n.ID))
	require.NoError(t, svc.PurgeOne(ctx, n.ID))

	require.Equal(t, []int64{n.ID}, *reclaimed)
}

// ⚠ The boundary. Trashing is not purging: the file is restorable and its
// cached bytes must still be there when it comes back.
func TestSoftDelete_ReclaimsNothing(t *testing.T) {
	ctx := context.Background()
	store, _, reclaimed, sid := reclaimFixture(t)

	n := mkNode(t, store, sid, "trashed.png")
	require.NoError(t, store.SoftDeleteNode(ctx, n.ID))

	require.Empty(t, *reclaimed)
}

// EmptyOlderThan is the bulk path (admin "empty the trash", and the retention
// loop). It shares purgeOne, so every row it destroys is reclaimed too — a
// leak here would be invisible precisely because nobody watches a scheduled job.
func TestEmptyTrash_ReclaimsEveryRowItPurges(t *testing.T) {
	ctx := context.Background()
	store, svc, reclaimed, sid := reclaimFixture(t)

	var ids []int64
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		n := mkNode(t, store, sid, name)
		require.NoError(t, store.SoftDeleteNode(ctx, n.ID))
		ids = append(ids, n.ID)
	}
	res, err := svc.EmptyOlderThan(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, 3, res.Deleted)
	require.ElementsMatch(t, ids, *reclaimed)
}

// A service with no Reclaim wired must still purge — the callback is optional
// wiring, not a precondition.
func TestPurge_WorksWithNoReclaimWired(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "s", Enabled: true, ConfigJSON: []byte(`{}`),
	})
	require.NoError(t, err)
	n := mkNode(t, store, st.ID, "x.png")
	require.NoError(t, store.SoftDeleteNode(ctx, n.ID))

	require.NoError(t, trash.New(store, nil, nil).PurgeOne(ctx, n.ID))
	_, err = store.GetNode(ctx, n.ID)
	require.Error(t, err)
}
