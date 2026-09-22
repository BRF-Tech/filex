package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// A folder whose name is not plain ASCII went to the trash WITHOUT its
// contents.
//
// ⚠⚠ The rows under a trashed folder are found with SUBSTR(path, 1, n) = the
// folder's path, and n was the Go byte length of that path. SQL counts
// characters. "/Müşteri/" is 9 characters and 11 bytes, so SUBSTR returned the
// folder path plus two characters of each child's name, which never equals the
// prefix: the folder row went to the trash, its files stayed live under a
// parent that was gone — listed nowhere, still counted, still searchable — and
// the next sync, finding no bytes at their paths, tombstoned them one by one
// outside the folder's trash entry. Restoring the folder had the same blind
// spot on the way back.
func TestTrashAndRestore_AFolderWithANonASCIIName_TakesItsContents(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			sid := engineStorage(t, store, "musteri")

			mk := func(p string, kind model.NodeType, parent *int64) *model.Node {
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: sid, ParentID: parent, Name: lastSeg(p), Path: p, StorageKey: p,
					PathHash: pathkey.Hash(sid, p), Type: kind, Size: 1,
				})
				require.NoError(t, err)
				return n
			}
			folder := mk("/Müşteri", model.NodeTypeDirectory, nil)
			child := mk("/Müşteri/sözleşme.pdf", model.NodeTypeFile, &folder.ID)
			sub := mk("/Müşteri/Çıktılar", model.NodeTypeDirectory, &folder.ID)
			grand := mk("/Müşteri/Çıktılar/özet.txt", model.NodeTypeFile, &sub.ID)

			trashPath := "/.filex-trash/1790000000-a1b2c3__Müşteri"
			require.NoError(t, store.SoftDeleteAndRetag(ctx, folder.ID, trashPath, pathkey.Hash(sid, trashPath), "/Müşteri"))

			for _, n := range []*model.Node{child, sub, grand} {
				got, err := store.GetNode(ctx, n.ID)
				require.NoError(t, err)
				assert.NotNil(t, got.DeletedAt, "%s stayed live after its folder went to the trash", n.Path)
				assert.Equal(t, trashPath+n.Path[len("/Müşteri"):], got.Path, "%s is not inside the folder's trash entry", n.Path)
				live, _ := store.GetNodeByPath(ctx, sid, pathkey.Hash(sid, n.Path))
				assert.Nil(t, live, "a live row still claims %s", n.Path)
			}

			require.NoError(t, store.RestoreNodeAt(ctx, folder.ID, nil, "/Müşteri"))
			for _, n := range []*model.Node{folder, child, sub, grand} {
				got, err := store.GetNodeByPath(ctx, sid, pathkey.Hash(sid, n.Path))
				require.NoError(t, err, "%s did not come back with its folder", n.Path)
				require.NotNil(t, got)
				assert.Equal(t, n.ID, got.ID)
				assert.Nil(t, got.DeletedAt)
			}
		})
	}
}

func lastSeg(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
