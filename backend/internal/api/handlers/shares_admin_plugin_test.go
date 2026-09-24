package handlers_test

// An app plugin's public page is a share (migration 00046), so the
// administrator sees and revokes it where every other link lives —
// GET /api/admin/shares — with a plugin/page column on the row. The narrowed
// listing (GET /api/admin/app-plugins/shares?plugin=…) is the same rows, for a
// panel that wants one app's table.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type adminShareRow struct {
	Share struct {
		Token    string `json:"token"`
		PluginID int64  `json:"plugin_id"`
		PageID   string `json:"page_id"`
		Subject  string `json:"subject"`
	} `json:"share"`
	PluginName string `json:"plugin_name"`
	URL        string `json:"url"`
}

func TestAdminShares_AppLinksCarryPluginAndPage(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
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
		StorageID: st.ID, Name: "sozlesme.pdf", Path: "sozlesme.pdf", Type: model.NodeTypeFile,
		PathHash: pathkey.Hash(st.ID, "sozlesme.pdf"), SeenAt: time.Now(),
	})
	require.NoError(t, err)

	plugin, err := store.CreateAppPlugin(ctx, &model.AppPlugin{
		Name: "sign", Version: "1.0.0", LabelJSON: `{"en":"Sign"}`, ManifestJSON: `{}`,
		WasmPath: "x", SHA256: "y", Source: model.AppPluginSourceUpload, PermissionsJSON: `[]`, Enabled: true,
	})
	require.NoError(t, err)

	_, err = store.CreateShare(ctx, &model.Share{
		NodeID: n.ID, Token: "tok-app-1", Kind: model.ShareKindDownload,
		PluginID: plugin.ID, PageID: "signer", Subject: "Please sign sozlesme.pdf",
	})
	require.NoError(t, err)
	_, err = store.CreateShare(ctx, &model.Share{NodeID: n.ID, Token: "tok-plain-1", Kind: model.ShareKindDownload})
	require.NoError(t, err)

	get := func(url string) []adminShareRow {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+url, nil)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, url)
		var body struct {
			Items []adminShareRow `json:"items"`
			Total int64           `json:"total"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		return body.Items
	}

	// ⭐ The whole Shares list carries the app column, so a signature request
	// is visible and revocable exactly where every other link is.
	all := get("/api/admin/shares?limit=50")
	require.Len(t, all, 2)
	byToken := map[string]adminShareRow{}
	for _, row := range all {
		byToken[row.Share.Token] = row
	}
	app := byToken["tok-app-1"]
	assert.Equal(t, plugin.ID, app.Share.PluginID)
	assert.Equal(t, "signer", app.Share.PageID)
	assert.Equal(t, "sign", app.PluginName)
	assert.Equal(t, "Please sign sozlesme.pdf", app.Share.Subject)
	assert.Contains(t, app.URL, "/s/tok-app-1", "an app page's link is a /s/ link like any other")

	plain := byToken["tok-plain-1"]
	assert.Zero(t, plain.Share.PluginID)
	assert.Empty(t, plain.PluginName, "an ordinary share carries no app column")

	// …and the narrowed listing gives one app's table.
	only := get("/api/admin/app-plugins/shares?plugin=sign")
	require.Len(t, only, 1)
	assert.Equal(t, "tok-app-1", only[0].Share.Token)

	// An unnamed plugin means every app, never every share.
	apps := get("/api/admin/app-plugins/shares")
	require.Len(t, apps, 1)
	assert.Equal(t, "tok-app-1", apps[0].Share.Token)

	// ⚠ An app that is not installed has no links — an empty page, not a 404,
	// so a panel polling one app keeps working through an uninstall.
	assert.Empty(t, get("/api/admin/app-plugins/shares?plugin=nosuchapp"))
}
