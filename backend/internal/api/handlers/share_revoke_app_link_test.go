package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A SIGNING link revoked from My shares: the listing says it was revoked AND
// still says what it is, and the single-row read the signing app's
// revoke detection goes through (share_state → pageFacts, over
// GetShareByToken) still sees a link that ended early.
//
// ⚠ Merge seam (feat/043-tables × feat/043-signing). Tables added
// `revoked_at` to the share LIST projection only, and made the revoke write
// it beside the expiry; signing added `visit_count` and `purpose_json` to the
// same projection and reads single shares to notice a revoke. The two scans
// meet in one Scan call per driver — a column out of order there and My
// shares either fails outright or reads the purpose into the wrong field —
// and the app's detection lives on the expiry the revoke must keep moving.
func TestShareRevoke_AnAppLinkReadsRevokedAndTheAppStillNotices(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEchoWith(t, func(m map[string]any) {
		m["public_pages"].([]any)[0].(map[string]any)["purpose"] = map[string]any{
			"label":   map[string]any{"en": "Signing request", "tr": "İmza isteği"},
			"revoke":  map[string]any{"en": "Revoking this link cancels the signing request.", "tr": "Bu bağlantıyı iptal etmek imza isteğini iptal eder."},
			"section": "requested",
		}
		m["views"] = append(m["views"].([]any), map[string]any{
			"id": "inbox", "placement": "home", "label": map[string]any{"en": "Inbox", "tr": "Gelenler"},
		})
	})
	f.seedDoc(t, "docs/terms.txt")
	client, _, _ := f.loginRegular(t, "sender", "editor")
	token := f.openSigningLinkAs(t, client, id, "docs/terms.txt")
	ctx := context.Background()

	status, raw := doReq(t, freshClient(t), http.MethodPost, f.srv.URL+"/api/public/s/"+token+"/event", map[string]any{"event": "open"})
	require.Equal(t, http.StatusOK, status, string(raw))

	before, err := f.store.GetShareByToken(ctx, token)
	require.NoError(t, err)
	require.False(t, before.IsExpired(time.Now()), "the link works before the revoke")
	given := before.ExpiresAt

	// The owner revokes it from My shares.
	req, _ := http.NewRequest(http.MethodDelete, f.srv.URL+"/api/files/share/"+strconv.FormatInt(before.ID, 10), nil)
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// 1. The single-row read: what share_state hands the app (`revoked` is
	//    IsExpired; the sign app also checks that the expiry moved EARLIER
	//    than the day the link was given — endedEarly).
	after, err := f.store.GetShareByToken(ctx, token)
	require.NoError(t, err)
	assert.True(t, after.IsExpired(time.Now().Add(time.Second)), "the app's revoke detection no longer sees the revoke")
	require.NotNil(t, after.ExpiresAt, "a revoke ends the link through its expiry")
	if given != nil {
		assert.True(t, after.ExpiresAt.Before(*given), "the expiry did not move earlier than the day the link was given")
	}
	assert.Equal(t, 1, after.VisitCount, "the visit counter survives the revoke")

	// 2. My shares: revoked, and still named for what it is.
	status, raw = doReq(t, client, http.MethodGet, f.srv.URL+"/api/shares", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var mine struct {
		Items []struct {
			Share struct {
				Token      string     `json:"token"`
				VisitCount int        `json:"visit_count"`
				RevokedAt  *time.Time `json:"revoked_at"`
				ExpiresAt  *time.Time `json:"expires_at"`
			} `json:"share"`
			App *struct {
				Plugin string            `json:"plugin"`
				Label  map[string]string `json:"label"`
				Revoke map[string]string `json:"revoke"`
			} `json:"app"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &mine))
	var found bool
	for _, it := range mine.Items {
		if it.Share.Token != token {
			continue
		}
		found = true
		assert.NotNil(t, it.Share.RevokedAt, "a revoked signing link must read as revoked, not expired")
		assert.NotNil(t, it.Share.ExpiresAt)
		assert.Equal(t, 1, it.Share.VisitCount, "visit_count read from its own column")
		require.NotNil(t, it.App, "the revoked signing link is still listed as one")
		assert.Equal(t, "echo", it.App.Plugin)
		assert.Equal(t, "İmza isteği", it.App.Label["tr"])
		assert.Equal(t, "Revoking this link cancels the signing request.", it.App.Revoke["en"])
	}
	require.True(t, found, "the revoked link is still in its creator's My shares")
}
