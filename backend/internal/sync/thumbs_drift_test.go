package sync_test

import (
	"context"
	"os"
	"path/filepath"
	gosync "sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// thumbsHeard collects what the walk hands the thumbnail refresher.
type thumbsHeard struct {
	mu    gosync.Mutex
	nodes []*model.Node
}

func (h *thumbsHeard) hear(_ context.Context, nodes []*model.Node) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, n := range nodes {
		cp := *n
		h.nodes = append(h.nodes, &cp)
	}
}

func (h *thumbsHeard) take() []*model.Node {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.nodes
	h.nodes = nil
	return out
}

func syncOnce(t *testing.T, store db.Store, st *model.Storage, heard *thumbsHeard) {
	t.Helper()
	ctx := context.Background()
	w := filexsync.New(store)
	w.AttachThumbs(heard.hear)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID))
	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", run.Status, run.Error)
}

// A file replaced on the backend, outside filex, is handed to the thumbnail
// refresher with its NEW size and date: 0.49 updated the row and left the
// picture of the old content in place for good. An unchanged file is not
// handed over on every pass.
func TestSyncHandsADriftedFileToTheThumbnails(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	require.NoError(t, os.WriteFile(filepath.Join(root, "kapak.png"), []byte("ilk"), 0o644))

	heard := &thumbsHeard{}
	syncOnce(t, store, st, heard)
	heard.take()

	waitPastSecondBoundary()
	syncOnce(t, store, st, heard)
	require.Empty(t, heard.take(), "an unchanged file is not handed over on every pass")

	waitPastSecondBoundary()
	require.NoError(t, os.WriteFile(filepath.Join(root, "kapak.png"), []byte("ikinci ve daha uzun"), 0o644))
	syncOnce(t, store, st, heard)
	got := heard.take()
	require.Len(t, got, 1)
	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/kapak.png"))
	require.NoError(t, err)
	require.Equal(t, n.ID, got[0].ID)
	require.Equal(t, int64(len("ikinci ve daha uzun")), got[0].Size, "the refresher is told what is there now")
	require.Equal(t, n.ContentFingerprint(), got[0].ContentFingerprint())
}
