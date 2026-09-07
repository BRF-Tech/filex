package notify_test

// Per-user bell preferences: muted_events and in_app_enabled.
//
// Both fields round-tripped through the API from the day they were added and
// were applied by nothing: Send inserted the row, List returned it, the badge
// counted it. A user could mute an event and keep receiving it. These tests
// pin the read path that makes them real, and — just as importantly — pin the
// two things that must NOT change: the row is still recorded (the audit is
// not a preference), and the admin/global view is never filtered.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// bellFixture seeds one user and sends three events at them: two
// replica_fail and one file.infected.
func bellFixture(t *testing.T) (db.Store, notify.Service, int64) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	uid := dbtest.SeedUserWithRole(t, store, "bell@example.com", "correct-horse-battery", "user")

	svc := notify.New(store, notify.Config{HTTPTimeout: time.Second, RetryBackoffs: []time.Duration{}})
	t.Cleanup(svc.Stop)

	for _, ev := range []notify.EventType{
		notify.EventReplicaFail, notify.EventReplicaFail, notify.EventFileInfected,
	} {
		_, err := svc.Send(context.Background(), notify.Event{
			Event:    ev,
			Severity: notify.SeverityWarning,
			Title:    string(ev),
			UserID:   &uid,
		})
		require.NoError(t, err)
	}
	return store, svc, uid
}

func setBellPrefs(t *testing.T, svc notify.Service, uid int64, inApp bool, muted string) {
	t.Helper()
	require.NoError(t, svc.UpsertSettings(context.Background(), &model.NotificationSettings{
		UserID:         uid,
		InAppEnabled:   inApp,
		MutedEventsRaw: []byte(muted),
	}))
}

// TestBell_MutedEventIsHidden is the red-proof for muted_events: before the
// read path existed this returned all three rows and a badge of 3.
func TestBell_MutedEventIsHidden(t *testing.T) {
	_, svc, uid := bellFixture(t)
	ctx := context.Background()

	items, total, err := svc.List(ctx, &uid, false, 50, 0)
	require.NoError(t, err)
	require.Len(t, items, 3, "baseline: nothing muted yet")
	require.EqualValues(t, 3, total)

	setBellPrefs(t, svc, uid, true, `["replica_fail"]`)

	items, total, err = svc.List(ctx, &uid, false, 50, 0)
	require.NoError(t, err)
	require.Len(t, items, 1, "the two replica_fail rows are muted")
	require.Equal(t, string(notify.EventFileInfected), items[0].Event)

	// ⚠ The total must shrink with the page. A total that still says 3 makes
	// the UI render pagination for rows it will never show.
	require.EqualValues(t, 1, total)

	n, err := svc.UnreadCount(ctx, &uid)
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "the badge must agree with the list")
}

// TestBell_MutedRowsAreStillRecorded proves muting gates the READ only: the
// rows survive, and the admin/global view still sees every one of them.
func TestBell_MutedRowsAreStillRecorded(t *testing.T) {
	_, svc, uid := bellFixture(t)
	ctx := context.Background()

	setBellPrefs(t, svc, uid, false, `["replica_fail","file.infected"]`)

	items, total, err := svc.List(ctx, nil, false, 50, 0)
	require.NoError(t, err)
	require.Len(t, items, 3, "admin/global view is never filtered by one user's prefs")
	require.EqualValues(t, 3, total)

	n, err := svc.UnreadCount(ctx, nil)
	require.NoError(t, err)
	require.EqualValues(t, 3, n)
}

// TestBell_InAppDisabledSilencesTheBell is the red-proof for in_app_enabled.
func TestBell_InAppDisabledSilencesTheBell(t *testing.T) {
	_, svc, uid := bellFixture(t)
	ctx := context.Background()

	setBellPrefs(t, svc, uid, false, `[]`)

	items, total, err := svc.List(ctx, &uid, false, 50, 0)
	require.NoError(t, err)
	require.Empty(t, items)
	require.EqualValues(t, 0, total)

	n, err := svc.UnreadCount(ctx, &uid)
	require.NoError(t, err)
	require.EqualValues(t, 0, n)

	// Turning it back on restores the history — nothing was destroyed.
	setBellPrefs(t, svc, uid, true, `[]`)
	items, total, err = svc.List(ctx, &uid, false, 50, 0)
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.EqualValues(t, 3, total)
}

// TestBell_MutedPageIsNotShort pins the reason the filter is in SQL rather
// than applied to the returned page: with limit=2 and one event muted, a page
// must still come back full.
func TestBell_MutedPageIsNotShort(t *testing.T) {
	_, svc, uid := bellFixture(t)
	ctx := context.Background()

	// Two more of the muted event, so a post-filter would eat a whole page.
	for i := 0; i < 2; i++ {
		_, err := svc.Send(ctx, notify.Event{
			Event: notify.EventReplicaFail, Severity: notify.SeverityWarning,
			Title: "noise", UserID: &uid,
		})
		require.NoError(t, err)
	}
	for i := 0; i < 2; i++ {
		_, err := svc.Send(ctx, notify.Event{
			Event: notify.EventFileInfected, Severity: notify.SeverityWarning,
			Title: "kept", UserID: &uid,
		})
		require.NoError(t, err)
	}
	setBellPrefs(t, svc, uid, true, `["replica_fail"]`)

	items, total, err := svc.List(ctx, &uid, false, 2, 0)
	require.NoError(t, err)
	require.Len(t, items, 2, "a filtered page must be full, not short")
	require.EqualValues(t, 3, total, "3 file.infected rows survive the mute")
	for _, it := range items {
		require.Equal(t, string(notify.EventFileInfected), it.Event)
	}
}

// TestBell_UnreadableSettingsFailOpen: a corrupt muted_events column must show
// the user their notifications, not hide all of them.
func TestBell_UnreadableSettingsFailOpen(t *testing.T) {
	_, svc, uid := bellFixture(t)
	ctx := context.Background()

	setBellPrefs(t, svc, uid, true, `not json at all`)

	items, total, err := svc.List(ctx, &uid, false, 50, 0)
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.EqualValues(t, 3, total)
}

// TestMutedList covers the model helper's own edges.
func TestMutedList(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty array", `[]`, nil},
		{"whitespace only entries", `["  ",""]`, nil},
		{"trimmed", `[" replica_fail "]`, []string{"replica_fail"}},
		{"malformed json fails open", `{`, nil},
		{"wrong shape fails open", `{"a":1}`, nil},
		{"two", `["a","b"]`, []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &model.NotificationSettings{MutedEventsRaw: []byte(tc.raw)}
			require.Equal(t, tc.want, s.MutedList())
		})
	}

	require.False(t, (*model.NotificationSettings)(nil).IsMuted("x"))
	s := &model.NotificationSettings{MutedEventsRaw: []byte(`["a"]`)}
	require.True(t, s.IsMuted("a"))
	require.False(t, s.IsMuted("b"))
}
