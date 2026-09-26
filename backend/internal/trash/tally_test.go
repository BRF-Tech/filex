package trash

// What a folder's trip into, out of and out of the trash counts on the
// context's tally (storage.Tally): each of its objects once, and nothing else.
//
// ⚠ An object store's Delete counts every call as one object, found and done,
// whether or not anything was at the key (a DeleteObject of a missing key
// succeeds). The folder-marker clean-up after a per-object walk made two such
// calls on the job's tally, and the operations centre read "N+2 of N+2".

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// objectStore is memCore with the S3 driver's habits: keys never carry a
// leading slash, a move counts its object, and a delete counts one object per
// call — or each object under a prefix that has some.
type objectStore struct{ *memCore }

func key(p string) string { return strings.Trim(p, "/") }

func (d objectStore) Move(ctx context.Context, src, dst string) error {
	t := storage.TallyOf(ctx)
	t.Found(1)
	if err := d.move(key(src), key(dst)); err != nil {
		return err
	}
	t.Done(1)
	return nil
}

func (d objectStore) Delete(ctx context.Context, p string) error {
	t := storage.TallyOf(ctx)
	var under []string
	for _, k := range d.keys() {
		if strings.HasPrefix(k, key(p)+"/") {
			under = append(under, k)
		}
	}
	if len(under) > 0 {
		t.Found(len(under))
		for _, k := range under {
			_ = d.del(k)
			t.Done(1)
		}
		return nil
	}
	t.Found(1)
	_ = d.del(key(p))
	t.Done(1)
	return nil
}

func (d objectStore) Stat(ctx context.Context, p string) (storage.Object, error) {
	return d.memCore.Stat(ctx, key(p))
}

func (d objectStore) List(ctx context.Context, p string) ([]storage.Object, error) {
	return d.memCore.List(ctx, key(p))
}

// RED PROOF (PR #67, 2026-09-26): Put of a two-object folder by the
// per-object walk ended at 4 of 4 — the two marker deletes counted.
func TestPutCountsOnlyTheFolderObjects(t *testing.T) {
	core := newCore("proje/bir.txt", "proje/alt/iki.txt")
	tally := &storage.Tally{}
	out, err := Put(storage.WithTally(context.Background(), tally), objectStore{core}, "proje")
	require.NoError(t, err)
	require.Equal(t, 2, out.Files)
	done, total := tally.Load()
	require.EqualValues(t, 2, total, "the folder markers were counted as objects")
	require.EqualValues(t, 2, done)
}

// A restore counts the folder's objects once each, however it comes back: a
// single call that stops part-way keeps what it finished and takes back what
// it only found, and the walk that carries on counts the rest.
//
// RED PROOF (PR #67, 2026-09-26): the restore counted nothing of its own; on a
// driver that counts, the attempt's three, the walk's two and the markers'
// two made 7 found and 5 done — a restore that never reached its total.
func TestTakeBackCountsEachObjectOnce(t *testing.T) {
	const trashKey = Prefix + "/1-abc__proje"
	core := newCore(trashKey+"/bir.txt", trashKey+"/alt/iki.txt", trashKey+"/uc.txt")
	tally := &storage.Tally{}
	require.NoError(t, TakeBack(storage.WithTally(context.Background(), tally), partialTakeBack{objectStore{core}, trashKey}, trashKey, "proje"))
	require.Equal(t, []string{"proje/alt/iki.txt", "proje/bir.txt", "proje/uc.txt"}, core.keys())
	done, total := tally.Load()
	require.EqualValues(t, 3, total, "an object was counted twice, or a marker counted as one")
	require.EqualValues(t, 3, done)
}

// partialTakeBack moves the trashed folder the object-store way — finds its
// three objects, moves the first, then fails — and single objects as usual.
type partialTakeBack struct {
	objectStore
	folder string
}

func (d partialTakeBack) Move(ctx context.Context, src, dst string) error {
	if key(src) != d.folder {
		return d.objectStore.Move(ctx, src, dst)
	}
	t := storage.TallyOf(ctx)
	t.Found(3)
	if err := d.move(d.folder+"/bir.txt", key(dst)+"/bir.txt"); err != nil {
		return err
	}
	t.Done(1)
	return storage.ErrUnsupported
}

// A purge of a trashed folder counts its files, not the marker clean-up after
// them.
//
// RED PROOF (PR #67 + #63, 2026-09-26): 4 of 4 for a folder of two files.
func TestPurgeOneCountsTheFilesNotTheMarkers(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "s", Enabled: true, ConfigJSON: []byte(`{}`),
	})
	require.NoError(t, err)
	row := func(p string, parent *int64, kind model.NodeType) *model.Node {
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, ParentID: parent, Name: p[strings.LastIndex(p, "/")+1:], Path: p,
			PathHash: pathkey.Hash(st.ID, p), Type: kind, Size: 1,
		})
		require.NoError(t, err)
		return n
	}
	dir := row("/proje", nil, model.NodeTypeDirectory)
	row("/proje/a.txt", &dir.ID, model.NodeTypeFile)
	row("/proje/b.txt", &dir.ID, model.NodeTypeFile)
	const trashKey = "/" + Prefix + "/1-abc__proje"
	require.NoError(t, store.SoftDeleteAndRetag(ctx, dir.ID, trashKey, pathkey.Hash(st.ID, trashKey), "/proje"))
	core := newCore(key(trashKey)+"/a.txt", key(trashKey)+"/b.txt")
	svc := New(store, func(int64) (storage.Driver, error) { return objectStore{core}, nil }, nil)

	tally := &storage.Tally{}
	require.NoError(t, svc.PurgeOne(storage.WithTally(ctx, tally), dir.ID))
	require.Empty(t, core.keys(), "bytes were left in the trash")
	done, total := tally.Load()
	require.EqualValues(t, 2, total, "the folder's markers were counted as objects")
	require.EqualValues(t, 2, done)
}
