package handlers_test

// The read state of a broadcast, per reader.
//
// A notification row has ONE `read_at`. For a row addressed to a user that is
// that user's read state and nothing else. For a broadcast — a row addressed to
// nobody, which many readers are shown — it was everybody's: the read handlers
// used the bell predicate `(user_id IS NULL OR user_id = ?)`, so one member's
// "mark all read" stamped every broadcast on the instance. That includes the
// ones the member's bell never showed: another tenant's antivirus alert, the
// operator's replica report, an alert about a folder the member has no grant
// on. The admins who were meant to act on those found them already read.
//
// The rule these tests hold: marking something read changes the caller's
// bell and nobody else's, and only for rows the caller's bell shows.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/notify"
)

// badge reads a client's bell badge.
func (f *mtFix) badge(t *testing.T, c *http.Client) int {
	t.Helper()
	status, body := mtGet(t, c, f.URL+"/api/notifications/unread-count")
	require.Equal(t, http.StatusOK, status, "%s", body)
	var out struct {
		Count int `json:"count"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	return out.Count
}

// post sends an empty POST and returns the status.
func (f *mtFix) post(t *testing.T, c *http.Client, path string) int {
	t.Helper()
	resp, err := c.Post(f.URL+path, "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// rowID is the id of the one notification whose body names needle.
func (f *mtFix) rowID(t *testing.T, needle string) int64 {
	t.Helper()
	return f.waitNotification(t, needle).ID
}

// TestNotifications_ReadAllTouchesOnlyWhatTheReaderSees — the report's second
// half. A member presses "mark all read"; every other reader's bell must look
// exactly as it did.
//
// RED PROOF (unfixed code): after writer's read-all the tenant admin's badge
// went 2 → 0, the other tenant's member's 1 → 0 and the operator's 4 → 0.
func TestNotifications_ReadAllTouchesOnlyWhatTheReaderSees(t *testing.T) {
	f := newMTFix(t, true)
	writer, _ := f.rbacAlpha(t)
	f.emitWorkerAV(t, f.StA.ID, "/muhasebe/virus.exe")
	f.emitWorkerAV(t, f.StA.ID, "/blog/ek.exe")
	f.emitWorkerAV(t, f.StB.ID, "/bravo-gizli/virus.exe")
	f.emitWorkerReplica(t, "/bravo/muhasebe/2026-yevmiye.xlsx")

	require.Equal(t, 1, f.badge(t, writer))
	require.Equal(t, 2, f.badge(t, f.AdminA))
	require.Equal(t, 1, f.badge(t, f.B))
	require.Equal(t, 4, f.badge(t, f.Super))

	require.Equal(t, http.StatusNoContent, f.post(t, writer, "/api/notifications/read-all"))

	require.Equal(t, 0, f.badge(t, writer), "the reader's own bell is cleared")
	require.Equal(t, 2, f.badge(t, f.AdminA), "a member cleared the tenant admin's alerts")
	require.Equal(t, 1, f.badge(t, f.B), "a member cleared another tenant's alert")
	require.Equal(t, 4, f.badge(t, f.Super), "a member cleared the operator's alerts")
}

// TestNotifications_BroadcastReadStateIsPerReader — two members who may both
// see an alert each have their own "read". The admin history reports the
// CALLER's read state for a broadcast, so the operator's unread filter follows
// what the operator marked, not what somebody else did.
//
// RED PROOF (unfixed code): after member A marked the alert read, writer's
// badge went 1 → 0 and the alert was marked read in every bell.
func TestNotifications_BroadcastReadStateIsPerReader(t *testing.T) {
	f := newMTFix(t, true)
	writer, _ := f.rbacAlpha(t)
	// alpha-arsiv has RBAC off: every member of alpha can see the file.
	f.emitWorkerAV(t, f.StA2.ID, "/arsiv/virus.exe")
	id := f.rowID(t, "/arsiv/virus.exe")
	require.Equal(t, 1, f.badge(t, f.A))
	require.Equal(t, 1, f.badge(t, writer))

	require.Equal(t, http.StatusNoContent, f.post(t, f.A, fmt.Sprintf("/api/notifications/%d/read", id)))
	require.Equal(t, 0, f.badge(t, f.A))
	require.Equal(t, 1, f.badge(t, writer), "one member's read is not another member's")
	require.Equal(t, 1, f.badge(t, f.AdminA))

	_, list := mtGet(t, writer, f.URL+"/api/notifications?unread=true")
	require.Contains(t, list, "/arsiv/virus.exe", "%s", list)

	_, history := mtGet(t, f.Super, f.URL+"/api/admin/notifications?unread=true")
	require.Contains(t, history, "/arsiv/virus.exe", "unread for the operator until the operator reads it: %s", history)
	require.Equal(t, http.StatusNoContent, f.post(t, f.Super, fmt.Sprintf("/api/notifications/%d/read", id)))
	_, history = mtGet(t, f.Super, f.URL+"/api/admin/notifications?unread=true")
	require.NotContains(t, history, "/arsiv/virus.exe", "%s", history)
	require.Equal(t, 1, f.badge(t, f.AdminA), "the operator's read is the operator's")
}

// TestNotifications_ReadAllCoversEverythingUpToThatMoment — "mark all read"
// reads every broadcast up to the moment it was pressed, for the caller: one
// write, however many rows there are and however many of them the caller's bell
// never showed (those being read for the caller changes nothing anybody sees).
// What arrives afterwards is news again.
//
// RED PROOF (unfixed code): the tenant admin's badge was cleared by writer's
// read-all.
func TestNotifications_ReadAllCoversEverythingUpToThatMoment(t *testing.T) {
	f := newMTFix(t, true)
	writer, writerID := f.rbacAlpha(t)
	f.emitWorkerAV(t, f.StA.ID, "/blog/ek.exe")
	for i := 0; i < 520; i++ {
		f.emitWorkerAV(t, f.StA.ID, fmt.Sprintf("/muhasebe/virus-%03d.exe", i))
	}

	require.Equal(t, http.StatusNoContent, f.post(t, writer, "/api/notifications/read-all"))

	rows, _, err := f.Notif.History(context.Background(), writerID, true, 500, 0)
	require.NoError(t, err)
	require.Empty(t, rows, "everything up to the press is read for writer, the alert behind 520 others included")
	// v0.43.0: the badge of a filtered bell is exact (the reader's own rows
	// counted in SQL, only the broadcasts walked — Service.ListVisible), so it
	// is 521, no longer the old walk's cap of 500.
	require.Equal(t, 521, f.badge(t, f.AdminA), "the admin's badge is untouched")

	f.emitWorkerAV(t, f.StA.ID, "/blog/yeni.exe")
	require.Equal(t, 1, f.badge(t, writer), "a broadcast after the press is news")
	_, list := mtGet(t, writer, f.URL+"/api/notifications?unread=true")
	require.Contains(t, list, "/blog/yeni.exe", "%s", list)
	require.NotContains(t, list, "/blog/ek.exe", "%s", list)
}

// TestNotifications_MarkReadOfARowTheReaderCannotSeeIsANoOp — a row id is a
// number anybody can type. Marking one the caller's bell does not show must
// change nothing, and must answer exactly like marking one it does show (204),
// so the endpoint does not tell anyone which ids exist.
//
// RED PROOF (unfixed code): writer's POST stamped the accounting alert read,
// and the tenant admin's badge went 1 → 0; the other tenant's member did the
// same with it.
func TestNotifications_MarkReadOfARowTheReaderCannotSeeIsANoOp(t *testing.T) {
	f := newMTFix(t, true)
	writer, writerID := f.rbacAlpha(t)
	f.emitWorkerAV(t, f.StA.ID, "/muhasebe/virus.exe")
	id := f.rowID(t, "/muhasebe/virus.exe")
	require.Equal(t, 1, f.badge(t, f.AdminA))

	require.Equal(t, http.StatusNoContent, f.post(t, writer, fmt.Sprintf("/api/notifications/%d/read", id)))
	require.Equal(t, http.StatusNoContent, f.post(t, f.B, fmt.Sprintf("/api/notifications/%d/read", id)))
	require.Equal(t, http.StatusNoContent, f.post(t, writer, "/api/notifications/999999/read"),
		"an id that does not exist answers like one that does")

	require.Equal(t, 1, f.badge(t, f.AdminA), "the alert is still unread for the admin it is for")
	_, history := mtGet(t, f.Super, f.URL+"/api/admin/notifications?unread=true")
	require.Contains(t, history, "/muhasebe/virus.exe", "%s", history)

	rows, _, err := f.Notif.List(context.Background(), nil, notify.AdminBell, true, 50, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the stored row itself was not stamped")
	rows, _, err = f.Notif.History(context.Background(), writerID, true, 50, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1, "nor did writer get a mark of their own on a row their bell does not show")
}
