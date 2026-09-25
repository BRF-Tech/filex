package sync_test

// The lazy catalogue reconciles folders beside a full scan by design: neither
// takes the other's lock. These pin what makes that safe — every create on
// either side survives losing the race to the other — and run the two side by
// side on a real tree.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	gosync "sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// racingStore lets another writer win the insert of one path: the first
// CreateNode for it writes the row through the real store, then fails the
// caller's insert the way the unique index does.
type racingStore struct {
	db.Store
	path string
	mu   gosync.Mutex
	done bool
}

func (r *racingStore) CreateNode(ctx context.Context, n *model.Node) (*model.Node, error) {
	r.mu.Lock()
	first := !r.done && n.Path == r.path
	if first {
		r.done = true
	}
	r.mu.Unlock()
	if first {
		winner := *n
		if _, err := r.Store.CreateNode(ctx, &winner); err != nil {
			return nil, err
		}
		return nil, errors.New("UNIQUE constraint failed: nodes.storage_id, nodes.path_hash")
	}
	return r.Store.CreateNode(ctx, n)
}

// A full scan that loses the insert of a folder row to another writer carries
// on with that writer's row and walks the folder.
//
// Break: drop the `raced` branch in catalogueEntry — the scan leaves
// /yaris/icerik.txt and /yaris/alt/derin.txt out of the catalogue.
func TestFullScanWalksIntoAFolderAnotherWriterCreated(t *testing.T) {
	ctx := context.Background()
	_, raw := dbtest.NewTestDB(t)
	store := &racingStore{Store: raw, path: "/yaris"}
	st, _, root := localStorage(t, raw)
	writeUnder(t, root, "yaris/icerik.txt", "x")
	writeUnder(t, root, "yaris/alt/derin.txt", "y")
	writeUnder(t, root, "baska.txt", "z")

	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID))

	require.True(t, store.done, "the race was staged")
	for _, p := range []string{"/yaris", "/yaris/icerik.txt", "/yaris/alt", "/yaris/alt/derin.txt", "/baska.txt"} {
		assert.NotNil(t, node(t, raw, st.ID, p), "%s is catalogued", p)
	}
	run, err := raw.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, "ok", run.Status)
	assert.Zero(t, run.Deleted)
}

// A full scan ("Scan now") and the lazy catalogue's background filler, on the
// same tree at the same time: the catalogue ends up holding exactly what is on
// the disk, nothing is removed, and no row is written twice.
func TestFullScanBesideTheFillerLosesNothing(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	want := map[string]bool{}
	for d := 0; d < 12; d++ {
		for f := 0; f < 15; f++ {
			rel := fmt.Sprintf("k%02d/alt%d/f%02d.txt", d, d%3, f)
			writeUnder(t, root, rel, "x")
			want["/"+rel] = true
			want[fmt.Sprintf("/k%02d", d)] = true
			want[fmt.Sprintf("/k%02d/alt%d", d, d%3)] = true
		}
	}
	cfg, _ := json.Marshal(map[string]any{"root": root})
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "tembel", Driver: "local", MountPath: "/t", ConfigJSON: cfg, SyncMode: model.SyncModeLazy, Enabled: true,
	})
	require.NoError(t, err)

	pause := filexsync.LazyFillIdlePause
	filexsync.LazyFillIdlePause = 0
	defer func() { filexsync.LazyFillIdlePause = pause }()
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, w.Trigger(ctx, st.ID), "the full scan runs beside the filler")

	require.Eventually(t, func() bool {
		cov, ok := w.CatalogueStatus(ctx, st.ID)
		return ok && cov.Complete
	}, 30*time.Second, 50*time.Millisecond, "the filler converges")

	for p := range want {
		assert.NotNil(t, node(t, store, st.ID, p), "%s is catalogued", p)
	}
	for d := 0; d < 12; d++ {
		n := node(t, store, st.ID, fmt.Sprintf("/k%02d", d))
		require.NotNil(t, n)
		kids, err := store.ListNodesByParent(ctx, st.ID, &n.ID)
		require.NoError(t, err)
		assert.Len(t, kids, 1, "/k%02d has one child row, not two", d)
	}
	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	assert.Zero(t, run.Deleted)
	entries, err := os.ReadDir(filepath.Join(root, "k00", "alt0"))
	require.NoError(t, err)
	assert.Len(t, entries, 15)
}
