package handlers_test

// Restoring onto a name that is now occupied destroyed the occupant, silently.
//
//   - a FILE: trash.TakeBack is a rename, which on Linux replaces the
//     destination. The bytes were gone before the DB got its turn — and the
//     DB's turn then failed anyway, because the live-only unique index on
//     (storage_id, path_hash) (migration 00032) refuses a second live row at
//     the path. A 500, after the data loss.
//   - a FOLDER never failed at all: the single rename fails with ENOTEMPTY,
//     TakeBack falls back to walking the tree object by object, and the
//     trashed tree is poured into the occupying folder.
//
// The server now refuses (409 `EXISTS`) before any byte moves. Found by
// @alfatm in his fork (dbf94c23), where the refusal is also the default of a
// wider `if_exists` choice this tree does not have.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// trashNow puts a catalogued node into the trash exactly the way a delete
// does: bytes into `.filex-trash/`, row retagged with the original path in
// storage_key.
func trashNow(t *testing.T, f *restoreFixture, n *model.Node) {
	t.Helper()
	ctx := context.Background()
	out, err := trash.Put(ctx, f.drv, strings.TrimPrefix(n.Path, "/"))
	require.NoError(t, err)
	require.True(t, out.Trashed)
	trashClean := "/" + strings.Trim(out.Key, "/")
	require.NoError(t, f.store.SoftDeleteAndRetag(ctx, n.ID, trashClean,
		pathkey.Hash(f.st.ID, trashClean), n.Path))
}

// seedDirWith creates a folder on the storage with one file in it, and
// catalogues the folder (the row the user would click "restore" on).
func seedDirWith(t *testing.T, f *restoreFixture, dir, child, content string) *model.Node {
	t.Helper()
	abs := filepath.Join(f.root, filepath.FromSlash(dir))
	require.NoError(t, os.MkdirAll(abs, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(abs, child), []byte(content), 0o644))
	clean := "/" + strings.Trim(dir, "/")
	n, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID:  f.st.ID,
		Name:       filepath.Base(dir),
		Path:       clean,
		PathHash:   pathkey.Hash(f.st.ID, clean),
		StorageKey: clean,
		Type:       model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	return n
}

func (f *restoreFixture) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(strings.TrimPrefix(rel, "/"))))
	require.NoError(t, err)
	return string(b)
}

// stillTrashed asserts the row never left the trash — the other half of a
// refusal: nothing moved AND nothing was un-trashed.
func (f *restoreFixture) stillTrashed(t *testing.T, id int64, origPath string) {
	t.Helper()
	n, err := f.store.GetNode(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, n.DeletedAt, "the refused restore un-trashed the row anyway")
	assert.True(t, trash.IsTrashPath(n.Path), "the row must still point at its trash key, got %q", n.Path)
	assert.Equal(t, origPath, n.StorageKey)
}

func TestTrashRestore_FileOntoTakenName_409_OccupantBytesIntact(t *testing.T) {
	f := newRestoreFixture(t, false)

	gone := f.seedFile(t, "belgeler/rapor.txt", "the file that was deleted")
	trashNow(t, f, gone)
	// Somebody wrote a new file at that name in the meantime.
	f.seedFile(t, "belgeler/rapor.txt", "the file that is there now")

	rec := f.postJSON(t, f.trashH.Restore, map[string]any{"node_id": gone.ID})

	// The point of the test: a rename would have replaced these bytes.
	assert.Equal(t, "the file that is there now", f.read(t, "belgeler/rapor.txt"),
		"the occupant was overwritten by the restored file")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "EXISTS", body["code"])
	assert.Equal(t, "rapor.txt", body["name"])
	f.stillTrashed(t, gone.ID, "/belgeler/rapor.txt")
}

func TestTrashRestore_FolderOntoTakenName_409_NothingMerged(t *testing.T) {
	f := newRestoreFixture(t, false)

	gone := seedDirWith(t, f, "proje", "notlar.txt", "the deleted folder's copy")
	trashNow(t, f, gone)

	// A new folder of the same name, holding a same-named file (the one a merge
	// overwrites) and one of its own.
	occupied := filepath.Join(f.root, "proje")
	require.NoError(t, os.MkdirAll(occupied, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(occupied, "notlar.txt"), []byte("the new folder's copy"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(occupied, "sadece-yeni.txt"), []byte("only in the new folder"), 0o644))

	rec := f.postJSON(t, f.trashH.Restore, map[string]any{"node_id": gone.ID})

	assert.Equal(t, "the new folder's copy", f.read(t, "proje/notlar.txt"),
		"the trashed tree was poured over the occupying folder")
	assert.Equal(t, "only in the new folder", f.read(t, "proje/sadece-yeni.txt"))
	_, err := os.Stat(filepath.Join(f.root, "proje", trash.Prefix))
	assert.True(t, os.IsNotExist(err), "the restore left a trash key nested inside the occupying folder")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	f.stillTrashed(t, gone.ID, "/proje")
}

// A LIVE row at the name blocks the restore too, even with no bytes behind it:
// the unique index would refuse the row write after the bytes had moved, and
// the entry would be left in the trash pointing at an emptied key.
func TestTrashRestore_NameHeldOnlyByALiveRow_409_EntryStaysRetryable(t *testing.T) {
	ctx := context.Background()
	f := newRestoreFixture(t, false)

	gone := f.seedFile(t, "rapor.txt", "the file that was deleted")
	trashNow(t, f, gone)
	_, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.st.ID, Name: "rapor.txt", Path: "/rapor.txt",
		PathHash: pathkey.Hash(f.st.ID, "/rapor.txt"), StorageKey: "/rapor.txt",
		Type: model.NodeTypeFile,
	})
	require.NoError(t, err)

	rec := f.postJSON(t, f.trashH.Restore, map[string]any{"node_id": gone.ID})
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	f.stillTrashed(t, gone.ID, "/rapor.txt")

	row, err := f.store.GetNode(ctx, gone.ID)
	require.NoError(t, err)
	assert.Equal(t, "the file that was deleted", f.read(t, row.Path),
		"the refused restore moved the bytes out of the trash key its row still names")
}

// A restore whose bytes are no longer under the trash key answers 200 and
// un-trashes the row anyway — the documented best-effort contract. The advice
// that goes with it ("find the object under `.filex-trash/`") only works if
// something wrote down which key, and ErrNotFound was the one error filtered
// out of the warning.
func TestTrashRestore_BytesGoneFromTheTrashKey_SaysSoInTheLog(t *testing.T) {
	ctx := context.Background()
	f := newRestoreFixture(t, false)

	gone := f.seedFile(t, "belgeler/rapor.txt", "the file that was deleted")
	trashNow(t, f, gone)
	trashed, err := f.store.GetNode(ctx, gone.ID)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(f.root, filepath.FromSlash(strings.TrimPrefix(trashed.Path, "/")))))

	logs := captureLogs(t)
	rec := f.postJSON(t, f.trashH.Restore, map[string]any{"node_id": gone.ID})
	out := logs()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	back, err := f.store.GetNode(ctx, gone.ID)
	require.NoError(t, err)
	assert.Nil(t, back.DeletedAt, "the contract: the row comes back even though the bytes did not")
	assert.Contains(t, out, "trash restore move failed",
		"a 200 that delivered no bytes left no record of itself:\n%s", out)
	assert.Contains(t, out, trashed.Path, "the warning has to name the trash key:\n%s", out)
}
