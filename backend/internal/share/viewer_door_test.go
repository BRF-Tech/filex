package share

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// TestViewer_AFileRequestIsNeverReadHere: the package's own viewer is a read
// door like the router's /s/ page (GHSA-rcrp-r83c-g8hv) - a file request's
// token, with or without its PIN, resolves to nothing and is not described,
// and a download link of the same folder still is.
func TestViewer_AFileRequestIsNeverReadHere(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	stg, err := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	dir, err := store.CreateNode(ctx, &model.Node{
		StorageID: stg.ID, Name: "inbox", Path: "/inbox", PathHash: "hdoor1",
		Type: model.NodeTypeDirectory, SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)
	svc := NewService(store)
	drop, err := svc.Create(ctx, CreateOpts{NodeID: dir.ID, Kind: model.ShareKindDrop})
	require.NoError(t, err)
	dropPIN, err := svc.Create(ctx, CreateOpts{NodeID: dir.ID, Kind: model.ShareKindDrop, PIN: "4242"})
	require.NoError(t, err)
	download, err := svc.Create(ctx, CreateOpts{NodeID: dir.ID})
	require.NoError(t, err)

	_, err = svc.Resolve(ctx, drop.Token, "")
	assert.True(t, errors.Is(err, ErrNotFound), "a file request resolved for a download: %v", err)
	// Its PIN is not even compared: the right one and a wrong one get the
	// same answer as an unknown token.
	for _, pin := range []string{"4242", "0000"} {
		_, err = svc.Resolve(ctx, dropPIN.Token, pin)
		assert.True(t, errors.Is(err, ErrNotFound), "pin %s: %v", pin, err)
	}
	got, err := svc.Resolve(ctx, download.Token, "")
	require.NoError(t, err, "a download link of the same folder still resolves")
	assert.Equal(t, download.ID, got.ID)

	v := &Viewer{Service: svc, Store: store}
	meta := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/files/share/"+token, nil)
		req.SetPathValue("token", token)
		rec := httptest.NewRecorder()
		v.HandleMetadata(rec, req)
		return rec.Code
	}
	assert.Equal(t, http.StatusNotFound, meta(drop.Token))
	assert.Equal(t, http.StatusNotFound, meta(dropPIN.Token))
	assert.Equal(t, http.StatusOK, meta(download.Token))

	req := httptest.NewRequest(http.MethodGet, "/s/"+dropPIN.Token+"?pin=4242", nil)
	req.SetPathValue("token", dropPIN.Token)
	rec := httptest.NewRecorder()
	v.HandleDownload(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}
