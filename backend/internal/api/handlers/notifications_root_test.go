package handlers_test

// A token confined to one folder reads — and marks read — only the notices
// about that folder.
//
// v0.43.0 built a whole ceiling model on "a narrow token is exactly as narrow
// as it says" (token_ceiling.go): a `root:`-scoped token cannot list, read,
// search, share or mint outside its folder. /api/notifications was the door
// left open: the same token read its owner's bell, and the bell names files —
// "maas-2026.xlsx uploaded", an antivirus hit, a move out of the folder — from
// the whole account. Found while integrating PR #42 (Berk Başarır), whose own
// report listed it as a gap. The rule now: a notice about a file outside the
// token's root is invisible to that token (list, badge, mark read, mark all
// read); a notice that names no file is fine.

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// bearer is an http.Client that signs every request with one API token.
type bearer struct{ tok string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}

func tokenClient(tok string) *http.Client { return &http.Client{Transport: bearer{tok}} }

// headerClient adds one header to every request of an existing client.
type withHeader struct {
	next     http.RoundTripper
	key, val string
}

func (h withHeader) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(h.key, h.val)
	return h.next.RoundTrip(r)
}

// seedRootBell gives UserA (a member of alpha, homed in the platform provider on
// a single-tenant install) a bell with notices inside and outside `blog/`.
func (f *mtFix) seedRootBell(t *testing.T, uid int64) {
	t.Helper()
	own := func(ev notify.EventType, path string, extra func(*notify.Event)) {
		e := notify.Event{Event: ev, Body: path,
			Node: &notify.NodeRef{StorageID: f.StA.ID, Path: path, Name: path[len(path)-8:]}, UserID: &uid}
		if extra != nil {
			extra(&e)
		}
		f.sendNotice(t, e)
	}
	own(notify.EventFileUploaded, "/blog/icerde-yazi.md", nil)
	own(notify.EventFileUploaded, "/muhasebe/maas-2026.xlsx", nil)
	// A move INTO the folder from outside: the row names where it came from.
	own(notify.EventFileMoved, "/blog/tasinan.pdf", func(e *notify.Event) {
		e.Body = "moved: tasinan.pdf"
		e.Meta = map[string]any{"from": "/muhasebe/tasinan.pdf", "to": "/blog/tasinan.pdf"}
	})
	// The same folder name on ANOTHER storage is not inside the root.
	f.sendNotice(t, notify.Event{Event: notify.EventFileUploaded, Body: "/blog/arsivde.md", UserID: &uid,
		Node: &notify.NodeRef{StorageID: f.StA2.ID, Path: "/blog/arsivde.md", Name: "arsivde.md"}})
	// An open-with working copy: a name with no path.
	f.sendNotice(t, notify.Event{Event: notify.EventFileUpdated, Body: "", UserID: &uid, Title: "Bütçe Özeti güncellendi",
		Node: &notify.NodeRef{StorageID: f.StA.ID, Path: "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx",
			Name: "a1b2c3d4e5f6-Bütçe Özeti.xlsx"}})
	f.emitWorkerAV(t, f.StA.ID, "/blog/virus-icerde.exe")
	f.emitWorkerAV(t, f.StA.ID, "/muhasebe/virus-disarda.exe")
	// Names no file at all.
	f.sendNotice(t, notify.Event{Event: notify.EventAdminTest, Title: "filex test notification", Body: "plumbing check"})
}

func TestNotifications_AFolderConfinedTokenReadsOnlyItsFolder(t *testing.T) {
	f := newMTFix(t, false)
	f.seedRootBell(t, f.UserA)
	tok := tokenClient(testutil.NewAPIToken(t, f.Store, f.UserA, "read,write,root:"+f.StA.Name+"://blog"))

	status, list := mtGet(t, tok, f.URL+"/api/notifications")
	require.Equal(t, http.StatusOK, status, list)
	for _, inside := range []string{"icerde-yazi.md", "virus-icerde.exe", "filex test notification"} {
		require.Contains(t, list, inside, "a notice inside the folder (or about no file) is the token's: %s", list)
	}
	for _, outside := range []string{"maas-2026", "tasinan", "arsivde", "Bütçe", "virus-disarda"} {
		require.NotContains(t, list, outside, "a folder-confined token read a notice about %s: %s", outside, list)
	}
	require.Contains(t, list, `"total":3`, "%s", list)
	require.Equal(t, 3, f.badge(t, tok), "the badge counts what the token can read")

	// The owner's own session still reads the whole bell.
	_, all := mtGet(t, f.A, f.URL+"/api/notifications")
	for _, n := range []string{"maas-2026", "tasinan", "arsivde", "virus-disarda", "icerde-yazi.md"} {
		require.Contains(t, all, n, "%s", all)
	}
	whole := f.badge(t, f.A)
	require.Equal(t, 8, whole)

	// Marking read what it cannot read changes nothing, and answers the same.
	outside := f.rowID(t, "/muhasebe/maas-2026.xlsx")
	require.Equal(t, http.StatusNoContent, f.post(t, tok, fmt.Sprintf("/api/notifications/%d/read", outside)))
	outBroadcast := f.rowID(t, "/muhasebe/virus-disarda.exe")
	require.Equal(t, http.StatusNoContent, f.post(t, tok, fmt.Sprintf("/api/notifications/%d/read", outBroadcast)))
	require.Equal(t, whole, f.badge(t, f.A), "the token marked a notice it cannot read")

	// Mark all read: the token's folder, and nothing of the rest of the account.
	require.Equal(t, http.StatusNoContent, f.post(t, tok, "/api/notifications/read-all"))
	require.Equal(t, 0, f.badge(t, tok))
	require.Equal(t, whole-3, f.badge(t, f.A), "read-all through a confined token reads only its folder")
	_, unread := mtGet(t, f.A, f.URL+"/api/notifications?unread=true")
	require.Contains(t, unread, "maas-2026", "%s", unread)
	require.Contains(t, unread, "virus-disarda", "%s", unread)
	require.NotContains(t, unread, "icerde-yazi.md", "%s", unread)
}

// An administrator's token confined to a folder is just as narrow: admins read
// every broadcast in an unconfined bell, and none of those bypasses reach past
// the root — an operator alarm naming paths it cannot place included.
func TestNotifications_AnAdminsFolderConfinedTokenIsJustAsNarrow(t *testing.T) {
	f := newMTFix(t, false)
	admin, err := f.Store.GetUserByEmail(context.Background(), "admin@alpha.test")
	require.NoError(t, err)
	f.seedRootBell(t, admin.ID)
	f.emitWorkerReplica(t, "/muhasebe/2026-yevmiye.xlsx")

	tok := tokenClient(testutil.NewAPIToken(t, f.Store, admin.ID, "read,write,root:"+f.StA.Name+"://blog"))
	_, list := mtGet(t, tok, f.URL+"/api/notifications")
	require.Contains(t, list, "icerde-yazi.md", "%s", list)
	require.Contains(t, list, "virus-icerde.exe", "%s", list)
	for _, outside := range []string{"maas-2026", "virus-disarda", "yevmiye", "arsivde"} {
		require.NotContains(t, list, outside, "%s", list)
	}
	require.Equal(t, 3, f.badge(t, tok))

	_, own := mtGet(t, f.AdminA, f.URL+"/api/notifications")
	require.Contains(t, own, "yevmiye", "the administrator's own session keeps the alarm: %s", own)

	// The other doors to the same rows — the admin history and the admin
	// surface's mark-read (the MCP tool) — refuse a folder-confined token
	// outright (auth.TokenMayAdminister), even one that carries `admin`.
	aiTok := tokenClient(testutil.NewAPIToken(t, f.Store, admin.ID, "read,write,admin,root:"+f.StA.Name+"://blog"))
	before := f.badge(t, f.AdminA)
	outside := f.rowID(t, "/muhasebe/maas-2026.xlsx")
	require.Equal(t, http.StatusForbidden, f.post(t, aiTok, fmt.Sprintf("/api/ai/admin/notifications/%d/read", outside)))
	status, _ := mtGet(t, aiTok, f.URL+"/api/ai/admin/notifications")
	require.Equal(t, http.StatusForbidden, status)
	status, _ = mtGet(t, aiTok, f.URL+"/api/admin/notifications")
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, before, f.badge(t, f.AdminA))
}

// The X-Filex-Root header a host app's proxy sets narrows a session the same
// way — the files routes' own rule (confine.Middleware).
func TestNotifications_AnXFilexRootHeaderNarrowsTheBell(t *testing.T) {
	f := newMTFix(t, false)
	f.seedRootBell(t, f.UserA)
	narrowed := *f.A
	next := narrowed.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	narrowed.Transport = withHeader{next: next, key: "X-Filex-Root", val: f.StA.Name + "://blog"}

	_, list := mtGet(t, &narrowed, f.URL+"/api/notifications")
	require.Contains(t, list, "icerde-yazi.md", "%s", list)
	require.NotContains(t, list, "maas-2026", "%s", list)
	require.Equal(t, 3, f.badge(t, &narrowed))

	// A header that tries to widen a token's root is refused outright.
	tok := testutil.NewAPIToken(t, f.Store, f.UserA, "read,root:"+f.StA.Name+"://blog")
	widened := &http.Client{Transport: withHeader{next: bearer{tok}, key: "X-Filex-Root", val: f.StA.Name + "://"}}
	status, _ := mtGet(t, widened, f.URL+"/api/notifications")
	require.Equal(t, http.StatusForbidden, status)
}
