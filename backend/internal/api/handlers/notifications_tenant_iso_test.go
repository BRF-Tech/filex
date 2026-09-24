package handlers_test

// Worker-emitted notifications across the tenant boundary.
//
// A notification row carries `user_id`, and the store's list predicate is
//
//	(user_id IS NULL OR user_id = ?)
//
// Everything a WORKER emits has `user_id = NULL`, because a worker has no
// request and therefore no user: the antivirus scanner
// (internal/queue/antivirus_scan.go), the replica failure recorder and the
// replica reconciler all send that way. So every one of those rows is visible
// to every user of every tenant through the plain-user `GET /api/notifications`
// — and their payloads are the infected file's full path and, for the replica
// status report, a `failed_paths` dump.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/notify"
)

// emitWorkerAV reproduces exactly what antivirus_scan.go sends: no UserID, a
// NodeRef naming the storage, and the infected path in the body.
func (f *mtFix) emitWorkerAV(t *testing.T, storageID int64, path string) {
	t.Helper()
	_, err := f.Notif.Send(context.Background(), notify.Event{
		Event:    notify.EventFileInfected,
		Severity: notify.SeverityWarning,
		Title:    "Infected file detected",
		Body:     path + ": Eicar-Test-Signature",
		Meta:     map[string]any{"signature": "Eicar-Test-Signature", "quarantined": true},
		Node:     &notify.NodeRef{StorageID: storageID, Path: path, Name: "virus.exe"},
	})
	require.NoError(t, err)
}

// emitWorkerReplica reproduces internal/replica/reconcile.go's status report:
// no UserID and — the part that matters — NO node reference at all. Its meta is
// a bare `failed_paths` dump.
func (f *mtFix) emitWorkerReplica(t *testing.T, failed string) {
	t.Helper()
	_, err := f.Notif.Send(context.Background(), notify.Event{
		Event:    notify.EventReplicaStatusReport,
		Severity: notify.SeverityInfo,
		Title:    "Replica status report",
		Body:     "1 file still failing",
		Meta:     map[string]any{"failed_count": 1, "failed_paths": []string{failed}},
	})
	require.NoError(t, err)
}

// TestNotifications_WorkerBroadcastsDoNotCrossTenants
//
// RED PROOF (unfixed code): an alpha member's `GET /api/notifications`
// answered `200 OK` with every worker row on the instance:
//
//	{"items":[
//	  {"id":3,"event":"replica.status_report","severity":"info",
//	   "title":"Replica status report","body":"1 file still failing",
//	   "meta":{"failed_count":1,"failed_paths":["/bravo/muhasebe/2026-yevmiye.xlsx"]}},
//	  {"id":2,"event":"file.infected","severity":"warning",
//	   "title":"Infected file detected",
//	   "body":"/bravo-gizli/virus.exe: Eicar-Test-Signature",
//	   "meta":{"node":{"storage_id":2,"path":"/bravo-gizli/virus.exe","name":"virus.exe"},…}},
//	  {"id":1,…alpha's own…}],
//	 "total":3,…}
//
// — another customer's directory layout, twice over.
func TestNotifications_WorkerBroadcastsDoNotCrossTenants(t *testing.T) {
	f := newMTFix(t, true)
	f.emitWorkerAV(t, f.StA.ID, "/alpha-gizli/virus.exe")
	f.emitWorkerAV(t, f.StB.ID, "/bravo-gizli/virus.exe")
	f.emitWorkerReplica(t, "/bravo/muhasebe/2026-yevmiye.xlsx")

	status, body := mtGet(t, f.A, f.URL+"/api/notifications")
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, body, "/bravo-gizli/virus.exe",
		"a worker row about another tenant's storage must not be listed: %s", body)
	require.NotContains(t, body, "2026-yevmiye.xlsx",
		"and neither must one that cannot be attributed to any storage: %s", body)
	require.Contains(t, body, "/alpha-gizli/virus.exe",
		"the tenant still gets the alerts about its OWN files: %s", body)
	require.Contains(t, body, `"total":1`, "the count must describe what was returned: %s", body)
}

// TestNotifications_UnreadCountMatchesTheList — the badge and the list must
// agree. A count that still includes the hidden rows is both a (small) leak of
// how much is happening elsewhere on the platform and a permanently unclearable
// badge: the user cannot mark read what they cannot see.
func TestNotifications_UnreadCountMatchesTheList(t *testing.T) {
	f := newMTFix(t, true)
	f.emitWorkerAV(t, f.StA.ID, "/alpha-gizli/virus.exe")
	f.emitWorkerAV(t, f.StB.ID, "/bravo-gizli/virus.exe")
	f.emitWorkerReplica(t, "/bravo/muhasebe/2026-yevmiye.xlsx")

	status, body := mtGet(t, f.A, f.URL+"/api/notifications/unread-count")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"count":1`, "%s", body)
}

// TestNotifications_SupertenantStillSeesEveryWorkerRow — these are the
// platform operator's alerts. An infected file and a failing replica are
// exactly what they are on call for.
func TestNotifications_SupertenantStillSeesEveryWorkerRow(t *testing.T) {
	f := newMTFix(t, true)
	f.emitWorkerAV(t, f.StB.ID, "/bravo-gizli/virus.exe")
	f.emitWorkerReplica(t, "/bravo/muhasebe/2026-yevmiye.xlsx")

	status, body := mtGet(t, f.Super, f.URL+"/api/notifications")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "/bravo-gizli/virus.exe")
	require.Contains(t, body, "2026-yevmiye.xlsx")
}

// TestNotifications_TenantAdminStaysInsideItsTenant — being an admin buys a
// tenant admin every file of THEIR tenant (admins bypass RBAC), and nothing of
// anybody else's. The admin bypass sits right beside the tenant check, so the
// order of the two is load-bearing: the admin short-cut placed before the
// tenant check, or an unconfined-admin test that forgets the tenant, hands
// every tenant admin every other tenant's alerts.
func TestNotifications_TenantAdminStaysInsideItsTenant(t *testing.T) {
	f := newMTFix(t, true)
	f.emitWorkerAV(t, f.StA.ID, "/alpha-gizli/virus.exe")
	f.emitWorkerAV(t, f.StB.ID, "/bravo-gizli/virus.exe")
	f.emitWorkerReplica(t, "/bravo/muhasebe/2026-yevmiye.xlsx")

	status, body := mtGet(t, f.AdminA, f.URL+"/api/notifications")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "/alpha-gizli/virus.exe", "%s", body)
	require.NotContains(t, body, "/bravo-gizli/virus.exe", "%s", body)
	require.NotContains(t, body, "2026-yevmiye.xlsx", "%s", body)

	status, body = mtGet(t, f.AdminA, f.URL+"/api/notifications/unread-count")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"count":1`, "%s", body)
}

// TestNotifications_TenantRuleIsInertOnASingleTenantInstall — mode off: the
// TENANT rule is inert, so a worker alert about a storage the member can see
// (RBAC off: all of it) stays in the bell. Passes on `main`.
//
// ⚠ The replica report is in no plain member's bell, and that is the per-user
// rule, not the tenant one: a broadcast that names no storage cannot be checked
// against anybody's grants, so it goes to admins only
// (TestNotifications_OperatorEventsAreForAdmins). The count below leaves it out
// for that reason.
func TestNotifications_TenantRuleIsInertOnASingleTenantInstall(t *testing.T) {
	f := newMTFix(t, false)
	f.emitWorkerAV(t, f.StB.ID, "/bravo-gizli/virus.exe")
	f.emitWorkerReplica(t, "/bravo/muhasebe/2026-yevmiye.xlsx")

	status, body := mtGet(t, f.A, f.URL+"/api/notifications")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "/bravo-gizli/virus.exe", "%s", body)

	status, body = mtGet(t, f.A, f.URL+"/api/notifications/unread-count")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"count":1`, "%s", body)
}
