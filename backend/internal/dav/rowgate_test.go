package dav

// Issue #201: a WebDAV MOVE and DELETE hold the storage's row gate
// (internal/rowgate) from the first byte to the last row, as the explorer and
// the operations queue have since 0.54 (#192). Without it a storage scan
// running beside a MOVE saw a renamed folder's bytes at the new name while its
// rows still sat at the old one, judged everything in it gone and dropped the
// rows; beside a DELETE it confirmed a trashed file's row gone and dropped it
// with the trash entry.
//
// Break: route davFS.Rename or davFS.RemoveAll past protocolsync.Relocate /
// Discard (mv.Move + syncMove, trash.Put + syncTrash, as up to 0.54): the
// scan's judgement gets the gate half way and HeldHalfWay fails.

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/gatetest"
)

// gatedHarness is the /dav harness with a way to put one storage on a driver
// that stops its next Move or Delete half way (gatetest.Driver). The driver
// starts disarmed, so the test seeds through it first. gated(st,
// (*gatetest.Driver).NoTrash) serves st through the driver's view of a
// storage that keeps no trash, where a delete is for good.
func gatedHarness(t *testing.T) (*harness, func(st *model.Storage, as ...gatetest.View) *gatetest.Driver) {
	t.Helper()
	var mu sync.Mutex
	gated := map[int64]storage.Driver{}
	ha := newHarnessWith(t, func(s db.Store) db.Store { return s }, func(cfg *Config) {
		orig := cfg.Resolver
		cfg.Resolver = func(id int64) (storage.Driver, error) {
			mu.Lock()
			d, ok := gated[id]
			mu.Unlock()
			if ok {
				return d, nil
			}
			return orig(id)
		}
	})
	put := func(st *model.Storage, as ...gatetest.View) *gatetest.Driver {
		cfg := map[string]any{}
		require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
		base := &local.Driver{}
		require.NoError(t, base.Init(context.Background(), cfg))
		d := gatetest.Wrap(base)
		d.Arm(false)
		t.Cleanup(d.Finish)
		mu.Lock()
		gated[st.ID] = d.Serve(as...)
		mu.Unlock()
		return d
	}
	return ha, put
}

// davDo is one WebDAV request as the admin, for a goroutine: no require.
func (ha *harness) davDo(method, path string, hdr map[string]string) (int, error) {
	r, err := http.NewRequest(method, ha.srv.URL+path, nil)
	if err != nil {
		return 0, err
	}
	r.SetBasicAuth(ha.adminEmail, ha.adminPass)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	resp, err := ha.srv.Client().Do(r)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func liveRowAt(store db.Store, storageID int64, rel string) *model.Node {
	n, err := store.GetNodeByPath(context.Background(), storageID, pathkey.Hash(storageID, normalizeDBPath(rel)))
	if err != nil {
		return nil
	}
	return n
}

func TestMoveOverDAVHoldsTheRowGate(t *testing.T) {
	ha, gated := gatedHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	d := gated(st)

	resp := ha.req(t, "MKCOL", "/dav/depo/Leon", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp = ha.req(t, http.MethodPut, "/dav/depo/Leon/a.txt", ha.adminEmail, ha.adminPass, "a", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	row := liveRowAt(ha.store, st.ID, "Leon/a.txt")
	require.NotNil(t, row, "the PUT did not catalogue the file")
	d.Arm(true)

	type answer struct {
		code int
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		code, err := ha.davDo("MOVE", "/dav/depo/Leon",
			map[string]string{"Destination": ha.srv.URL + "/dav/depo/Leo"})
		done <- answer{code, err}
	}()

	gatetest.HeldHalfWay(t, st.ID, d, func() bool {
		n := liveRowAt(ha.store, st.ID, "Leo/a.txt")
		return n != nil && n.ID == row.ID
	})
	got := <-done
	require.NoError(t, got.err)
	require.Equal(t, http.StatusCreated, got.code)
}

func TestDeleteOverDAVHoldsTheRowGate(t *testing.T) {
	ha, gated := gatedHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	d := gated(st)

	resp := ha.req(t, http.MethodPut, "/dav/depo/a.txt", ha.adminEmail, ha.adminPass, "a", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	row := liveRowAt(ha.store, st.ID, "a.txt")
	require.NotNil(t, row, "the PUT did not catalogue the file")
	d.Arm(true)

	type answer struct {
		code int
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		code, err := ha.davDo(http.MethodDelete, "/dav/depo/a.txt", nil)
		done <- answer{code, err}
	}()

	gatetest.HeldHalfWay(t, st.ID, d, func() bool {
		n, err := ha.store.GetNode(context.Background(), row.ID)
		// Retagged into the trash: still there, no longer live.
		return err == nil && n != nil && n.DeletedAt != nil
	})
	got := <-done
	require.NoError(t, got.err)
	require.Equal(t, http.StatusNoContent, got.code)
}

// On a storage that cannot keep what it deletes (no Move, no Copy) a DELETE
// is a delete for good (files.purge), and its bytes and rows go under the
// gate too (Syncer.Purge).
//
// Break: route davFS.RemoveAll's purge branch past protocolsync.Purge
// (del.Delete + syncDelete, as up to 0.54): the scan's judgement gets the
// gate half way.
func TestPurgeOverDAVHoldsTheRowGate(t *testing.T) {
	ha, gated := gatedHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	d := gated(st, (*gatetest.Driver).NoTrash)

	resp := ha.req(t, http.MethodPut, "/dav/depo/a.txt", ha.adminEmail, ha.adminPass, "a", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	row := liveRowAt(ha.store, st.ID, "a.txt")
	require.NotNil(t, row, "the PUT did not catalogue the file")
	d.Arm(true)

	type answer struct {
		code int
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		code, err := ha.davDo(http.MethodDelete, "/dav/depo/a.txt", nil)
		done <- answer{code, err}
	}()

	gatetest.HeldHalfWay(t, st.ID, d, func() bool {
		n, err := ha.store.GetNode(context.Background(), row.ID)
		// Dropped, not retagged into the trash: nothing could restore it.
		return err != nil || n == nil
	})
	got := <-done
	require.NoError(t, got.err)
	require.Equal(t, http.StatusNoContent, got.code)
}
