package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// Who put a row in the trash, on every engine (migration 00061):
//   - naming the folder names every row trashed with it, because the trash
//     lists a folder's contents as rows of their own;
//   - a restore forgets the name;
//   - a soft delete that names nobody (the scanner's tombstone pass) never
//     inherits a name from an earlier trip through the trash.
func TestTrashRecordsWhoDeletedOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)
			who, err := store.CreateUser(ctx, "deleter@test.local", "", model.RoleUser, "en", "UTC")
			require.NoError(t, err)

			mk := func(p string, typ model.NodeType, parent *int64) *model.Node {
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: st.ID, ParentID: parent, Name: lastSegment(p), Path: p,
					PathHash: pathkey.Hash(st.ID, p), StorageKey: p, Type: typ, Size: 1,
				})
				require.NoError(t, err, "create %s", p)
				return n
			}
			get := func(id int64) *model.Node {
				n, err := store.GetNode(ctx, id)
				require.NoError(t, err)
				return n
			}

			dir := mk("/Rapor", model.NodeTypeDirectory, nil)
			child := mk("/Rapor/ek.txt", model.NodeTypeFile, &dir.ID)
			loose := mk("/ayri.txt", model.NodeTypeFile, nil)

			trashKey := "/.filex-trash/1700000000-ab12__Rapor"
			require.NoError(t, store.SoftDeleteAndRetag(ctx, dir.ID, trashKey, pathkey.Hash(st.ID, trashKey), "/Rapor"))
			require.Nil(t, get(dir.ID).DeletedBy, "the soft delete itself names nobody")
			require.NoError(t, store.SetNodeDeletedBy(ctx, dir.ID, &who.ID))

			require.NotNil(t, get(dir.ID).DeletedBy)
			require.Equal(t, who.ID, *get(dir.ID).DeletedBy)
			require.NotNil(t, get(child.ID).DeletedBy, "the folder's contents are named too")
			require.Equal(t, who.ID, *get(child.ID).DeletedBy)
			require.Nil(t, get(loose.ID).DeletedBy, "a row outside the folder is not")

			rows, _, err := store.ListTrashed(ctx, &st.ID, 50, 0)
			require.NoError(t, err)
			require.Len(t, rows, 2)
			for _, r := range rows {
				require.NotNil(t, r.DeletedBy, "the trash listing reads the name: %s", r.Path)
			}

			require.NoError(t, store.RestoreNodeAt(ctx, dir.ID, nil, "/Rapor"))
			require.Nil(t, get(dir.ID).DeletedBy, "a restore forgets who deleted it")
			require.Nil(t, get(child.ID).DeletedBy, "for the folder's contents too")

			// A live row is never named.
			require.NoError(t, store.SetNodeDeletedBy(ctx, loose.ID, &who.ID))
			require.Nil(t, get(loose.ID).DeletedBy, "a row that is not in the trash was named")

			// Named, restored, then trashed by the scanner: nobody.
			require.NoError(t, store.SoftDeleteNode(ctx, loose.ID))
			require.NoError(t, store.SetNodeDeletedBy(ctx, loose.ID, &who.ID))
			require.NoError(t, store.RestoreNode(ctx, loose.ID))
			require.NoError(t, store.SoftDeleteNode(ctx, loose.ID))
			require.Nil(t, get(loose.ID).DeletedBy, "an earlier name came back on a delete that named nobody")
		})
	}
}

func lastSegment(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
