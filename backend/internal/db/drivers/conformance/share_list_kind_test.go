package conformance_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// The share listings ("My shares", the administrator's Shares, agents'
// share_list) read one projection, shareMetaCols. Until 0.50 it did not carry
// the share's kind, so a file request listed as a download link: its address
// was built as /s/<token>, which answers not_found for a drop share, and its
// upload count was always 0.
func TestListAllShares_AFileRequestListsAsOne(t *testing.T) {
	each(t, fileRequestListsAsOne)
}

func fileRequestListsAsOne(t *testing.T, _ *sql.DB, store db.Store, _ string) {
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	// 32 characters, as every real token: PostgreSQL keeps tokens in a
	// CHAR(32) column, which pads a shorter one with spaces.
	dropToken, linkToken := fmt.Sprintf("d%031d", time.Now().UnixNano()), fmt.Sprintf("l%031d", time.Now().UnixNano())
	u, err := store.CreateUser(ctx, "share-kind-"+suffix+"@example.com", "x", "user", "en", "UTC")
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "share-kind-" + suffix, Driver: "local", MountPath: "/k", Enabled: true, ConfigJSON: []byte(`{}`)})
	require.NoError(t, err)
	mk := func(name string, typ model.NodeType) *model.Node {
		n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, Name: name, Path: "/" + name, PathHash: pathkey.Hash(st.ID, "/"+name), Type: typ})
		require.NoError(t, err)
		return n
	}
	inbox := mk("Gelen kutusu", model.NodeTypeDirectory)
	file := mk("rapor.pdf", model.NodeTypeFile)
	maxUploads := 3
	_, err = store.CreateShare(ctx, &model.Share{NodeID: inbox.ID, Token: dropToken, CreatedBy: &u.ID, Kind: model.ShareKindDrop, MaxUploads: &maxUploads})
	require.NoError(t, err)
	_, err = store.CreateShare(ctx, &model.Share{NodeID: file.ID, Token: linkToken, CreatedBy: &u.ID})
	require.NoError(t, err)

	rows, total, err := store.ListAllShares(ctx, &u.ID, false, 50, 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	kinds := map[string]string{}
	for _, r := range rows {
		kinds[r.Share.Token] = r.Share.Kind
		if r.Share.Kind == model.ShareKindDrop {
			require.NotNil(t, r.Share.MaxUploads, "the file request's cap is listed")
			require.Equal(t, 3, *r.Share.MaxUploads)
			require.True(t, r.Share.IsDrop())
		}
	}
	require.Equal(t, map[string]string{dropToken: model.ShareKindDrop, linkToken: model.ShareKindDownload}, kinds)
}
