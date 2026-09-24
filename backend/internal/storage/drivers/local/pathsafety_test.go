package local

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Containment tests for the LEXICAL layer — the one that decides before
// anything is touched on disk.
//
// ⚠ Every case here was a live escape. They are written so that they run on
// both hosts and assert the same property (the path stayed inside the root),
// because the two hosts reach that property by different routes: on Windows
// `\` is a separator and the payload has to be neutralised, on Linux it is an
// ordinary filename character and the payload is already harmless. A test that
// only ran on Linux would have gone green against the broken code.

// rootedAt returns a driver on an explicitly named root (rather than a random
// TempDir), so a test can control the root's NAME — which is half of the
// prefix bug.
func rootedAt(t *testing.T, root string, cfg ...map[string]any) *Driver {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0o755))
	c := map[string]any{"path": root}
	if len(cfg) > 0 {
		for k, v := range cfg[0] {
			c[k] = v
		}
	}
	d := &Driver{}
	require.NoError(t, d.Init(context.Background(), c))
	return d
}

// A backslash payload must not leave the root on the host where a backslash
// separates path components.
//
// Measured before the fix, on a GOOS=windows binary: resolve(`..\storage10`)
// returned `…\001\storage10` — a sibling of the root — and List on it came
// back with the sibling's entries. The same payload with a forward slash was
// correctly refused, which is what made this invisible for so long.
func TestResolve_BackslashPayloadStaysInsideTheRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "storage1")
	sibling := filepath.Join(base, "storage10")
	require.NoError(t, os.MkdirAll(sibling, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sibling, "tenant.txt"), []byte("other tenant"), 0o644))

	d := rootedAt(t, root)

	for _, p := range []string{`..\storage10`, `..\storage10\tenant.txt`, `sub\..\..\storage10`, `/..\storage10`} {
		abs, err := d.resolve(p)
		if runtime.GOOS == "windows" {
			// ⚠ On the host where a backslash IS a separator the payload must
			// be NEUTRALISED, not merely refused. Asserting only "it did not
			// escape" leaves this test green with the separator fold deleted,
			// because the boundary test below would refuse the escaped path
			// anyway — two layers, and a test that cannot tell which one is
			// doing the work protects neither. Verified by deleting the fold:
			// with this assertion the Windows run goes red, without it the
			// whole suite stayed green.
			require.NoError(t, err,
				"resolve(%q) was refused outright; the payload should have been cleaned to a path INSIDE the root", p)
			require.True(t, within(root, abs), "resolve(%q) = %q escaped %q", p, abs, root)
		}
		if err == nil {
			assert.True(t, within(root, abs),
				"resolve(%q) = %q, which is outside the root %q", p, abs, root)
		}
		// And the property that actually matters to a caller: the sibling
		// storage is not listable through this driver.
		if objs, lerr := d.List(context.Background(), p); lerr == nil {
			for _, o := range objs {
				assert.NotEqual(t, "tenant.txt", o.Name,
					"List(%q) returned the neighbouring storage's contents", p)
			}
		}
	}
}

// The forward-slash payload was always refused; it is pinned so a future
// rewrite of resolve cannot lose it while fixing the backslash one.
func TestResolve_ForwardSlashTraversalStaysInsideTheRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "storage1")
	sibling := filepath.Join(base, "storage10")
	require.NoError(t, os.MkdirAll(sibling, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sibling, "tenant.txt"), []byte("x"), 0o644))

	d := rootedAt(t, root)
	for _, p := range []string{"../storage10", "/../storage10", "a/../../storage10", "../../etc"} {
		abs, err := d.resolve(p)
		if err == nil {
			assert.True(t, within(root, abs), "resolve(%q) = %q escaped %q", p, abs, root)
		}
	}
}

// within is the root test itself, and the reason it is a function rather than
// a strings.HasPrefix call inline.
//
// ⚠⚠ The prefix form is reachable in the symlink appeal, not just in resolve:
// a link inside `/srv/storage1` pointing at `/srv/storage10` resolves to a
// path that HasPrefix accepts and that is, in fact, another tenant's storage.
func TestWithin_SiblingSharingTheRootsNamePrefixIsNotInside(t *testing.T) {
	sep := string(filepath.Separator)
	root := filepath.Join("/srv", "storage1")

	assert.True(t, within(root, root), "the root is inside itself")
	assert.True(t, within(root, filepath.Join(root, "a", "b.txt")))

	for _, outside := range []string{
		filepath.Join("/srv", "storage10"),
		filepath.Join("/srv", "storage10", "tenant.txt"),
		filepath.Join("/srv", "storage1-backup"),
		filepath.Join("/srv", "storage1x", "deep", "file"),
		"/srv",
		filepath.Join("/srv", "other"),
	} {
		assert.False(t, within(root, outside),
			"within(%q, %q) must be false — %q merely starts with the root's name", root, outside, outside)
	}
	_ = sep
}

// The host decides how names compare, and the test must not hardcode either
// answer: Windows matches case-insensitively, Linux does not. Measured with
// filepath.Rel on both.
func TestWithin_CaseSensitivityFollowsTheHost(t *testing.T) {
	root := filepath.Join(string(filepath.Separator)+"srv", "Storage")
	upper := filepath.Join(string(filepath.Separator)+"srv", "STORAGE", "file.txt")
	got := within(root, upper)
	if runtime.GOOS == "windows" {
		assert.True(t, got, "Windows compares names case-insensitively, so this IS inside the root")
	} else {
		assert.False(t, got, "Linux names are case-sensitive, so this is a different directory")
	}
}

// toSlash must fold the host's separators and nothing else. A Linux file
// really can be called `weird\name.txt`, and folding it would make an entry
// List returns impossible to open — the exact shape of the bug being fixed.
func TestToSlash_FoldsOnlyTheHostsSeparators(t *testing.T) {
	if runtime.GOOS == "windows" {
		assert.Equal(t, "a/b/c", toSlash(`a\b\c`))
		assert.Equal(t, "a/b/c", toSlash("a/b\\c"))
		return
	}
	assert.Equal(t, `weird\name.txt`, toSlash(`weird\name.txt`),
		"a backslash is a legal filename character on this host")

	// And end to end: a file whose NAME contains a backslash must survive a
	// round trip through the driver.
	d := newDriver(t)
	ctx := context.Background()
	require.NoError(t, d.Write(ctx, `weird\name.txt`, strings.NewReader("payload"), 7))
	objs, err := d.List(ctx, "/")
	require.NoError(t, err)
	require.Len(t, objs, 1)
	assert.Equal(t, `weird\name.txt`, objs[0].Name)
	rc, err := d.Read(ctx, objs[0].Path)
	require.NoError(t, err, "an entry List returned must be openable")
	_ = rc.Close()
}
