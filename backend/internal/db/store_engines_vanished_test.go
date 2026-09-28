package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// Issue #74, on every engine: the rows the storage sync soft-deleted WHERE THEY
// STOOD (up to 0.47) are found by the query the upgrade's repair runs — and a
// row whose bytes are parked in `.filex-trash/` never is, in either spelling
// of the path and whatever its folder is called. CountChildRows counts what
// the parent_id cascade would take, live or deleted.
func TestVanishedRowsAndChildCountOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)
			other, err := store.CreateStorage(ctx, &model.Storage{
				Name: "other", Driver: "local", MountPath: "/data/other",
				ConfigJSON: []byte(`{"root":"/data/other"}`), SyncMode: model.SyncModePoll,
				SyncIntervalS: 900, Enabled: true,
			})
			require.NoError(t, err)

			mk := func(sid int64, p string, typ model.NodeType, parent *int64) *model.Node {
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: sid, ParentID: parent, Name: lastSegment(p), Path: p,
					PathHash: pathkey.Hash(sid, p), StorageKey: p, Type: typ, Size: 1,
				})
				require.NoError(t, err, "create %s", p)
				return n
			}

			dir := mk(st.ID, "/Müşteri", model.NodeTypeDirectory, nil)
			goneA := mk(st.ID, "/Müşteri/gitti.txt", model.NodeTypeFile, &dir.ID)
			goneB := mk(st.ID, "bare/gitti.txt", model.NodeTypeFile, nil)
			liveF := mk(st.ID, "/Müşteri/duruyor.txt", model.NodeTypeFile, &dir.ID)
			trashed := mk(st.ID, "/sil.txt", model.NodeTypeFile, nil)
			trashedBare := mk(st.ID, "/sil2.txt", model.NodeTypeFile, nil)
			elsewhere := mk(other.ID, "/baska.txt", model.NodeTypeFile, nil)

			// The 0.47 tombstone shape: soft-deleted where it stood.
			for _, n := range []*model.Node{goneA, goneB, elsewhere} {
				require.NoError(t, store.SoftDeleteNode(ctx, n.ID))
			}
			// The everyday delete: retagged into the trash, in both spellings.
			k1 := "/.filex-trash/1700000000-ab12__sil.txt"
			require.NoError(t, store.SoftDeleteAndRetag(ctx, trashed.ID, k1, pathkey.Hash(st.ID, k1), "/sil.txt"))
			k2 := ".filex-trash/1700000001-cd34__sil2.txt"
			require.NoError(t, store.SoftDeleteAndRetag(ctx, trashedBare.ID, k2, pathkey.Hash(st.ID, k2), "/sil2.txt"))

			ids, err := store.ListVanishedNodeIDs(ctx, st.ID, 0, 500)
			require.NoError(t, err)
			require.ElementsMatch(t, []int64{goneA.ID, goneB.ID}, ids,
				"exactly the rows deleted where they stood — never a trash entry, never a live row, never another storage's")

			// Paged by id.
			first, err := store.ListVanishedNodeIDs(ctx, st.ID, 0, 1)
			require.NoError(t, err)
			require.Len(t, first, 1)
			rest, err := store.ListVanishedNodeIDs(ctx, st.ID, first[0], 500)
			require.NoError(t, err)
			require.Len(t, rest, 1)
			require.NotEqual(t, first[0], rest[0])

			n, err := store.CountChildRows(ctx, dir.ID)
			require.NoError(t, err)
			require.Equal(t, 2, n, "a live child and a deleted one both name the folder")
			require.NoError(t, store.HardDeleteNode(ctx, goneA.ID))
			require.NoError(t, store.HardDeleteNode(ctx, liveF.ID))
			n, err = store.CountChildRows(ctx, dir.ID)
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}
