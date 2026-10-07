package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestNotifyDigestOnEveryEngine walks the notification digest's tables
// (migration 00087, db.DigestSQL) on every engine: a person's digest point is
// made once and starts at the newest notification, a window keeps the
// earliest end it was given, the due windows are found by time, the point
// moves by compare-and-set only, a quiet read and a digest read select the
// right rows, the administrator's defaults are replaced whole, and a save of
// the settings that leaves the urgent choices out keeps them. Written once
// for every engine, so measured once for every engine — the timestamps
// included, which SQLite and MySQL compare as text.
func TestNotifyDigestOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)

			u, err := store.CreateUser(ctx, "özet@örnek.test", "x", model.RoleUser, "tr", "UTC")
			require.NoError(t, err)
			uid := u.ID
			insert := func(event string, user *int64) int64 {
				id, err := store.InsertNotification(ctx, &model.NotificationInput{
					Event: event, Severity: "info", Title: event, MetaJSON: []byte(`{}`), UserID: user,
				})
				require.NoError(t, err)
				return id
			}
			before := insert("file.uploaded", &uid)

			// The point is made once, at the newest notification.
			_, _, found, err := store.DigestState(ctx, uid)
			require.NoError(t, err)
			require.False(t, found)
			through, err := store.EnsureDigestState(ctx, uid)
			require.NoError(t, err)
			require.Equal(t, before, through)
			again, err := store.EnsureDigestState(ctx, uid)
			require.NoError(t, err)
			require.Equal(t, through, again, "a second call made a second point")

			// The earliest end wins; exact sets it as given.
			require.NoError(t, store.SetDigestDue(ctx, uid, now.Add(2*time.Minute), false))
			require.NoError(t, store.SetDigestDue(ctx, uid, now.Add(5*time.Minute), false))
			_, due, _, err := store.DigestState(ctx, uid)
			require.NoError(t, err)
			require.NotNil(t, due)
			require.WithinDuration(t, now.Add(2*time.Minute), *due, time.Second)
			require.NoError(t, store.SetDigestDue(ctx, uid, now.Add(time.Minute), false))
			_, due, _, err = store.DigestState(ctx, uid)
			require.NoError(t, err)
			require.WithinDuration(t, now.Add(time.Minute), *due, time.Second)
			require.NoError(t, store.SetDigestDue(ctx, uid, now.Add(3*time.Minute), true))
			_, due, _, err = store.DigestState(ctx, uid)
			require.NoError(t, err)
			require.WithinDuration(t, now.Add(3*time.Minute), *due, time.Second)

			ids, err := store.DueDigests(ctx, now.Add(2*time.Minute), 10)
			require.NoError(t, err)
			require.Empty(t, ids, "a window that ends later was found due")
			ids, err = store.DueDigests(ctx, now.Add(4*time.Minute), 10)
			require.NoError(t, err)
			require.Equal(t, []int64{uid}, ids)

			// Quiet rows: above the point, of a held kind.
			held := insert("file.uploaded", &uid)
			urgent := insert("file.infected", &uid)
			quiet := &model.DigestFilter{After: through, Quiet: []string{"file.uploaded", "comment.added"}}
			f := model.BroadcastFilter{ReaderID: uid, Digest: quiet}
			unread, total, err := store.ListNotifications(ctx, &uid, true, nil, nil, f, 50, 0)
			require.NoError(t, err)
			require.EqualValues(t, 2, total, "the unread read counts a quiet row")
			gotIDs := []int64{}
			for _, n := range unread {
				gotIDs = append(gotIDs, n.ID)
			}
			require.ElementsMatch(t, []int64{before, urgent}, gotIDs)
			n, err := store.UnreadNotificationCount(ctx, &uid, nil, nil, f)
			require.NoError(t, err)
			require.EqualValues(t, 2, n, "the badge counts a quiet row")
			all, total, err := store.ListNotifications(ctx, &uid, false, nil, nil, f, 50, 0)
			require.NoError(t, err)
			require.EqualValues(t, 3, total, "a quiet row left the list")
			require.Len(t, all, 3)
			only := model.BroadcastFilter{ReaderID: uid, Digest: &model.DigestFilter{After: through, Quiet: quiet.Quiet, Only: true, UpTo: held}}
			rows, total, err := store.ListNotifications(ctx, &uid, false, nil, nil, only, 50, 0)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Equal(t, held, rows[0].ID)
			oldest, err := store.OldestQuietOwn(ctx, uid, through, quiet.Quiet)
			require.NoError(t, err)
			require.NotNil(t, oldest, "the window's first row was not found")

			// The point moves by compare-and-set only.
			ok, err := store.AdvanceDigest(ctx, uid, through, held, now)
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = store.AdvanceDigest(ctx, uid, through, held, now)
			require.NoError(t, err)
			require.False(t, ok, "a stale point moved: two digests of one window")
			through2, due, _, err := store.DigestState(ctx, uid)
			require.NoError(t, err)
			require.Equal(t, held, through2)
			require.Nil(t, due, "the window stayed open")
			newest, err := store.NewestNotificationID(ctx)
			require.NoError(t, err)
			require.Equal(t, urgent, newest)

			// Read for the person: only their own unread rows.
			require.NoError(t, store.MarkOwnNotificationsRead(ctx, uid, []int64{held, urgent}))
			row, err := store.GetNotification(ctx, held)
			require.NoError(t, err)
			require.NotNil(t, row.ReadAt)

			// The administrator's defaults.
			p, err := store.GetDigestPolicy(ctx, 7)
			require.NoError(t, err)
			require.Nil(t, p, "nothing saved: (nil, nil)")
			require.NoError(t, store.SaveDigestPolicy(ctx, &model.DigestPolicy{Scope: 7, WindowMinutes: 4, Urgent: []string{"file.infected"}}))
			require.NoError(t, store.SaveDigestPolicy(ctx, &model.DigestPolicy{Scope: 7, WindowMinutes: 6}))
			p, err = store.GetDigestPolicy(ctx, 7)
			require.NoError(t, err)
			require.NotNil(t, p)
			require.Equal(t, 6, p.WindowMinutes)
			require.Nil(t, p.Urgent, "a save without a list left the old one")
			require.NoError(t, store.SaveDigestPolicy(ctx, &model.DigestPolicy{Scope: 0, WindowMinutes: 2, Urgent: []string{}}))
			p, err = store.GetDigestPolicy(ctx, 0)
			require.NoError(t, err)
			require.NotNil(t, p.Urgent, "an empty list (nothing urgent) read back as the built-in one")
			require.Empty(t, p.Urgent)

			// The settings: a save that leaves the urgent choices out keeps them.
			require.NoError(t, store.UpsertNotificationSettings(ctx, &model.NotificationSettings{
				UserID: uid, InAppEnabled: true, MutedEventsRaw: []byte(`[]`), UrgentOverridesRaw: []byte(`{"comment.added":true}`),
			}))
			require.NoError(t, store.UpsertNotificationSettings(ctx, &model.NotificationSettings{
				UserID: uid, InAppEnabled: false, MutedEventsRaw: []byte(`["share.created"]`),
			}))
			st, err := store.GetNotificationSettings(ctx, uid)
			require.NoError(t, err)
			require.False(t, st.InAppEnabled)
			require.Equal(t, map[string]bool{"comment.added": true}, st.UrgentOverrides())

			// Closing a window when none is open is not an error.
			require.NoError(t, store.ClearDigestDue(ctx, uid, now))
		})
	}
}
