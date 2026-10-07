package replica

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// treeDriver lists a tree held in a map: folder -> its entries, in an order
// that is deliberately not sorted (a backend's own order).
type treeDriver struct {
	storage.Driver
	dirs  map[string][]storage.Object
	lists int
}

func (d *treeDriver) List(_ context.Context, p string) ([]storage.Object, error) {
	d.lists++
	objs, ok := d.dirs[p]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append([]storage.Object(nil), objs...), nil
}

func fileObj(name string) storage.Object {
	return storage.Object{Name: name, Kind: storage.KindFile, Size: 1}
}

func dirObj(name string) storage.Object {
	return storage.Object{Name: name, Kind: storage.KindDirectory}
}

func sampleTree() *treeDriver {
	return &treeDriver{dirs: map[string][]storage.Object{
		"/":          {fileObj("z.txt"), dirObj("docs"), fileObj("a.txt"), dirObj("b"), fileObj("docs-old.txt")},
		"/docs":      {fileObj("2.pdf"), dirObj("in"), fileObj("1.pdf")},
		"/docs/in":   {fileObj("x.txt")},
		"/b":         {},
		"/docs/gone": nil,
	}}
}

func walkAll(t *testing.T, d storage.Driver, after string) []string {
	t.Helper()
	// Empty, not nil: resuming after the last file walks nothing, and the
	// rest of the full walk it is compared with is an empty slice.
	got := []string{}
	require.NoError(t, walkFiles(context.Background(), d, after, func(o storage.Object) error {
		got = append(got, o.Path)
		return nil
	}))
	return got
}

func TestWalkFiles_OneFixedOrder(t *testing.T) {
	got := walkAll(t, sampleTree(), "")
	assert.Equal(t, []string{
		"/a.txt",
		"/docs/1.pdf", "/docs/2.pdf", "/docs/in/x.txt",
		"/docs-old.txt",
		"/z.txt",
	}, got, "a folder's contents come at the folder's place, names byte by byte")
}

// Resuming after ANY file gives exactly the rest of the walk: what the copy's
// cursor relies on to stop anywhere and go on without a file twice or missed.
func TestWalkFiles_ResumesAfterAnyFile(t *testing.T) {
	full := walkAll(t, sampleTree(), "")
	for i, cursor := range full {
		assert.Equal(t, full[i+1:], walkAll(t, sampleTree(), cursor), "resuming after %s", cursor)
	}
}

// A resumed walk lists only the folders on the way to the cursor and after
// it, never the ones it finished.
func TestWalkFiles_AResumedWalkDoesNotListFinishedFolders(t *testing.T) {
	d := sampleTree()
	walkAll(t, d, "/docs-old.txt")
	assert.Equal(t, 1, d.lists, "the finished folders (/b, /docs, /docs/in) were listed again")
}

func TestWalkFiles_StopsWhenToldAndSaysWhy(t *testing.T) {
	n := 0
	err := walkFiles(context.Background(), sampleTree(), "", func(storage.Object) error {
		n++
		if n == 2 {
			return errSliceOver
		}
		return nil
	})
	assert.True(t, errors.Is(err, errSliceOver))
	assert.Equal(t, 2, n)
}

func TestWalkBefore(t *testing.T) {
	assert.True(t, walkBefore("/a", "/a/b"), "a folder comes before what is in it")
	assert.True(t, walkBefore("/a/z", "/b"))
	assert.True(t, walkBefore("/docs/x", "/docs-old.txt"), "segment by segment, not the whole string")
	assert.False(t, walkBefore("/b", "/a/z"))
	assert.False(t, walkBefore("/a", "/a"))
	assert.True(t, isAncestor("/docs", "/docs/in/x.txt"))
	assert.False(t, isAncestor("/docs", "/docs-old.txt"))
}
