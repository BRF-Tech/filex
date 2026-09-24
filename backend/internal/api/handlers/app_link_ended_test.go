package handlers_test

// A PERSON ending an app's link wakes the app.
//
// A signing link revoked from the Shares screen stopped working, and the
// request it belonged to stayed open ("Sent · 0/1 signed", the file frozen)
// because nothing told the app. The app now asks after its own links on every
// hourly wake-up; these tests pin the host half — each of the three places a
// person ends a link brings that wake-up forward, and nothing else is sent.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// installEchoScheduled installs the fixture WITH the `schedule` grant, which
// is what makes an app wakeable at all.
func (f *appFixture) installEchoScheduled(t *testing.T) int64 {
	t.Helper()
	return f.installEchoWith(t, func(m map[string]any) {
		m["permissions"] = append(m["permissions"].([]any), "schedule")
	})
}

// wakeUpAt parks the app's wake-up at the next hour, where a running
// instance keeps it between wake-ups, and reads it back later.
//
// ⚠ At least a minute away. A revoke brings the wake-up forward to now + 5 s
// (tickNudgeGrace); parked at the next hour from 09:59:57, "brought forward"
// landed ON that hour and the test read "the wake-up stayed at 10:00" - a
// failure only a run crossing the hour saw (measured 2026-09-22, 09:59 UTC).
func (f *appFixture) parkWakeUp(t *testing.T, pluginID int64) time.Time {
	t.Helper()
	hour := time.Now().UTC().Add(time.Minute).Truncate(time.Hour).Add(time.Hour)
	require.NoError(t, f.store.PutAppPluginScheduleItem(context.Background(), &model.AppPluginScheduleItem{
		PluginID: pluginID, Key: model.AppPluginScheduleWakeupKey, DueAt: hour, Status: model.AppPluginScheduleDue,
	}))
	return hour
}

func (f *appFixture) wakeUpDue(t *testing.T, pluginID int64) time.Time {
	t.Helper()
	rows, err := f.reg.ScheduleOf(context.Background(), pluginID)
	require.NoError(t, err)
	for _, r := range rows {
		if r.Key == model.AppPluginScheduleWakeupKey {
			return r.DueAt
		}
	}
	t.Fatal("the app has no wake-up row")
	return time.Time{}
}

func TestAppLinkEnded_EveryWayAPersonEndsALinkWakesTheApp(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		url    func(id int64) string
	}{
		{"My shares: revoke", http.MethodDelete, func(id int64) string { return "/api/files/share/" + itoa(id) }},
		{"Admin → Shares: revoke", http.MethodPost, func(id int64) string { return "/api/admin/shares/" + itoa(id) + "/revoke" }},
		{"Admin → Shares: delete", http.MethodDelete, func(id int64) string { return "/api/admin/shares/" + itoa(id) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAppFixture(t, nil)
			id := f.installEchoScheduled(t)
			f.seedDoc(t, "docs/terms.txt")
			token := f.openSigningLink(t, id, "docs/terms.txt")
			sh, err := f.store.GetShareByToken(context.Background(), token)
			require.NoError(t, err)
			hour := f.parkWakeUp(t, id)

			before := time.Now().UTC()
			status, raw := doReq(t, f.admin, tc.method, f.srv.URL+tc.url(sh.ID), nil)
			require.Equal(t, http.StatusOK, status, string(raw))

			due := f.wakeUpDue(t, id)
			assert.True(t, due.Before(hour), "the wake-up stayed at %s", hour)
			assert.WithinDuration(t, before, due, 30*time.Second, "the app is woken in seconds, not at the hour")
		})
	}
}

// ⚠ A link no app opened wakes nobody, and an app's wake-up is not moved by
// somebody else's link.
func TestAppLinkEnded_AnOrdinaryShareWakesNobody(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEchoScheduled(t)
	f.seedDoc(t, "docs/terms.txt")
	hour := f.parkWakeUp(t, id)

	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/share", map[string]any{"path": "main://docs/terms.txt"})
	require.Equal(t, http.StatusOK, status, string(raw))
	var created struct {
		ID    int64 `json:"id"`
		Share struct {
			ID int64 `json:"id"`
		} `json:"share"`
	}
	require.NoError(t, json.Unmarshal(raw, &created))
	sid := created.ID
	if sid == 0 {
		sid = created.Share.ID
	}
	require.NotZero(t, sid, string(raw))
	status, raw = doReq(t, f.admin, http.MethodDelete, f.srv.URL+"/api/files/share/"+itoa(sid), nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.True(t, f.wakeUpDue(t, id).Equal(hour), "an ordinary share moved an app's wake-up")
}
