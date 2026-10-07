package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestOfficeSessionsOnEveryEngine walks the office_sessions table (migration
// 00092, #184) on every engine: the first writer's row stands (Add), an
// overwrite is an overwrite (Put, never a second row), a modification time in
// NANOSECONDS comes back exactly as it went in (a timestamp column would have
// rounded it on some engines, and a re-read of an unchanged file would no
// longer match), the flags survive, an expired row is replaced by Add, and the
// prune removes only what expired.
func TestOfficeSessionsOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			now := time.Unix(1_790_000_000, 0)

			got, err := store.GetOfficeSession(ctx, "k1")
			require.NoError(t, err)
			require.Nil(t, got, "no row: (nil, nil), not an error")

			first := &model.OfficeSession{
				DocKey: "k1", NodeID: 7, Size: 1234, MtimeNs: 1_790_000_000_123_456_789,
				Etag: "", ExpiresUnix: now.Add(48 * time.Hour).Unix(),
			}
			stood, err := store.AddOfficeSession(ctx, first, now)
			require.NoError(t, err)
			require.Equal(t, first.MtimeNs, stood.MtimeNs)

			// A second opener of the same key: the first record stands.
			second := *first
			second.Size, second.MtimeNs = 9999, 1
			stood, err = store.AddOfficeSession(ctx, &second, now)
			require.NoError(t, err)
			require.Equal(t, int64(1234), stood.Size, "the first writer's row stands")
			got, err = store.GetOfficeSession(ctx, "k1")
			require.NoError(t, err)
			require.Equal(t, int64(1_790_000_000_123_456_789), got.MtimeNs, "nanoseconds, exactly")
			require.Equal(t, int64(7), got.NodeID)

			// Put overwrites, flags included, and never makes a second row.
			got.Dropped, got.Unknown, got.Etag = true, true, `"abc-2"`
			require.NoError(t, store.PutOfficeSession(ctx, got))
			require.NoError(t, store.PutOfficeSession(ctx, got), "an unchanged save is not an error (MySQL reports 0 changed rows)")
			again, err := store.GetOfficeSession(ctx, "k1")
			require.NoError(t, err)
			require.True(t, again.Dropped)
			require.True(t, again.Unknown)
			require.Equal(t, `"abc-2"`, again.Etag)

			// An expired row does not stand: Add replaces it.
			stale := &model.OfficeSession{DocKey: "k2", NodeID: 8, Size: 1, ExpiresUnix: now.Add(-time.Hour).Unix()}
			require.NoError(t, store.PutOfficeSession(ctx, stale))
			fresh := &model.OfficeSession{DocKey: "k2", NodeID: 8, Size: 2, ExpiresUnix: now.Add(time.Hour).Unix()}
			stood, err = store.AddOfficeSession(ctx, fresh, now)
			require.NoError(t, err)
			require.Equal(t, int64(2), stood.Size)

			// The prune removes what expired, and only that.
			require.NoError(t, store.PutOfficeSession(ctx, &model.OfficeSession{DocKey: "old", NodeID: 9, ExpiresUnix: now.Add(-time.Minute).Unix()}))
			n, err := store.PruneOfficeSessions(ctx, now)
			require.NoError(t, err)
			require.Equal(t, int64(1), n)
			for _, k := range []string{"k1", "k2"} {
				row, err := store.GetOfficeSession(ctx, k)
				require.NoError(t, err)
				require.NotNil(t, row, k+" was not expired")
			}

			// The session's end removes its row.
			require.NoError(t, store.DeleteOfficeSession(ctx, "k1"))
			gone, err := store.GetOfficeSession(ctx, "k1")
			require.NoError(t, err)
			require.Nil(t, gone)
			require.NoError(t, store.DeleteOfficeSession(ctx, "k1"), "deleting nothing is not an error")
		})
	}
}
