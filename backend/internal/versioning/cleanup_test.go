package versioning_test

// Retention job tests ("Koru" v0.4): keep_n=0 is a strict no-op, keep_n>0
// trims every versioned node down to the configured count. Storage-object
// deletion is best-effort inside Cleanup, so the fixtures only need DB
// rows — the resolver deliberately errors.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// seedVersionedNode creates a file node with `count` version rows.
func seedVersionedNode(t *testing.T, store db.Store, storageID int64, name string, count int) *model.Node {
	t.Helper()
	ctx := context.Background()
	n, err := store.CreateNode(ctx, &model.Node{
		StorageID: storageID,
		Name:      name,
		Path:      "/" + name,
		PathHash:  "hash-" + name,
		Type:      model.NodeTypeFile,
		Size:      10,
	})
	require.NoError(t, err)
	for i := 1; i <= count; i++ {
		_, err := store.CreateNodeVersion(ctx, &model.NodeVersion{
			NodeID:     n.ID,
			VersionN:   i,
			StorageKey: fmt.Sprintf(".versions/%d/%d", n.ID, i),
			Size:       10,
		})
		require.NoError(t, err)
	}
	return n
}

func newRetentionFixture(t *testing.T) (db.Store, *versioning.Service, int64) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true,
	})
	require.NoError(t, err)
	svc := versioning.New(store, func(int64) (storage.Driver, error) {
		return nil, errors.New("no driver in retention test")
	})
	return store, svc, st.ID
}

func TestVersionRetention_KeepNZeroIsNoop(t *testing.T) {
	store, svc, stID := newRetentionFixture(t)
	n := seedVersionedNode(t, store, stID, "a.txt", 5)

	// No setting row at all → disabled.
	res, err := svc.RunRetentionOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, res.KeepN)
	assert.Equal(t, 0, res.Nodes)
	assert.Equal(t, 0, res.Deleted)

	// Explicit 0 → still disabled.
	require.NoError(t, store.UpsertSetting(context.Background(), versioning.SettingKeyKeepN, "0"))
	res, err = svc.RunRetentionOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, res.Deleted)

	vs, err := store.ListNodeVersions(context.Background(), n.ID)
	require.NoError(t, err)
	assert.Len(t, vs, 5, "keep_n=0 must not delete anything")
}

func TestVersionRetention_AppliesKeepN(t *testing.T) {
	store, svc, stID := newRetentionFixture(t)
	big := seedVersionedNode(t, store, stID, "big.txt", 5)
	small := seedVersionedNode(t, store, stID, "small.txt", 2)

	require.NoError(t, store.UpsertSetting(context.Background(), versioning.SettingKeyKeepN, "3"))
	res, err := svc.RunRetentionOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, res.KeepN)
	assert.Equal(t, 2, res.Nodes)
	assert.Equal(t, 2, res.Deleted, "5-version node loses exactly 2")
	assert.Equal(t, 0, res.Failed)

	vs, err := store.ListNodeVersions(context.Background(), big.ID)
	require.NoError(t, err)
	require.Len(t, vs, 3)
	// Newest-first listing: versions 5,4,3 survive.
	assert.Equal(t, 5, vs[0].VersionN)
	assert.Equal(t, 3, vs[2].VersionN)

	// Under-quota node untouched.
	vs, err = store.ListNodeVersions(context.Background(), small.ID)
	require.NoError(t, err)
	assert.Len(t, vs, 2)

	// Idempotent: a second run deletes nothing more.
	res, err = svc.RunRetentionOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, res.Deleted)
}
