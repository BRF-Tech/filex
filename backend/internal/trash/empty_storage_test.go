package trash_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// trashed creates a node on a storage and puts it in the trash.
func trashed(t *testing.T, store db.Store, sid int64, name string) int64 {
	t.Helper()
	n := mkNode(t, store, sid, name)
	require.NoError(t, store.SoftDeleteNode(context.Background(), n.ID))
	return n.ID
}

// secondStorage adds a storage beside the fixture's, so "only the one you
// named" has something to be measured against.
func secondStorage(t *testing.T, store db.Store) int64 {
	t.Helper()
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "other", Driver: "local", MountPath: "other", Enabled: true, ConfigJSON: []byte(`{}`),
	})
	require.NoError(t, err)
	return st.ID
}

// "Empty the trash" for ONE storage must leave the others alone.
//
// ⚠⚠ It did not. `POST /api/admin/trash/empty` decoded `storage_id` into a
// struct field that nothing read, and `EmptyOlderThan` had no storage
// parameter at all — so the narrowed operation purged every storage. The admin
// UI's confirmation dialog said the opposite in as many words:
//
//	"If you picked a storage, only that one is affected."
//
// Permanent, irreversible, and the dialog reassured the operator while it
// happened. This asserts the SURVIVOR as well as the victim: a purge that
// removes the right rows and some extra ones would pass a test that only
// checked the target.
func TestEmptyOlderThan_OnlyTheStorageYouNamed(t *testing.T) {
	ctx := context.Background()
	store, svc, _, sid := reclaimFixture(t)
	other := secondStorage(t, store)

	keep := trashed(t, store, other, "other-tenant-invoice.pdf")
	drop := trashed(t, store, sid, "mine.txt")

	res, err := svc.EmptyOlderThan(ctx, 0, sid)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Deleted, "exactly the named storage's row")

	_, err = store.GetNode(ctx, drop)
	assert.Error(t, err, "the named storage's trashed row is purged")

	survived, err := store.GetNode(ctx, keep)
	require.NoError(t, err, "the other storage's trash must survive — this is the bug")
	assert.NotNil(t, survived.DeletedAt, "and it stays in the trash, not restored")
}

// 0 still means "every storage the caller can reach": the nightly retention
// sweep and an unnarrowed "empty the trash" both depend on it, so narrowing
// must not have quietly become mandatory.
func TestEmptyOlderThan_ZeroStillMeansEveryStorage(t *testing.T) {
	ctx := context.Background()
	store, svc, _, sid := reclaimFixture(t)
	other := secondStorage(t, store)

	trashed(t, store, other, "a.pdf")
	trashed(t, store, sid, "b.txt")

	res, err := svc.EmptyOlderThan(ctx, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Deleted, "both storages")
}
