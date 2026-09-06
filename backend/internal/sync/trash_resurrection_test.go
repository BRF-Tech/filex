package sync_test

// The storage sync un-deleted soft-deleted rows.
//
// Deleting a file in filex is a RENAME, not a removal: the bytes move to
// `.filex-trash/<unix>-<rand>__<base>` and the node row is soft-deleted and
// retagged to that key. Quarantine does the identical thing — the antivirus
// job calls the same SoftDeleteAndRetag with the same key shape — so the two
// are indistinguishable in the catalogue and share every bug.
//
// The sync worker then walked the storage from "/" downwards with no idea any
// of that existed. It saw an object at `.filex-trash/...`, found no LIVE row
// for it, found a soft-deleted one, and — by a rule written for a different
// problem (issue #5: a stray delete wedging the unique index) — cleared
// deleted_at. The file left the trash on its own, and an infected file left
// quarantine on its own, at the next pass.
//
// These tests are the red proof. They fail on the code as it was.

import (
	"context"
	"encoding/json"
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
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// ---------------------------------------------------------------------------
// local-filesystem fixture — a real driver over a real temp dir
// ---------------------------------------------------------------------------

func localStorage(t *testing.T, store db.Store) (*model.Storage, storage.Driver, string) {
	t.Helper()
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": root}))
	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "yerel",
		Driver:     "local",
		MountPath:  "/yerel",
		ConfigJSON: cfg,
		SyncMode:   model.SyncModeOnDemand,
		Enabled:    true,
	})
	require.NoError(t, err)
	return st, drv, root
}

// ---------------------------------------------------------------------------
// object-store fixture — the shape fm.example.com runs on: S3, where a directory is
// not a thing that exists. Only keys exist; every "folder" in a listing is a
// prefix synthesised on the way out, and there is no object to Stat for one.
// ---------------------------------------------------------------------------

var objBuckets struct {
	sync.Mutex
	m map[string]*objBucket
}

type objBucket struct {
	mu   sync.Mutex
	objs map[string][]byte
}

func newObjBucket(t *testing.T) string {
	t.Helper()
	objBuckets.Lock()
	defer objBuckets.Unlock()
	if objBuckets.m == nil {
		objBuckets.m = map[string]*objBucket{}
	}
	name := fmt.Sprintf("bucket-%s-%d", t.Name(), time.Now().UnixNano())
	objBuckets.m[name] = &objBucket{objs: map[string][]byte{}}
	return name
}

func bucketOf(name string) *objBucket {
	objBuckets.Lock()
	defer objBuckets.Unlock()
	return objBuckets.m[name]
}

type objDriver struct{ b *objBucket }

func init() {
	storage.Register("objstore-test", func() storage.Driver { return &objDriver{} })
}

func (d *objDriver) Init(_ context.Context, cfg map[string]any) error {
	name, _ := cfg["bucket"].(string)
	d.b = bucketOf(name)
	if d.b == nil {
		return fmt.Errorf("objstore-test: unknown bucket %q", name)
	}
	return nil
}

func (d *objDriver) Name() string { return "objstore-test" }

func (d *objDriver) Capabilities() storage.Capabilities {
	return storage.Capabilities{Read: true, Write: true, Move: true, Copy: true, Delete: true}
}

func objKey(p string) string { return strings.Trim(path.Clean("/"+p), "/") }

// List synthesises directory entries from key prefixes — no folder objects.
func (d *objDriver) List(_ context.Context, p string) ([]storage.Object, error) {
	prefix := objKey(p)
	if prefix != "" {
		prefix += "/"
	}
	d.b.mu.Lock()
	defer d.b.mu.Unlock()
	files := map[string]int64{}
	dirs := map[string]bool{}
	for k, v := range d.b.objs {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := strings.TrimPrefix(k, prefix)
		if rest == "" {
			continue
		}
		if i := strings.Index(rest, "/"); i >= 0 {
			dirs[rest[:i]] = true
			continue
		}
		files[rest] = int64(len(v))
	}
	out := make([]storage.Object, 0, len(files)+len(dirs))
	for name := range dirs {
		out = append(out, storage.Object{
			Path: prefix + name, Name: name, Kind: storage.KindDirectory,
		})
	}
	for name, size := range files {
		out = append(out, storage.Object{
			Path: prefix + name, Name: name, Kind: storage.KindFile,
			Size: size, Etag: fmt.Sprintf("%d", size), Mtime: time.Now(),
		})
	}
	return out, nil
}

// Stat answers for objects only. A prefix has no object, exactly like S3.
func (d *objDriver) Stat(_ context.Context, p string) (storage.Object, error) {
	k := objKey(p)
	d.b.mu.Lock()
	defer d.b.mu.Unlock()
	v, ok := d.b.objs[k]
	if !ok {
		return storage.Object{}, storage.ErrNotFound
	}
	return storage.Object{
		Path: k, Name: path.Base(k), Kind: storage.KindFile,
		Size: int64(len(v)), Etag: fmt.Sprintf("%d", len(v)), Mtime: time.Now(),
	}, nil
}

func (d *objDriver) Read(_ context.Context, p string) (io.ReadCloser, error) {
	k := objKey(p)
	d.b.mu.Lock()
	defer d.b.mu.Unlock()
	v, ok := d.b.objs[k]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(string(v))), nil
}

func (d *objDriver) Write(_ context.Context, p string, r io.Reader, _ int64) error {
	buf, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	d.b.mu.Lock()
	defer d.b.mu.Unlock()
	d.b.objs[objKey(p)] = buf
	return nil
}

func (d *objDriver) Copy(_ context.Context, src, dst string) error {
	d.b.mu.Lock()
	defer d.b.mu.Unlock()
	v, ok := d.b.objs[objKey(src)]
	if !ok {
		return storage.ErrNotFound
	}
	d.b.objs[objKey(dst)] = v
	return nil
}

func (d *objDriver) Move(_ context.Context, src, dst string) error {
	d.b.mu.Lock()
	defer d.b.mu.Unlock()
	v, ok := d.b.objs[objKey(src)]
	if !ok {
		return storage.ErrNotFound
	}
	d.b.objs[objKey(dst)] = v
	delete(d.b.objs, objKey(src))
	return nil
}

func (d *objDriver) Delete(_ context.Context, p string) error {
	d.b.mu.Lock()
	defer d.b.mu.Unlock()
	delete(d.b.objs, objKey(p))
	return nil
}

func objStorage(t *testing.T, store db.Store) (*model.Storage, storage.Driver, *objBucket) {
	t.Helper()
	bucket := newObjBucket(t)
	drv := &objDriver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"bucket": bucket}))
	cfg, _ := json.Marshal(map[string]any{"bucket": bucket})
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "kova",
		Driver:     "objstore-test",
		MountPath:  "/kova",
		ConfigJSON: cfg,
		SyncMode:   model.SyncModeOnDemand,
		Enabled:    true,
	})
	require.NoError(t, err)
	return st, drv, bucketOf(bucket)
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// deleteLikeTheManager reproduces vfDelete exactly: trash.Put moves the bytes,
// SoftDeleteAndRetag flips the row and retags it to the trash key.
func deleteLikeTheManager(t *testing.T, store db.Store, st *model.Storage, drv storage.Driver, rel string) *model.Node {
	t.Helper()
	ctx := context.Background()
	clean := path.Clean("/" + strings.Trim(rel, "/"))
	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, clean))
	require.NoError(t, err)
	require.NotNil(t, n, "no catalogue row for %s", clean)

	out, err := trash.Put(ctx, drv, strings.TrimPrefix(clean, "/"))
	require.NoError(t, err)
	require.True(t, out.Trashed, "trash.Put did not park the bytes")

	trashClean := path.Clean("/" + strings.Trim(out.Key, "/"))
	require.NoError(t, store.SoftDeleteAndRetag(ctx, n.ID, trashClean, pathkey.Hash(st.ID, trashClean), clean))

	got, err := store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	require.NotNil(t, got.DeletedAt, "precondition: the row must be soft-deleted")
	require.True(t, strings.HasPrefix(got.Path, "/"+trash.Prefix+"/"), got.Path)
	return got
}

func trashCount(t *testing.T, store db.Store, storageID int64) int {
	t.Helper()
	_, total, err := store.ListTrashed(context.Background(), &storageID, 500, 0)
	require.NoError(t, err)
	return total
}

// ---------------------------------------------------------------------------
// 1. the ordinary deletion
// ---------------------------------------------------------------------------

func TestSyncDoesNotUndeleteAnOrdinaryDeletion_Local(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	require.NoError(t, os.WriteFile(filepath.Join(root, "rapor.txt"), []byte("kullanicinin dosyasi"), 0o644))

	runSync(t, store, st)
	deleted := deleteLikeTheManager(t, store, st, drv, "rapor.txt")
	require.Equal(t, 1, trashCount(t, store, st.ID), "precondition: one item in the trash")

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	after, err := store.GetNode(context.Background(), deleted.ID)
	require.NoError(t, err)
	assert.NotNil(t, after.DeletedAt,
		"the sync pass un-deleted a file the user deleted (row %d, path %s)", after.ID, after.Path)
	assert.Equal(t, 1, trashCount(t, store, st.ID),
		"the item vanished from the trash: no longer restorable, no longer purged by retention")
}

func TestSyncDoesNotUndeleteAnOrdinaryDeletion_ObjectStore(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, drv, bucket := objStorage(t, store)
	bucket.objs["belgeler/rapor.txt"] = []byte("kullanicinin dosyasi")

	runSync(t, store, st)
	deleted := deleteLikeTheManager(t, store, st, drv, "belgeler/rapor.txt")
	require.Equal(t, 1, trashCount(t, store, st.ID))

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	after, err := store.GetNode(context.Background(), deleted.ID)
	require.NoError(t, err)
	assert.NotNil(t, after.DeletedAt, "object-store shape: sync un-deleted the row")
	assert.Equal(t, 1, trashCount(t, store, st.ID))
}

// ---------------------------------------------------------------------------
// 2. the quarantine
// ---------------------------------------------------------------------------

// alwaysInfected is ClamAV saying yes to everything.
type alwaysInfected struct{}

func (alwaysInfected) Supports() bool { return true }
func (alwaysInfected) Scan(context.Context, io.Reader) (bool, string, error) {
	return true, "Eicar-Test-Signature", nil
}

// quarantineLikeTheScanner runs the real antivirus job against a scanner that
// always reports infected — the same code path a real detection takes.
func quarantineLikeTheScanner(t *testing.T, store db.Store, st *model.Storage, drv storage.Driver, rel string) *model.Node {
	t.Helper()
	ctx := context.Background()
	clean := path.Clean("/" + strings.Trim(rel, "/"))
	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, clean))
	require.NoError(t, err)
	require.NotNil(t, n)

	job := queue.NewAntivirusScanner(store,
		func(id int64) (storage.Driver, error) {
			if id != st.ID {
				return nil, errors.New("unknown storage")
			}
			return drv, nil
		}, alwaysInfected{}, nil, nil, 0)
	require.NoError(t, job.Handle(ctx, queue.Op{
		Type:    queue.TypeAntivirusScan,
		Payload: map[string]any{"node_id": n.ID},
	}))

	got, err := store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	require.NotNil(t, got.DeletedAt, "precondition: quarantine must soft-delete the row")
	require.True(t, strings.HasPrefix(got.Path, "/"+trash.Prefix+"/"), got.Path)
	return got
}

func TestSyncDoesNotReleaseAQuarantinedFile_Local(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	require.NoError(t, os.WriteFile(filepath.Join(root, "fatura.exe"), []byte("virus payload"), 0o644))

	runSync(t, store, st)
	q := quarantineLikeTheScanner(t, store, st, drv, "fatura.exe")

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	after, err := store.GetNode(context.Background(), q.ID)
	require.NoError(t, err)
	assert.NotNil(t, after.DeletedAt,
		"the sync pass released an INFECTED file from quarantine (row %d, path %s)", after.ID, after.Path)
	assert.Equal(t, 1, trashCount(t, store, st.ID),
		"the quarantine record disappeared: retention will never purge the infected bytes")
}

func TestSyncDoesNotReleaseAQuarantinedFile_ObjectStore(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, drv, bucket := objStorage(t, store)
	bucket.objs["gelen/fatura.exe"] = []byte("virus payload")

	runSync(t, store, st)
	q := quarantineLikeTheScanner(t, store, st, drv, "gelen/fatura.exe")

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	after, err := store.GetNode(context.Background(), q.ID)
	require.NoError(t, err)
	assert.NotNil(t, after.DeletedAt, "object-store shape: sync released the quarantined file")
	assert.Equal(t, 1, trashCount(t, store, st.ID))
}

// ---------------------------------------------------------------------------
// 3. it is not only the trash bucket
// ---------------------------------------------------------------------------

// A row soft-deleted WITHOUT the storage rename — the tombstone pass's own
// SoftDeleteNode, and the error branches in applyDBMove/SyncHardDelete — keeps
// its ORIGINAL path. When bytes come back at that path (an object restored out
// of band, or simply a new file with the same name), the old row must not be
// quietly revived: it carries another file's identity, history and versions,
// and reviving it means nothing ever looks at the new bytes.
func TestSyncCataloguesAReappearedObjectAsANewFile(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	require.NoError(t, os.WriteFile(filepath.Join(root, "veri.csv"), []byte("eski"), 0o644))

	runSync(t, store, st)
	old, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/veri.csv"))
	require.NoError(t, err)
	require.NotNil(t, old)

	// The tombstone pass's shape: the row is soft-deleted where it stands.
	require.NoError(t, store.SoftDeleteNode(ctx, old.ID))

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	stale, err := store.GetNode(ctx, old.ID)
	require.NoError(t, err)
	assert.NotNil(t, stale.DeletedAt, "the trashed row must stay trashed")

	live, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/veri.csv"))
	require.NoError(t, err)
	require.NotNil(t, live, "the object that is really there must be catalogued")
	assert.NotEqual(t, old.ID, live.ID, "it must be a NEW row, not the old identity revived")
}

// ---------------------------------------------------------------------------
// 4. an install that already took the damage repairs itself
// ---------------------------------------------------------------------------

// Every filex that ran an earlier version has rows the old walk left behind:
// deletions it un-deleted, and rows it minted for the trash bucket and for the
// contents of trashed folders. Skipping the trash from now on does not undo
// any of that, and leaving it is not neutral -- a live row for the trash
// DIRECTORY becomes stale, gets tombstoned, and lands in the trash listing as
// an entry whose purge would delete the whole `.filex-trash` directory.
func TestSyncRepairsWhatAnEarlierVersionDidToTheTrash(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	require.NoError(t, os.WriteFile(filepath.Join(root, "sozlesme.pdf"), []byte("kullanicinin dosyasi"), 0o644))

	runSync(t, store, st)
	deleted := deleteLikeTheManager(t, store, st, drv, "sozlesme.pdf")

	// Replay exactly what the old walk did on the next pass.
	require.NoError(t, store.RestoreNode(ctx, deleted.ID))
	bucket, err := store.CreateNode(ctx, &model.Node{
		StorageID:  st.ID,
		Name:       trash.Prefix,
		Path:       "/" + trash.Prefix,
		PathHash:   pathkey.Hash(st.ID, "/"+trash.Prefix),
		StorageKey: "/" + trash.Prefix,
		Type:       model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	require.Equal(t, 0, trashCount(t, store, st.ID), "precondition: the damage is done")

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	// The user's deletion is a deletion again, restorable and on the clock.
	back, err := store.GetNode(ctx, deleted.ID)
	require.NoError(t, err)
	require.NotNil(t, back.DeletedAt, "the revived deletion must go back to the trash")
	assert.Equal(t, "/sozlesme.pdf", back.StorageKey, "restore still knows where it came from")
	assert.Equal(t, 1, trashCount(t, store, st.ID))

	// The row minted for the trash bucket is gone -- not trashed, which would
	// make purging it delete the trash directory.
	_, err = store.GetNode(ctx, bucket.ID)
	assert.Error(t, err, "the row for the trash bucket itself must be dropped outright")

	// And the bytes were never touched.
	onDisk, err := filepath.Glob(filepath.Join(root, trash.Prefix, "*__sozlesme.pdf"))
	require.NoError(t, err)
	assert.Len(t, onDisk, 1, "the trashed bytes must still be there")
}
