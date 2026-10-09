package nfssrv_test

// Issue #201: an NFS rename and remove hold the storage's row gate
// (internal/rowgate) from the first byte to the last row, as the explorer and
// the operations queue have since 0.54 (#192). Without it a storage scan
// running beside RENAME saw a renamed folder's bytes at the new name while its
// rows still sat at the old one, judged everything in it gone and dropped the
// rows; beside REMOVE it confirmed a trashed file's row gone and dropped it with
// the trash entry.
//
// Break: route fs.Rename or fs.Remove past protocolsync.Relocate / Discard
// (mover.Move + syncer.Move, trash.Put + syncer.Trash, as up to 0.54): the
// scan's judgement gets the gate half way and HeldHalfWay fails.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/nfssrv"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/gatetest"
)

// gatedHarness is the NFS harness over a driver that stops each storage's
// next Move or Delete half way (gatetest.Driver). gated(st) serves st through
// that driver; gated(st, (*gatetest.Driver).NoTrash) through its view of a
// storage that keeps no trash, where a delete is for good.
func gatedHarness(t *testing.T) (*harness, func(st *model.Storage, as ...gatetest.View) *gatetest.Driver) {
	t.Helper()
	var mu sync.Mutex
	drivers := map[int64]storage.Driver{}
	var store db.Store
	hz := newHarnessCfg(t, func(cfg *nfssrv.Config) {
		store = cfg.Store
		cfg.Resolver = func(id int64) (storage.Driver, error) {
			mu.Lock()
			d, ok := drivers[id]
			mu.Unlock()
			if ok {
				return d, nil
			}
			st, err := store.GetStorage(context.Background(), id)
			if err != nil {
				return nil, err
			}
			var c map[string]any
			if err := json.Unmarshal(st.ConfigJSON, &c); err != nil {
				return nil, err
			}
			drv := &local.Driver{}
			if err := drv.Init(context.Background(), c); err != nil {
				return nil, err
			}
			return drv, nil
		}
	})
	gated := func(st *model.Storage, as ...gatetest.View) *gatetest.Driver {
		base := &local.Driver{}
		if err := base.Init(context.Background(), map[string]any{"path": hz.rootOf(t, st)}); err != nil {
			t.Fatalf("local driver: %v", err)
		}
		d := gatetest.Wrap(base)
		t.Cleanup(d.Finish)
		mu.Lock()
		drivers[st.ID] = d.Serve(as...)
		mu.Unlock()
		return d
	}
	return hz, gated
}

// seedRow catalogues rel the way an upload would have (the folder rows above
// it too) and answers its row.
func seedRow(t *testing.T, store db.Store, st *model.Storage, rel string, size int64) *model.Node {
	t.Helper()
	n, _, ok := protocolsync.New(store, nil, nil, "test").WriteRows(context.Background(), st, rel, size, "text/plain")
	if !ok || n == nil {
		t.Fatalf("seed row %s", rel)
	}
	return n
}

func liveRowAt(store db.Store, st *model.Storage, rel string) *model.Node {
	n, err := store.GetNodeByPath(context.Background(), st.ID, pathkey.Hash(st.ID, rel))
	if err != nil {
		return nil
	}
	return n
}

func TestRenameOverNFSHoldsTheRowGate(t *testing.T) {
	hz, gated := gatedHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Leon/a.txt", []byte("a"))
	row := seedRow(t, hz.store, st, "Leon/a.txt", 1)
	d := gated(st)
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	done := make(chan error, 1)
	go func() { done <- target.Rename("/Leon", "/Leo") }()

	gatetest.HeldHalfWay(t, st.ID, d, func() bool {
		n := liveRowAt(hz.store, st, "Leo/a.txt")
		return n != nil && n.ID == row.ID
	})
	if err := <-done; err != nil {
		t.Fatalf("rename: %v", err)
	}
}

func TestRemoveOverNFSHoldsTheRowGate(t *testing.T) {
	hz, gated := gatedHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Leon/a.txt", []byte("a"))
	row := seedRow(t, hz.store, st, "Leon/a.txt", 1)
	d := gated(st)
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	done := make(chan error, 1)
	go func() { done <- target.Remove("/Leon/a.txt") }()

	gatetest.HeldHalfWay(t, st.ID, d, func() bool {
		n, err := hz.store.GetNode(context.Background(), row.ID)
		// Retagged into the trash: still there, no longer live.
		return err == nil && n != nil && n.DeletedAt != nil
	})
	if err := <-done; err != nil {
		t.Fatalf("remove: %v", err)
	}
}

// rowGone reports whether row id has left the catalogue: a delete for good
// drops it rather than retagging it into the trash.
func rowGone(store db.Store, id int64) bool {
	n, err := store.GetNode(context.Background(), id)
	return err != nil || n == nil
}

// On a storage that cannot keep what it deletes (no Move, no Copy) REMOVE is
// a delete for good, and its bytes and rows go under the gate too
// (Syncer.Purge).
//
// Break: route fs.Remove's purge branch past protocolsync.Purge (del.Delete +
// syncer.Delete, as up to 0.54): the scan's judgement gets the gate half way.
func TestPurgeOverNFSHoldsTheRowGate(t *testing.T) {
	hz, gated := gatedHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Leon/a.txt", []byte("a"))
	row := seedRow(t, hz.store, st, "Leon/a.txt", 1)
	d := gated(st, (*gatetest.Driver).NoTrash)
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	done := make(chan error, 1)
	go func() { done <- target.Remove("/Leon/a.txt") }()

	gatetest.HeldHalfWay(t, st.ID, d, func() bool { return rowGone(hz.store, row.ID) })
	if err := <-done; err != nil {
		t.Fatalf("remove: %v", err)
	}
}
