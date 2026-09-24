package db_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

func engineStorage(t *testing.T, store db.Store, name string) int64 {
	t.Helper()
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: "local", MountPath: "/" + name,
		ConfigJSON: json.RawMessage(`{"root":"/data/` + name + `"}`), Enabled: true,
	})
	require.NoError(t, err)
	return st.ID
}

func engineTrashed(t *testing.T, store db.Store, sid int64, p string) int64 {
	t.Helper()
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: sid, Name: p[1:], Path: p, PathHash: pathkey.Hash(sid, p), Type: model.NodeTypeFile, Size: 1,
	})
	require.NoError(t, err)
	require.NoError(t, store.SoftDeleteNode(context.Background(), n.ID))
	return n.ID
}

func ids(nodes []*model.Node) []int64 {
	out := make([]int64, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

// The trash purge asks for the expired rows of the storages it may purge, in
// SQL. Filtering them afterwards in Go is what made emptying one storage's
// trash re-read the same batch of somebody else's rows forever.
func TestListTrashedExpired_NarrowsToStoragesOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			a := engineStorage(t, store, "alpha")
			b := engineStorage(t, store, "beta")
			na := engineTrashed(t, store, a, "/x.txt")
			nb := engineTrashed(t, store, b, "/y.txt")
			cutoff := time.Now().Add(24 * time.Hour)

			all, err := store.ListTrashedExpired(ctx, cutoff, nil, 0, 500)
			require.NoError(t, err)
			assert.ElementsMatch(t, []int64{na, nb}, ids(all), "nil means every storage")

			onlyA, err := store.ListTrashedExpired(ctx, cutoff, []int64{a}, 0, 500)
			require.NoError(t, err)
			assert.Equal(t, []int64{na}, ids(onlyA))

			both, err := store.ListTrashedExpired(ctx, cutoff, []int64{a, b}, 0, 500)
			require.NoError(t, err)
			assert.ElementsMatch(t, []int64{na, nb}, ids(both))

			none, err := store.ListTrashedExpired(ctx, cutoff, []int64{}, 0, 500)
			require.NoError(t, err)
			assert.Empty(t, none, "an empty scope reaches no storage — never every storage")
		})
	}
}
