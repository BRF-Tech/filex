package sync_test

// Issue #104: a file deleted outside filex leaves the catalogue for good
// (issue #74), and its node_versions rows cascade with its row - so the
// snapshots under `.versions/<id>/` were left on the storage with nothing that
// could ever reach them again. They go with the row now, once the drop has
// committed; another file's history is not touched.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestExternallyDeletedFile_TakesItsVersionHistoryWithIt(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	writeUnder(t, root, "rapor.txt", "guncel")
	writeUnder(t, root, "kalan.txt", "kalir")
	fillers(t, root, 20)
	runSync(t, store, st)

	deleted := mustNode(t, store, st.ID, "/rapor.txt")
	kept := mustNode(t, store, st.ID, "/kalan.txt")
	snapshot := func(n *model.Node) string {
		key := ".versions/" + strconv.FormatInt(n.ID, 10) + "/1"
		writeUnder(t, root, key, "eski surum")
		_, err := store.CreateNodeVersion(ctx, &model.NodeVersion{NodeID: n.ID, VersionN: 1, StorageKey: key, Size: 10})
		require.NoError(t, err)
		return key
	}
	deletedKey, keptKey := snapshot(deleted), snapshot(kept)

	// Deleted from the shell, not through filex.
	require.NoError(t, os.Remove(filepath.Join(root, "rapor.txt")))
	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	require.True(t, gone(t, store, deleted.ID), "fixture check: the row of the deleted file is dropped")
	onDisk := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil
	}
	assert.False(t, onDisk(deletedKey), "the snapshot of a file deleted outside filex is left on the storage")
	assert.False(t, onDisk(filepath.Dir(deletedKey)), "its folder under .versions/ is left behind")
	assert.True(t, onDisk(keptKey), "the sync deleted the history of a file that is still there")
	versions, err := store.ListNodeVersions(ctx, kept.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 1)
}
