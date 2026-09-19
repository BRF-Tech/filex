package s3

// WalkTree hands the sync worker a whole subtree in one un-delimited listing
// (issue #33: one ListObjectsV2 per prefix made a 150K-object scan outlast its
// own interval). These pin that the single pass reports exactly what the
// per-directory List would have reported, directory by directory: the same
// files, the same synthesised folders, each folder once and before anything
// in it, the `.empty` marker and folder objects understood the same way.

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

func walkAll(t *testing.T, d *Driver, p string) []storage.Object {
	t.Helper()
	var out []storage.Object
	require.NoError(t, d.WalkTree(context.Background(), p, func(o storage.Object) error {
		out = append(out, o)
		return nil
	}))
	return out
}

func paths(objs []storage.Object) []string {
	out := make([]string, 0, len(objs))
	for _, o := range objs {
		if o.Kind == storage.KindDirectory {
			out = append(out, o.Path+"/")
		} else {
			out = append(out, o.Path)
		}
	}
	return out
}

func TestWalkTree_OnePassReportsWhatListWouldDirectoryByDirectory(t *testing.T) {
	// The stub answers the un-delimited listing (prefix "") with every key.
	d := statStub(t, map[string][]string{
		"": {
			"a/1.txt",
			"a/b/2.txt",
			"a/b/.empty", // marker: proves a/b, is not a file
			"c/",         // a folder object another tool wrote
			"root.txt",
		},
	})

	got := paths(walkAll(t, d, "/"))
	assert.Equal(t, []string{
		"/a/", "/a/1.txt",
		"/a/b/", "/a/b/2.txt",
		"/c/",
		"/root.txt",
	}, got)

	// Every directory exactly once, and before its first child.
	seen := map[string]int{}
	for i, o := range walkAll(t, d, "/") {
		if o.Kind == storage.KindDirectory {
			seen[o.Path]++
			continue
		}
		if o.Path != "/root.txt" {
			_, parentSeen := seen[dirOf(o.Path)]
			assert.True(t, parentSeen, "object %d %s came before its folder", i, o.Path)
		}
	}
	for p, n := range seen {
		assert.Equal(t, 1, n, "folder %s reported %d times", p, n)
	}
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return ""
}

// A storage mounted at a prefix reports logical paths, never bucket keys.
func TestWalkTree_PrefixIsStrippedFromEveryPath(t *testing.T) {
	d := statStub(t, map[string][]string{
		"fileman/": {"fileman/docs/a.txt", "fileman/b.txt", "fileman/"},
	})
	d.prefix = "fileman"

	assert.Equal(t, []string{"/docs/", "/docs/a.txt", "/b.txt"}, paths(walkAll(t, d, "/")))

	// And a subtree below the root walks with the same spelling.
	d2 := statStub(t, map[string][]string{
		"fileman/docs/": {"fileman/docs/a.txt", "fileman/docs/deep/z.txt"},
	})
	d2.prefix = "fileman"
	assert.Equal(t, []string{"/docs/a.txt", "/docs/deep/", "/docs/deep/z.txt"}, paths(walkAll(t, d2, "/docs")))
}

// The caller bounds the pass: its error stops the walk and comes back as-is.
func TestWalkTree_CallbackErrorStopsThePass(t *testing.T) {
	d := statStub(t, map[string][]string{
		"": {"a/1.txt", "a/2.txt", "a/3.txt"},
	})
	stop := errors.New("enough")
	calls := 0
	err := d.WalkTree(context.Background(), "/", func(storage.Object) error {
		calls++
		if calls == 2 {
			return stop
		}
		return nil
	})
	require.ErrorIs(t, err, stop)
	assert.Equal(t, 2, calls)
}

// A prefix with nothing under it is an empty listing, not a miss.
func TestWalkTree_EmptyPrefixYieldsNothing(t *testing.T) {
	d := statStub(t, map[string][]string{})
	assert.Empty(t, walkAll(t, d, "/nothing/here"))
}
