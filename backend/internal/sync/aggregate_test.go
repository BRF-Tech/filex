package sync_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestRecomputeFolderSizes — each folder's cached size becomes the recursive sum
// of its descendant file sizes, and its date becomes the newest descendant mtime
// (the "last activity" semantic, so folders show a date even when the driver
// reports none for directories, e.g. synthetic S3 prefixes).
func TestRecomputeFolderSizes(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/",
		ConfigJSON: []byte(`{"path":"/tmp/x"}`),
		SyncMode:   model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)

	older := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)

	mk := func(parent *int64, name, path string, typ model.NodeType, size int64, mtime *time.Time) int64 {
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, ParentID: parent, Name: name, Path: path, PathHash: path,
			Type: typ, Size: size, BackendMtime: mtime,
		})
		require.NoError(t, err)
		return n.ID
	}

	//  /a            (dir, no native mtime)
	//  /a/f1  = 100  (file, older)
	//  /a/b          (dir, no native mtime)
	//  /a/b/f2 = 50  (file, newer)
	a := mk(nil, "a", "/a", model.NodeTypeDirectory, 0, nil)
	mk(&a, "f1", "/a/f1", model.NodeTypeFile, 100, &older)
	b := mk(&a, "b", "/a/b", model.NodeTypeDirectory, 0, nil)
	mk(&b, "f2", "/a/b/f2", model.NodeTypeFile, 50, &newer)

	require.NoError(t, filexsync.RecomputeFolderSizes(ctx, store, st.ID))

	na, err := store.GetNode(ctx, a)
	require.NoError(t, err)
	assert.EqualValues(t, 150, na.Size, "/a = f1 + f2")
	require.NotNil(t, na.BackendMtime, "/a gets a date from its descendants")
	assert.True(t, na.BackendMtime.Equal(newer), "/a date = newest descendant (f2)")

	nb, err := store.GetNode(ctx, b)
	require.NoError(t, err)
	assert.EqualValues(t, 50, nb.Size, "/a/b = f2")
	require.NotNil(t, nb.BackendMtime, "/a/b gets a date from f2")
	assert.True(t, nb.BackendMtime.Equal(newer), "/a/b date = f2")
}
