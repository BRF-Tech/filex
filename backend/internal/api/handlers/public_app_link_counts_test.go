package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The owner's decision of 2026-09-21, on signing links in My shares: they
// stay listed, MARKED as what they are (named, opening the app's page for
// them, and saying before a revoke that the revoke ends the request), and a
// page VIEW is not a download — a signing link that had only been looked at
// twice said "İndirme 2", while a drop link said "İndirme 0" after an upload.
func TestAppLink_ListedForWhatItIsAndCountedHonestly(t *testing.T) {
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

	// Two visits; the page's viewer SHOWING the document twice (no
	// `download=1`: not a download); one real download of the exposed copy,
	// plus the rest of that same download fetched in a second piece.
	for i := 0; i < 2; i++ {
		status, raw := doReq(t, freshClient(t), http.MethodPost, f.srv.URL+"/api/public/s/"+token+"/event", map[string]any{"event": "open"})
		require.Equal(t, http.StatusOK, status, string(raw))
		status, raw = f.visitorGet(t, token, "/file/pub:0")
		require.Equal(t, http.StatusOK, status, string(raw))
	}
	status, raw := f.visitorGet(t, token, "/file/pub:0?download=1")
	require.Equal(t, http.StatusOK, status, string(raw))
	req, err := http.NewRequest(http.MethodGet, f.srv.URL+"/api/public/s/"+token+"/file/pub:0?download=1", nil)
	require.NoError(t, err)
	req.Header.Set("Range", "bytes=3-")
	resp, err := freshClient(t).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)

	sh, err := f.store.GetShareByToken(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, 2, sh.VisitCount, "each opening of the page is a visit")
	assert.Equal(t, 1, sh.DownloadCount, "only taking the exposed file is a download — not showing it, and once, not once per piece")

	// My shares: the row says what it is and where it lives.
	status, raw = doReq(t, client, http.MethodGet, f.srv.URL+"/api/shares", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var mine struct {
		Items []struct {
			Share struct {
				Token         string `json:"token"`
				DownloadCount int    `json:"download_count"`
				VisitCount    int    `json:"visit_count"`
			} `json:"share"`
			App *struct {
				Plugin  string            `json:"plugin"`
				Page    string            `json:"page"`
				Label   map[string]string `json:"label"`
				Revoke  map[string]string `json:"revoke"`
				View    string            `json:"view"`
				Section string            `json:"section"`
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
		require.NotNil(t, it.App, "a signing link is listed as one")
		assert.Equal(t, "echo", it.App.Plugin)
		assert.Equal(t, "İmza isteği", it.App.Label["tr"])
		assert.Equal(t, "Revoking this link cancels the signing request.", it.App.Revoke["en"])
		assert.Equal(t, "inbox", it.App.View)
		assert.Equal(t, "requested", it.App.Section)
		assert.Equal(t, 1, it.Share.DownloadCount)
		assert.Equal(t, 2, it.Share.VisitCount)
	}
	require.True(t, found, "the link is in its creator's My shares")
}
