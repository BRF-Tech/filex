package notify_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// everyEvent is every kind event.go declares. ⚠ A new constant belongs here
// too: the test below is only as complete as this list.
var everyEvent = []notify.EventType{
	notify.EventReplicaFail, notify.EventReplicaFailSpike, notify.EventReplicaReconcileDone,
	notify.EventReplicaStatusReport, notify.EventPrimaryReadFail, notify.EventQuotaNearFull,
	notify.EventQuotaFull, notify.EventQueueStuck, notify.EventAuthFailSpike, notify.EventDiskFull,
	notify.EventUpdateAvailable, notify.EventUpdateApplied,
	notify.EventFileUploaded, notify.EventFileUpdated, notify.EventFileUploadFailed,
	notify.EventFileDeleted, notify.EventFileMoved, notify.EventFileTrashed,
	notify.EventShareCreated, notify.EventDropReceived, notify.EventFileInfected,
	notify.EventCommentAdded, notify.EventE2EEscrowUsed, notify.EventPluginNotice,
	notify.EventAdminTest,
}

// Bell.Admits is the Go twin of the SQL a bell reads with: "mark read" judges
// one row by Admits, the list judges a page by the store's filter, and the two
// must never disagree — a row a member can mark read but not see (or see but
// not mark) is exactly the kind of door PR #43 closed.
func TestBell_AdmitsIsTheTwinOfTheStoreFilter(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	reader, err := store.CreateUser(ctx, "okur@example.test", "x", model.RoleUser, "tr", "UTC")
	require.NoError(t, err)
	svc := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
	defer svc.Stop()
	for _, ev := range everyEvent {
		_, err := svc.Send(ctx, notify.Event{Event: ev, Severity: notify.SeverityInfo, Title: string(ev)})
		require.NoError(t, err)
	}

	for _, bell := range []notify.Bell{notify.AdminBell, notify.TenantAdminBell, notify.MemberBell} {
		rows, total, err := svc.List(ctx, &reader.ID, bell, false, 100, 0)
		require.NoError(t, err)
		var fromSQL, fromGo []string
		for _, r := range rows {
			fromSQL = append(fromSQL, r.Event)
		}
		for _, ev := range everyEvent {
			if bell.Admits(string(ev)) {
				fromGo = append(fromGo, string(ev))
			}
		}
		sort.Strings(fromSQL)
		sort.Strings(fromGo)
		require.Equal(t, fromGo, fromSQL, "bell %d: Admits and the store disagree", bell)
		require.EqualValues(t, len(rows), total)
	}
}

// The admin history's Scope column is the bells' rule (BroadcastAudience), and
// a member's bell carries the operator alarms at NO address (v0.43.0's rule is
// by event, PR #42's by broadcast kind — merged, both hold).
func TestBell_OperatorAlarmsStayOutOfAMembersBellAtAnyAddress(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	reader, err := store.CreateUser(ctx, "okur@example.test", "x", model.RoleUser, "tr", "UTC")
	require.NoError(t, err)
	svc := notify.New(store, notify.Config{RetryBackoffs: []time.Duration{}})
	defer svc.Stop()
	_, err = svc.Send(ctx, notify.Event{Event: notify.EventQuotaNearFull, Severity: notify.SeverityWarning, UserID: &reader.ID})
	require.NoError(t, err)
	_, err = svc.Send(ctx, notify.Event{Event: notify.EventShareCreated, Severity: notify.SeverityInfo, UserID: &reader.ID})
	require.NoError(t, err)

	rows, total, err := svc.List(ctx, &reader.ID, notify.MemberBell, false, 50, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, string(notify.EventShareCreated), rows[0].Event)
	require.EqualValues(t, 1, total)
	n, err := svc.UnreadCount(ctx, &reader.ID, notify.MemberBell)
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "the badge agrees")
}
