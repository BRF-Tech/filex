package protocolsync

// Issue #104: DeleteRows is where every "the bytes are gone for good" path
// drops its rows (the protocol servers, the explorer's delete of an item
// already gone, the operations queue). A folder's live subtree went one row at
// a time, but rows an earlier version had soft-deleted WHERE THEY STOOD below
// the folder still named it as their parent: the folder's row took them
// through the parent_id cascade, and the bytes they still counted stayed on
// their owner's quota for good.

import (
	"context"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestDeleteRows_AFolderReleasesTheRowsLeftDeletedWhereTheyStoodUnderIt(t *testing.T) {
	ctx := context.Background()
	_, raw := dbtest.NewTestDB(t)
	acct := quotastore.New(raw)
	st, err := raw.CreateStorage(ctx, &model.Storage{
		Name: "Local", Driver: "local", MountPath: "/local", Enabled: true,
		ConfigJSON: []byte(`{"path":"/tmp/does-not-matter"}`),
	})
	require.NoError(t, err)
	u, err := raw.CreateUser(ctx, "owner@test.local", "not-a-real-hash", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	owned := quotastore.WithOwner(ctx, u.ID)
	mk := func(parent *model.Node, p string, kind model.NodeType, size int64) *model.Node {
		n := &model.Node{StorageID: st.ID, Name: path.Base(p), Path: p, StorageKey: p,
			PathHash: pathkey.Hash(st.ID, p), Type: kind, Size: size}
		if parent != nil {
			n.ParentID = &parent.ID
		}
		created, err := acct.CreateNode(owned, n)
		require.NoError(t, err)
		return created
	}
	dir := mk(nil, "/Proje", model.NodeTypeDirectory, 0)
	live := mk(dir, "/Proje/a.txt", model.NodeTypeFile, 100)
	// What the sync left for a file it found gone, up to 0.47: the row,
	// deleted where it stood - still under the folder, still billed.
	tomb := mk(dir, "/Proje/eski.txt", model.NodeTypeFile, 50)
	require.NoError(t, raw.SoftDeleteNode(ctx, tomb.ID))
	used, _, err := raw.GetUserUsage(ctx, u.ID)
	require.NoError(t, err)
	require.EqualValues(t, 150, used, "fixture check: a row deleted where it stood still counts")

	_, ok := New(acct, nil, nil, "test").DeleteRows(ctx, st, "Proje")
	require.True(t, ok)

	for _, n := range []*model.Node{dir, live, tomb} {
		_, err := raw.GetNode(ctx, n.ID)
		assert.Error(t, err, "the row of %s is still there", n.Path)
	}
	used, _, err = raw.GetUserUsage(ctx, u.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 0, used, "the folder's cascade took a row and left its bytes on the owner's quota")
}
