package sync_test

// Issue #74 — "Items deleted outside FileX are incorrectly shown in Trash".
//
// A file or folder deleted outside filex (a shell, another program on the
// disk, `aws s3 rm`) was found gone by the next scan, and the scan soft-deleted
// its row WHERE IT STOOD. The Trash lists every soft-deleted row, so the item
// showed up there with a Restore — but its bytes were never parked in
// `.filex-trash/`, and there was nothing to restore.
//
// Now a row whose object is confirmed gone is dropped from the catalogue, the
// Trash holds only what filex itself put there, and the rows an earlier
// version left behind are dropped on the next scan. What must NOT change: a
// delete made in filex still goes to the Trash and still restores; the 30%
// guard still stops a listing that came back short; an object that comes back
// is catalogued as a new file; nothing is removed that the scan could not
// confirm gone.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// gone reports whether the row with id no longer exists at all — not live,
// not trashed.
func gone(t *testing.T, store db.Store, id int64) bool {
	t.Helper()
	n, err := store.GetNode(context.Background(), id)
	return err != nil || n == nil
}

// fillers writes n files that stay put, so what a test deletes stays well
// under the 30% the whole-listing guard allows.
func fillers(t *testing.T, root string, n int) {
	t.Helper()
	for i := range n {
		writeUnder(t, root, fmt.Sprintf("kalici/f%02d.txt", i), "x")
	}
}

func mustNode(t *testing.T, store db.Store, storageID int64, p string) *model.Node {
	t.Helper()
	n := node(t, store, storageID, p)
	require.NotNil(t, n, "no catalogue row for %s", p)
	return n
}

// ---------------------------------------------------------------------------
// 1. deleted outside filex: not in the trash, on both storage shapes
// ---------------------------------------------------------------------------

func TestExternallyDeletedFileAndFolder_AreNotInTheTrash_Local(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	writeUnder(t, root, "notlar.txt", "silinecek")
	writeUnder(t, root, "Proje/a.txt", "a")
	writeUnder(t, root, "Proje/alt/b.txt", "b")
	writeUnder(t, root, "kalan.txt", "kalir")
	fillers(t, root, 20)

	runSync(t, store, st)
	file := mustNode(t, store, st.ID, "/notlar.txt")
	dir := mustNode(t, store, st.ID, "/Proje")
	a := mustNode(t, store, st.ID, "/Proje/a.txt")
	sub := mustNode(t, store, st.ID, "/Proje/alt")
	b := mustNode(t, store, st.ID, "/Proje/alt/b.txt")

	// Deleted from the shell, not through filex.
	require.NoError(t, os.Remove(filepath.Join(root, "notlar.txt")))
	require.NoError(t, os.RemoveAll(filepath.Join(root, "Proje")))

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	assert.Zero(t, trashCount(t, store, st.ID),
		"a file deleted outside filex is in the Trash, with a Restore that has nothing to bring back")
	for _, n := range []*model.Node{file, dir, a, sub, b} {
		assert.True(t, gone(t, store, n.ID), "the row of %s is still in the catalogue", n.Path)
	}
	assert.Equal(t, 5, run.Deleted, "the run reports what it removed")
	assert.NotNil(t, node(t, store, st.ID, "/kalan.txt"), "a file still on the disk was removed")
}

func TestExternallyDeletedFileAndFolder_AreNotInTheTrash_ObjectStore(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, _, bucket := objStorage(t, store)
	bucket.objs["tek.txt"] = []byte("silinecek")
	bucket.objs["klasor/a.txt"] = []byte("a")
	bucket.objs["klasor/alt/b.txt"] = []byte("b")
	bucket.objs["kalan.txt"] = []byte("kalir")
	for i := range 20 {
		bucket.objs[fmt.Sprintf("kalici/f%02d.txt", i)] = []byte("x")
	}

	runSync(t, store, st)
	ids := []int64{
		mustNode(t, store, st.ID, "/tek.txt").ID,
		mustNode(t, store, st.ID, "/klasor").ID,
		mustNode(t, store, st.ID, "/klasor/a.txt").ID,
		mustNode(t, store, st.ID, "/klasor/alt").ID,
		mustNode(t, store, st.ID, "/klasor/alt/b.txt").ID,
	}

	bucket.mu.Lock()
	delete(bucket.objs, "tek.txt")
	delete(bucket.objs, "klasor/a.txt")
	delete(bucket.objs, "klasor/alt/b.txt")
	bucket.mu.Unlock()

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	assert.Zero(t, trashCount(t, store, st.ID), "object-store shape: a deleted key landed in the Trash")
	for _, id := range ids {
		assert.True(t, gone(t, store, id), "row %d is still in the catalogue", id)
	}
	assert.NotNil(t, node(t, store, st.ID, "/kalan.txt"))
}

// ---------------------------------------------------------------------------
// 2. deleted in filex: unchanged — in the trash, restorable
// ---------------------------------------------------------------------------

func TestDeletedInFilex_StaysInTheTrashAndRestores_Local(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	writeUnder(t, root, "rapor.txt", "kullanicinin dosyasi")
	writeUnder(t, root, "disarida.txt", "disaridan silinecek")
	fillers(t, root, 10)

	runSync(t, store, st)
	outside := mustNode(t, store, st.ID, "/disarida.txt")
	deleted := deleteLikeTheManager(t, store, st, drv, "rapor.txt")
	require.NoError(t, os.Remove(filepath.Join(root, "disarida.txt")))

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	require.Equal(t, 1, trashCount(t, store, st.ID), "the Trash holds exactly what filex put there")
	require.True(t, gone(t, store, outside.ID), "the file deleted outside filex is still catalogued")
	still, err := store.GetNode(ctx, deleted.ID)
	require.NoError(t, err, "the scan removed a deletion made in filex")
	require.NotNil(t, still.DeletedAt)

	svc := trash.New(store, func(int64) (storage.Driver, error) { return drv, nil }, nil)
	require.NoError(t, svc.Restore(ctx, deleted.ID))
	back, err := store.GetNode(ctx, deleted.ID)
	require.NoError(t, err)
	assert.Nil(t, back.DeletedAt)
	body, err := os.ReadFile(filepath.Join(root, "rapor.txt"))
	require.NoError(t, err, "the restore did not put the bytes back")
	assert.Equal(t, "kullanicinin dosyasi", string(body))
}

// ---------------------------------------------------------------------------
// 3. the object comes back
// ---------------------------------------------------------------------------

func TestExternallyDeletedFile_ThatComesBack_IsANewFile(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	writeUnder(t, root, "veri.csv", "eski")
	fillers(t, root, 10)
	runSync(t, store, st)
	old := mustNode(t, store, st.ID, "/veri.csv")

	require.NoError(t, os.Remove(filepath.Join(root, "veri.csv")))
	waitPastSecondBoundary()
	runSync(t, store, st)
	require.True(t, gone(t, store, old.ID))

	writeUnder(t, root, "veri.csv", "yeni icerik")
	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	back := mustNode(t, store, st.ID, "/veri.csv")
	assert.NotEqual(t, old.ID, back.ID, "the new bytes must not inherit the deleted file's identity")
	assert.Nil(t, back.DeletedAt)
	assert.Equal(t, int64(len("yeni icerik")), back.Size)
	assert.Zero(t, trashCount(t, store, st.ID))
}

// ---------------------------------------------------------------------------
// 4. the guards still hold
// ---------------------------------------------------------------------------

// The 30% guard: a listing that came back with far fewer entries than the
// last good run removes nothing, even though every missing file is gone.
func TestExternalDeletes_TheWholeListingGuardStillHolds(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	for i := range 10 {
		writeUnder(t, root, fmt.Sprintf("f%02d.txt", i), "x")
	}
	runSync(t, store, st)
	var ids []int64
	for i := range 6 {
		ids = append(ids, mustNode(t, store, st.ID, fmt.Sprintf("/f%02d.txt", i)).ID)
		require.NoError(t, os.Remove(filepath.Join(root, fmt.Sprintf("f%02d.txt", i))))
	}

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)
	assert.Zero(t, run.Deleted, "the guard tripped, so nothing may be removed")
	for _, id := range ids {
		n, err := store.GetNode(context.Background(), id)
		require.NoError(t, err, "the guard tripped and the row was removed anyway")
		assert.Nil(t, n.DeletedAt)
	}
	assert.Zero(t, trashCount(t, store, st.ID))
}

// scriptDriver answers List and Stat from tables, for the shapes a real disk
// will not produce on demand.
type scriptDriver struct {
	mu      sync.Mutex
	list    map[string][]storage.Object
	listErr map[string]error
	// statErr: what Stat answers for a path (normalised, no leading slash);
	// absent means "it is there".
	statErr map[string]error
}

func (d *scriptDriver) Init(context.Context, map[string]any) error { return nil }
func (d *scriptDriver) Name() string                               { return "script-fake" }
func (d *scriptDriver) Capabilities() storage.Capabilities {
	return storage.Capabilities{Read: true}
}
func (d *scriptDriver) Read(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrUnsupported
}
func (d *scriptDriver) List(_ context.Context, p string) ([]storage.Object, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := strings.Trim(p, "/")
	if err := d.listErr[k]; err != nil {
		return nil, err
	}
	return append([]storage.Object(nil), d.list[k]...), nil
}
func (d *scriptDriver) Stat(_ context.Context, p string) (storage.Object, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := strings.Trim(p, "/")
	if err, ok := d.statErr[k]; ok && err != nil {
		return storage.Object{}, err
	}
	return storage.Object{Path: k, Name: path.Base(k), Kind: storage.KindFile, Size: 1, Mtime: time.Now()}, nil
}

func scriptStorage(t *testing.T, store db.Store, d *scriptDriver) *model.Storage {
	t.Helper()
	name := fmt.Sprintf("script-%d", time.Now().UnixNano())
	storage.Register(name, func() storage.Driver { return d })
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: name, MountPath: "/" + name, ConfigJSON: []byte(`{}`),
		SyncMode: model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	return st
}

func seedRowUnder(t *testing.T, store db.Store, st *model.Storage, p string, typ model.NodeType, parent *int64) *model.Node {
	t.Helper()
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID, ParentID: parent, Name: path.Base(p), Path: p,
		PathHash: pathkey.Hash(st.ID, p), StorageKey: p, Type: typ, Size: 1,
	})
	require.NoError(t, err)
	return n
}

// A folder row goes only when the folder is confirmed gone by its own Stat
// AND nothing is left under it: a child whose Stat still sees it keeps the
// folder, or the parent_id cascade would take the child with it.
func TestExternalDeletes_AKeptChildKeepsItsFolder(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	d := &scriptDriver{
		list: map[string][]storage.Object{"": {}},
		statErr: map[string]error{
			"Arsiv":         storage.ErrNotFound,
			"Arsiv/yok.txt": storage.ErrNotFound,
			// Arsiv/var.txt: the listing missed it, Stat still sees it.
		},
	}
	st := scriptStorage(t, store, d)
	dir := seedRowUnder(t, store, st, "/Arsiv", model.NodeTypeDirectory, nil)
	missing := seedRowUnder(t, store, st, "/Arsiv/yok.txt", model.NodeTypeFile, &dir.ID)
	there := seedRowUnder(t, store, st, "/Arsiv/var.txt", model.NodeTypeFile, &dir.ID)

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	assert.True(t, gone(t, store, missing.ID), "the file confirmed gone is removed")
	kept, err := store.GetNode(context.Background(), there.ID)
	require.NoError(t, err, "the parent_id cascade took a file the scan kept")
	assert.Nil(t, kept.DeletedAt)
	folder, err := store.GetNode(context.Background(), dir.ID)
	require.NoError(t, err, "a folder with a kept child was removed")
	assert.Nil(t, folder.DeletedAt)
	assert.Zero(t, trashCount(t, store, st.ID))
}

// A folder whose Stat still sees it is not gone, whatever the listing said.
func TestExternalDeletes_AFolderStatStillSeesIsKept(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	d := &scriptDriver{list: map[string][]storage.Object{"": {}}, statErr: map[string]error{}}
	st := scriptStorage(t, store, d)
	dir := seedRowUnder(t, store, st, "/Bos", model.NodeTypeDirectory, nil)

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)
	n, err := store.GetNode(context.Background(), dir.ID)
	require.NoError(t, err, "a folder the driver still reports was removed on the listing alone")
	assert.Nil(t, n.DeletedAt)
}

// Below a folder whose listing failed, "unseen" says nothing — even when a
// Stat answers not-found (a mount that went away under a folder that stayed).
func TestExternalDeletes_NothingBelowAFolderTheScanCouldNotListIsRemoved(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	d := &scriptDriver{
		list: map[string][]storage.Object{
			"": {{Path: "paylasim", Name: "paylasim", Kind: storage.KindDirectory}},
		},
		listErr: map[string]error{"paylasim": errors.New("input/output error")},
		statErr: map[string]error{"paylasim/belge.pdf": storage.ErrNotFound},
	}
	st := scriptStorage(t, store, d)
	dir := seedRowUnder(t, store, st, "/paylasim", model.NodeTypeDirectory, nil)
	doc := seedRowUnder(t, store, st, "/paylasim/belge.pdf", model.NodeTypeFile, &dir.ID)

	waitPastSecondBoundary()
	runSync(t, store, st)

	n, err := store.GetNode(context.Background(), doc.ID)
	require.NoError(t, err, "a row below a folder the scan could not list was removed")
	assert.Nil(t, n.DeletedAt, "a row below a folder the scan could not list was trashed")
}

// ---------------------------------------------------------------------------
// 5. the upgrade: what an earlier version left in the trash
// ---------------------------------------------------------------------------

// Every install that ran 0.47 or earlier has rows its scans soft-deleted where
// they stood. They leave the Trash (see the trash handler) and the next scan
// drops them; a real deletion in the same trash is not touched.
func TestUpgrade_ARowAnEarlierScanLeftInTheTrashIsDropped(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	writeUnder(t, root, "gercek.txt", "filex icinde silinecek")
	writeUnder(t, root, "Eski/dosya.txt", "disarida silindi")
	writeUnder(t, root, "Duran/icinde.txt", "disarida silindi")
	writeUnder(t, root, "Duran/kalan.txt", "hala orada")
	runSync(t, store, st)

	real := deleteLikeTheManager(t, store, st, drv, "gercek.txt")
	oldDir := mustNode(t, store, st.ID, "/Eski")
	oldFile := mustNode(t, store, st.ID, "/Eski/dosya.txt")
	inLive := mustNode(t, store, st.ID, "/Duran/icinde.txt")
	liveDir := mustNode(t, store, st.ID, "/Duran")

	// What 0.47's scan did to things deleted outside filex: soft-deleted in
	// place, bytes gone.
	require.NoError(t, os.RemoveAll(filepath.Join(root, "Eski")))
	require.NoError(t, os.Remove(filepath.Join(root, "Duran", "icinde.txt")))
	for _, n := range []*model.Node{oldFile, oldDir, inLive} {
		require.NoError(t, store.SoftDeleteNode(ctx, n.ID))
	}
	require.Equal(t, 4, trashCount(t, store, st.ID), "precondition: the damage is done")

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	for _, n := range []*model.Node{oldFile, oldDir, inLive} {
		assert.True(t, gone(t, store, n.ID), "the 0.47 tombstone of %s is still there", n.Path)
	}
	assert.Equal(t, 1, trashCount(t, store, st.ID), "only the deletion made in filex is left in the Trash")
	still, err := store.GetNode(ctx, real.ID)
	require.NoError(t, err)
	assert.NotNil(t, still.DeletedAt)
	dirNow, err := store.GetNode(ctx, liveDir.ID)
	require.NoError(t, err, "a live folder went with an old tombstone under it")
	assert.Nil(t, dirNow.DeletedAt)
	assert.NotNil(t, node(t, store, st.ID, "/Duran/kalan.txt"))

	// Nothing on the disk was touched by any of it.
	onDisk, err := filepath.Glob(filepath.Join(root, trash.Prefix, "*__gercek.txt"))
	require.NoError(t, err)
	assert.Len(t, onDisk, 1, "the trashed bytes must still be there")
}

// A folder that vanished while earlier tombstones of its contents still name it
// as their parent: those would pin it for good (and a purge of the folder
// would cascade them without releasing anything). They go with it — on a
// folder rescan too, which does not run the full pass's repair (dropVanished).
func TestExternalDeletes_AFolderTakesTheOldTombstonesUnderIt(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	writeUnder(t, root, "Ust/Klasor/once.txt", "once")
	writeUnder(t, root, "Ust/Klasor/sonra.txt", "sonra")
	for i := range 10 {
		writeUnder(t, root, fmt.Sprintf("Ust/kalan%02d.txt", i), "x")
	}
	w, _ := runSync(t, store, st)
	dir := mustNode(t, store, st.ID, "/Ust/Klasor")
	earlier := mustNode(t, store, st.ID, "/Ust/Klasor/once.txt")
	later := mustNode(t, store, st.ID, "/Ust/Klasor/sonra.txt")
	// What 0.47's scan left of a file deleted outside filex earlier on.
	require.NoError(t, os.Remove(filepath.Join(root, "Ust", "Klasor", "once.txt")))
	require.NoError(t, store.SoftDeleteNode(ctx, earlier.ID))

	require.NoError(t, os.RemoveAll(filepath.Join(root, "Ust", "Klasor")))
	waitPastSecondBoundary()
	res, err := w.RescanFolder(ctx, st.ID, "/Ust")
	require.NoError(t, err)
	require.Empty(t, res.RemovalSkipped)

	for _, id := range []int64{dir.ID, earlier.ID, later.ID} {
		assert.True(t, gone(t, store, id), "row %d is still in the catalogue", id)
	}
	assert.Equal(t, 2, res.Removed, "the folder and the file the rescan found gone")
	assert.Zero(t, trashCount(t, store, st.ID))
}
