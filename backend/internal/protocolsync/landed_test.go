package protocolsync

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// A write through a protocol (WebDAV PUT, the S3 gateway, SFTP, FTPS, NFS, the
// agent surface, archive extract) that REPLACED a file kept the replaced
// file's etag on the row: WriteRows carried `existing.Etag` forward while it
// updated the size. search.ContentFingerprint prefers the etag, so the new
// text was never extracted; the listing handed clients an etag that said
// "unchanged"; and a scan that finds the etag it recorded believes nothing
// happened.

// etagStore is a backend that reports an etag per path, like S3 or WebDAV.
type etagStore struct {
	mu    sync.Mutex
	etags map[string]string
	fail  bool
}

func (d *etagStore) Init(context.Context, map[string]any) error { return nil }
func (d *etagStore) Name() string                               { return "etagstore" }
func (d *etagStore) List(context.Context, string) ([]storage.Object, error) {
	return nil, nil
}
func (d *etagStore) Read(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrNotFound
}
func (d *etagStore) Capabilities() storage.Capabilities { return storage.Capabilities{} }
func (d *etagStore) Stat(_ context.Context, p string) (storage.Object, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail {
		return storage.Object{}, errors.New("backend unavailable")
	}
	p = strings.Trim(p, "/")
	e, ok := d.etags[p]
	if !ok {
		return storage.Object{}, storage.ErrNotFound
	}
	return storage.Object{Path: p, Name: path.Base(p), Kind: storage.KindFile, Size: int64(len(e)), Etag: e,
		Mtime: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}, nil
}

func (d *etagStore) put(p, etag string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.etags[p] = etag
}

func TestWriteRows_AnOverwriteRecordsTheNewEtag(t *testing.T) {
	installEmitter(t)
	s, st := newSyncer(t)
	ctx := context.Background()
	drv := &etagStore{etags: map[string]string{}}
	s.WithResolver(func(int64) (storage.Driver, error) { return drv, nil })

	drv.put("docs/a.txt", "etag-one")
	node, _, ok := s.WriteRows(ctx, st, "docs/a.txt", 8, "text/plain")
	require.True(t, ok)
	// What a scan leaves on a row the first time it sees the object.
	require.NoError(t, s.Store.UpdateNodeMeta(ctx, node.ID, 8, "text/plain", "etag-one", time.Now()))
	before, err := s.Store.GetNode(ctx, node.ID)
	require.NoError(t, err)
	fp := search.ContentFingerprint(before)

	drv.put("docs/a.txt", "etag-two-longer")
	_, _, ok = s.WriteRows(ctx, st, "docs/a.txt", 15, "text/plain")
	require.True(t, ok)

	after, err := s.Store.GetNode(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, "etag-two-longer", after.Etag, "the replaced file's etag was carried onto the new bytes")
	assert.EqualValues(t, len("etag-two-longer"), after.Size)
	require.NotNil(t, after.BackendMtime)
	assert.True(t, after.BackendMtime.Equal(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)),
		"the backend's own mtime, so a size+mtime scan does not see drift that is not there")
	assert.NotEqual(t, fp, search.ContentFingerprint(after), "content search would keep the old text")
}

// A Syncer that cannot read the backend back, or a Stat that fails, records an
// EMPTY etag — never the old one. An empty etag is what the next scan reads
// as drift, and it makes the search fingerprint fall back to size+mtime now.
func TestWriteRows_WithoutAReadBackTheEtagIsEmptiedNotKept(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(s *Syncer)
	}{
		{"no resolver", func(*Syncer) {}},
		{"stat fails", func(s *Syncer) {
			s.WithResolver(func(int64) (storage.Driver, error) { return &etagStore{fail: true}, nil })
		}},
		{"resolver fails", func(s *Syncer) {
			s.WithResolver(func(int64) (storage.Driver, error) { return nil, errors.New("offline") })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installEmitter(t)
			s, st := newSyncer(t)
			ctx := context.Background()
			node, _, ok := s.WriteRows(ctx, st, "a.txt", 3, "text/plain")
			require.True(t, ok)
			require.NoError(t, s.Store.UpdateNodeMeta(ctx, node.ID, 3, "text/plain", "old-etag", time.Now()))

			tc.setup(s)
			_, _, ok = s.WriteRows(ctx, st, "a.txt", 9, "text/plain")
			require.True(t, ok)
			after, err := s.Store.GetNode(ctx, node.ID)
			require.NoError(t, err)
			assert.Equal(t, "", after.Etag)
			assert.EqualValues(t, 9, after.Size)
		})
	}
}
