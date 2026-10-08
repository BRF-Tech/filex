package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestVaultLocksOnEveryEngine walks vault_locks and vault_prefs (migration
// 00096, #94) on every engine: a vault has no row until one is inserted, a
// second insert of the same (tenant, vault) loses instead of making a second
// row, a write names the revision it read and loses when another writer came
// first (the compare-and-set two processes on one database race through), the
// same vault id in another tenant is another row, every millisecond column
// comes back exactly as it went in, and a person's idle time is (0 = unset)
// written over in place.
func TestVaultLocksOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			const vid = "c29535e79852477843d6ef1b4a71afef"

			got, err := store.GetVaultLock(ctx, 0, vid)
			require.NoError(t, err)
			require.Nil(t, got, "no row: (nil, nil), not an error")

			row := &model.VaultLock{
				TenantID: 0, VaultID: vid, TokenHash: "aa", HolderUserID: 7, HolderName: "Ayşe",
				HolderClient: "web", HolderLabel: "Firefox, ofis", StorageID: 3, Path: "Kasa/Çay",
				TakenMs: 1_791_000_000_123, LeaseMs: 1_791_000_060_123, ActiveMs: 1_791_000_000_123,
				IdleSeconds: 180,
			}
			ok, err := store.InsertVaultLock(ctx, row)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, int64(1), row.Rev)

			again := *row
			ok, err = store.InsertVaultLock(ctx, &again)
			require.NoError(t, err)
			require.False(t, ok, "a second row for the same vault is refused, not added")

			got, err = store.GetVaultLock(ctx, 0, vid)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, *row, *got, "every column comes back exactly as it went in")

			// Two writers read rev 1; the first wins, the second loses.
			a, b := *got, *got
			a.TokenHash, a.LastGen = "bb", 4
			b.TokenHash = "cc"
			ok, err = store.UpdateVaultLock(ctx, &a, 1)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, int64(2), a.Rev)
			ok, err = store.UpdateVaultLock(ctx, &b, 1)
			require.NoError(t, err)
			require.False(t, ok, "a write from a stale revision must lose")

			got, err = store.GetVaultLock(ctx, 0, vid)
			require.NoError(t, err)
			require.Equal(t, "bb", got.TokenHash)
			require.Equal(t, int64(4), got.LastGen)
			require.Equal(t, int64(2), got.Rev)

			// Ending a lock: the token goes, the ending is kept.
			got.TokenHash, got.EndedTokenHash, got.EndedReason, got.EndedBy, got.EndedMs = "", "bb", "broken", "Gülşen Çelik", 1_791_000_030_999
			ok, err = store.UpdateVaultLock(ctx, got, 2)
			require.NoError(t, err)
			require.True(t, ok)
			ended, err := store.GetVaultLock(ctx, 0, vid)
			require.NoError(t, err)
			require.Equal(t, "", ended.TokenHash)
			require.Equal(t, "broken", ended.EndedReason)
			require.Equal(t, "Gülşen Çelik", ended.EndedBy, "who broke it comes back as written")
			require.Equal(t, int64(1_791_000_030_999), ended.EndedMs)

			// The same vault id in another tenant is another vault's lock.
			other, err := store.GetVaultLock(ctx, 9, vid)
			require.NoError(t, err)
			require.Nil(t, other)
			ok, err = store.InsertVaultLock(ctx, &model.VaultLock{TenantID: 9, VaultID: vid, TokenHash: "dd"})
			require.NoError(t, err)
			require.True(t, ok)

			// The idle time: unset, set, set again (one row).
			n, err := store.GetVaultIdleMinutes(ctx, 7)
			require.NoError(t, err)
			require.Equal(t, 0, n)
			require.NoError(t, store.SetVaultIdleMinutes(ctx, 7, 5))
			require.NoError(t, store.SetVaultIdleMinutes(ctx, 7, 5))
			require.NoError(t, store.SetVaultIdleMinutes(ctx, 7, 10))
			n, err = store.GetVaultIdleMinutes(ctx, 7)
			require.NoError(t, err)
			require.Equal(t, 10, n)
			n, err = store.GetVaultIdleMinutes(ctx, 8)
			require.NoError(t, err)
			require.Equal(t, 0, n, "somebody else's is not mine")
		})
	}
}
