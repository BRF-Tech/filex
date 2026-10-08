package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestPushOnEveryEngine walks the Web Push tables (task #191, migration
// 00097_push) on every engine: a device is kept by its endpoint hash, saving
// it again keeps its mark, a second account on the same browser takes the row
// over, a device is removed only by its own person, the mark moves by
// compare-and-set, refusals are counted and reset, the VAPID key is made once
// and a rotation forgets every device, and a deleted account takes its devices
// with it. Written once (db.PushSQL), measured once per engine.
//
// ⚠ Red on the code before it: the tables and the Store methods do not exist.
func TestPushOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			me, err := store.CreateUser(ctx, "me@example.com", "hash", "user", "tr", "UTC")
			require.NoError(t, err)
			other, err := store.CreateUser(ctx, "other@example.com", "hash", "user", "en", "UTC")
			require.NoError(t, err)

			phone := &model.PushSubscription{
				UserID: me.ID, Endpoint: "https://fcm.googleapis.com/fcm/send/phone", EndpointHash: "h-phone",
				P256dh: "p1", Auth: "a1", Label: "Chrome on Android", ThroughID: 40,
			}
			saved, err := store.SavePushSubscription(ctx, phone)
			require.NoError(t, err)
			require.NotZero(t, saved.ID)
			require.Equal(t, int64(40), saved.ThroughID)
			require.Equal(t, "Chrome on Android", saved.Label)
			require.False(t, saved.CreatedAt.IsZero())
			require.Nil(t, saved.LastOKAt)

			// The mark moves by compare-and-set: once, from where it is.
			ok, err := store.AdvancePushMark(ctx, saved.ID, 40, 45)
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = store.AdvancePushMark(ctx, saved.ID, 40, 47)
			require.NoError(t, err)
			require.False(t, ok, "a second server pushing the same rows must lose")

			// The same person saving the same browser again: same row, same
			// mark, new keys.
			again, err := store.SavePushSubscription(ctx, &model.PushSubscription{
				UserID: me.ID, Endpoint: phone.Endpoint, EndpointHash: "h-phone", P256dh: "p2", Auth: "a2", Label: "Chrome", ThroughID: 99,
			})
			require.NoError(t, err)
			require.Equal(t, saved.ID, again.ID)
			require.Equal(t, int64(45), again.ThroughID, "re-saving a device does not move its mark")
			require.Equal(t, "p2", again.P256dh)

			laptop, err := store.SavePushSubscription(ctx, &model.PushSubscription{
				UserID: me.ID, Endpoint: "https://updates.push.services.mozilla.com/wpush/v2/x", EndpointHash: "h-laptop",
				P256dh: "p3", Auth: "a3", Label: "Firefox on Windows", ThroughID: 50,
			})
			require.NoError(t, err)
			mine, err := store.ListPushSubscriptions(ctx, me.ID)
			require.NoError(t, err)
			require.Len(t, mine, 2)
			require.Equal(t, saved.ID, mine[0].ID, "oldest first")

			// Refusals are counted, a push taken resets them.
			n, err := store.RecordPushResult(ctx, laptop.ID, false)
			require.NoError(t, err)
			require.Equal(t, 1, n)
			n, err = store.RecordPushResult(ctx, laptop.ID, false)
			require.NoError(t, err)
			require.Equal(t, 2, n)
			n, err = store.RecordPushResult(ctx, laptop.ID, true)
			require.NoError(t, err)
			require.Equal(t, 0, n)
			mine, err = store.ListPushSubscriptions(ctx, me.ID)
			require.NoError(t, err)
			require.NotNil(t, mine[1].LastOKAt, "a push taken is dated")

			// Another account on the same browser takes the row over.
			taken, err := store.SavePushSubscription(ctx, &model.PushSubscription{
				UserID: other.ID, Endpoint: phone.Endpoint, EndpointHash: "h-phone", P256dh: "p4", Auth: "a4", ThroughID: 60,
			})
			require.NoError(t, err)
			require.Equal(t, other.ID, taken.UserID)
			require.Equal(t, int64(60), taken.ThroughID)
			mine, err = store.ListPushSubscriptions(ctx, me.ID)
			require.NoError(t, err)
			require.Len(t, mine, 1, "the first account no longer has the browser another one took")
			who, err := store.ListPushSubscribers(ctx)
			require.NoError(t, err)
			require.ElementsMatch(t, []int64{me.ID, other.ID}, who)

			// A device is removed by its own person only.
			ok, err = store.DeletePushSubscription(ctx, me.ID, taken.ID)
			require.NoError(t, err)
			require.False(t, ok, "somebody else's device is not mine to remove")
			ok, err = store.DeletePushSubscriptionByHash(ctx, me.ID, "h-phone")
			require.NoError(t, err)
			require.False(t, ok)
			ok, err = store.DeletePushSubscriptionByHash(ctx, other.ID, "h-phone")
			require.NoError(t, err)
			require.True(t, ok)
			count, err := store.CountPushSubscriptions(ctx)
			require.NoError(t, err)
			require.EqualValues(t, 1, count)

			// The VAPID key: made once, and a rotation forgets every device.
			k, err := store.GetPushVAPIDKey(ctx)
			require.NoError(t, err)
			require.Nil(t, k)
			require.NoError(t, store.CreatePushVAPIDKey(ctx, &model.PushVAPIDKey{PublicKey: "pub-1", PrivateSealed: "enc:v1:one"}))
			require.Error(t, store.CreatePushVAPIDKey(ctx, &model.PushVAPIDKey{PublicKey: "pub-x", PrivateSealed: "enc:v1:x"}),
				"a second server starting at the same time does not replace the first key")
			k, err = store.GetPushVAPIDKey(ctx)
			require.NoError(t, err)
			require.Equal(t, "pub-1", k.PublicKey)
			require.Equal(t, "enc:v1:one", k.PrivateSealed)
			require.False(t, k.CreatedAt.IsZero())

			var dropped int64
			require.NoError(t, store.WithTx(ctx, func(ctx context.Context) error {
				var err error
				dropped, err = store.ReplacePushVAPIDKey(ctx, &model.PushVAPIDKey{PublicKey: "pub-2", PrivateSealed: "enc:v1:two"})
				return err
			}))
			require.EqualValues(t, 1, dropped)
			k, err = store.GetPushVAPIDKey(ctx)
			require.NoError(t, err)
			require.Equal(t, "pub-2", k.PublicKey)
			count, err = store.CountPushSubscriptions(ctx)
			require.NoError(t, err)
			require.Zero(t, count)

			// A deleted account takes its devices with it.
			_, err = store.SavePushSubscription(ctx, &model.PushSubscription{
				UserID: other.ID, Endpoint: "https://web.push.apple.com/x", EndpointHash: "h-ipad", P256dh: "p5", Auth: "a5",
			})
			require.NoError(t, err)
			require.NoError(t, store.DeleteUser(ctx, other.ID))
			count, err = store.CountPushSubscriptions(ctx)
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}
