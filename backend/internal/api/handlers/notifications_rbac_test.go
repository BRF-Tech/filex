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

	writerID = seedUserIn(t, f.Store, f.ProvA, "writer@alpha.test")
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
		rows, _, err := f.Notif.List(context.Background(), nil, false, 500, 0)
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
		rows, _, err := notif.List(context.Background(), nil, false, 50, 0)
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
