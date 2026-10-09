package sync

// sec055 follow-up: the lazy catalogue leaves a folder a long change fences
// (rowgate.FenceCtx: a folder moved or trashed object by object) alone. A
// reconcile of the fenced folder itself is deferred (rowgate.ErrBusy), and its
// parent's reconcile neither catalogues the fenced destination nor removes
// the fenced source, while every other entry of the parent is reconciled as
// usual. Once the fence opens, the parent's next reconcile sees the folder
// where the change put it, with its own row.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/rowgate"
)

// Break: take the fence check out of reconcileOnce (Covers, fencedOut and the
// seen marks) - the root's reconcile half way catalogues /hedef as a new
// folder and removes /tasinan, whose rows the move was about to re-home.
func TestLazyFence_AFolderMovedObjectByObjectIsLeftAloneUntilTheFenceOpens(t *testing.T) {
	l := newLazyLab(t, nil)
	l.write(t, "tasinan/a.txt", "a")
	l.write(t, "kalan.txt", "k")
	l.reconcile(t, "/")
	l.reconcile(t, "/tasinan")
	dir := l.row("/tasinan")
	file := l.row("/tasinan/a.txt")
	require.NotNil(t, dir, "fixture: the folder was not catalogued")
	require.NotNil(t, file, "fixture: its file was not catalogued")

	release, err := rowgate.FenceCtx(l.ctx, l.st.ID, "/tasinan", "/hedef")
	require.NoError(t, err)
	// Process-wide: the next test's storage id is the same number.
	t.Cleanup(release)
	// Half way: the bytes are at the new name, the rows are not.
	require.NoError(t, os.Rename(filepath.Join(l.root, "tasinan"), filepath.Join(l.root, "hedef")))
	l.write(t, "yeni.txt", "y")

	res := l.reconcile(t, "/")
	assert.Zero(t, res.Removed, "the root's reconcile removed the fenced folder")
	assert.NotNil(t, l.row("/yeni.txt"), "the rest of the folder was not reconciled beside the fence")
	assert.Nil(t, l.row("/hedef"), "the fenced destination was catalogued as a new folder")
	kept := l.row("/tasinan")
	require.NotNil(t, kept, "the fenced source's row was removed")
	assert.Equal(t, dir.ID, kept.ID)

	_, err = l.lc.reconcile(l.ctx, "/hedef", reasonOpen)
	assert.ErrorIs(t, err, rowgate.ErrBusy, "the fenced destination was reconciled")
	_, err = l.lc.reconcile(l.ctx, "/tasinan", reasonOpen)
	assert.ErrorIs(t, err, rowgate.ErrBusy, "the fenced source was reconciled")

	// The move's second step, then the fence opens.
	require.True(t, l.lc.ps.MoveRows(l.ctx, l.st, "tasinan", "hedef"))
	release()

	res = l.reconcile(t, "/")
	assert.Zero(t, res.Removed)
	moved := l.row("/hedef")
	require.NotNil(t, moved, "the moved folder has no row once the fence opened")
	assert.Equal(t, dir.ID, moved.ID, "the moved folder is another row")
	l.reconcile(t, "/hedef")
	f := l.row("/hedef/a.txt")
	require.NotNil(t, f)
	assert.Equal(t, file.ID, f.ID, "its file is another row")
}
