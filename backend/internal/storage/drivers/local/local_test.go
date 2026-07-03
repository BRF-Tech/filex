package local

import (
	"bytes"
	"context"
	"io"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// newDriver returns an Init'd local driver pointed at t.TempDir().
func newDriver(t *testing.T) *Driver {
	t.Helper()
	d := &Driver{}
	root := t.TempDir()
	require.NoError(t, d.Init(context.Background(), map[string]any{"root": root}))
	require.Equal(t, root, d.Root())
	return d
}

func TestInit_RootRequired(t *testing.T) {
	d := &Driver{}
	err := d.Init(context.Background(), map[string]any{})
	require.Error(t, err)
}

func TestMkdirAndList_Root(t *testing.T) {
	d := newDriver(t)
	ctx := context.Background()

	require.NoError(t, d.Mkdir(ctx, "/foo"))

	entries, err := d.List(ctx, "/")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "foo", entries[0].Name)
	assert.Equal(t, storage.KindDirectory, entries[0].Kind)
}

func TestStat_Directory(t *testing.T) {
	d := newDriver(t)
	ctx := context.Background()
	require.NoError(t, d.Mkdir(ctx, "/foo"))

	obj, err := d.Stat(ctx, "/foo")
	require.NoError(t, err)
	assert.Equal(t, storage.KindDirectory, obj.Kind)
	assert.Equal(t, "foo", obj.Name)
}

func TestStat_NotFound(t *testing.T) {
	d := newDriver(t)
	_, err := d.Stat(context.Background(), "/nope")
	require.Error(t, err)
	// local driver translates os.IsNotExist to storage.ErrNotFound.
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestWriteAndRead(t *testing.T) {
	d := newDriver(t)
	ctx := context.Background()
	require.NoError(t, d.Mkdir(ctx, "/foo"))

	body := "hello world"
	require.NoError(t, d.Write(ctx, "/foo/bar.txt", strings.NewReader(body), int64(len(body))))

	rc, err := d.Read(ctx, "/foo/bar.txt")
	require.NoError(t, err)
	defer rc.Close()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, body, string(got))
}

// TestWrite_AutoCreatesParent ensures Write does not fail when the parent
// directory is missing — local.Write os.MkdirAll's the parent.
func TestWrite_AutoCreatesParent(t *testing.T) {
	d := newDriver(t)
	ctx := context.Background()
	body := []byte("hi")
	require.NoError(t, d.Write(ctx, "/auto/created/dir/file.txt", bytes.NewReader(body), int64(len(body))))
	rc, err := d.Read(ctx, "/auto/created/dir/file.txt")
	require.NoError(t, err)
	defer rc.Close()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "hi", string(got))
}

func TestMove(t *testing.T) {
	d := newDriver(t)
	ctx := context.Background()
	require.NoError(t, d.Mkdir(ctx, "/foo"))
	require.NoError(t, d.Write(ctx, "/foo/bar.txt", strings.NewReader("data"), 4))

	require.NoError(t, d.Move(ctx, "/foo/bar.txt", "/baz.txt"))

	// /foo should now be empty
	entries, err := d.List(ctx, "/foo")
	require.NoError(t, err)
	assert.Empty(t, entries)

	// /baz.txt should exist with the original content
	rc, err := d.Read(ctx, "/baz.txt")
	require.NoError(t, err)
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	assert.Equal(t, "data", string(got))
}

func TestCopy_File(t *testing.T) {
	d := newDriver(t)
	ctx := context.Background()
	require.NoError(t, d.Write(ctx, "/src.txt", strings.NewReader("payload"), 7))

	require.NoError(t, d.Copy(ctx, "/src.txt", "/dst.txt"))

	src, err := d.Read(ctx, "/src.txt")
	require.NoError(t, err)
	defer src.Close()
	srcBytes, _ := io.ReadAll(src)

	dst, err := d.Read(ctx, "/dst.txt")
	require.NoError(t, err)
	defer dst.Close()
	dstBytes, _ := io.ReadAll(dst)

	assert.Equal(t, srcBytes, dstBytes)
}

func TestCopy_Directory(t *testing.T) {
	d := newDriver(t)
	ctx := context.Background()
	require.NoError(t, d.Mkdir(ctx, "/src"))
	require.NoError(t, d.Mkdir(ctx, "/src/sub"))
	require.NoError(t, d.Write(ctx, "/src/a.txt", strings.NewReader("A"), 1))
	require.NoError(t, d.Write(ctx, "/src/sub/b.txt", strings.NewReader("B"), 1))

	require.NoError(t, d.Copy(ctx, "/src", "/dst"))

	rc, err := d.Read(ctx, "/dst/sub/b.txt")
	require.NoError(t, err)
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	assert.Equal(t, "B", string(got))
}

func TestDelete(t *testing.T) {
	d := newDriver(t)
	ctx := context.Background()
	require.NoError(t, d.Write(ctx, "/file.txt", strings.NewReader("x"), 1))

	require.NoError(t, d.Delete(ctx, "/file.txt"))

	_, err := d.Stat(ctx, "/file.txt")
	require.Error(t, err)
}

// TestDelete_Idempotent verifies deleting a missing path is not an error
// — matches storage.Driver contract docs.
func TestDelete_Idempotent(t *testing.T) {
	d := newDriver(t)
	require.NoError(t, d.Delete(context.Background(), "/never-existed"))
}

// TestPathEscape_Read attempts a `..` traversal — the local driver clean
// joins inside its root, so the path must be rejected (or normalized to
// stay inside the root, which still won't read /etc/passwd).
func TestPathEscape_Read(t *testing.T) {
	d := newDriver(t)
	ctx := context.Background()

	// Various traversal payloads. After path.Clean, all of these resolve
	// to a path inside the root or to "/" — none escape.
	cases := []string{
		"../../../etc/passwd",
		"/../etc/passwd",
		"foo/../../bar",
	}
	for _, p := range cases {
		_, err := d.Read(ctx, p)
		// Either ErrNotFound (cleaned to a non-existent path inside root)
		// or an explicit "path escapes root" — both safe outcomes.
		require.Error(t, err, "expected error reading %q", p)
	}
}

// TestPathEscape_Write same shape as Read but for Write.
func TestPathEscape_Write(t *testing.T) {
	d := newDriver(t)
	ctx := context.Background()

	// Writing through a `..` payload must NOT land outside the root.
	body := []byte("payload")
	err := d.Write(ctx, "../escape.txt", bytes.NewReader(body), int64(len(body)))
	// Even if Write succeeds (because path.Clean normalizes the slash),
	// the file MUST land inside the configured root, never above it.
	if err == nil {
		// If Write succeeded, verify Stat sees the file inside root.
		obj, err := d.Stat(ctx, "/escape.txt")
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(path.Clean(obj.Path), "/"))
	}
}

func TestList_NotFound(t *testing.T) {
	d := newDriver(t)
	_, err := d.List(context.Background(), "/totally-not-there")
	require.Error(t, err)
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

func TestCapabilities(t *testing.T) {
	d := newDriver(t)
	c := d.Capabilities()
	assert.True(t, c.Read)
	assert.True(t, c.Write)
	assert.True(t, c.Move)
	assert.True(t, c.Copy)
	assert.True(t, c.Delete)
	assert.True(t, c.Mkdir)
	assert.True(t, c.Watch)
}

func TestComputeCapabilities_Composed(t *testing.T) {
	d := newDriver(t)
	c := storage.ComputeCapabilities(d)
	// All of these must come back true because the local driver
	// implements every optional sub-interface.
	assert.True(t, c.Read)
	assert.True(t, c.Write)
	assert.True(t, c.Move)
	assert.True(t, c.Copy)
	assert.True(t, c.Delete)
	assert.True(t, c.Mkdir)
}
