package queue_test

// wiring:e2 — content extraction must never index E2E-encrypted material:
// magic-prefixed files index EMPTY content, and any file under a folder
// carrying `.filex-e2e.json` is skipped via the marker ancestor walk.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// e2eFakeNodes implements queue.NodeGetter AND e2e.NodeByPathLookup so the
// marker ancestor walk has something to hit.
type e2eFakeNodes struct {
	byID   map[int64]*model.Node
	byHash map[string]*model.Node
}

func e2eTestPathHash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
}

func (f *e2eFakeNodes) GetNode(_ context.Context, id int64) (*model.Node, error) {
	if n, ok := f.byID[id]; ok {
		return n, nil
	}
	return nil, nil
}

func (f *e2eFakeNodes) GetNodeByPath(_ context.Context, _ int64, hash string) (*model.Node, error) {
	if n, ok := f.byHash[hash]; ok {
		return n, nil
	}
	return nil, nil
}

func TestContentIndexer_SkipsMagicPrefixedCiphertext(t *testing.T) {
	ctx := context.Background()
	idx := newContentTestIndex(t)
	n := textNode(9, "gizli.txt", "/kasa/gizli.txt", 64)
	require.NoError(t, idx.IndexNode(ctx, n))

	nodes := fakeNodes{9: n}
	drv := &fakeStorage{files: map[string][]byte{
		// 'filexe2e' magic + garbage — what an encrypted upload looks like.
		"/kasa/gizli.txt": append([]byte("filexe2e\x01"), []byte("çokgizlisözcük ciphertext")...),
	}}
	ci := queue.NewContentIndexer(nodes, func(int64) (storage.Driver, error) { return drv, nil }, idx, 0)

	require.NoError(t, ci.Handle(ctx, queue.Op{Type: queue.TypeContentIndex, Payload: map[string]any{"node_id": int64(9)}}))

	// Nothing from the ciphertext landed in the content index…
	hits := idx.SafeSearchScoped(ctx, "çokgizlisözcük", 10, search.ScopeContent)
	assert.Len(t, hits, 0)
	// …while the name metadata is still searchable.
	nameHits := idx.SafeSearchScoped(ctx, "gizli", 10, search.ScopeName)
	assert.NotEmpty(t, nameHits)
}

func TestContentIndexer_SkipsFilesUnderE2eMarker(t *testing.T) {
	ctx := context.Background()
	idx := newContentTestIndex(t)

	// A PLAINTEXT file inside a marker-carrying folder (e.g. written via
	// DAV) — the ancestor walk must keep its content out of the index.
	n := textNode(11, "not.txt", "/kasa/not.txt", 64)
	require.NoError(t, idx.IndexNode(ctx, n))
	marker := &model.Node{
		ID: 12, StorageID: 1, Name: ".filex-e2e.json",
		Path: "/kasa/.filex-e2e.json", Type: model.NodeTypeFile, Size: 10,
	}
	nodes := &e2eFakeNodes{
		byID:   map[int64]*model.Node{11: n},
		byHash: map[string]*model.Node{e2eTestPathHash(1, "kasa/.filex-e2e.json"): marker},
	}
	drv := &fakeStorage{files: map[string][]byte{
		"/kasa/not.txt": []byte("plaintextsızıntı olmasın"),
	}}
	ci := queue.NewContentIndexer(nodes, func(int64) (storage.Driver, error) { return drv, nil }, idx, 0)

	require.NoError(t, ci.Handle(ctx, queue.Op{Type: queue.TypeContentIndex, Payload: map[string]any{"node_id": int64(11)}}))

	hits := idx.SafeSearchScoped(ctx, "plaintextsızıntı", 10, search.ScopeContent)
	assert.Len(t, hits, 0)
}

func TestContentIndexer_MarkerFileNeverEligible(t *testing.T) {
	ci := queue.NewContentIndexer(fakeNodes{}, nil, nil, 1024)
	m := textNode(1, ".filex-e2e.json", "/kasa/.filex-e2e.json", 100)
	m.Mime = "application/json"
	assert.False(t, ci.Eligible(m))
}
