package notify_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// An operator alarm is an administrator's to read. Measured in the release
// candidate sweep (2026-09-21): a plain user's bell read "filex v0.42.2
// yayınlandı — Bu sunucu 0.1.0-dev sürümünde çalışıyor", because the update
// notice is a broadcast and every broadcast reached everybody.
func TestOperatorAlarmsReachOnlyAdministrators(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	admin, err := store.CreateUser(ctx, "admin@example.test", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	user, err := store.CreateUser(ctx, "ayse@example.test", "x", model.RoleUser, "tr", "UTC")
	require.NoError(t, err)

	svc := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
	defer svc.Stop()
	for _, e := range []notify.Event{
		{Event: notify.EventUpdateAvailable, Severity: notify.SeverityInfo, Title: "filex 0.42.2 available",
			Meta: map[string]any{"version": "0.42.2", "current": "0.1.0-dev"}},
		{Event: notify.EventReplicaFail, Severity: notify.SeverityError, Meta: map[string]any{"path": "a.txt"}},
		// A broadcast that IS for everybody — the admin panel's test, which a
		// non-admin reading their own bell relies on (e2e 109).
		{Event: "admin_test", Severity: notify.SeverityInfo, Title: "test"},
	} {
		_, err := svc.Send(ctx, e)
		require.NoError(t, err)
	}

	events := func(uid *int64, bell notify.Bell) []string {
		rows, total, err := svc.List(ctx, uid, bell, false, 50, 0)
		require.NoError(t, err)
		assert.EqualValues(t, len(rows), total, "the total counts what is shown")
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Event)
		}
		return out
	}
	assert.ElementsMatch(t, []string{"admin_test"}, events(&user.ID, notify.MemberBell),
		"a plain user's bell must carry no operator alarm")
	assert.ElementsMatch(t, []string{"update_available", "replica_fail", "admin_test"}, events(&admin.ID, notify.AdminBell))
	assert.ElementsMatch(t, []string{"update_available", "replica_fail", "admin_test"}, events(nil, notify.AdminBell),
		"the admin audit list is unchanged")

	n, err := svc.UnreadCount(ctx, &user.ID, notify.MemberBell)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n, "the badge agrees with the bell")
	n, err = svc.UnreadCount(ctx, &admin.ID, notify.AdminBell)
	require.NoError(t, err)
	assert.EqualValues(t, 3, n)
}

// The admin list names the person a row belongs to. Its Scope column printed
// `user #2` — a database id is not who somebody is (release-candidate sweep,
// 2026-09-21).
func TestAdminListNamesThePerson(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	u, err := store.CreateUser(ctx, "ayse@example.test", "x", model.RoleUser, "tr", "UTC")
	require.NoError(t, err)
	svc := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
	defer svc.Stop()
	_, err = svc.Send(ctx, notify.Event{Event: notify.EventShareCreated, Severity: notify.SeverityInfo, UserID: &u.ID})
	require.NoError(t, err)
	_, err = svc.Send(ctx, notify.Event{Event: "admin_test", Severity: notify.SeverityInfo})
	require.NoError(t, err)

	rows, _, err := svc.List(ctx, nil, notify.AdminBell, false, 10, 0)
	require.NoError(t, err)
	byEvent := map[string]*model.Notification{}
	for _, r := range rows {
		byEvent[r.Event] = r
	}
	require.Contains(t, byEvent, "share.created")
	assert.NotEmpty(t, byEvent["share.created"].UserName)
	assert.NotContains(t, byEvent["share.created"].UserName, "#")
	assert.Empty(t, byEvent["admin_test"].UserName, "a broadcast belongs to nobody")

	// Only the admin list carries it: a person's own bell has no use for it.
	mine, _, err := svc.List(ctx, &u.ID, notify.MemberBell, false, 10, 0)
	require.NoError(t, err)
	for _, r := range mine {
		assert.Empty(t, r.UserName)
	}
}
