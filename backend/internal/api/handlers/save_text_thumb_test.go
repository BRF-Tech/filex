package handlers_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// Saving in the built-in editor draws the file again. Before 0.50 the editor
// wrote the bytes, the row and the search document and left the thumbnail
// alone: an SVG edited in place kept the picture of its first version.
func TestSaveText_DrawsTheFileAgain(t *testing.T) {
	cacheDir := t.TempDir()
	var pipe *thumb.Pipeline
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		pipe = thumb.New(d.Store, cacheDir, thumb.Capabilities{Image: true})
		d.Thumbs = pipe
	})
	ctx := context.Background()
	st := f.storage
	drv, err := f.deps.StorageResolver(st.ID)
	require.NoError(t, err)
	pipe.AttachStorage(st.ID, drv)

	require.Equal(t, http.StatusOK, f.saveText(t, "main://notlar.txt", "ilk"))
	n := waitNode(t, f, "/notlar.txt")
	first := waitThumb(t, f, n.ID, func(r *model.Thumbnail) bool { return r.State == "ready" })

	require.Equal(t, http.StatusOK, f.saveText(t, "main://notlar.txt", "ikinci surum, daha uzun"))
	fresh, err := f.store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	row := waitThumb(t, f, n.ID, func(r *model.Thumbnail) bool {
		return r.State == "ready" && r.SourceSig == fresh.ContentFingerprint()
	})
	require.NotEqual(t, first.SourceSig, row.SourceSig, "the second save was drawn from the second content")
}

func waitNode(t *testing.T, f *stagedFixture, p string) *model.Node {
	t.Helper()
	n, err := f.store.GetNodeByPath(context.Background(), f.storage.ID, pathkey.Hash(f.storage.ID, p))
	require.NoError(t, err)
	require.NotNil(t, n)
	return n
}

func waitThumb(t *testing.T, f *stagedFixture, id int64, ok func(*model.Thumbnail) bool) *model.Thumbnail {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := f.store.GetThumbnail(context.Background(), id); err == nil && r != nil && ok(r) {
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
	r, _ := f.store.GetThumbnail(context.Background(), id)
	t.Fatalf("thumbnail never reached the expected state: %+v", r)
	return nil
}
