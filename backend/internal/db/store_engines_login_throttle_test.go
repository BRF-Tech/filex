package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestLoginThrottleOnEveryEngine walks the login_throttle table (migration
// 00072) on every engine: a counter is created on first sight, overwritten on
// the next save (one row per scope+subject, never two), listed by scope and by
// "locked right now", deleted, and pruned once it has been quiet long enough.
// Written ONCE for every engine (db.LoginThrottleSQL), so measured once for
// every engine; the timestamps are written by the store, so each engine's
// spelling of them is measured too.
func TestLoginThrottleOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)

			got, err := store.GetLoginThrottle(ctx, model.LoginThrottleAccount, "ada@example.com")
			require.NoError(t, err)
			require.Nil(t, got, "nothing counted yet: (nil, nil), not an error")

			lock := now.Add(4 * time.Minute)
			in := &model.LoginThrottle{
				Scope: model.LoginThrottleAccount, Subject: "müşteri@örnek.com", Fails: 3, LockLevel: 1,
				WindowStart: now.Add(-time.Minute), LockedUntil: &lock, LastFailAt: now,
				LastIP: "2001:db8::7", LastProtocol: "web",
			}
			require.NoError(t, store.SaveLoginThrottle(ctx, in))

			got, err = store.GetLoginThrottle(ctx, model.LoginThrottleAccount, "müşteri@örnek.com")
			require.NoError(t, err)
			require.NotNil(t, got, "a Turkish identifier is stored and found byte for byte")
			require.Equal(t, 3, got.Fails)
			require.Equal(t, 1, got.LockLevel)
			require.Equal(t, "2001:db8::7", got.LastIP)
			require.Equal(t, "web", got.LastProtocol)
			require.NotNil(t, got.LockedUntil)
			require.WithinDuration(t, lock, *got.LockedUntil, time.Second)
			require.WithinDuration(t, now, got.LastFailAt, time.Second)
			require.True(t, got.Locked(now))
			require.False(t, got.Locked(now.Add(5*time.Minute)))

			// Saving again is an overwrite, not a second row; a cleared lock reads back NULL.
			in.Fails, in.LockedUntil, in.LockLevel = 0, nil, 0
			require.NoError(t, store.SaveLoginThrottle(ctx, in))
			require.NoError(t, store.SaveLoginThrottle(ctx, in), "an unchanged save is not an error either (MySQL reports 0 changed rows)")
			got, err = store.GetLoginThrottle(ctx, model.LoginThrottleAccount, "müşteri@örnek.com")
			require.NoError(t, err)
			require.Equal(t, 0, got.Fails)
			require.Nil(t, got.LockedUntil)
			all, err := store.ListLoginThrottles(ctx, "", nil, 0)
			require.NoError(t, err)
			require.Len(t, all, 1)

			// The same subject under another scope is another counter.
			ipLock := now.Add(10 * time.Minute)
			require.NoError(t, store.SaveLoginThrottle(ctx, &model.LoginThrottle{
				Scope: model.LoginThrottleIP, Subject: "müşteri@örnek.com", Fails: 10, LockLevel: 2, LockedUntil: &ipLock,
				WindowStart: now, LastFailAt: now.Add(time.Second),
			}))
			require.NoError(t, store.SaveLoginThrottle(ctx, &model.LoginThrottle{
				Scope: model.LoginThrottleIP, Subject: "203.0.113.9", Fails: 1, WindowStart: now, LastFailAt: now.Add(-2 * time.Hour),
			}))

			all, err = store.ListLoginThrottles(ctx, "", nil, 0)
			require.NoError(t, err)
			require.Len(t, all, 3)
			require.Equal(t, model.LoginThrottleIP, all[0].Scope, "most recently active first")

			ips, err := store.ListLoginThrottles(ctx, model.LoginThrottleIP, nil, 0)
			require.NoError(t, err)
			require.Len(t, ips, 2)

			locked, err := store.ListLoginThrottles(ctx, "", &now, 0)
			require.NoError(t, err)
			require.Len(t, locked, 1, "only the row locked right now")
			require.Equal(t, 2, locked[0].LockLevel)

			limited, err := store.ListLoginThrottles(ctx, "", nil, 1)
			require.NoError(t, err)
			require.Len(t, limited, 1)

			// What the limiter loads into memory at start: exactly what a prune
			// at that instant would keep, newest first.
			loaded, err := store.LoadLoginThrottles(ctx, now.Add(-time.Hour), 0)
			require.NoError(t, err)
			require.Len(t, loaded, 2, "the counter quiet for two hours is not loaded")
			require.Equal(t, model.LoginThrottleIP, loaded[0].Scope, "most recently active first")
			require.Equal(t, "müşteri@örnek.com", loaded[1].Subject)
			loaded, err = store.LoadLoginThrottles(ctx, now.Add(5*time.Minute), 0)
			require.NoError(t, err)
			require.Len(t, loaded, 1, "a lock that runs past the instant is loaded even with no recent failure")
			require.NotNil(t, loaded[0].LockedUntil)
			loaded, err = store.LoadLoginThrottles(ctx, now.Add(-3*time.Hour), 1)
			require.NoError(t, err)
			require.Len(t, loaded, 1, "the limit is the size of the memory")

			// Prune: the idle unlocked rows go, the locked one stays.
			n, err := store.PruneLoginThrottles(ctx, now.Add(-time.Hour))
			require.NoError(t, err)
			require.EqualValues(t, 1, n, "the 203.0.113.9 counter had been quiet for two hours")
			n, err = store.PruneLoginThrottles(ctx, now.Add(5*time.Minute))
			require.NoError(t, err)
			require.EqualValues(t, 1, n, "the account row: quiet, no lock")
			left, err := store.ListLoginThrottles(ctx, "", nil, 0)
			require.NoError(t, err)
			require.Len(t, left, 1, "a lock still running is never pruned")

			ok, err := store.DeleteLoginThrottle(ctx, model.LoginThrottleIP, "müşteri@örnek.com")
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = store.DeleteLoginThrottle(ctx, model.LoginThrottleIP, "müşteri@örnek.com")
			require.NoError(t, err)
			require.False(t, ok)
		})
	}
}
