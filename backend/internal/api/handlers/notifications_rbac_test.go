package handlers_test

// The bell against per-user access control (RBAC), inside ONE tenant.
//
// notifications_tenant_iso_test.go closed the tenant boundary: a worker row
// about another customer's storage no longer reaches your bell. It did that by
// asking "may this TENANT see this storage" — and a tenant is not a person.
// Reported from a live instance: a member of a tenant whose storage has RBAC on
// found, in their bell, the names of files deleted from folders they have no
// grant on. Tens of thousands of such rows were in every member's bell; only a
// handful were inside the reporting member's grants.
//
// Two things put them there, and both are pinned below:
//
//   - The ops worker (queued copy/move/delete, and the commit of every staged
//     upload) has no request user. The queue row DOES carry the person who
//     asked — `pending_ops.actor_id`, which ops.execute puts back on the
//     context with quotastore.WithActor — but writehook.emit only asked
//     auth.UserFrom, found nobody, and wrote the event with `user_id = NULL`.
//   - The bell predicate is `(user_id IS NULL OR user_id = ?)`, so a NULL row
//     is in every member's bell, and the only filter on it was the tenant one.
//
// The rule the fix holds, for every row that is not addressed to the reader:
// a bell never names a file the reader could not see in a listing.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// rbacAlpha turns RBAC on for alpha's storage and sets up two members: UserA
// (member@alpha.test) works in `muhasebe/`, and a second member, `writer`, is
// granted `blog/` and nothing else.
func (f *mtFix) rbacAlpha(t *testing.T) (writer *http.Client, writerID int64) {
	t.Helper()
	ctx := context.Background()
	f.StA.RBACEnabled = true
	require.NoError(t, f.Store.UpdateStorage(ctx, f.StA))

	home := f.ProvA
	if !f.MultiTenant {
		// A single-tenant install homes everybody in the platform provider;
		// an account left in a customer provider is refused at the login
		// (auth.LoginAllowed), see newMTFix.
		super, err := f.Store.GetSupertenant(ctx)
		require.NoError(t, err)
		home = super.ID
	}
	writerID = seedUserIn(t, f.Store, home, "writer@alpha.test")
	for _, g := range []*model.FileGrant{
		{StorageID: f.StA.ID, PathPrefix: "muhasebe", IsDir: true, UserID: f.UserA, Level: model.GrantEditor},
		{StorageID: f.StA.ID, PathPrefix: "blog", IsDir: true, UserID: writerID, Level: model.GrantEditor},
	} {
		_, err := f.Store.CreateFileGrant(ctx, g)
		require.NoError(t, err)
	}
	return mtLogin(t, &httptest.Server{URL: f.URL}, "writer@alpha.test", mtUserPass), writerID
}

// waitNotification polls the admin-global view until a row whose body names
// `needle` has been written. Emission is asynchronous — writehook.emit sends
// from its own goroutine — so a drained queue does not yet mean a written row.
func (f *mtFix) waitNotification(t *testing.T, needle string) *model.Notification {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows, _, err := f.Notif.List(context.Background(), nil, notify.AdminBell, false, 500, 0)
		require.NoError(t, err)
		for _, n := range rows {
			if strings.Contains(n.Body, needle) {
				return n
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no notification naming %q was recorded", needle)
	return nil
}

// TestNotifications_QueuedDeleteReachesOnlyTheActor is the report, end to end:
// a delete from the browser goes through the queue, and the member who cannot
// open the folder must not learn the file's name from their bell.
//
// RED PROOF (unfixed code): writer's `GET /api/notifications` listed
// `/muhasebe/maas-2026.xlsx` (event file.trashed, meta.origin "ops") and the
// badge counted it; the stored row had `user_id = NULL` although the queue row
// named UserA as the actor.
func TestNotifications_QueuedDeleteReachesOnlyTheActor(t *testing.T) {
	f := newMTFix(t, true)
	writer, _ := f.rbacAlpha(t)
	f.seedFile(t, f.StA, f.RootA, "muhasebe/maas-2026.xlsx", "payroll")

	status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/delete", map[string]any{
		"source": []string{"alpha://muhasebe/maas-2026.xlsx"},
	})
	require.Equal(t, http.StatusAccepted, status, "the delete must go through the queue: %v", body)
	f.drainOps(t)
	row := f.waitNotification(t, "maas-2026.xlsx")

	_, list := mtGet(t, writer, f.URL+"/api/notifications")
	require.NotContains(t, list, "maas-2026.xlsx",
		"a member was told the name of a file in a folder they cannot open: %s", list)
	_, count := mtGet(t, writer, f.URL+"/api/notifications/unread-count")
	require.Contains(t, count, `"count":0`, "%s", count)

	require.NotNil(t, row.UserID,
		"the queue knew who asked (pending_ops.actor_id) and the event was still recorded as a broadcast")
	require.Equal(t, f.UserA, *row.UserID)

	_, own := mtGet(t, f.A, f.URL+"/api/notifications")
	require.Contains(t, own, "maas-2026.xlsx",
		"the person who deleted it keeps it in their own bell, exactly as a synchronous delete does: %s", own)
}

// TestNotifications_StagedCommitIsAttributedToTheUploader — the other half of
// the queue: every upload above the staging threshold is finished by the ops
// worker (OpUploadCommit), so its file.uploaded event was written with
// `user_id = NULL` too — one broadcast per large upload.
//
// RED PROOF (unfixed code): the row for `rapor.pdf` had `user_id = NULL`.
func TestNotifications_StagedCommitIsAttributedToTheUploader(t *testing.T) {
	var notif notify.Service
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		notif = notify.New(d.Store, notify.Config{})
		d.Notify = notif
	})
	t.Cleanup(notif.Stop)

	src := randomBytes(5000)
	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "rapor.pdf", "size": len(src), "chunk_size": 8192,
		"hash": "sha256:" + sha256Hex(src),
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id, _ := begun["id"].(string)
	code, put := f.putChunk(t, id, 0, int64(len(src)), int64(len(src)), src)
	require.Equal(t, http.StatusOK, code, "%v", put)
	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", committed)
	require.Equal(t, "ok", f.waitForOp(t, num(committed["op_id"])))

	var row *model.Notification
	deadline := time.Now().Add(5 * time.Second)
	for row == nil && time.Now().Before(deadline) {
		rows, _, err := notif.List(context.Background(), nil, notify.AdminBell, false, 50, 0)
		require.NoError(t, err)
		for _, n := range rows {
			if n.Event == string(notify.EventFileUploaded) && strings.Contains(n.Body, "rapor.pdf") {
				row = n
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NotNil(t, row, "the commit never announced the upload")
	require.NotNil(t, row.UserID, "a staged upload was recorded as a broadcast")
	require.Equal(t, f.userID, *row.UserID)
}

// TestNotifications_WorkerAlertReachesOnlyMembersWhoCanSeeTheFile — a real
// broadcast (the antivirus scanner has no actor to attribute to, by nature) is
// still for the members who can see the file it names, and only for them.
//
// RED PROOF (unfixed code): writer's bell listed `/muhasebe/virus.exe` with a
// badge of 2.
func TestNotifications_WorkerAlertReachesOnlyMembersWhoCanSeeTheFile(t *testing.T) {
	f := newMTFix(t, true)
	writer, _ := f.rbacAlpha(t)
	f.emitWorkerAV(t, f.StA.ID, "/muhasebe/virus.exe")
	f.emitWorkerAV(t, f.StA.ID, "/blog/ek.exe")

	_, list := mtGet(t, writer, f.URL+"/api/notifications")
	require.NotContains(t, list, "/muhasebe/virus.exe", "%s", list)
	require.Contains(t, list, "/blog/ek.exe",
		"an alert about a file they CAN see is still theirs: %s", list)
	_, count := mtGet(t, writer, f.URL+"/api/notifications/unread-count")
	require.Contains(t, count, `"count":1`, "the badge must agree with the list: %s", count)

	_, member := mtGet(t, f.A, f.URL+"/api/notifications")
	require.Contains(t, member, "/muhasebe/virus.exe", "%s", member)
	require.NotContains(t, member, "/blog/ek.exe", "%s", member)

	_, admin := mtGet(t, f.AdminA, f.URL+"/api/notifications")
	require.Contains(t, admin, "/muhasebe/virus.exe", "a tenant admin sees every file of the tenant: %s", admin)
	require.Contains(t, admin, "/blog/ek.exe", "%s", admin)
}

// TestNotifications_LinkNoticesDoNotReachMembers — a drop notice falls back to
// a broadcast when its link has no creator on record (handlers/drop.go), and it
// carries the link: `meta.share.token` is a bearer token that lets whoever
// holds it write into the folder. Being able to SEE a folder — as a viewer, or
// only as a folder one walks through to reach a grant — is not a reason to be
// handed that. Members receive only the broadcast kinds in
// notify.MemberBell; the admins of the tenant still get the notice.
//
// RED PROOF (unfixed code): writer's bell listed the drop notice, token
// included.
func TestNotifications_LinkNoticesDoNotReachMembers(t *testing.T) {
	f := newMTFix(t, true)
	writer, _ := f.rbacAlpha(t)
	_, err := f.Notif.Send(context.Background(), notify.Event{
		Event:    notify.EventDropReceived,
		Severity: notify.SeverityInfo,
		Title:    "New upload",
		Body:     "3 files arrived in gelen",
		Meta:     map[string]any{"folder": "gelen", "count": 3},
		Node:     &notify.NodeRef{StorageID: f.StA.ID, Path: "/blog/gelen", Name: "gelen"},
		Share:    &notify.ShareRef{Token: "drop-bearer-token", Path: "/blog/gelen"},
	})
	require.NoError(t, err)

	_, list := mtGet(t, writer, f.URL+"/api/notifications")
	require.NotContains(t, list, "drop-bearer-token", "%s", list)
	_, count := mtGet(t, writer, f.URL+"/api/notifications/unread-count")
	require.Contains(t, count, `"count":0`, "%s", count)

	_, admin := mtGet(t, f.AdminA, f.URL+"/api/notifications")
	require.Contains(t, admin, "drop-bearer-token", "the tenant's admins still get it: %s", admin)
}

// TestNotifications_LegacyWorkerFileActivityStaysOutOfTheBell — the rows the
// queue wrote before the fix are still in the table (tens of thousands on a
// busy instance). Routine file activity is only ever addressed to the person
// who did it, so such a row is an audit record whose actor was lost, not a
// message for anybody. They must not be listed, and — the part a post-filter
// cannot get right — they must not push the reader's own rows off the page.
//
// RED PROOF (unfixed code): the first page held 50 of the 60 legacy rows and
// not writer's own upload.
func TestNotifications_LegacyWorkerFileActivityStaysOutOfTheBell(t *testing.T) {
	f := newMTFix(t, true)
	writer, writerID := f.rbacAlpha(t)
	ctx := context.Background()

	_, err := f.Notif.Send(ctx, notify.Event{
		Event:    notify.EventFileUploaded,
		Severity: notify.SeverityInfo,
		Body:     "/blog/kendi-yazim.md",
		Node:     &notify.NodeRef{StorageID: f.StA.ID, Path: "/blog/kendi-yazim.md", Name: "kendi-yazim.md"},
		UserID:   &writerID,
	})
	require.NoError(t, err)
	for i := 0; i < 60; i++ {
		// Exactly what the queue wrote: no user, no actor, origin "ops". Half of
		// them are in the one folder writer may see — that does not make them
		// writer's.
		dir := "muhasebe"
		if i%2 == 0 {
			dir = "blog"
		}
		p := fmt.Sprintf("/%s/eski-%02d.xlsx", dir, i)
		_, err := f.Notif.Send(ctx, notify.Event{
			Event:    notify.EventFileTrashed,
			Severity: notify.SeverityInfo,
			Body:     p,
			Meta:     map[string]any{"origin": "ops"},
			Node:     &notify.NodeRef{StorageID: f.StA.ID, Path: p, Name: "eski.xlsx"},
		})
		require.NoError(t, err)
	}

	_, list := mtGet(t, writer, f.URL+"/api/notifications?limit=50")
	require.Contains(t, list, "kendi-yazim.md", "their own row was buried under rows that are not theirs: %s", list)
	require.NotContains(t, list, "eski-", "%s", list)
	_, count := mtGet(t, writer, f.URL+"/api/notifications/unread-count")
	require.Contains(t, count, `"count":1`, "%s", count)

	_, admin := mtGet(t, f.AdminA, f.URL+"/api/notifications")
	require.NotContains(t, admin, "eski-",
		"an admin's bell is a bell too; the audit of everything is the admin notification list: %s", admin)
}

// TestNotifications_OperatorEventsAreForAdmins — a broadcast that cannot be
// placed in a storage (the replica reports carry bare paths from every storage)
// cannot be checked against anybody's grants, so it goes to the people it was
// written for: the schema says it plainly, "user_id IS NULL → admin-visible"
// (migrations/*/00007_notifications.sql). Single-tenant mode, because in
// multi-tenant mode such rows already reach nobody but the supertenant.
//
// RED PROOF (unfixed code): the plain member's bell listed the replica
// report's `failed_paths`.
func TestNotifications_OperatorEventsAreForAdmins(t *testing.T) {
	f := newMTFix(t, false)
	f.emitWorkerReplica(t, "/bravo/muhasebe/2026-yevmiye.xlsx")

	_, member := mtGet(t, f.A, f.URL+"/api/notifications")
	require.NotContains(t, member, "2026-yevmiye.xlsx", "%s", member)
	_, count := mtGet(t, f.A, f.URL+"/api/notifications/unread-count")
	require.Contains(t, count, `"count":0`, "%s", count)

	_, admin := mtGet(t, f.AdminA, f.URL+"/api/notifications")
	require.Contains(t, admin, "2026-yevmiye.xlsx", "the operator keeps the alert: %s", admin)
}

// TestNotifications_OperatorEventsDoNotCrowdAMembersBell — the operator's
// alarms are not only hidden from a member, they are not READ for one: a
// replica report a day for two months is a page of rows a member may not see,
// and filtering them after the page was cut left the member's own row on page
// two behind an empty page one, under a badge of 1.
//
// RED PROOF (the per-row filter alone): `{"items":[],"total":0}` with
// `{"count":1}`.
func TestNotifications_OperatorEventsDoNotCrowdAMembersBell(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	uid := f.UserA
	_, err := f.Notif.Send(ctx, notify.Event{
		Event:    notify.EventFileUploaded,
		Severity: notify.SeverityInfo,
		Body:     "/kendi.txt",
		Node:     &notify.NodeRef{StorageID: f.StA.ID, Path: "/kendi.txt", Name: "kendi.txt"},
		UserID:   &uid,
	})
	require.NoError(t, err)
	for i := 0; i < 60; i++ {
		f.emitWorkerReplica(t, fmt.Sprintf("/bravo/rapor-%02d.xlsx", i))
	}

	_, list := mtGet(t, f.A, f.URL+"/api/notifications?limit=50")
	require.Contains(t, list, "/kendi.txt", "%s", list)
	_, count := mtGet(t, f.A, f.URL+"/api/notifications/unread-count")
	require.Contains(t, count, `"count":1`, "%s", count)
}
