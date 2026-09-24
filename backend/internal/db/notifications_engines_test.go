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
