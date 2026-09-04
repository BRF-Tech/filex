package handlers_test

// The admin rebuild endpoint against the automatic schema repair.
//
// filex rebuilds its own index when it finds one written by an older
// document schema (internal/search/rebuild.go). Before that existed, the
// only guard against two rebuilds running at once was an atomic on this
// handler — which by construction could not see a rebuild the handler had
// not started. Two rebuilds would each build a replacement index and race
// to swap it in.

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// heldLister keeps a rebuild running until release is closed.
type heldLister struct {
	entered chan struct{}
	release chan struct{}
}

func (h *heldLister) AllNodesForIndex(_ context.Context) ([]*model.Node, error) {
	close(h.entered)
	<-h.release
	return nil, nil
}

func TestSearchAdminRebuild_RefusedWhileAnAutomaticRebuildRuns(t *testing.T) {
	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.Index = idx
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	post := func() int {
		resp, err := client.Post(srv.URL+"/api/admin/search/rebuild?content=1", "application/json", nil)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode
	}
	stats := func() map[string]any {
		resp, err := client.Get(srv.URL + "/api/admin/search/stats")
		require.NoError(t, err)
		defer resp.Body.Close()
		var body map[string]any
		testutil.ReadJSON(t, resp, &body)
		return body
	}

	// A rebuild started by the server itself, not by this endpoint.
	held := &heldLister{entered: make(chan struct{}), release: make(chan struct{})}
	require.NoError(t, idx.StartRebuild(held, search.RebuildOptions{Reason: "schema-upgrade"}))
	<-held.entered

	require.Equal(t, http.StatusConflict, post(),
		"the admin endpoint must refuse while the automatic rebuild runs")
	require.Equal(t, true, stats()["rebuilding"],
		"stats must say a rebuild is in progress so the UI does not look broken")

	close(held.release)
	deadline := time.Now().Add(30 * time.Second)
	for idx.Rebuilding() {
		require.True(t, time.Now().Before(deadline), "rebuild never finished")
		time.Sleep(5 * time.Millisecond)
	}

	// And the other order: a manual rebuild is accepted once nothing is
	// running, and a second one while it runs is refused.
	require.Equal(t, http.StatusAccepted, post())
	require.Equal(t, false, stats()["needs_rebuild"])
}
