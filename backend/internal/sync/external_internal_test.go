package sync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutermostFolders(t *testing.T) {
	assert.Equal(t, []string{"/a", "/a b", "/c"}, outermostFolders([]string{"/a/b", "/c", "/a", "/a b", "/a/b/c"}))
	assert.Equal(t, []string{"/"}, outermostFolders([]string{"/x", "/", "/y/z"}))
	assert.Equal(t, []string{"/ab", "/abc"}, outermostFolders([]string{"/abc", "/ab"}), "a shared prefix is not a parent")
	assert.Nil(t, outermostFolders(nil))
}

func TestParentFolder(t *testing.T) {
	assert.Equal(t, "/", parentFolder("/a"))
	assert.Equal(t, "/a", parentFolder("/a/b"))
	assert.Equal(t, "/a/b", parentFolder("/a/b/c"))
}

// On a lazy storage a reported folder goes into the engine's queue the way its
// own watch's event does: one reconcile of that folder, and the new file is in
// the catalogue.
func TestExternalChanges_LazyQueuesTheFolderAsItsOwnWatchWould(t *testing.T) {
	l := newLazyLab(t, nil)
	l.write(t, "docs/a.txt", "a")
	l.reconcile(t, "/")
	l.reconcile(t, "/docs")

	w := New(l.store)
	w.syncers[l.st.ID] = l.lc.s
	l.write(t, "docs/b.txt", "b")
	later, err := w.ExternalChanges(l.ctx, l.st.ID, []string{"/docs", "/.versions/3"})
	require.NoError(t, err)
	assert.Empty(t, later)

	it, ok := l.lc.next()
	require.True(t, ok, "the folder was queued")
	assert.Equal(t, "/docs", it.dir)
	assert.Equal(t, reasonWatch, it.why)
	_, more := l.lc.next()
	assert.False(t, more, "filex's own tree was not")

	_, err = l.lc.reconcile(l.ctx, it.dir, it.why)
	require.NoError(t, err)
	assert.NotNil(t, l.row("/docs/b.txt"))
}

// A lazy resync reconciles the folders the catalogue vouches for - the watched
// ones - under the folder asked about, and nothing else: any other folder is
// listed from the disk the next time it is opened.
func TestExternalResync_LazyReconcilesTheWatchedFoldersUnderIt(t *testing.T) {
	l := newLazyLab(t, nil)
	l.mkdir(t, "a/b")
	l.mkdir(t, "c")
	l.lc.watch.add("/a")
	l.lc.watch.add("/a/b")
	l.lc.watch.add("/c")
	require.Eventually(t, func() bool { return len(l.lc.watch.within("/")) == 3 }, 5*time.Second, 10*time.Millisecond)

	w := New(l.store)
	w.syncers[l.st.ID] = l.lc.s
	later, err := w.ExternalResync(l.ctx, l.st.ID, []string{"/a"})
	require.NoError(t, err)
	assert.Empty(t, later)

	var got []string
	for {
		it, ok := l.lc.next()
		if !ok {
			break
		}
		got = append(got, it.dir)
	}
	assert.ElementsMatch(t, []string{"/a", "/a/b"}, got)
}
