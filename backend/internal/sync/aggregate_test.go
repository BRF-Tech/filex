package sync_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestRecomputeFolderSizes — each folder's cached size becomes the recursive sum
// of its descendant file sizes.
func TestRecomputeFolderSizes(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/",
		ConfigJSON: []byte(`{"path":"/tmp/x"}`),
		SyncMode:   model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)

	mk := func(parent *int64, name, path string, typ model.NodeType, size int64) int64 {
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, ParentID: parent, Name: name, Path: path, PathHash: path,
			Type: typ, Size: size,
		})
		require.NoError(t, err)
		return n.ID
	}

	//  /a            (dir)
	//  /a/f1  = 100  (file)
	//  /a/b          (dir)
	//  /a/b/f2 = 50  (file)
	a := mk(nil, "a", "/a", model.NodeTypeDirectory, 0)
	mk(&a, "f1", "/a/f1", model.NodeTypeFile, 100)
	b := mk(&a, "b", "/a/b", model.NodeTypeDirectory, 0)
	mk(&b, "f2", "/a/b/f2", model.NodeTypeFile, 50)

	require.NoError(t, filexsync.RecomputeFolderSizes(ctx, store, st.ID))

	na, err := store.GetNode(ctx, a)
	require.NoError(t, err)
	assert.EqualValues(t, 150, na.Size, "/a = f1 + f2")

	nb, err := store.GetNode(ctx, b)
	require.NoError(t, err)
	assert.EqualValues(t, 50, nb.Size, "/a/b = f2")
}
