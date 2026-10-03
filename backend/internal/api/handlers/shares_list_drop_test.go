package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// A file request (a drop share) in the two share listings. Until 0.50 the
// listing's projection did not read a link's kind: "My shares" built the
// address as /s/<token> and the administrator's Shares always did, and /s/
// answers not_found for a file request.
func TestShareListings_AFileRequestListsWithItsUploadAddress(t *testing.T) {
	f, ownerClient, as := newMineFixture(t, mineSecretKey)
	ctx := context.Background()
	folder, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.node.StorageID, Name: "Gelen kutusu", Path: "Gelen kutusu", Type: model.NodeTypeDirectory,
		PathHash: pathkey.Hash(f.node.StorageID, "Gelen kutusu"), SeenAt: time.Now(),
	})
	require.NoError(t, err)
	owner := f.ownerID
	maxUploads := 5
	drop, err := f.store.CreateShare(ctx, &model.Share{NodeID: folder.ID, Token: "dropdropdropdropdropdropdropdr01", CreatedBy: &owner, Kind: model.ShareKindDrop, MaxUploads: &maxUploads})
	require.NoError(t, err)
	link, err := f.store.CreateShare(ctx, &model.Share{NodeID: f.node.ID, Token: "linklinklinklinklinklinklinkli01", CreatedBy: &owner})
	require.NoError(t, err)

	type row struct {
		URL   string `json:"url"`
		Share struct {
			Token      string `json:"token"`
			Kind       string `json:"kind"`
			MaxUploads *int   `json:"max_uploads"`
		} `json:"share"`
	}
	list := func(c *http.Client, path string) map[string]row {
		t.Helper()
		resp, err := c.Get(f.srv.URL + path)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, http.StatusOK, resp.StatusCode, "%s: %s", path, raw)
		var body struct {
			Items []row `json:"items"`
		}
		require.NoError(t, json.Unmarshal(raw, &body), "%s: %s", path, raw)
		out := map[string]row{}
		for _, r := range body.Items {
			out[r.Share.Token] = r
		}
		return out
	}

	for name, rows := range map[string]map[string]row{
		"my shares":            list(ownerClient, "/api/shares"),
		"administrator's list": list(as(f.adminEmail, f.adminPw), "/api/admin/shares"),
	} {
		d, ok := rows[drop.Token]
		require.True(t, ok, "%s: the file request is listed", name)
		require.Equal(t, model.ShareKindDrop, d.Share.Kind, "%s", name)
		require.True(t, strings.HasSuffix(d.URL, "/d/"+drop.Token), "%s: the upload address, got %q", name, d.URL)
		require.NotNil(t, d.Share.MaxUploads, "%s: its upload cap", name)
		require.Equal(t, 5, *d.Share.MaxUploads)
		l, ok := rows[link.Token]
		require.True(t, ok, "%s: the download link is listed", name)
		require.Equal(t, model.ShareKindDownload, l.Share.Kind, "%s", name)
		require.True(t, strings.HasSuffix(l.URL, "/s/"+link.Token), "%s: got %q", name, l.URL)
	}
}
