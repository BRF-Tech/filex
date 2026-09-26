package db_test

// Migration 00060 + SetStorageOrder, on every engine (issue #57).
//
// The admin decides the order the storages are listed in — the sidebar, the
// explorer root, the admin list. A storage nobody has placed keeps NULL and
// comes after every placed one, in creation order, so an install that never
// touches the order lists exactly as it did before (ORDER BY id).

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

func storageNames(list []*model.Storage) []string {
	out := make([]string, 0, len(list))
	for _, st := range list {
		out = append(out, st.Name)
	}
	return out
}

func sortOrderOf(t *testing.T, store db.Store, id int64) *int64 {
	t.Helper()
	st, err := store.GetStorage(context.Background(), id)
	require.NoError(t, err)
	return st.SortOrder
}

func TestStorageSortOrderOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			mk := func(name string, enabled bool) *model.Storage {
				st, err := store.CreateStorage(ctx, &model.Storage{
					Name: name, Driver: "local", MountPath: "/" + name,
					ConfigJSON: json.RawMessage(`{"root":"/tmp/` + name + `"}`),
					SyncMode:   model.SyncModePoll, SyncIntervalS: 900, Enabled: enabled,
				})
				require.NoError(t, err)
				require.Nil(t, st.SortOrder, "%s: a new storage has not been placed", name)
				return st
			}
			a := mk("alpha", true)
			b := mk("bravo", true)
			c := mk("charlie", false)
			d := mk("delta", true)

			list := func() []string {
				t.Helper()
				all, err := store.ListStorages(ctx)
				require.NoError(t, err)
				return storageNames(all)
			}
			enabled := func() []string {
				t.Helper()
				on, err := store.ListEnabledStorages(ctx)
				require.NoError(t, err)
				return storageNames(on)
			}

			// Nobody placed anything: creation order, as before the column.
			require.Equal(t, []string{"alpha", "bravo", "charlie", "delta"}, list())
			require.Equal(t, []string{"alpha", "bravo", "delta"}, enabled())

			// Placed storages first, by position; the rest after them by id.
			require.NoError(t, store.SetStorageOrder(ctx, []int64{d.ID, c.ID}, nil))
			require.Equal(t, []string{"delta", "charlie", "alpha", "bravo"}, list())
			require.Equal(t, []string{"delta", "alpha", "bravo"}, enabled(),
				"the explorer's list follows the same order, minus the disabled storage")
			require.Equal(t, int64(1), *sortOrderOf(t, store, d.ID))
			require.Equal(t, int64(2), *sortOrderOf(t, store, c.ID))
			require.Nil(t, sortOrderOf(t, store, a.ID))

			// The storage edit form saves a whole row; it must not reset the
			// position it knows nothing about.
			d.Name = "delta-renamed"
			d.SortOrder = nil
			require.NoError(t, store.UpdateStorage(ctx, d))
			got := sortOrderOf(t, store, d.ID)
			require.NotNil(t, got, "UpdateStorage cleared the admin's order")
			require.Equal(t, int64(1), *got)

			// A new order and the storages it no longer names, in one call.
			require.NoError(t, store.SetStorageOrder(ctx, []int64{b.ID, a.ID}, []int64{c.ID, d.ID}))
			require.Equal(t, []string{"bravo", "alpha", "charlie", "delta-renamed"}, list())
			require.Equal(t, int64(1), *sortOrderOf(t, store, b.ID))
			require.Equal(t, int64(2), *sortOrderOf(t, store, a.ID))
			require.Nil(t, sortOrderOf(t, store, c.ID))
			require.Nil(t, sortOrderOf(t, store, d.ID))

			// It joins a transaction the caller opened, and leaves with it.
			boom := errors.New("boom")
			err := store.WithTx(ctx, func(ctx context.Context) error {
				if err := store.SetStorageOrder(ctx, []int64{d.ID}, []int64{a.ID, b.ID}); err != nil {
					return err
				}
				return boom
			})
			require.ErrorIs(t, err, boom)
			require.Equal(t, []string{"bravo", "alpha", "charlie", "delta-renamed"}, list(),
				"a rolled-back transaction must take the new order with it")

			// Reset: nothing placed, creation order again.
			require.NoError(t, store.SetStorageOrder(ctx, nil, []int64{a.ID, b.ID, c.ID, d.ID}))
			require.Equal(t, []string{"alpha", "bravo", "charlie", "delta-renamed"}, list())
			for _, id := range []int64{a.ID, b.ID, c.ID, d.ID} {
				require.Nil(t, sortOrderOf(t, store, id))
			}
		})
	}
}
