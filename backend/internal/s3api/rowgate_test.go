package s3api_test

// Issue #201: the S3 gateway's DeleteObject and DeleteObjects hold the
// storage's row gate (internal/rowgate) from the first byte to the last row,
// as the explorer and the operations queue have since 0.54 (#192). Without it
// a storage scan running beside the delete confirmed the trashed object's row
// gone by a Stat of its old key and dropped it, and the trash entry the person
// could restore from went with it.
//
// Break: route deleteObject or deleteObjects past protocolsync.Discard
// (trash.Put + sync().Trash, as up to 0.54): the scan's judgement gets the
// gate half way and HeldHalfWay fails.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/s3api"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/gatetest"
)

// gatedHarness is the gateway harness with a way to put one storage on a
// driver that stops its next Move or Delete half way (gatetest.Driver).
// gated(st, (*gatetest.Driver).NoTrash) serves st through the driver's view
// of a storage that keeps no trash, where a delete is for good.
func gatedHarness(t *testing.T) (*harness, func(st *model.Storage, as ...gatetest.View) *gatetest.Driver) {
	t.Helper()
	var mu sync.Mutex
	gated := map[int64]storage.Driver{}
	hz := newHarnessCfg(t, false, func(cfg *s3api.Config) {
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
		var cfg map[string]any
		if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
			t.Fatalf("storage config: %v", err)
		}
		base := &local.Driver{}
		if err := base.Init(context.Background(), cfg); err != nil {
			t.Fatalf("local driver: %v", err)
		}
		d := gatetest.Wrap(base)
		t.Cleanup(d.Finish)
		mu.Lock()
		gated[st.ID] = d.Serve(as...)
		mu.Unlock()
		return d
	}
	return hz, put
}

// seedTrashableRow catalogues key the way an upload would have and answers its
// row.
func seedTrashableRow(t *testing.T, store db.Store, st *model.Storage, key string, size int64) *model.Node {
	t.Helper()
	n, _, ok := protocolsync.New(store, nil, nil, "test").WriteRows(context.Background(), st, key, size, "text/plain")
	if !ok || n == nil {
		t.Fatalf("seed row %s", key)
	}
	return n
}

func trashedRow(store db.Store, id int64) bool {
	n, err := store.GetNode(context.Background(), id)
	// Retagged into the trash: still there, no longer live.
	return err == nil && n != nil && n.DeletedAt != nil
}

func TestDeleteObjectHoldsTheRowGate(t *testing.T) {
	hz, gated := gatedHarness(t)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Leon/a.txt", []byte("a"))
	row := seedTrashableRow(t, hz.store, st, "Leon/a.txt", 1)
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	d := gated(st)

	req := signed(t, key, http.MethodDelete, "https://s3.filex.test/main/Leon/a.txt", hz.at)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- recorderFor(hz, req) }()

	gatetest.HeldHalfWay(t, st.ID, d, func() bool { return trashedRow(hz.store, row.ID) })
	if rec := <-done; rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBulkDeleteHoldsTheRowGate(t *testing.T) {
	hz, gated := gatedHarness(t)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Leon/a.txt", []byte("a"))
	row := seedTrashableRow(t, hz.store, st, "Leon/a.txt", 1)
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	d := gated(st)

	body := []byte(`<Delete><Object><Key>Leon/a.txt</Key></Object></Delete>`)
	req := signedBody(t, key, http.MethodPost, "https://s3.filex.test/main?delete", body, hz.at)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- recorderFor(hz, req) }()

	gatetest.HeldHalfWay(t, st.ID, d, func() bool { return trashedRow(hz.store, row.ID) })
	if rec := <-done; rec.Code != http.StatusOK {
		t.Fatalf("bulk delete = %d: %s", rec.Code, rec.Body.String())
	}
}

// rowGone reports whether row id has left the catalogue: a delete for good
// drops it rather than retagging it into the trash.
func rowGone(store db.Store, id int64) bool {
	n, err := store.GetNode(context.Background(), id)
	return err != nil || n == nil
}

// On a storage that cannot keep what it deletes (no Move, no Copy)
// DeleteObject is a delete for good (files.purge), and its bytes and rows go
// under the gate too (Syncer.Purge).
//
// Break: route deleteObject's purge branch past protocolsync.Purge
// (del.Delete + sync().Delete, as up to 0.54): the scan's judgement gets the
// gate half way.
func TestPurgeObjectHoldsTheRowGate(t *testing.T) {
	hz, gated := gatedHarness(t)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Leon/a.txt", []byte("a"))
	row := seedTrashableRow(t, hz.store, st, "Leon/a.txt", 1)
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	d := gated(st, (*gatetest.Driver).NoTrash)

	req := signed(t, key, http.MethodDelete, "https://s3.filex.test/main/Leon/a.txt", hz.at)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- recorderFor(hz, req) }()

	gatetest.HeldHalfWay(t, st.ID, d, func() bool { return rowGone(hz.store, row.ID) })
	if rec := <-done; rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", rec.Code, rec.Body.String())
	}
}
