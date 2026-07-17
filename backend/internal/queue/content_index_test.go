package queue_test

// Tests for the content_index job ("Bul" wave): eligibility gating,
// extraction through a fake storage driver, and the search index landing
// the extracted text (verified via a content-scoped query).

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// fakeNodes implements queue.NodeGetter over a map.
type fakeNodes map[int64]*model.Node

func (f fakeNodes) GetNode(_ context.Context, id int64) (*model.Node, error) {
	n, ok := f[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return n, nil
}

// fakeStorage implements storage.Driver serving fixed bytes per path.
type fakeStorage struct{ files map[string][]byte }

func (f *fakeStorage) Init(context.Context, map[string]any) error { return nil }
func (f *fakeStorage) Name() string                               { return "fake" }
func (f *fakeStorage) List(context.Context, string) ([]storage.Object, error) {
	return nil, storage.ErrUnsupported
}
func (f *fakeStorage) Stat(context.Context, string) (storage.Object, error) {
	return storage.Object{}, storage.ErrUnsupported
}
func (f *fakeStorage) Read(_ context.Context, path string) (io.ReadCloser, error) {
	b, ok := f.files[path]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (f *fakeStorage) Capabilities() storage.Capabilities { return storage.Capabilities{} }

func newContentTestIndex(t *testing.T) *search.Index {
	t.Helper()
	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func textNode(id int64, name, path string, size int64) *model.Node {
	return &model.Node{
		ID:        id,
		StorageID: 1,
		Name:      name,
		Path:      path,
		Mime:      "text/plain",
		Type:      model.NodeTypeFile,
		Size:      size,
		Etag:      "etag-1",
	}
}

func TestContentIndexer_Eligible(t *testing.T) {
	ci := queue.NewContentIndexer(fakeNodes{}, nil, nil, 1024)

	assert.True(t, ci.Eligible(textNode(1, "a.txt", "/a.txt", 100)))
	assert.True(t, ci.Eligible(textNode(1, "main.go", "/main.go", 100)))

	// Over the source cap.
	assert.False(t, ci.Eligible(textNode(1, "big.txt", "/big.txt", 2048)))
	// Empty file.
	assert.False(t, ci.Eligible(textNode(1, "empty.txt", "/empty.txt", 0)))
	// No extractor for the type.
	png := textNode(1, "img.png", "/img.png", 100)
	png.Mime = "image/png"
	assert.False(t, ci.Eligible(png))
	// Directories never.
	dir := &model.Node{ID: 2, Name: "d", Path: "/d", Type: model.NodeTypeDirectory, Size: 1}
	assert.False(t, ci.Eligible(dir))
	// Soft-deleted rows never.
	del := textNode(3, "gone.txt", "/gone.txt", 10)
	now := time.Now()
	del.DeletedAt = &now
	assert.False(t, ci.Eligible(del))
}

func TestContentIndexer_HandleExtractsAndIndexes(t *testing.T) {
	ctx := context.Background()
	idx := newContentTestIndex(t)
	n := textNode(7, "tutanak.txt", "/toplanti/tutanak.txt", 64)
	require.NoError(t, idx.IndexNode(ctx, n))

	nodes := fakeNodes{7: n}
	drv := &fakeStorage{files: map[string][]byte{
		"/toplanti/tutanak.txt": []byte("toplantı kararı: sunucu kapasitesi artacak"),
	}}
	resolver := func(id int64) (storage.Driver, error) { return drv, nil }
	ci := queue.NewContentIndexer(nodes, resolver, idx, 0)

	// Payload as float64 — the shape a JSON round-trip through a queue
	// driver produces.
	err := ci.Handle(ctx, queue.Op{Type: queue.TypeContentIndex, Payload: map[string]any{"node_id": float64(7)}})
	require.NoError(t, err)

	hits := idx.SafeSearchScoped(ctx, "kapasitesi", 10, search.ScopeContent)
	require.Len(t, hits, 1)
	assert.Equal(t, int64(7), hits[0].NodeID)
	assert.Contains(t, hits[0].Snippet, "«kapasitesi»")

	// The metadata survived the content write.
	nameHits := idx.SafeSearchScoped(ctx, "tutanak", 10, search.ScopeName)
	require.Len(t, nameHits, 1)
}

func TestContentIndexer_HandleSkipsGracefully(t *testing.T) {
	ctx := context.Background()
	idx := newContentTestIndex(t)
	ci := queue.NewContentIndexer(fakeNodes{}, nil, idx, 0)

	// Missing payload and vanished nodes are done, not failures.
	assert.NoError(t, ci.Handle(ctx, queue.Op{Type: queue.TypeContentIndex}))
	assert.NoError(t, ci.Handle(ctx, queue.Op{Type: queue.TypeContentIndex, Payload: map[string]any{"node_id": float64(404)}}))

	// Storage read failure IS an error → the queue's retry budget applies.
	n := textNode(1, "a.txt", "/a.txt", 10)
	ci = queue.NewContentIndexer(fakeNodes{1: n}, func(int64) (storage.Driver, error) {
		return &fakeStorage{files: map[string][]byte{}}, nil
	}, idx, 0)
	err := ci.Handle(ctx, queue.Op{Type: queue.TypeContentIndex, Payload: map[string]any{"node_id": int64(1)}})
	assert.Error(t, err)
}

func TestContentIndexer_EnqueueOnlyEligible(t *testing.T) {
	ctx := context.Background()
	drv := setupSQLite(t)
	ci := queue.NewContentIndexer(fakeNodes{}, nil, nil, 1024)

	ci.Enqueue(ctx, drv, textNode(1, "a.txt", "/a.txt", 100)) // eligible
	png := textNode(2, "img.png", "/img.png", 100)
	png.Mime = "image/png"
	ci.Enqueue(ctx, drv, png) // ineligible → no op row

	ops, total, err := drv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, ops, 1)
	assert.Equal(t, queue.TypeContentIndex, ops[0].Type)
	// node_id survives the driver round-trip in a form payloadInt64 reads.
	assert.EqualValues(t, 1, ops[0].Payload["node_id"])
}
