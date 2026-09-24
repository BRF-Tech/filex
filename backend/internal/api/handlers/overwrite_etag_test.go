package handlers_test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// An overwrite recorded the new SIZE and the OLD etag.
//
// The upload, file-drop, text-editor and new-document paths all wrote
// `UpdateNodeMeta(…, newSize, mime, existing.Etag, time.Now())`: the etag of
// the file that had just been replaced. On a backend that reports etags (S3,
// WebDAV) that stale value is what search.ContentFingerprint keys content
// re-extraction on — so the new text was never extracted — and what the
// listing hands clients, whose change detection then saw "unchanged".

// etagLocal is the local driver reporting an md5 etag for every file, the way
// an S3 backend does. The embedded *local.Driver keeps Write, Move and the
// other optional interfaces.
type etagLocal struct {
	*local.Driver
	failStat *atomic.Bool
}

func md5Of(b []byte) string {
	s := md5.Sum(b)
	return hex.EncodeToString(s[:])
}

func (d etagLocal) Stat(ctx context.Context, p string) (storage.Object, error) {
	if d.failStat != nil && d.failStat.Load() {
		return storage.Object{}, storage.ErrUnsupported
	}
	o, err := d.Driver.Stat(ctx, p)
	if err != nil || o.Kind != storage.KindFile {
		return o, err
	}
	rc, rerr := d.Driver.Read(ctx, p)
	if rerr != nil {
		return o, nil
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	o.Etag = md5Of(b)
	return o, nil
}

// withEtagDriver wraps the fixture's resolver. The returned flag makes every
// Stat fail from the moment it is set.
func withEtagDriver(fail *atomic.Bool) func(*api.Deps) {
	return func(d *api.Deps) {
		inner := d.StorageResolver
		d.StorageResolver = func(id int64) (storage.Driver, error) {
			drv, err := inner(id)
			if err != nil {
				return nil, err
			}
			return etagLocal{Driver: drv.(*local.Driver), failStat: fail}, nil
		}
	}
}

func (f *stagedFixture) rowAt(t *testing.T, rel string) *model.Node {
	t.Helper()
	n, err := f.store.GetNodeByPath(context.Background(), f.storage.ID, pathkey.Hash(f.storage.ID, rel))
	require.NoError(t, err, "no row at %s", rel)
	return n
}

// seedEtag puts on the row what a storage scan leaves there the first time it
// sees the object: the backend's etag.
func (f *stagedFixture) seedEtag(t *testing.T, n *model.Node, etag string) *model.Node {
	t.Helper()
	require.NoError(t, f.store.UpdateNodeMeta(context.Background(), n.ID, n.Size, n.Mime, etag, time.Now()))
	return f.rowAt(t, n.Path)
}

func TestOverwrite_ManagerUpload_RecordsTheNewEtag(t *testing.T) {
	f := newStagedFixtureWith(t, withEtagDriver(nil))
	f.uploadMultipart(t, "notes.txt", []byte("VERSION-ONE"))
	before := f.seedEtag(t, f.rowAt(t, "/notes.txt"), md5Of([]byte("VERSION-ONE")))

	f.uploadMultipart(t, "notes.txt", []byte("VERSION-TWO-LONGER"))

	after := f.rowAt(t, "/notes.txt")
	assert.Equal(t, before.ID, after.ID)
	assert.Equal(t, md5Of([]byte("VERSION-TWO-LONGER")), after.Etag, "the replaced file's etag was kept")
	assert.EqualValues(t, len("VERSION-TWO-LONGER"), after.Size)
	assert.NotEqual(t, search.ContentFingerprint(before), search.ContentFingerprint(after),
		"the search fingerprint did not move, so the new text would never be extracted")
}

// When the backend cannot be asked what landed, the etag is emptied — never
// kept. An empty etag reads as drift on the next scan, which corrects the row.
func TestOverwrite_ManagerUpload_UnreadableBackend_EmptiesTheEtag(t *testing.T) {
	var fail atomic.Bool
	f := newStagedFixtureWith(t, withEtagDriver(&fail))
	f.uploadMultipart(t, "notes.txt", []byte("VERSION-ONE"))
	f.seedEtag(t, f.rowAt(t, "/notes.txt"), md5Of([]byte("VERSION-ONE")))

	fail.Store(true)
	f.uploadMultipart(t, "notes.txt", []byte("VERSION-TWO"))
	fail.Store(false)

	after := f.rowAt(t, "/notes.txt")
	assert.Equal(t, "", after.Etag)
	assert.EqualValues(t, len("VERSION-TWO"), after.Size)
}

func TestOverwrite_SaveText_RecordsTheNewEtag(t *testing.T) {
	f := newStagedFixtureWith(t, withEtagDriver(nil))
	require.Equal(t, http.StatusOK, f.saveText(t, "main://draft.txt", "first draft"))
	before := f.seedEtag(t, f.rowAt(t, "/draft.txt"), md5Of([]byte("first draft")))

	require.Equal(t, http.StatusOK, f.saveText(t, "main://draft.txt", "second, longer draft"))

	after := f.rowAt(t, "/draft.txt")
	assert.Equal(t, md5Of([]byte("second, longer draft")), after.Etag)
	assert.EqualValues(t, len("second, longer draft"), after.Size)
	assert.NotEqual(t, search.ContentFingerprint(before), search.ContentFingerprint(after))
}

// "New document" never overwrites a file — it refuses — but it does meet a
// catalogue row whose file is gone, and it used to keep that row's etag for
// bytes that no longer exist.
func TestOverwrite_NewFile_OverAStaleRow_RecordsWhatLanded(t *testing.T) {
	f := newStagedFixtureWith(t, withEtagDriver(nil))
	stale, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.storage.ID, Name: "minutes.md", Path: "/minutes.md", StorageKey: "/minutes.md",
		PathHash: pathkey.Hash(f.storage.ID, "/minutes.md"), Type: model.NodeTypeFile,
		Size: 99, Etag: "etag-of-a-file-that-is-gone",
	})
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, f.mutate(t, "newfile", map[string]any{
		"path": "main://", "name": "minutes", "type": "md",
	}))

	onDisk, err := os.ReadFile(filepath.Join(f.rootDir, "minutes.md"))
	require.NoError(t, err)
	after := f.rowAt(t, "/minutes.md")
	assert.Equal(t, stale.ID, after.ID)
	assert.Equal(t, md5Of(onDisk), after.Etag)
	assert.EqualValues(t, len(onDisk), after.Size)
}

// The public file-drop link writes through IngestFile.
func TestOverwrite_IngestFile_RecordsTheNewEtag(t *testing.T) {
	f := newStagedFixtureWith(t, withEtagDriver(nil))
	mh := handlers.NewManager(f.store, f.deps.StorageResolver)
	ctx := context.Background()

	_, err := mh.IngestFile(ctx, f.storage, "drop", "form.txt", strings.NewReader("one"), 3)
	require.NoError(t, err)
	before := f.seedEtag(t, f.rowAt(t, "/drop/form.txt"), md5Of([]byte("one")))

	_, err = mh.IngestFile(ctx, f.storage, "drop", "form.txt", strings.NewReader("second"), 6)
	require.NoError(t, err)

	after := f.rowAt(t, "/drop/form.txt")
	assert.Equal(t, before.ID, after.ID)
	assert.Equal(t, md5Of([]byte("second")), after.Etag)
	assert.EqualValues(t, 6, after.Size)
}
