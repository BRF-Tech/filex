package handlers_test

// PR #42's audience rule and v0.43.0's notifications, as ONE rule.
//
// v0.43.0 sends two broadcasts to everybody on purpose — the admin page's test
// (e2e 109 seeds a non-admin's bell with it) and an app's instance-wide notice
// — and PR #42 (Berk Başarır) let a member read only the two broadcast kinds
// that name a file, and only when the member can see that file. Merged, a
// notice for everybody reaches every member bell unless it names a file, and
// then only the members who can see it. These tests hold the seams: the
// kinds, the file an app's notice names, the name an open-with working copy
// keeps without a path, the pages of a filtered bell, and the admin history's
// Scope column.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/notify"
)

func (f *mtFix) sendNotice(t *testing.T, e notify.Event) {
	t.Helper()
	if e.Severity == "" {
		e.Severity = notify.SeverityInfo
	}
	_, err := f.Notif.Send(context.Background(), e)
	require.NoError(t, err)
}

// appNotice is what wasmplugin's notify_send writes for to_user_id 0: an
// instance-wide notice, naming a file when the app gave it one.
func appNotice(title string, storageID int64, path string) notify.Event {
	e := notify.Event{Event: notify.EventPluginNotice, Title: title,
		Meta: map[string]any{"plugin": "sign", "title_en": title}}
	if path != "" {
		e.Node = &notify.NodeRef{StorageID: storageID, Path: path, Name: path[len("/blog/"):]}
		e.Target = notify.FileTarget(path)
	}
	return e
}

// TestNotifications_NoticesForEverybodyNeverNameAFileTheReaderCannotSee — the
// seam itself, on a single-tenant install with RBAC on.
//
// RED PROOF (merge of #42 as it stood): writer's bell had neither the admin
// test nor the app's notices (members read only file.infected and
// file.upload_failed broadcasts), which is what e2e 109 reads. And an app
// notice naming `muhasebe/` would, once admitted by kind alone, have told
// writer a file name from a folder they cannot open.
func TestNotifications_NoticesForEverybodyNeverNameAFileTheReaderCannotSee(t *testing.T) {
	f := newMTFix(t, false)
	writer, _ := f.rbacAlpha(t)

	f.sendNotice(t, notify.Event{Event: notify.EventAdminTest, Title: "filex test notification"})
	f.sendNotice(t, appNotice("Converter is ready", 0, ""))
	blog := appNotice("Signed: yazi.pdf", f.StA.ID, "/blog/yazi.pdf")
	f.sendNotice(t, blog)
	secret := appNotice("Signed: maas-bordrosu.pdf", f.StA.ID, "/blog/x.pdf")
	secret.Node = &notify.NodeRef{StorageID: f.StA.ID, Path: "/muhasebe/maas-bordrosu.pdf", Name: "maas-bordrosu.pdf"}
	secret.Target = notify.FileTarget("/muhasebe/maas-bordrosu.pdf")
	f.sendNotice(t, secret)

	_, list := mtGet(t, writer, f.URL+"/api/notifications")
	require.Contains(t, list, "filex test notification", "the admin page's test is for everybody (e2e 109): %s", list)
	require.Contains(t, list, "Converter is ready", "an app's notice that names nothing is for everybody: %s", list)
	require.Contains(t, list, "yazi.pdf", "an app's notice about a file the member can see is theirs: %s", list)
	require.NotContains(t, list, "maas-bordrosu", "a member was told a name from a folder they cannot open: %s", list)
	_, count := mtGet(t, writer, f.URL+"/api/notifications/unread-count")
	require.Contains(t, count, `"count":3`, "the badge agrees with the list: %s", count)

	_, member := mtGet(t, f.A, f.URL+"/api/notifications")
	require.Contains(t, member, "maas-bordrosu", "the member who works in muhasebe/ gets it: %s", member)
	_, admin := mtGet(t, f.AdminA, f.URL+"/api/notifications")
	require.Contains(t, admin, "maas-bordrosu", "%s", admin)
}

// TestNotifications_ConfinedReadersGetNoUnplaceableNotice — multi-tenant: a
// notice that names nothing cannot be placed in a tenant, so a confined reader
// is not shown it (the rule v0.43.0 already had for every unplaceable
// broadcast); one that names a file in their tenant still reaches them.
func TestNotifications_ConfinedReadersGetNoUnplaceableNotice(t *testing.T) {
	f := newMTFix(t, true)
	f.sendNotice(t, notify.Event{Event: notify.EventAdminTest, Title: "filex test notification"})
	f.sendNotice(t, appNotice("Signed: yazi.pdf", f.StA.ID, "/blog/yazi.pdf"))

	_, list := mtGet(t, f.A, f.URL+"/api/notifications")
	require.NotContains(t, list, "filex test notification", "%s", list)
	require.Contains(t, list, "yazi.pdf", "%s", list)
	_, other := mtGet(t, f.B, f.URL+"/api/notifications")
	require.NotContains(t, other, "yazi.pdf", "another tenant's member: %s", other)
	_, super := mtGet(t, f.Super, f.URL+"/api/notifications")
	require.Contains(t, super, "filex test notification", "the operator's own bell: %s", super)
}

// TestNotifications_ANameWithoutAPathReachesNoMember — v0.43.0's person view
// keeps an "open with filex" working copy's document NAME and drops its path
// (no path names anything anybody can open). A name with no path cannot be
// checked against a member's grants, so such a broadcast reaches no member:
// "Bütçe Özeti.xlsx is infected" must not be read by every member who can see
// the storage's root. (The antivirus scanner addresses its own to the copy's
// owner — TestAntivirusScan_OpenWithCopyAlertGoesToItsOwner.)
//
// RED PROOF (the merge before this rule): writer's bell showed the name —
// notificationPlace read the missing path as "the storage root", which every
// member with a grant anywhere in it can see.
func TestNotifications_ANameWithoutAPathReachesNoMember(t *testing.T) {
	f := newMTFix(t, false)
	writer, _ := f.rbacAlpha(t)
	f.sendNotice(t, notify.Event{
		Event: notify.EventFileInfected, Severity: notify.SeverityWarning,
		Title: "Infected file detected", Body: "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx: Eicar",
		Node: &notify.NodeRef{StorageID: f.StA.ID, Path: "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx",
			Name: "a1b2c3d4e5f6-Bütçe Özeti.xlsx"},
	})

	for name, c := range map[string]*http.Client{"writer": writer, "member": f.A} {
		_, list := mtGet(t, c, f.URL+"/api/notifications")
		require.NotContains(t, list, "Bütçe", "%s learnt a document's name: %s", name, list)
	}
	_, admin := mtGet(t, f.AdminA, f.URL+"/api/notifications")
	require.Contains(t, admin, "Bütçe Özeti.xlsx", "an administrator still gets the alert: %s", admin)
}

// TestNotifications_AMembersPagesReachEveryRow — the total of a member's bell
// is what they may see, and the pages are cut there.
//
// RED PROOF (PR #42's `total = len(kept)`): the first page answered total 25
// for 30 visible rows, so the full-list screen drew no pager and rows 26-30
// could not be reached (e2e 109's shape); with hidden rows between them the
// second page also came back short.
func TestNotifications_AMembersPagesReachEveryRow(t *testing.T) {
	f := newMTFix(t, false)
	writer, _ := f.rbacAlpha(t)
	for i := 0; i < 30; i++ {
		f.sendNotice(t, notify.Event{Event: notify.EventAdminTest, Title: fmt.Sprintf("test %02d", i)})
		if i%5 == 0 {
			f.emitWorkerAV(t, f.StA.ID, fmt.Sprintf("/muhasebe/gizli-%02d.exe", i))
		}
	}

	seen := map[float64]bool{}
	for _, offset := range []int{0, 25} {
		status, body := mtGet(t, writer, fmt.Sprintf("%s/api/notifications?limit=25&offset=%d", f.URL, offset))
		require.Equal(t, http.StatusOK, status)
		var page struct {
			Items []struct {
				ID float64 `json:"id"`
			} `json:"items"`
			Total int `json:"total"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &page))
		require.Equal(t, 30, page.Total, "offset %d: %s", offset, body)
		require.NotContains(t, body, "gizli-", "%s", body)
		for _, it := range page.Items {
			require.False(t, seen[it.ID], "row %v came back on two pages", it.ID)
			seen[it.ID] = true
		}
	}
	require.Len(t, seen, 30, "every row the member may see is on some page")
	_, count := mtGet(t, writer, f.URL+"/api/notifications/unread-count")
	require.Contains(t, count, `"count":30`, "%s", count)
}

// TestNotifications_AdminHistorySaysWhoEachBroadcastReaches — the Scope column
// is the bells' own rule, not a second list: v0.43.0 marked only the operator
// alarms "Administrators"; after PR #42 a drop notice is administrators' too,
// an antivirus hit reaches the members who can see the file, and a legacy
// broadcast of routine file activity reaches no bell at all.
func TestNotifications_AdminHistorySaysWhoEachBroadcastReaches(t *testing.T) {
	f := newMTFix(t, false)
	f.sendNotice(t, notify.Event{Event: notify.EventAdminTest, Title: "filex test notification"})
	f.sendNotice(t, appNotice("Converter is ready", 0, ""))
	f.sendNotice(t, appNotice("Signed: yazi.pdf", f.StA.ID, "/blog/yazi.pdf"))
	f.emitWorkerAV(t, f.StA.ID, "/blog/virus.exe")
	f.emitWorkerReplica(t, "/alpha/x.txt")
	f.sendNotice(t, notify.Event{Event: notify.EventDropReceived, Title: "New upload",
		Node: &notify.NodeRef{StorageID: f.StA.ID, Path: "/blog/gelen", Name: "gelen"}})
	f.sendNotice(t, notify.Event{Event: notify.EventFileUploaded, Body: "/blog/eski.md",
		Node: &notify.NodeRef{StorageID: f.StA.ID, Path: "/blog/eski.md", Name: "eski.md"}})

	status, body := mtGet(t, f.AdminA, f.URL+"/api/admin/notifications?limit=50")
	require.Equal(t, http.StatusOK, status, body)
	var list struct {
		Items []struct {
			Event      string `json:"event"`
			Title      string `json:"title"`
			Audience   string `json:"audience"`
			AdminsOnly bool   `json:"admins_only"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &list))
	got := map[string]string{}
	for _, it := range list.Items {
		key := it.Event
		if it.Event == string(notify.EventPluginNotice) {
			key += ":" + it.Title
		}
		got[key] = it.Audience
		require.Equal(t, it.Audience == notify.AudienceAdmins, it.AdminsOnly, "%s: admins_only is audience=admins", key)
	}
	require.Equal(t, map[string]string{
		"admin_test":                       notify.AudienceEveryone,
		"plugin.notice:Converter is ready": notify.AudienceEveryone,
		"plugin.notice:Signed: yazi.pdf":   notify.AudienceViewers,
		"file.infected":                    notify.AudienceViewers,
		"replica_status_report":            notify.AudienceAdmins,
		"drop.received":                    notify.AudienceAdmins,
		"file.uploaded":                    notify.AudienceNobody,
	}, got)
}
