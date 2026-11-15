package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// watching starts a tree watcher over a fresh folder holding the given
// subfolders, with its event loop running.
func watching(t *testing.T, dirs ...string) (*treeWatcher, *queue, string) {
	t.Helper()
	root := t.TempDir()
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755))
	}
	q := newQueue(4096)
	tw, err := newTreeWatcher(root, q, quietLog())
	require.NoError(t, err)
	t.Cleanup(tw.close)
	tw.addTree(root, false)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go tw.loop(stop)
	return tw, q, root
}

// queuedEventually waits until the queue holds every folder in want, and
// returns what it held.
func queuedEventually(t *testing.T, q *queue, want ...string) []string {
	t.Helper()
	var got []string
	require.Eventually(t, func() bool {
		for _, p := range q.take(10000).folders {
			if !slices.Contains(got, p) {
				got = append(got, p)
			}
		}
		for _, w := range want {
			if !slices.Contains(got, w) {
				return false
			}
		}
		return true
	}, 5*time.Second, 20*time.Millisecond, "wanted %v", want)
	return got
}

func TestTreeWatcher_AFileWrittenReportsItsFolder(t *testing.T) {
	_, q, root := watching(t, "a/b")
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "b", "x.txt"), []byte("x"), 0o644))
	got := queuedEventually(t, q, "/a/b")
	assert.NotContains(t, got, "/a", "the folder holding the file, not the one above it")
}

// A folder made with files already in it - before its watch could be placed -
// is reported whole, and is watched from then on.
func TestTreeWatcher_ANewFolderIsWatchedAndReportedWhole(t *testing.T) {
	tw, q, root := watching(t, "a")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "yeni", "derin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "yeni", "derin", "f.txt"), []byte("f"), 0o644))
	queuedEventually(t, q, "/a", "/a/yeni")
	require.Eventually(t, func() bool {
		return slices.Contains(tw.w.WatchList(), filepath.Join(root, "a", "yeni", "derin"))
	}, 5*time.Second, 20*time.Millisecond, "the new folder's subfolders are watched too")

	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "yeni", "derin", "g.txt"), []byte("g"), 0o644))
	queuedEventually(t, q, "/a/yeni/derin")
}

// A removed folder loses its watches; its parent is reported.
func TestTreeWatcher_ARemovedFolderIsForgotten(t *testing.T) {
	tw, q, root := watching(t, "a/gone/inner")
	require.NoError(t, os.RemoveAll(filepath.Join(root, "a", "gone")))
	queuedEventually(t, q, "/a")
	require.Eventually(t, func() bool {
		for _, p := range tw.w.WatchList() {
			if p == filepath.Join(root, "a", "gone") || p == filepath.Join(root, "a", "gone", "inner") {
				return false
			}
		}
		return true
	}, 5*time.Second, 20*time.Millisecond)
}

// filex's own trees on the storage are not watched and not reported: filex
// never catalogues them by path, and it is usually the one writing there.
func TestTreeWatcher_LeavesFilexsOwnTreesAlone(t *testing.T) {
	tw, q, root := watching(t, ".filex-trash/old", ".versions/12", "docs")
	for _, p := range tw.w.WatchList() {
		assert.NotContains(t, p, ".filex-trash")
		assert.NotContains(t, p, ".versions")
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, ".filex-trash", "old", "x"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "docs", "y"), []byte("y"), 0o644))
	got := queuedEventually(t, q, "/docs")
	for _, p := range got {
		assert.NotContains(t, p, ".filex-trash")
	}
}

func TestTreeWatcher_Rel(t *testing.T) {
	root := t.TempDir()
	tw := &treeWatcher{root: root}
	p, ok := tw.rel(root)
	assert.True(t, ok)
	assert.Equal(t, "/", p)
	p, ok = tw.rel(filepath.Join(root, "a", "b"))
	assert.True(t, ok)
	assert.Equal(t, "/a/b", p)
	_, ok = tw.rel(filepath.Dir(root))
	assert.False(t, ok, "outside the root")
}

func TestSkipped(t *testing.T) {
	assert.False(t, skipped("/"))
	assert.False(t, skipped("/docs"))
	assert.True(t, skipped("/.filex-trash"))
	assert.True(t, skipped("/.versions/12"))
	assert.True(t, skipped("/.thumbs"))
}
