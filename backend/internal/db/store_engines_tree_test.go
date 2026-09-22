package db_test

import (
	"context"
	"path"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// seedTreeRow writes one catalogue row at p (either spelling), or fails.
func seedTreeRow(t *testing.T, store db.Store, storageID int64, p string, typ model.NodeType) *model.Node {
	t.Helper()
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: storageID, Name: path.Base(p), Path: p, PathHash: pathkey.Hash(storageID, p),
		StorageKey: p, Type: typ, Size: 1,
	})
	require.NoError(t, err, "seed %s", p)
	return n
}

func pathsOf(nodes []*model.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Path)
	}
	sort.Strings(out)
	return out
}

// ListNodesUnder decides which rows the sync worker drops from the catalogue,
// so its match has to be exact on every engine: the tree and nothing that
// merely looks like it, in both path spellings, and a folder whose name is
// not ASCII as reliably as one that is.
func TestListNodesUnderOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)

			seedTreeRow(t, store, st.ID, "/.versions", model.NodeTypeDirectory)
			seedTreeRow(t, store, st.ID, "/.versions/7", model.NodeTypeDirectory)
			seedTreeRow(t, store, st.ID, "/.versions/7/1", model.NodeTypeFile)
			trashed := seedTreeRow(t, store, st.ID, "/.versions/8", model.NodeTypeDirectory)
			require.NoError(t, store.SoftDeleteNode(ctx, trashed.ID))
			seedTreeRow(t, store, st.ID, ".versions/9", model.NodeTypeDirectory) // the bare spelling
			// look-alikes that must never match
			seedTreeRow(t, store, st.ID, "/.versions-old", model.NodeTypeDirectory)
			seedTreeRow(t, store, st.ID, "/.versions-old/1", model.NodeTypeFile)
			seedTreeRow(t, store, st.ID, "/.Versions", model.NodeTypeDirectory)
			seedTreeRow(t, store, st.ID, "/docs/.versions", model.NodeTypeDirectory)
			seedTreeRow(t, store, st.ID, "/a_b", model.NodeTypeDirectory)
			seedTreeRow(t, store, st.ID, "/axb/c.txt", model.NodeTypeFile)
			// non-ASCII names: a byte-length bound would miss all of these
			seedTreeRow(t, store, st.ID, "/Müşteri", model.NodeTypeDirectory)
			seedTreeRow(t, store, st.ID, "/Müşteri/sözleşme.pdf", model.NodeTypeFile)
			seedTreeRow(t, store, st.ID, "/Müşteri2/ek.pdf", model.NodeTypeFile)

			live, err := store.ListNodesUnder(ctx, st.ID, ".versions", false)
			require.NoError(t, err)
			require.Equal(t, []string{".versions/9", "/.versions", "/.versions/7", "/.versions/7/1"}, pathsOf(live))

			all, err := store.ListNodesUnder(ctx, st.ID, "/.versions/", true)
			require.NoError(t, err)
			require.Equal(t, []string{".versions/9", "/.versions", "/.versions/7", "/.versions/7/1", "/.versions/8"}, pathsOf(all),
				"includeDeleted must add the rows already in the trash")

			tr, err := store.ListNodesUnder(ctx, st.ID, "Müşteri", false)
			require.NoError(t, err)
			require.Equal(t, []string{"/Müşteri", "/Müşteri/sözleşme.pdf"}, pathsOf(tr))

			us, err := store.ListNodesUnder(ctx, st.ID, "/a_b", false)
			require.NoError(t, err)
			require.Equal(t, []string{"/a_b"}, pathsOf(us), "an underscore is a name, not a wildcard")

			root, err := store.ListNodesUnder(ctx, st.ID, "/", true)
			require.NoError(t, err)
			require.Empty(t, root, "the storage root is never a subtree")
		})
	}
}
