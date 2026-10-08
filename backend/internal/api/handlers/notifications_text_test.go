package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// The bell's words are the SERVER's (internal/notify say.go), said when the
// row is read, in the reader's language - the maintainers' decision,
// 2026-10-08: the browser composes no sentence. GET /api/notifications is
// what the bell, the page's pop-up, the desktop app's toast and an agent
// (notifications_list) read, so the words are decided here once for all.
//
// ⚠ Red on the code before it: the row came back with its emitter's title -
// `file.uploaded`, the wire id, for a write that set none - and every screen
// composed its own sentence from the meta.

type bellItem struct {
	ID    int64           `json:"id"`
	Event string          `json:"event"`
	Title string          `json:"title"`
	Body  string          `json:"body"`
	E2E   json.RawMessage `json:"e2e"`
	Opens *bool           `json:"opens"`
}

// uploadedIn is the one file.uploaded row of a bell page.
func uploadedIn(t *testing.T, items []bellItem) bellItem {
	t.Helper()
	var out []bellItem
	for _, it := range items {
		if it.Event == string(notify.EventFileUploaded) {
			out = append(out, it)
		}
	}
	require.Len(t, out, 1, "%+v", items)
	return out[0]
}

func bellPage(t *testing.T, c *http.Client, url string) []bellItem {
	t.Helper()
	status, body := mtGet(t, c, url)
	require.Equal(t, http.StatusOK, status, body)
	var page struct {
		Items []bellItem `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &page), body)
	return page.Items
}

func TestNotifications_TheBellIsSaidByTheServerInTheReadersLanguage(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	require.NoError(t, f.Store.UpdateUserLocale(ctx, f.UserA, "tr", "UTC"))
	uid := f.UserA
	_, err := f.Notif.Send(ctx, notify.Event{
		Event: notify.EventFileUploaded, Severity: notify.SeverityInfo, Body: "blog/rapor.pdf",
		Node:   &notify.NodeRef{StorageID: f.StA.ID, Path: "blog/rapor.pdf", Name: "rapor.pdf"},
		Target: notify.FileTarget("blog/rapor.pdf"),
		UserID: &uid,
	})
	require.NoError(t, err)
	f.Notif.Wait()

	// The account's language.
	row := uploadedIn(t, bellPage(t, f.A, f.URL+"/api/notifications"))
	require.Equal(t, "Yeni dosya: rapor.pdf", row.Title, "never the wire id the row was written with")
	require.Equal(t, "blog/rapor.pdf", row.Body)
	require.Empty(t, row.E2E, "a row that names no encrypted item says no e2e")
	require.NotNil(t, row.Opens, "the server says whether a click goes somewhere")
	require.True(t, *row.Opens, "a file has somewhere to go")

	// ⚠ #191, translated at the last stop: a language the REQUEST names is
	// not the reader's - the account's is. Red on the third-round code, which
	// said the row in `lang=` (the screen's), so the bell could read English
	// while the same person's push and email read Turkish.
	row = uploadedIn(t, bellPage(t, f.A, f.URL+"/api/notifications?lang=en"))
	require.Equal(t, "Yeni dosya: rapor.pdf", row.Title, "lang= is not the reader's language")

	// The account's language changed: the same row, said again.
	require.NoError(t, f.Store.UpdateUserLocale(ctx, f.UserA, "en", "UTC"))
	row = uploadedIn(t, bellPage(t, f.A, f.URL+"/api/notifications"))
	require.Equal(t, "New file: rapor.pdf", row.Title)

	// The administrators' history is said in its reader's ACCOUNT language
	// too - Turkish here, whatever the request names.
	admin, err := f.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	require.NoError(t, f.Store.UpdateUserLocale(ctx, admin.ID, "tr", "UTC"))
	hist := bellPage(t, f.AdminA, f.URL+"/api/admin/notifications?limit=50&lang=en")
	found := false
	for _, it := range hist {
		if it.Event == string(notify.EventFileUploaded) {
			found = true
			require.Equal(t, "Yeni dosya: rapor.pdf", it.Title)
		}
	}
	require.True(t, found, "the row is in the history")
}

// An item inside an end-to-end encrypted folder: the server says the lock
// word and where it stands; it never prints the ciphertext name.
func TestNotifications_AnEncryptedItemIsTheLockWordAndWhereItStands(t *testing.T) {
	const S = "cnCYVvOrMoH0uQKjxUUeYr9h7KREShFsI3Y"
	f := newMTFix(t, false)
	ctx := context.Background()
	uid := f.UserA
	_, err := f.Notif.Send(ctx, notify.Event{
		Event: notify.EventFileUploaded, Severity: notify.SeverityInfo, Body: "blog/Kasa/" + S,
		Meta:   map[string]any{"e2e_root": f.StA.Name + "://blog/Kasa"},
		Node:   &notify.NodeRef{StorageID: f.StA.ID, Path: "blog/Kasa/" + S, Name: S},
		Target: notify.FileTarget("blog/Kasa/" + S),
		UserID: &uid,
	})
	require.NoError(t, err)
	f.Notif.Wait()

	require.NoError(t, f.Store.UpdateUserLocale(ctx, f.UserA, "en", "UTC"))
	row := uploadedIn(t, bellPage(t, f.A, f.URL+"/api/notifications"))
	require.Equal(t, "New file: 🔒 Encrypted item", row.Title)
	require.Equal(t, "blog/Kasa/🔒 Encrypted item", row.Body)
	require.NotContains(t, row.Title+row.Body, S)
	var e2e struct {
		Title []struct {
			Text string `json:"text"`
			Name *int   `json:"name"`
		} `json:"title"`
		Names []struct {
			Wire, Root, Part, Locked string
		} `json:"names"`
	}
	require.NoError(t, json.Unmarshal(row.E2E, &e2e), string(row.E2E))
	require.Len(t, e2e.Title, 2)
	require.Equal(t, "New file: ", e2e.Title[0].Text)
	require.NotNil(t, e2e.Title[1].Name)
	name := e2e.Names[*e2e.Title[1].Name]
	require.Equal(t, f.StA.Name+"://blog/Kasa/"+S, name.Wire)
	require.Equal(t, f.StA.Name+"://blog/Kasa", name.Root)
	require.Equal(t, "name", name.Part)
	require.Equal(t, "🔒 Encrypted item", name.Locked)
}

// ⚠⚠ SECURITY: a notification is made on the server, by the server's own
// events, and nowhere else. No endpoint of the bell or of its administration
// takes a notification's words - or its event, its addressee - from a client:
// every one of them below is sent a body full of them, and not one word of it
// reaches a row. The administrators' "Send test notification" writes the
// server's own row.
func TestNotifications_NoEndpointTakesItsWordsFromTheClient(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	const evil = "EVIL-CLIENT-WORDS"
	payload := map[string]any{
		"event": "file.uploaded", "severity": "critical", "title": evil, "body": evil, "message": evil, "text": evil,
		"meta":    map[string]any{"title_en": evil, "body_en": evil, "notice": evil},
		"user_id": f.UserB, "users": []int64{f.UserB}, "endpoint": "https://fcm.googleapis.com/" + evil,
		"keys": map[string]any{"p256dh": evil, "auth": evil}, "label": evil, "url": evil, "token": evil,
		"window_minutes": 5, "in_app_enabled": true, "muted_events": []string{}, "id": 1,
	}
	before, _, err := f.Notif.List(ctx, nil, notify.AdminBell, false, 500, 0)
	require.NoError(t, err)
	seen := map[int64]bool{}
	for _, n := range before {
		seen[n.ID] = true
	}

	for _, c := range []struct {
		client *http.Client
		method string
		path   string
	}{
		{f.A, http.MethodPost, "/api/notifications/read-all"},
		{f.A, http.MethodPost, "/api/notifications/1/read"},
		{f.A, http.MethodPatch, "/api/notifications/settings"},
		{f.A, http.MethodPost, "/api/notifications/push/subscriptions"},
		{f.A, http.MethodPost, "/api/notifications/push/forget"},
		{f.A, http.MethodPost, "/api/notifications/push/test"},
		{f.A, http.MethodPost, "/api/notifications"},
		{f.AdminA, http.MethodPost, "/api/admin/notifications/test"},
		{f.AdminA, http.MethodPatch, "/api/admin/notifications/digest"},
		{f.AdminA, http.MethodPost, "/api/admin/notifications/push/rotate"},
		{f.AdminA, http.MethodPost, "/api/admin/notifications"},
	} {
		sessionJSON(t, c.client, c.method, f.URL+c.path, payload)
	}
	f.Notif.Wait()

	after, _, err := f.Notif.List(ctx, nil, notify.AdminBell, false, 500, 0)
	require.NoError(t, err)
	var made []*model.Notification
	for _, n := range after {
		require.NotContains(t, n.Title+n.Body+string(n.MetaJSON), evil, "a client's words reached row %d (%s)", n.ID, n.Event)
		if !seen[n.ID] {
			made = append(made, n)
		}
	}
	require.Len(t, made, 1, "only the server's own test row may be written: %s", rowEvents(made))
	require.Equal(t, string(notify.EventAdminTest), made[0].Event)
	require.Nil(t, made[0].UserID, "a client chose who the row is addressed to")
	require.Equal(t, "filex test notification", notify.Say("en", made[0]).Title, "the test row is the server's own")

	// And what the other tenant's member reads names none of it either.
	_, theirs := mtGet(t, f.B, f.URL+"/api/notifications")
	require.False(t, strings.Contains(theirs, evil), theirs)
}

func rowEvents(rows []*model.Notification) string {
	out := make([]string, 0, len(rows))
	for _, n := range rows {
		out = append(out, n.Event)
	}
	return strings.Join(out, ", ")
}
