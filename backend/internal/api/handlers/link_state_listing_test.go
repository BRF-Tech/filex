package handlers_test

// Why a link will not open, in every listing (issue #34, migration 00098).
//
// A link filex will not follow is listed `symlink: true`, and the explorer's
// badge names the reason when the row carries a `link_state`: "Outside
// storage", "Broken link", "Remote link". Until 0.54 only a listing read from
// the storage itself carried one; the catalogue kept THAT a row is such a link,
// not WHY, so every listing it answered - all of them, once the storage's first
// sync is done - said the general "Link". The sync now records the driver's
// reason with the row, and the listings send it.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// listedByName lists main:// and keys the rows by basename.
func listedByName(t *testing.T, r *goneRig) map[string]map[string]any {
	t.Helper()
	rec := callList(t, r.mh, url.Values{"action": {"index"}, "path": {"main://"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Files []map[string]any `json:"files"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	seen := map[string]map[string]any{}
	for _, f := range resp.Files {
		seen[fmt.Sprint(f["basename"])] = f
	}
	return seen
}

// After the sync: the folder is answered by the catalogue, and a link row
// carries the reason the sync recorded for it.
//
// RED before 0.54: `link_state` was never on a catalogue row.
func TestLinkState_TheCatalogueListingSaysWhyALinkWillNotOpen(t *testing.T) {
	r := newGoneRig(t)
	ctx := context.Background()
	require.NoError(t, r.raw.UpdateStorageSyncCursor(ctx, r.st.ID, time.Now(), ""))
	archive := r.row(t, nil, "/archive", model.NodeTypeSymlink, 0)
	gone := r.row(t, nil, "/gone", model.NodeTypeSymlink, 0)
	r.row(t, nil, "/notlar.txt", model.NodeTypeFile, 5)
	changed, err := r.raw.SetNodeLinkState(ctx, archive.ID, storage.LinkOutsideRoot)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = r.raw.SetNodeLinkState(ctx, gone.ID, storage.LinkBroken)
	require.NoError(t, err)
	require.True(t, changed)
	// A row the sync has not given a reason yet (catalogued before 0.54).
	r.row(t, nil, "/eski", model.NodeTypeSymlink, 0)

	seen := listedByName(t, r)
	require.Contains(t, seen, "archive")
	assert.Equal(t, true, seen["archive"]["symlink"])
	assert.Equal(t, "file", seen["archive"]["type"], "the wire type stays the closed file | dir union")
	assert.Equal(t, storage.LinkOutsideRoot, seen["archive"]["link_state"], "the catalogue listing dropped the recorded reason")
	assert.Equal(t, storage.LinkBroken, seen["gone"]["link_state"])
	require.Contains(t, seen, "eski")
	assert.Equal(t, true, seen["eski"]["symlink"])
	_, said := seen["eski"]["link_state"]
	assert.False(t, said, "no reason recorded: none sent, the explorer says the general Link")
	_, onFile := seen["notlar.txt"]["link_state"]
	assert.False(t, onFile, "an ordinary row is not a link")
}

// Before the first sync is done the folder is listed from the storage with the
// catalogue laid over it: the storage's reason, read just now, wins over the
// one the catalogue recorded.
func TestLinkState_TheMergedListingSaysTheStoragesReasonNow(t *testing.T) {
	r := newGoneRig(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(r.root, "archive")); err != nil {
		t.Skipf("this host cannot create symlinks: %v", err)
	}
	ctx := context.Background()
	archive := r.row(t, nil, "/archive", model.NodeTypeSymlink, 0)
	// Recorded while the target was missing; it is back now, outside the root.
	_, err := r.raw.SetNodeLinkState(ctx, archive.ID, storage.LinkBroken)
	require.NoError(t, err)

	seen := listedByName(t, r)
	require.Contains(t, seen, "archive")
	assert.Equal(t, true, seen["archive"]["symlink"])
	assert.Equal(t, storage.LinkOutsideRoot, seen["archive"]["link_state"])
	assert.Equal(t, archive.ID, int64(seen["archive"]["id"].(float64)), "projected from its row")
}

// Recent, Starred and the tag views ship the node row as it is; a link row
// there carries its reason under the same name, so the explorer badges it as
// the folder listing does (packages/core lib/nodeRow).
func TestLinkState_StarredSaysWhyALinkWillNotOpen(t *testing.T) {
	r := newGoneRig(t)
	ctx := context.Background()
	archive := r.row(t, nil, "/archive", model.NodeTypeSymlink, 0)
	_, err := r.raw.SetNodeLinkState(ctx, archive.ID, storage.LinkOutsideRoot)
	require.NoError(t, err)
	require.NoError(t, r.raw.SetUserNodeMeta(ctx, r.owner, archive.ID, "starred", "1"))
	u, err := r.raw.GetUser(ctx, r.owner)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/meta/starred", nil)
	req = req.WithContext(auth.WithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	handlers.NewMeta(r.store).ListStarred(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Nodes []map[string]any `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Nodes, 1)
	assert.Equal(t, "symlink", resp.Nodes[0]["type"])
	assert.Equal(t, storage.LinkOutsideRoot, resp.Nodes[0]["link_state"])
}
