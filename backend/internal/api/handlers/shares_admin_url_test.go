package handlers_test

// Every row of GET /api/admin/shares carries the canonical public link.
//
// Issue #32 (2026-09-19): the admin Shares page built its copy-link from
// window.location.origin because this list never sent one, so an
// administrator signed in on http://localhost:5212 copied a localhost link
// even with FILEX_PUBLIC_URL set — and on a proxied host, the wrong origin.
// The share dialog has always used the server's answer; this pins that the
// list does too, from the same configured origin.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestAdminShares_RowsCarryTheCanonicalURL(t *testing.T) {
	srv, client, store := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.PublicURL = "https://files.example.com"
		c.PublicURLSet = true
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "paylasim", Driver: "local", MountPath: "/paylasim",
		ConfigJSON: []byte(`{"path":"` + jsonPath(t.TempDir()) + `"}`),
		SyncMode:   model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	n, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "rapor.pdf", Path: "rapor.pdf", Type: model.NodeTypeFile,
		PathHash: pathkey.Hash(st.ID, "rapor.pdf"), SeenAt: time.Now(),
	})
	require.NoError(t, err)
	_, err = store.CreateShare(ctx, &model.Share{
		NodeID: n.ID, Token: "tok-32-url", Kind: model.ShareKindDownload,
	})
	require.NoError(t, err)

	// Ask from a host that is NOT the configured one: the link must still be
	// the configured origin, never the request's.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/shares?limit=10", nil)
	req.Host = "localhost:5212"
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Items []struct {
			Share struct {
				Token string `json:"token"`
			} `json:"share"`
			URL string `json:"url"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Items, 1)
	require.Equal(t, "tok-32-url", body.Items[0].Share.Token)
	require.Equal(t, "https://files.example.com/s/tok-32-url", body.Items[0].URL,
		"the list must hand the panel the configured public link, not leave it to guess from its own address")
}

// jsonPath escapes a filesystem path for a JSON string literal (Windows
// backslashes in t.TempDir()).
func jsonPath(p string) string {
	b, _ := json.Marshal(p)
	return string(b[1 : len(b)-1])
}
