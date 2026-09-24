package trash_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// The purge deleted a file that had come back.
//
// ⚠⚠ Most trash rows live under `.filex-trash/`, where their bytes are. But the
// storage sync's tombstone pass soft-deletes a row WHERE IT STANDS once the
// file is found gone, and so do the queue's "already missing" branches: those
// rows keep their original path, and there are no bytes of theirs behind it.
// The purge deleted `row.path` on the driver all the same. When a file had
// reappeared at that path in the meantime — a new upload with an old name, a
// restore from backup — the purge 30 days later deleted THAT file, and for a
// folder row it deleted the whole folder that stood there again.

type purgeRig struct {
	store db.Store
	svc   *trash.Service
	root  string
	sid   int64
}

func newPurgeRig(t *testing.T) *purgeRig {
	t.Helper()
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "s", Enabled: true, ConfigJSON: []byte(`{}`),
	})
	require.NoError(t, err)
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))
	svc := trash.New(store, func(int64) (storage.Driver, error) { return drv, nil }, nil)
	return &purgeRig{store: store, svc: svc, root: root, sid: st.ID}
}

func (r *purgeRig) write(t *testing.T, rel, body string) {
	t.Helper()
	abs := filepath.Join(r.root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
}

func (r *purgeRig) row(t *testing.T, p string, kind model.NodeType) *model.Node {
	t.Helper()
	n, err := r.store.CreateNode(context.Background(), &model.Node{
		StorageID: r.sid, Name: filepath.Base(p), Path: p, StorageKey: p,
		PathHash: pathkey.Hash(r.sid, p), Type: kind, Size: 5,
	})
	require.NoError(t, err)
	return n
}

// tombstone is what the sync leaves for a file it found gone: the row,
// soft-deleted where it stood.
func (r *purgeRig) tombstone(t *testing.T, p string, kind model.NodeType) *model.Node {
	t.Helper()
	n := r.row(t, p, kind)
	require.NoError(t, r.store.SoftDeleteNode(context.Background(), n.ID))
	return n
}

func (r *purgeRig) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(rel)))
	return err == nil
}

func TestPurge_ATombstoneNeverDeletesTheFileThatCameBack(t *testing.T) {
	r := newPurgeRig(t)
	ctx := context.Background()
	old := r.tombstone(t, "/report.txt", model.NodeTypeFile)
	// The file comes back and the sync catalogues it as a NEW row.
	r.write(t, "report.txt", "brand new bytes")
	back := r.row(t, "/report.txt", model.NodeTypeFile)

	require.NoError(t, r.svc.PurgeOne(ctx, old.ID))

	_, err := r.store.GetNode(ctx, old.ID)
	assert.Error(t, err, "the tombstone row itself is purged")
	assert.True(t, r.exists("report.txt"), "the purge deleted the file that came back")
	live, err := r.store.GetNode(ctx, back.ID)
	require.NoError(t, err)
	assert.Nil(t, live.DeletedAt)
}

// Nothing has catalogued the new file yet (an on-demand storage nobody has
// synced). The tombstone's own bytes were gone before it was ever written, so
// whatever stands at its path now is somebody else's.
func TestPurge_ATombstoneNeverDeletesAnUncataloguedFileAtItsPath(t *testing.T) {
	r := newPurgeRig(t)
	old := r.tombstone(t, "/scan.pdf", model.NodeTypeFile)
	r.write(t, "scan.pdf", "arrived over SFTP, not synced yet")

	require.NoError(t, r.svc.PurgeOne(context.Background(), old.ID))
	assert.True(t, r.exists("scan.pdf"))
}

// A folder tombstone was worse: the purge deleted its path recursively, so a
// folder that stood there again went with everything in it.
func TestPurge_AFolderTombstoneNeverDeletesTheFolderThatCameBack(t *testing.T) {
	r := newPurgeRig(t)
	ctx := context.Background()
	oldDir := r.tombstone(t, "/docs", model.NodeTypeDirectory)
	oldChild := r.tombstone(t, "/docs/a.txt", model.NodeTypeFile)
	r.write(t, "docs/a.txt", "a again")
	r.write(t, "docs/new.txt", "never deleted")

	res, err := r.svc.EmptyOlderThan(ctx, 0, r.sid)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, res.Deleted, 1)

	assert.True(t, r.exists("docs/new.txt"), "the purge deleted a folder that stood there again")
	assert.True(t, r.exists("docs/a.txt"))
	for _, id := range []int64{oldDir.ID, oldChild.ID} {
		_, err := r.store.GetNode(ctx, id)
		assert.Error(t, err, "the tombstone rows are purged")
	}
}

// The everyday case is untouched: a row whose bytes are parked in the trash
// has them deleted when it is purged.
func TestPurge_ATrashedFileStillLosesItsBytes(t *testing.T) {
	r := newPurgeRig(t)
	ctx := context.Background()
	key := "/.filex-trash/1790000000-abc123__old.txt"
	r.write(t, key[1:], "trashed bytes")
	n := r.row(t, "/old.txt", model.NodeTypeFile)
	require.NoError(t, r.store.SoftDeleteAndRetag(ctx, n.ID, key, pathkey.Hash(r.sid, key), "/old.txt"))

	require.NoError(t, r.svc.PurgeOne(ctx, n.ID))
	assert.False(t, r.exists(key[1:]), "a purged trash entry must free its bytes")
}
