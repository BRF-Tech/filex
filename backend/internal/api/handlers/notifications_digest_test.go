package handlers_test

// The notification digest over real HTTP, in a real two-tenant instance with
// RBAC on (task #166, internal/notify digest.go).
//
// A digest sums up, folder by folder, the notifications a person did not mark
// urgent. The two ways that could leak are the two pinned here: a digest that
// counted or named a file the person may not see (another member's folder, a
// folder outside their grants), and one that mixed in another tenant's rows.
// The digest is made from the rows the person's own bell keeps — on their
// read, through the request's judge, and in the background pass, through the
// same judge built without a request (Notifications.DigestViewer).
//
// ⚠ Red on the code before it: notify.DigestConfig, notify.Digests and the
// /api/admin/notifications/digest routes do not exist.

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/notify"
)

// digestTestClock moves the digest's clock without sleeping; the rows are
// stamped by the database with the real time, so it starts at now.
type digestTestClock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *digestTestClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.off)
}

func (c *digestTestClock) advance(d time.Duration) {
	c.mu.Lock()
	c.off += d
	c.mu.Unlock()
}

func newDigestFix(t *testing.T) (*mtFix, *digestTestClock) {
	t.Helper()
	clock := &digestTestClock{}
	f := newMTFix(t, true, func(c *notify.Config) {
		c.Digest = &notify.DigestConfig{MultiTenant: true, Every: time.Hour}
		c.Now = clock.now
	})
	return f, clock
}

// holdViruses makes the client's account hold virus alerts for its digest —
// urgent by default, and a person may still put them in the digest (the
// owner's decision, 2026-10-06).
func holdViruses(t *testing.T, f *mtFix, c *http.Client) {
	t.Helper()
	status, body := doJSON(t, c, http.MethodPatch, f.URL+"/api/notifications/settings", map[string]any{
		"in_app_enabled":   true,
		"muted_events":     []string{},
		"urgent_overrides": map[string]bool{"file.infected": false},
	})
	require.Equal(t, http.StatusOK, status, "%v", body)
	digest, _ := body["digest"].(map[string]any)
	require.NotNil(t, digest, "the settings answer says nothing of the digest: %v", body)
	require.NotContains(t, digest["urgent_events"], "file.infected")
}

// TestNotificationDigest_AMemberIsToldOnlyOfWhatTheyMaySee — three worker
// alerts: one in a folder the member has no grant on, one in their own folder,
// one in another tenant. Their digest names and counts the one.
func TestNotificationDigest_AMemberIsToldOnlyOfWhatTheyMaySee(t *testing.T) {
	f, clock := newDigestFix(t)
	writer, _ := f.rbacAlpha(t)
	holdViruses(t, f, writer)
	holdViruses(t, f, f.B)

	f.emitWorkerAV(t, f.StA.ID, "/muhasebe/virus.exe")
	f.emitWorkerAV(t, f.StA.ID, "/blog/ek.exe")
	f.emitWorkerAV(t, f.StB.ID, "/gizli/bravo.exe")

	// Held: out of the badge. Reading the bell opens the window.
	for _, c := range []*http.Client{writer, f.B} {
		_, count := mtGet(t, c, f.URL+"/api/notifications/unread-count")
		require.Contains(t, count, `"count":0`, "a held alert raised the badge: %s", count)
	}
	_, list := mtGet(t, writer, f.URL+"/api/notifications")
	require.Contains(t, list, "/blog/ek.exe", "a held alert is still in the list: %s", list)

	// The background pass tells both windows — no request behind it, the
	// person's bell built by the HTTP layer's own rule.
	clock.advance(61 * time.Second)
	dg, ok := f.Notif.(notify.Digests)
	require.True(t, ok)
	made, err := dg.FlushDueDigests(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, made)

	_, list = mtGet(t, writer, f.URL+"/api/notifications?unread=true")
	require.Contains(t, list, `"event":"notification.digest"`, "%s", list)
	require.Contains(t, list, `"path":"blog"`, "%s", list)
	require.NotContains(t, list, "muhasebe", "a folder the member has no grant on is in their digest: %s", list)
	require.NotContains(t, list, "gizli", "another tenant's folder is in the digest: %s", list)
	require.NotContains(t, list, "bravo", "another tenant's storage is in the digest: %s", list)
	require.Contains(t, list, `"count":1`, "the digest counted a file the member may not see: %s", list)
	_, count := mtGet(t, writer, f.URL+"/api/notifications/unread-count")
	require.Contains(t, count, `"count":1`, "%s", count)

	_, blist := mtGet(t, f.B, f.URL+"/api/notifications?unread=true")
	require.Contains(t, blist, `"path":"gizli"`, "%s", blist)
	require.NotContains(t, blist, "muhasebe", "%s", blist)
	require.NotContains(t, blist, "blog", "%s", blist)

	// The member granted muhasebe/ keeps alerts urgent: told at once, as
	// before, and of nothing else.
	_, alist := mtGet(t, f.A, f.URL+"/api/notifications?unread=true")
	require.Contains(t, alist, "/muhasebe/virus.exe", "%s", alist)
	require.NotContains(t, alist, "notification.digest", "%s", alist)
	require.NotContains(t, alist, "/blog/ek.exe", "%s", alist)

	// A save that leaves the urgent choices out (an older client) keeps them.
	status, body := doJSON(t, writer, http.MethodPatch, f.URL+"/api/notifications/settings", map[string]any{
		"in_app_enabled": true, "muted_events": []string{"share.created"},
	})
	require.Equal(t, http.StatusOK, status, "%v", body)
	overrides, _ := body["urgent_overrides"].(map[string]any)
	require.Equal(t, false, overrides["file.infected"], "a save without urgent choices wiped them: %v", body)
}

// TestNotificationDigest_AdminDefaultsArePerTenant — out of the box every kind
// is urgent; a tenant's administrator holds kinds by default for their tenant
// and nobody else's; the window is 1-15 minutes; a member cannot change it.
func TestNotificationDigest_AdminDefaultsArePerTenant(t *testing.T) {
	f, _ := newDigestFix(t)

	// The default: every kind told at once.
	status, body := doJSON(t, f.AdminA, http.MethodGet, f.URL+"/api/admin/notifications/digest", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Equal(t, false, body["saved"])
	require.ElementsMatch(t, body["events"], body["urgent_events"], "a kind is held out of the box")
	_, before := doJSON(t, f.A, http.MethodGet, f.URL+"/api/notifications/settings", nil)
	beforeDigest, _ := before["digest"].(map[string]any)
	require.NotNil(t, beforeDigest, "%v", before)
	require.Contains(t, beforeDigest["urgent_events"], "file.uploaded")

	status, body = doJSON(t, f.AdminA, http.MethodPatch, f.URL+"/api/admin/notifications/digest", map[string]any{
		"window_minutes": 5,
		"urgent_events":  []string{"file.infected", "no.such.event"},
	})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Equal(t, "tenant", body["scope"])
	require.EqualValues(t, 5, body["window_minutes"])
	require.Equal(t, []any{"file.infected"}, body["urgent_events"], "an unknown event id was kept")
	require.Equal(t, true, body["saved"])

	status, body = doJSON(t, f.AdminA, http.MethodPatch, f.URL+"/api/admin/notifications/digest", map[string]any{
		"window_minutes": 16,
	})
	require.Equal(t, http.StatusBadRequest, status, "%v", body)
	require.Equal(t, "invalid_window", body["error"])

	// The platform's own defaults are untouched.
	status, body = doJSON(t, f.Super, http.MethodGet, f.URL+"/api/admin/notifications/digest", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.EqualValues(t, 1, body["window_minutes"])
	require.Equal(t, false, body["saved"])

	// Each member reads their own tenant's window.
	_, a := doJSON(t, f.A, http.MethodGet, f.URL+"/api/notifications/settings", nil)
	ad, _ := a["digest"].(map[string]any)
	require.NotNil(t, ad, "%v", a)
	require.EqualValues(t, 5, ad["window_minutes"])
	require.Equal(t, []any{"file.infected"}, ad["urgent_events"], "the tenant's defaults did not reach its member")
	_, b := doJSON(t, f.B, http.MethodGet, f.URL+"/api/notifications/settings", nil)
	bd, _ := b["digest"].(map[string]any)
	require.NotNil(t, bd, "%v", b)
	require.EqualValues(t, 1, bd["window_minutes"])
	require.Contains(t, bd["urgent_events"], "file.uploaded", "another tenant's defaults held this one's files")

	// "Restore defaults".
	status, body = doJSON(t, f.AdminA, http.MethodPatch, f.URL+"/api/admin/notifications/digest", map[string]any{
		"urgent_events": nil,
	})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.EqualValues(t, 5, body["window_minutes"], "restoring the urgent list moved the window")
	require.Contains(t, body["urgent_events"], "e2e.escrow_used", "the built-in list did not come back: %v", body)

	// A member may not.
	status, _ = doJSON(t, f.A, http.MethodPatch, f.URL+"/api/admin/notifications/digest", map[string]any{"window_minutes": 15})
	require.NotEqual(t, http.StatusOK, status)
}
