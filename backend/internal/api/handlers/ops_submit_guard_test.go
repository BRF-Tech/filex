package handlers_test

// POST /api/files/ops, the queue's generic door: what a client may ask of it,
// and where.
//
// The queue's funnel (ops.SubmitTo) takes every kind the server queues, and
// most of them are judged by the handler that queues them — a rename by
// vfRename, a restore by mayRestore, a purge by Purge. This door judges
// copies, moves and deletes, and nothing else may come through it.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
)

// RED PROOF (before clientKinds, 2026-09-26): `kind: "restore"` answered 202
// and the worker restored the node id it was given — any node id, of any
// tenant, a hidden open-with copy, a node outside a root token — without one
// of mayRestore's checks; `rename` answered 202 past vfRename's read-only and
// name checks, `purge` 202 for any entry.
func TestOpsSubmit_TheGenericDoorTakesOnlyCopyMoveAndDelete(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	n := f.seedFile(t, f.StA, f.RootA, "gizli.txt", "somebody's file")
	status, body := doJSON(t, f.AdminA, http.MethodPost, f.URL+"/api/files/manager?action=delete", map[string]any{
		"path": "alpha://", "items": []map[string]string{{"path": "alpha://gizli.txt"}},
	})
	require.Equal(t, http.StatusOK, status, "%v", body)
	id := strconv.FormatInt(n.ID, 10)

	for kind, req := range map[string]map[string]any{
		ops.OpRestore:      {"sources": []string{id}},
		ops.OpPurge:        {"sources": []string{id}},
		ops.OpRename:       {"sources": []string{"a.txt"}, "dest": "b.txt"},
		ops.OpUploadCommit: {"sources": []string{"upload-1"}},
		ops.OpPluginAction: {"sources": []string{"a.txt"}, "dest": "job-1"},
		"trash-empty":      {"sources": []string{"days=0"}},
		"archive-create":   {"sources": []string{"a.txt"}, "dest": "a.zip"},
	} {
		req["kind"], req["storage_id"] = kind, f.StA.ID
		status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/ops", req)
		assert.Equal(t, http.StatusBadRequest, status, "kind %q was let through the generic door: %v", kind, body)
	}

	list, err := f.Ops.List(ctx, "")
	require.NoError(t, err)
	assert.Empty(t, list, "a kind the door refuses was queued")
	f.drainOps(t)
	row, err := f.Store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	assert.NotNil(t, row.DeletedAt, "the trash entry was restored through the generic door")
}

// A `root:` token stays inside its root on this door too.
//
// RED PROOF (v0.46.0 and before, 2026-09-26): confine.Middleware rewrites the
// body keys it knows (`source`, `target`, …), and this body names its paths
// `sources` and `dest` beside a bare `storage_id`. A token confined to
// alpha://Ekip answered 202 to a delete of alpha://Gizli/rapor.txt, to a copy
// into alpha://Gizli/, and to a move into another storage — and the worker
// carried them out.
func TestOpsSubmit_ARootTokenStaysInItsRootOnTheGenericDoor(t *testing.T) {
	f := newMTFix(t, false)
	f.seedFile(t, f.StA, f.RootA, "Ekip/a.txt", "inside the root")
	f.seedFile(t, f.StA, f.RootA, "Gizli/rapor.txt", "outside the root")
	tok := issueToken(t, f.Store, f.UserA, "read,write,delete,root:alpha://Ekip", nil)

	for what, req := range map[string]map[string]any{
		"a delete outside the root": {"kind": "delete", "storage_id": f.StA.ID, "sources": []string{"Gizli/rapor.txt"}},
		"a delete walking out":      {"kind": "delete", "storage_id": f.StA.ID, "sources": []string{"Ekip/../Gizli/rapor.txt"}},
		"a copy out of the root":    {"kind": "copy", "storage_id": f.StA.ID, "sources": []string{"Ekip/a.txt"}, "dest": "Gizli/"},
		"a move into another storage": {"kind": "move", "storage_id": f.StA.ID, "dest_storage_id": f.StA2.ID,
			"sources": []string{"Ekip/a.txt"}, "dest": "/"},
		"a delete in another storage": {"kind": "delete", "storage_id": f.StA2.ID, "sources": []string{"Ekip/a.txt"}},
	} {
		status, body := fxPost(t, f.URL+"/api/files/ops", tok, req)
		assert.Equal(t, http.StatusForbidden, status, "%s was queued: %s", what, body)
	}

	// Inside its root it still works.
	status, body := fxPost(t, f.URL+"/api/files/ops", tok, map[string]any{
		"kind": "copy", "storage_id": f.StA.ID, "sources": []string{"Ekip/a.txt"}, "dest": "Ekip/",
	})
	require.Equal(t, http.StatusAccepted, status, body)

	f.drainOps(t)
	assert.FileExists(t, filepath.Join(f.RootA, "Gizli", "rapor.txt"), "a file outside the root was deleted")
	_, err := os.Stat(filepath.Join(f.RootA, "Gizli", "a.txt"))
	assert.True(t, os.IsNotExist(err), "a copy landed outside the root")
	assert.FileExists(t, filepath.Join(f.RootA, "Ekip", "a.txt"), "a file was moved out of the root")
	assert.FileExists(t, filepath.Join(f.RootA, "Ekip", "a-copy.txt"), "the copy inside the root did not run")
}

// A read-only storage is not changed by a queued operation: nothing is moved
// or deleted out of it, and nothing copied or moved into it — on either door.
// Reading from it (a copy out) is still fine.
//
// RED PROOF (v0.46.0 and before, 2026-09-26): the per-verb doors asked only
// about a destination named with an adapter, the generic door about nothing.
// A delete and a move out of the read-only storage answered 202 on both
// doors, a copy into it 202 on the generic one, and the worker carried them
// out: the file was gone from the read-only storage after the drain.
func TestOpsSubmit_AReadOnlyStorageIsNotChanged(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	f.seedFile(t, f.StA2, f.RootA2, "arsiv.txt", "kept as it is")
	f.seedFile(t, f.StA, f.RootA, "yeni.txt", "a writable storage's file")
	ro, err := f.Store.GetStorage(ctx, f.StA2.ID)
	require.NoError(t, err)
	ro.ReadOnly = true
	require.NoError(t, f.Store.UpdateStorage(ctx, ro))

	for what, call := range map[string]struct {
		url  string
		body map[string]any
	}{
		"a delete out of it":         {"/api/files/delete", map[string]any{"source": []string{"alpha-arsiv://arsiv.txt"}}},
		"a move out of it":           {"/api/files/move", map[string]any{"source": []string{"alpha-arsiv://arsiv.txt"}, "target": "alpha://"}},
		"a copy into it":             {"/api/files/copy", map[string]any{"source": []string{"alpha://yeni.txt"}, "target": "alpha-arsiv://"}},
		"a generic delete out of it": {"/api/files/ops", map[string]any{"kind": "delete", "storage_id": f.StA2.ID, "sources": []string{"arsiv.txt"}}},
		"a generic move out of it": {"/api/files/ops", map[string]any{"kind": "move", "storage_id": f.StA2.ID,
			"dest_storage_id": f.StA.ID, "sources": []string{"arsiv.txt"}, "dest": "/"}},
		"a generic copy into it": {"/api/files/ops", map[string]any{"kind": "copy", "storage_id": f.StA.ID,
			"dest_storage_id": f.StA2.ID, "sources": []string{"yeni.txt"}, "dest": "/"}},
		"a generic copy within it": {"/api/files/ops", map[string]any{"kind": "copy", "storage_id": f.StA2.ID,
			"sources": []string{"arsiv.txt"}, "dest": "/"}},
	} {
		status, body := doJSON(t, f.A, http.MethodPost, f.URL+call.url, call.body)
		assert.Equal(t, http.StatusForbidden, status, "%s was queued: %v", what, body)
		assert.Equal(t, "READ_ONLY", body["code"], "%s: %v", what, body)
	}

	// Reading from it is not changing it.
	status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/copy", map[string]any{
		"source": []string{"alpha-arsiv://arsiv.txt"}, "target": "alpha://",
	})
	require.Equal(t, http.StatusAccepted, status, "%v", body)

	f.drainOps(t)
	got, err := os.ReadDir(f.RootA2)
	require.NoError(t, err)
	names := make([]string, 0, len(got))
	for _, e := range got {
		names = append(names, e.Name())
	}
	assert.Equal(t, []string{"arsiv.txt"}, names, "the read-only storage was changed")
	assert.FileExists(t, filepath.Join(f.RootA, "arsiv.txt"), "the copy out of it did not run")
}
