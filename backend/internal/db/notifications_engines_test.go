package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestNotificationBellOnEveryEngine pins the per-user read on every engine: the
// reader's own rows plus the broadcasts the BroadcastFilter admits, in both of
// its forms. The PostgreSQL half numbers its placeholders by hand, and a
// numbering slip there is exactly the kind of fault SQLite cannot see — so the
// mute list and LIMIT/OFFSET are exercised AFTER the bell clause, where the
// numbering has to carry on correctly.
func TestNotificationBellOnEveryEngine(t *testing.T) {
	except := model.BroadcastFilter{Except: []string{"file.uploaded", "file.updated", "file.moved", "file.trashed", "file.deleted"}}
	only := model.BroadcastFilter{Only: []string{"file.infected", "file.upload_failed"}}

	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			reader, err := store.CreateUser(ctx, "reader@example.test", "x", model.RoleUser, "en", "UTC")
			require.NoError(t, err)
			other, err := store.CreateUser(ctx, "other@example.test", "x", model.RoleUser, "en", "UTC")
			require.NoError(t, err)

			insert := func(event, body string, userID *int64) {
				t.Helper()
				_, err := store.InsertNotification(ctx, &model.NotificationInput{
					Event: event, Severity: "info", Title: event, Body: body, UserID: userID,
				})
				require.NoError(t, err)
			}
			insert("file.uploaded", "own", &reader.ID)
			insert("file.trashed", "lost actor", nil)
			insert("file.infected", "alert", nil)
			insert("replica_status_report", "operator", nil)
			insert("file.uploaded", "someone else's", &other.ID)

			bodies := func(rows []*model.Notification) []string {
				out := make([]string, 0, len(rows))
				for _, n := range rows {
					out = append(out, n.Body)
				}
				return out
			}
			list := func(muted []string, f model.BroadcastFilter, onlyUnread bool, limit, offset int) ([]string, int64) {
				t.Helper()
				rows, total, err := store.ListNotifications(ctx, &reader.ID, onlyUnread, muted, f, limit, offset)
				require.NoError(t, err)
				return bodies(rows), total
			}

			got, total := list(nil, except, false, 50, 0)
			require.ElementsMatch(t, []string{"own", "alert", "operator"}, got, "Except")
			require.EqualValues(t, 3, total)

			got, total = list(nil, only, false, 50, 0)
			require.ElementsMatch(t, []string{"own", "alert"}, got, "Only")
			require.EqualValues(t, 2, total)

			got, total = list([]string{"file.infected"}, only, false, 50, 0)
			require.Equal(t, []string{"own"}, got, "mute list after the bell clause")
			require.EqualValues(t, 1, total)

			got, total = list([]string{"replica_status_report"}, except, true, 1, 1)
			require.Len(t, got, 1, "LIMIT/OFFSET after the bell clause and the mute list")
			require.EqualValues(t, 2, total)

			got, _ = list(nil, model.BroadcastFilter{}, false, 50, 0)
			require.ElementsMatch(t, []string{"own", "lost actor", "alert", "operator"}, got,
				"the zero filter is the predicate this read always had")

			rows, _, err := store.ListNotifications(ctx, nil, false, nil, only, 50, 0)
			require.NoError(t, err)
			require.Len(t, rows, 5, "the admin-global list is the audit and keeps every row")

			for _, c := range []struct {
				f     model.BroadcastFilter
				muted []string
				want  int64
			}{
				{except, nil, 3},
				{only, nil, 2},
				{only, []string{"file.infected"}, 1},
			} {
				n, err := store.UnreadNotificationCount(ctx, &reader.ID, c.muted, c.f)
				require.NoError(t, err)
				require.EqualValues(t, c.want, n, "%+v muted=%v", c.f, c.muted)
			}
		})
	}
}

// TestNotificationReadStateOnEveryEngine pins per-reader read state for
// broadcasts (migration 00043) on every engine: a reader's marks are theirs
// alone, marking is idempotent, only broadcasts take a per-reader mark, the
// store's own-row stamps never reach a broadcast, a "mark all read" point moves
// forward only and overtakes single marks, a broadcast stamped through the old
// shared column stays read for everyone, and the mute list and OFFSET still
// number correctly after the reader's clauses.
func TestNotificationReadStateOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			r1, err := store.CreateUser(ctx, "r1@example.test", "x", model.RoleUser, "en", "UTC")
			require.NoError(t, err)
			r2, err := store.CreateUser(ctx, "r2@example.test", "x", model.RoleUser, "en", "UTC")
			require.NoError(t, err)

			insert := func(event, body string, userID *int64) int64 {
				t.Helper()
				id, err := store.InsertNotification(ctx, &model.NotificationInput{
					Event: event, Severity: "info", Title: body, Body: body, UserID: userID,
				})
				require.NoError(t, err)
				return id
			}
			own := insert("file.infected", "own", &r1.ID)
			b1 := insert("file.infected", "b1", nil)
			insert("file.infected", "b2", nil)
			legacy := insert("file.infected", "legacy", nil)
			require.NoError(t, store.MarkNotificationRead(ctx, legacy, nil), "the shared stamp, as it was")

			unread := func(reader int64) []string {
				t.Helper()
				rows, total, err := store.ListNotifications(ctx, &reader, true, nil, model.BroadcastFilter{ReaderID: reader}, 50, 0)
				require.NoError(t, err)
				out := make([]string, 0, len(rows))
				for _, n := range rows {
					require.Nil(t, n.ReadAt, "an unread row carries no read_at: %s", n.Body)
					out = append(out, n.Body)
				}
				require.EqualValues(t, len(rows), total)
				n, err := store.UnreadNotificationCount(ctx, &reader, nil, model.BroadcastFilter{ReaderID: reader})
				require.NoError(t, err)
				require.EqualValues(t, len(rows), n, "the count agrees with the list")
				return out
			}
			readAt := func(reader int64, body string) bool {
				t.Helper()
				rows, _, err := store.ListNotifications(ctx, &reader, false, nil, model.BroadcastFilter{ReaderID: reader}, 50, 0)
				require.NoError(t, err)
				for _, n := range rows {
					if n.Body == body {
						return n.ReadAt != nil
					}
				}
				t.Fatalf("%s is not in the bell", body)
				return false
			}
			marks := func() int {
				t.Helper()
				var n int
				require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_reads`).Scan(&n))
				return n
			}

			// Single marks.
			require.NoError(t, store.MarkBroadcastsRead(ctx, r1.ID, []int64{b1, own}))
			require.NoError(t, store.MarkBroadcastsRead(ctx, r1.ID, []int64{b1}), "marking twice is not an error")
			require.Equal(t, 1, marks(), "`own` is not a broadcast and takes no per-reader mark")
			require.ElementsMatch(t, []string{"own", "b2"}, unread(r1.ID))
			require.ElementsMatch(t, []string{"b1", "b2"}, unread(r2.ID), "r1's mark is r1's alone")
			require.True(t, readAt(r1.ID, "b1"))
			require.True(t, readAt(r1.ID, "legacy"), "stamped through the shared column: read for everyone")
			require.True(t, readAt(r2.ID, "legacy"))

			// The mute list and OFFSET after the reader's clauses — the numbering
			// PostgreSQL has to carry by hand.
			rows, total, err := store.ListNotifications(ctx, &r2.ID, true, []string{"replica_status_report"},
				model.BroadcastFilter{ReaderID: r2.ID, Only: []string{"file.infected"}}, 1, 1)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.EqualValues(t, 2, total)

			// The admin-global list reports the CALLER's read state for a broadcast.
			hist := func(reader int64) []string {
				t.Helper()
				rows, _, err := store.ListNotifications(ctx, nil, true, nil, model.BroadcastFilter{ReaderID: reader}, 50, 0)
				require.NoError(t, err)
				out := make([]string, 0, len(rows))
				for _, n := range rows {
					out = append(out, n.Body)
				}
				return out
			}
			require.NotContains(t, hist(r1.ID), "b1")
			require.Contains(t, hist(r2.ID), "b1")
			require.Contains(t, hist(0), "b1", "no reader: the stored column, which nobody stamped")

			// Own-row stamps never reach a broadcast, or somebody else's row.
			require.NoError(t, store.MarkNotificationRead(ctx, b1, &r2.ID))
			require.NoError(t, store.MarkAllNotificationsRead(ctx, &r2.ID))
			require.NoError(t, store.MarkNotificationRead(ctx, own, &r2.ID))
			require.ElementsMatch(t, []string{"own", "b2"}, unread(r1.ID))
			require.ElementsMatch(t, []string{"b1", "b2"}, unread(r2.ID))

			// "Mark all read": everything up to now, for r2 alone, in one write.
			require.NoError(t, store.MarkAllBroadcastsRead(ctx, r2.ID))
			require.Empty(t, unread(r2.ID))
			require.True(t, readAt(r2.ID, "b2"))
			require.ElementsMatch(t, []string{"own", "b2"}, unread(r1.ID), "r2's read-all is r2's alone")

			// A broadcast after the point is news again.
			b3 := insert("file.infected", "b3", nil)
			require.ElementsMatch(t, []string{"b3"}, unread(r2.ID))
			require.ElementsMatch(t, []string{"own", "b2", "b3"}, unread(r1.ID))

			// r1's read-all overtakes r1's single mark on b1, and moves forward only.
			require.NoError(t, store.MarkAllBroadcastsRead(ctx, r1.ID))
			require.Equal(t, 0, marks(), "a read-all drops the reader's marks it has overtaken")
			require.ElementsMatch(t, []string{"own"}, unread(r1.ID), "own rows are stamped by MarkAllNotificationsRead, not here")
			var through int64
			require.NoError(t, sqlDB.QueryRowContext(ctx,
				`SELECT through_id FROM notification_read_through WHERE user_id = `+map[bool]string{true: "$1", false: "?"}[e.name == "postgres"], r1.ID).Scan(&through))
			require.Equal(t, b3, through)
			require.NoError(t, store.MarkAllBroadcastsRead(ctx, r1.ID), "pressing again is not an error")

			require.NoError(t, store.MarkNotificationRead(ctx, own, &r1.ID))
			require.Empty(t, unread(r1.ID))
		})
	}
}
