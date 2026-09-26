package handlers_test

// Deleting a storage finishes, whoever stops waiting, and a delete that fails
// leaves the storage as it was.
//
// ⚠ The delete ran on the request's context. Removing a large storage's rows
// outlasts the admin panel's 30 s wait (and nginx's 60 s), and the client's
// hang-up cancelled the delete: the row stayed, and the storage had already
// lost its syncer (it is stopped first), so it went on existing with no scan
// and "Sync now" answering 404 until the next restart.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// slowDeleteStore holds DeleteStorage until the client has gone, or fails it.
type slowDeleteStore struct {
	db.Store
	armed      atomic.Bool
	fail       atomic.Bool
	reached    chan struct{}
	clientGone chan struct{}
}

func (s *slowDeleteStore) DeleteStorage(ctx context.Context, id int64) error {
	if s.fail.Load() {
		return errors.New("disk I/O error")
	}
	if s.armed.CompareAndSwap(true, false) {
		close(s.reached)
		<-s.clientGone
		// Give the server the moment it takes to notice the hang-up; a
		// context the hang-up cannot reach simply waits it out.
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
	return s.Store.DeleteStorage(ctx, id)
}

func newDeleteFixture(t *testing.T) (string, *http.Client, *slowDeleteStore, *syncpkg.Worker, *model.Storage) {
	t.Helper()
	var sds *slowDeleteStore
	var worker *syncpkg.Worker
	srv, client, _ := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		sds = &slowDeleteStore{Store: d.Store, reached: make(chan struct{}), clientGone: make(chan struct{})}
		d.Store = sds
		worker = d.Worker
	})
	email, password := testutil.SeedAdmin(t, sds)
	testutil.LoginAs(t, srv, client, email, password)
	cfg, _ := json.Marshal(map[string]any{"root": t.TempDir()})
	st, err := sds.CreateStorage(context.Background(), &model.Storage{
		Name: "silinecek", Driver: "local", MountPath: "/silinecek", ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	require.NoError(t, worker.AddStorage(context.Background(), st))
	t.Cleanup(worker.Stop)
	return srv.URL, client, sds, worker, st
}

func TestStorageDelete_FinishesWhenTheClientLeaves(t *testing.T) {
	srv, client, sds, _, st := newDeleteFixture(t)
	sds.armed.Store(true)

	ctx, leave := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, srv+"/api/admin/storages/"+itoa64(st.ID), nil)
	go func() {
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-sds.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the delete never reached the store")
	}
	leave()
	close(sds.clientGone)

	require.Eventually(t, func() bool {
		_, err := sds.GetStorage(context.Background(), st.ID)
		return err != nil
	}, 10*time.Second, 50*time.Millisecond, "the storage is still there after its client left")
}

func TestStorageDelete_AFailedDeleteKeepsTheStoragesScan(t *testing.T) {
	srv, client, sds, worker, st := newDeleteFixture(t)
	sds.fail.Store(true)

	req, _ := http.NewRequest(http.MethodDelete, srv+"/api/admin/storages/"+itoa64(st.ID), nil)
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	_, err = sds.GetStorage(context.Background(), st.ID)
	require.NoError(t, err, "the storage is still there")
	assert.True(t, worker.Known(st.ID), "the storage that was not deleted lost its syncer")
}
