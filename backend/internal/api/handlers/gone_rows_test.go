package handlers_test

// Issue #104 (the rest of #74): what filex does with the rows of an item whose
// bytes are gone for good - the operations queue's "already missing", "could
// not trash, deleted outright" and "moved to another storage" branches
// (Manager.SyncHardDelete), and the explorer's delete of an item that is
// already gone (vfDelete).
//
//   - SyncHardDelete soft-deleted the one row WHERE IT STOOD: a trash entry with
//     nothing behind it (#74's shape, written by filex itself), and for a folder
//     its contents stayed live under a deleted parent.
//   - vfDelete hard-deleted the folder's row alone, and the parent_id cascade
//     took its contents without releasing a byte of them from the owner's quota.
//   - Neither touched the snapshots under `.versions/<id>/`, whose rows go with
//     the file's: their bytes stayed on the storage for good.
//
// Every one of them now ends in protocolsync.DeleteRows.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// goneRig is a Manager over the quota-accounting store (as in production), a
// local storage and one user who owns what the test writes.
type goneRig struct {
	mh    *handlers.Manager
	raw   db.Store
	store db.Store
	st    *model.Storage
	root  string
	owner int64
	// drv is what the Manager resolves the storage to; a test may swap it.
	drv storage.Driver
}

func newGoneRig(t *testing.T) *goneRig {
	t.Helper()
	writehook.ConfigureOverwriteGuard(nil)
	ctx := context.Background()
	_, raw := testutil.NewTestDB(t)
	acct := quotastore.New(raw)
	root := t.TempDir()
	ld := &local.Driver{}
	require.NoError(t, ld.Init(ctx, map[string]any{"root": root}))
	st, err := raw.CreateStorage(ctx, &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(root) + `"}`),
	})
	require.NoError(t, err)
	hash, err := authlocal.HashPassword("Passw0rd!123")
	require.NoError(t, err)
	u, err := raw.CreateUser(ctx, "owner@test.local", hash, model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	r := &goneRig{raw: raw, store: acct, st: st, root: root, owner: u.ID, drv: ld}
	r.mh = handlers.NewManager(acct, func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown storage %d", id)
		}
		return r.drv, nil
	})
	return r
}

// row records p in the catalogue, owned by the rig's user (a file's size
// counts against them), under parent.
func (r *goneRig) row(t *testing.T, parent *model.Node, p string, kind model.NodeType, size int64) *model.Node {
	t.Helper()
	n := &model.Node{
		StorageID: r.st.ID, Name: path.Base(p), Path: p, StorageKey: p,
		PathHash: pathkey.Hash(r.st.ID, p), Type: kind, Size: size,
	}
	if parent != nil {
		n.ParentID = &parent.ID
	}
	created, err := r.store.CreateNode(quotastore.WithOwner(context.Background(), r.owner), n)
	require.NoError(t, err)
	return created
}

// tree records /Proje, /Proje/a.txt (100 bytes), /Proje/alt and
// /Proje/alt/b.txt (200 bytes) - rows only: nothing of it is on the disk.
func (r *goneRig) tree(t *testing.T) []*model.Node {
	t.Helper()
	dir := r.row(t, nil, "/Proje", model.NodeTypeDirectory, 0)
	a := r.row(t, dir, "/Proje/a.txt", model.NodeTypeFile, 100)
	sub := r.row(t, dir, "/Proje/alt", model.NodeTypeDirectory, 0)
	b := r.row(t, sub, "/Proje/alt/b.txt", model.NodeTypeFile, 200)
	require.EqualValues(t, 300, r.usage(t), "fixture check: the owner is billed for both files")
	return []*model.Node{dir, a, sub, b}
}

// history gives n one snapshot, on the disk and in node_versions, the way
// internal/versioning records one, and returns its key.
func (r *goneRig) history(t *testing.T, n *model.Node) string {
	t.Helper()
	key := ".versions/" + strconv.FormatInt(n.ID, 10) + "/1"
	abs := filepath.Join(r.root, filepath.FromSlash(key))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte("the bytes before the last save"), 0o644))
	_, err := r.raw.CreateNodeVersion(context.Background(), &model.NodeVersion{
		NodeID: n.ID, VersionN: 1, StorageKey: key, Size: 30,
	})
	require.NoError(t, err)
	return key
}

func (r *goneRig) usage(t *testing.T) int64 {
	t.Helper()
	used, _, err := r.raw.GetUserUsage(context.Background(), r.owner)
	require.NoError(t, err)
	return used
}

// noRow: not live, not trashed, not deleted where it stood - gone.
func (r *goneRig) noRow(t *testing.T, n *model.Node) {
	t.Helper()
	got, err := r.raw.GetNode(context.Background(), n.ID)
	if err == nil && got != nil {
		t.Errorf("the row of %s is still there (deleted_at=%v, path=%s): a row whose bytes are gone for good is dropped, never kept or trashed",
			n.Path, got.DeletedAt, got.Path)
	}
}

func (r *goneRig) onDisk(rel string) bool {
	_, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(rel)))
	return err == nil
}

// ── the operations queue (SyncHardDelete) ──────────────────────────────

// A folder the queue found already gone, deleted outright, or moved to another
// storage: the folder's row and every row below it leave the catalogue, the
// owner's quota drops by both files, and nothing is left for the Trash to list.
//
// On the old code the folder's row was soft-deleted where it stood (a vanished
// row, in the Trash's eyes nothing at all) and the two files stayed LIVE under
// it, billed and searchable.
func TestSyncHardDelete_DropsTheFolderAndEveryRowBelowIt(t *testing.T) {
	r := newGoneRig(t)
	rows := r.tree(t)

	r.mh.SyncHardDelete(context.Background(), r.st.ID, "Proje")

	for _, n := range rows {
		r.noRow(t, n)
	}
	assert.EqualValues(t, 0, r.usage(t), "the files' bytes must leave the owner's quota with their rows")
	vanished, err := r.raw.ListVanishedNodeIDs(context.Background(), r.st.ID, 0, 100)
	require.NoError(t, err)
	assert.Empty(t, vanished, "no row may be left deleted where it stood")
}

// A single file: dropped, not soft-deleted in place.
func TestSyncHardDelete_AFileLeavesNoRowBehind(t *testing.T) {
	r := newGoneRig(t)
	f := r.row(t, nil, "/rapor.pdf", model.NodeTypeFile, 500)

	r.mh.SyncHardDelete(context.Background(), r.st.ID, "rapor.pdf")

	r.noRow(t, f)
	assert.EqualValues(t, 0, r.usage(t))
}

// The file's snapshots go with it: their node_versions rows cascade with the
// file's row, so nothing would ever reach `.versions/<id>/` again. Another
// file's history is not touched.
func TestSyncHardDelete_TakesTheFilesVersionHistoryWithIt(t *testing.T) {
	r := newGoneRig(t)
	gone := r.row(t, nil, "/rapor.pdf", model.NodeTypeFile, 500)
	kept := r.row(t, nil, "/kalan.pdf", model.NodeTypeFile, 10)
	goneKey := r.history(t, gone)
	keptKey := r.history(t, kept)

	r.mh.SyncHardDelete(context.Background(), r.st.ID, "rapor.pdf")

	assert.False(t, r.onDisk(goneKey), "the snapshot of a file gone for good is left on the storage")
	assert.False(t, r.onDisk(path.Dir(goneKey)), "the file's folder under .versions/ is left behind")
	assert.True(t, r.onDisk(keptKey), "another file's history was deleted")
}

// ── the explorer (vfDelete) ────────────────────────────────────────────

// A folder that is already gone from the storage (deleted in a shell, an index
// that has not caught up): every row goes, and both files leave the quota.
//
// On the old code the folder's row was hard-deleted on its own and the cascade
// took a.txt and b.txt with it: the rows were gone, the 300 bytes stayed on the
// owner's account for ever.
func TestVfDelete_AFolderAlreadyGoneReleasesEveryFilesQuota(t *testing.T) {
	r := newGoneRig(t)
	rows := r.tree(t)
	key := r.history(t, rows[3])

	rec := callMutate(t, r.mh, "delete", map[string]any{
		"path":  "main://",
		"items": []map[string]any{{"path": "main://Proje"}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	for _, n := range rows {
		r.noRow(t, n)
	}
	assert.EqualValues(t, 0, r.usage(t), "the cascade took the files' rows and left their bytes on the owner's quota")
	assert.False(t, r.onDisk(key), "the snapshot of a file in the deleted folder is left on the storage")
}

// deleteOnly is a storage that can neither move nor copy, only delete: the
// trash cannot hold its bytes, so a delete is for good (trash.ErrUnsupported).
type deleteOnly struct {
	storage.Driver
	d storage.Deleter
}

func (x deleteOnly) Delete(ctx context.Context, p string) error { return x.d.Delete(ctx, p) }

// The same on a storage that keeps no trash: the bytes are deleted outright,
// and every row below the folder goes with its own quota.
func TestVfDelete_AFolderOnAStorageWithNoTrashReleasesEveryFilesQuota(t *testing.T) {
	r := newGoneRig(t)
	ld := r.drv.(*local.Driver)
	r.drv = deleteOnly{Driver: ld, d: ld}
	rows := r.tree(t)
	for _, rel := range []string{"Proje/a.txt", "Proje/alt/b.txt"} {
		abs := filepath.Join(r.root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte("x"), 0o644))
	}

	rec := callMutate(t, r.mh, "delete", map[string]any{
		"path":  "main://",
		"items": []map[string]any{{"path": "main://Proje"}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.False(t, r.onDisk("Proje"), "fixture check: the storage deleted the folder")
	for _, n := range rows {
		r.noRow(t, n)
	}
	assert.EqualValues(t, 0, r.usage(t))
}
