package db_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// TestRecentSearchesOnEveryEngine walks the recent_searches table (migration
// 00090, task #168) on every engine: a search is kept at the top of its
// person's list, the same words again move up instead of repeating, the list
// keeps the newest `keep`, one person cannot remove another's row, a list is
// cleared alone, and a deleted account takes its searches with it (ON DELETE
// CASCADE). Written ONCE for every engine (db.RecentSearchSQL), so it is
// measured once for every engine.
func TestRecentSearchesOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			const surface = model.RecentSearchSurfaceAdmin

			me, err := store.CreateUser(ctx, "me@example.com", "hash", "admin", "tr", "UTC")
			require.NoError(t, err)
			other, err := store.CreateUser(ctx, "other@example.com", "hash", "admin", "tr", "UTC")
			require.NoError(t, err)

			queries := func(userID int64) []string {
				t.Helper()
				rows, err := store.ListRecentSearches(ctx, userID, surface, 0)
				require.NoError(t, err)
				out := make([]string, len(rows))
				for i, r := range rows {
					out[i] = r.Query
				}
				return out
			}

			first, err := store.AddRecentSearch(ctx, me.ID, surface, "convert", 20)
			require.NoError(t, err)
			require.Equal(t, "convert", first.Query)
			require.NotZero(t, first.ID)
			require.False(t, first.SearchedAt.IsZero())
			_, err = store.AddRecentSearch(ctx, me.ID, surface, "file:rapor", 20)
			require.NoError(t, err)
			again, err := store.AddRecentSearch(ctx, me.ID, surface, "convert", 20)
			require.NoError(t, err)
			require.Greater(t, again.ID, first.ID, "searched again: a new row at the top")
			require.Equal(t, []string{"convert", "file:rapor"}, queries(me.ID), "newest first, one row per query")

			// Another person's list is their own.
			theirs, err := store.AddRecentSearch(ctx, other.ID, surface, "groups", 20)
			require.NoError(t, err)
			require.Equal(t, []string{"groups"}, queries(other.ID))
			ok, err := store.DeleteRecentSearch(ctx, me.ID, theirs.ID)
			require.NoError(t, err)
			require.False(t, ok, "somebody else's row is not mine to remove")
			require.Equal(t, []string{"groups"}, queries(other.ID))

			ok, err = store.DeleteRecentSearch(ctx, me.ID, again.ID)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, []string{"file:rapor"}, queries(me.ID))

			// The newest `keep`, and a limit on the read.
			for i := 0; i < 7; i++ {
				_, err := store.AddRecentSearch(ctx, me.ID, surface, fmt.Sprintf("q%d", i), 5)
				require.NoError(t, err)
			}
			require.Equal(t, []string{"q6", "q5", "q4", "q3", "q2"}, queries(me.ID))
			rows, err := store.ListRecentSearches(ctx, me.ID, surface, 2)
			require.NoError(t, err)
			require.Len(t, rows, 2)
			require.Equal(t, "q6", rows[0].Query)

			// Another surface is another list.
			_, err = store.AddRecentSearch(ctx, me.ID, "explorer", "notes", 5)
			require.NoError(t, err)
			require.Len(t, queries(me.ID), 5, "the admin list did not grow")

			require.NoError(t, store.ClearRecentSearches(ctx, me.ID, surface))
			require.Empty(t, queries(me.ID))
			require.Equal(t, []string{"groups"}, queries(other.ID), "clearing mine leaves theirs")

			// A deleted account takes its searches with it.
			require.NoError(t, store.DeleteUser(ctx, other.ID))
			require.Empty(t, queries(other.ID))
			count := `SELECT COUNT(*) FROM recent_searches WHERE user_id=?`
			if e.name == "postgres" {
				count = db.DollarPlaceholders(count)
			}
			var left int
			require.NoError(t, sqlDB.QueryRowContext(ctx, count, other.ID).Scan(&left))
			require.Zero(t, left, "ON DELETE CASCADE")
		})
	}
}
