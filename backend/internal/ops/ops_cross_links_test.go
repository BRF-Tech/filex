package ops_test

// Links inside a folder carried to ANOTHER storage (cross.go, Transfer).
//
// The same-storage copy is one driver call, and the local driver's copyTree
// learned to skip links it may not follow in this release. The cross-storage
// transfer is filex's own walk, and it had neither that rule nor the cycle
// guard every other recursive walk carries. Measured 2026-09-21 before the
// fix, a folder holding one link of each kind copied to a second storage:
//
//	broken link            the WHOLE copy failed ("read tree/broken: not found")
//	link out of the root   the WHOLE copy failed ("symlink target is outside…")
//	`cycle -> .`           41 nested duplicate folders written, THEN failed
//	unresolved link on a   "ok", with the out-of-root file's bytes in the
//	source whose Read      destination
//	follows it (sftp/ftp)
//
// ⚠ These run on Linux (the suite's CI host): os.Symlink on Windows needs
// Developer Mode or elevation, and a test that skips there proves nothing.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
)

func needSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink needs Developer Mode on Windows; the suite runs these on Linux")
	}
}

// outsideDir is a directory beside the storage roots, holding bytes that must
// never arrive anywhere a transfer writes.
func outsideDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("OUTSIDE-SECRET"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "deep.txt"), []byte("OUTSIDE-DEEP"), 0o644))
	return dir
}

func linkA(t *testing.T, f *crossFixture, target, rel string) {
	t.Helper()
	abs := filepath.Join(f.rootA, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.Symlink(target, abs))
}

// walkB lists every file in the destination storage with its contents.
func walkB(t *testing.T, f *crossFixture) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.Walk(f.rootB, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		require.NoError(t, rerr)
		rel, _ := filepath.Rel(f.rootB, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	}))
	return out
}

func assertNothingFromOutside(t *testing.T, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		assert.Falsef(t, strings.HasPrefix(body, "OUTSIDE"), "%s carries bytes from OUTSIDE the source storage", rel)
	}
}

// The heart of it: one bad link must not cost the rest of the folder, and the
// row must say what was left out.
func TestCross_CopyTree_LeavesUnfollowableLinksBehindAndNamesThem(t *testing.T) {
	needSymlinks(t)
	f := newCrossFixture(t, nil)
	outside := outsideDir(t)
	writeA(t, f, "tree/a.txt", "a")
	writeA(t, f, "tree/sub/b.txt", "b")
	linkA(t, f, "../nope", "tree/broken")
	linkA(t, f, outside, "tree/escape-dir")
	linkA(t, f, filepath.Join(outside, "secret.txt"), "tree/sub/escape-file")

	op := f.run(t, ops.OpCopy, []string{"tree"}, "/")

	// ⚠ Before the fix: `failed`, with nothing but "tree/a.txt" copied — the
	// walk died at the first link it met (alphabetically, "broken").
	require.Equal(t, ops.StatusPartial, op.Status, "a copy that left entries behind must not read as clean: %s", op.Error)
	assert.Equal(t, 1, op.Done)
	assert.Equal(t, 0, op.Failed)
	files := walkB(t, f)
	assert.Equal(t, map[string]string{"tree/a.txt": "a", "tree/sub/b.txt": "b"}, files, "every ordinary file arrives; no link does")
	assertNothingFromOutside(t, files)
	// Said in words the person reads, one reason per link.
	for _, want := range []string{
		`"tree/broken" (a broken link)`,
		`"tree/escape-dir" (a link pointing outside the storage)`,
		`"tree/sub/escape-file" (a link pointing outside the storage)`,
		"3 entries were left out",
	} {
		assert.Contains(t, op.Error, want)
	}
}

// An in-root link is part of the storage: it is carried BY VALUE, exactly as
// the same-storage copy carries it, and it is not "left out".
func TestCross_CopyTree_CarriesInRootLinksByValue(t *testing.T) {
	needSymlinks(t)
	f := newCrossFixture(t, nil)
	writeA(t, f, "tree/a.txt", "a")
	writeA(t, f, "shared/c.txt", "c")
	linkA(t, f, "../shared", "tree/linked-dir")
	linkA(t, f, "../shared/c.txt", "tree/linked-file")

	op := f.run(t, ops.OpCopy, []string{"tree"}, "/")

	require.Equal(t, ops.StatusOK, op.Status, op.Error)
	assert.Equal(t, map[string]string{
		"tree/a.txt":            "a",
		"tree/linked-dir/c.txt": "c",
		"tree/linked-file":      "c",
	}, walkB(t, f))
	fi, err := os.Lstat(filepath.Join(f.rootB, "tree", "linked-dir"))
	require.NoError(t, err)
	assert.True(t, fi.IsDir() && fi.Mode()&os.ModeSymlink == 0, "a real folder at the destination, not a link")
}

// `cycle -> .` is walked ONCE (storage.CycleGuard records a link when the walk
// descends into it) and then refused, instead of nesting a copy per level
// until the OS gives up.
func TestCross_CopyTree_ALinkCycleIsWalkedOnceThenRefused(t *testing.T) {
	needSymlinks(t)
	f := newCrossFixture(t, nil)
	writeA(t, f, "tree/a.txt", "a")
	linkA(t, f, ".", "tree/cycle")

	op := f.run(t, ops.OpCopy, []string{"tree"}, "/")

	// ⚠ Before the fix: `failed` after writing tree/cycle/…/a.txt 41 levels
	// deep — the destination kept every one of those copies.
	require.Equal(t, ops.StatusPartial, op.Status, op.Error)
	assert.Equal(t, map[string]string{"tree/a.txt": "a", "tree/cycle/a.txt": "a"}, walkB(t, f))
	assert.Contains(t, op.Error, `"tree/cycle/cycle" (a folder link back into what was already copied)`)
}

// A move deletes only what it carried. The source is one tree and the delete
// is one call on it, so when anything was left behind the whole source stays —
// and the row says so, because the copy at the destination DID happen.
func TestCross_Move_KeepsTheSourceWhenAnythingWasLeftBehind(t *testing.T) {
	needSymlinks(t)
	f := newCrossFixture(t, nil)
	writeA(t, f, "tree/a.txt", "a")
	linkA(t, f, "../nope", "tree/broken")

	op := f.run(t, ops.OpMove, []string{"tree"}, "/")

	require.Equal(t, ops.StatusPartial, op.Status, op.Error)
	assert.Equal(t, map[string]string{"tree/a.txt": "a"}, walkB(t, f), "the copy is complete without the link")
	body, err := os.ReadFile(filepath.Join(f.rootA, "tree", "a.txt"))
	require.NoError(t, err, "the source must still be there")
	assert.Equal(t, "a", string(body))
	_, err = os.Lstat(filepath.Join(f.rootA, "tree", "broken"))
	require.NoError(t, err, "…and so must the link that could not travel")
	assert.Contains(t, op.Error, "the source was kept")
	assert.Contains(t, op.Error, `"tree/broken" (a broken link)`)
}

// ⚠ The one skip that CAN be real data nobody linked: a folder deeper than
// storage.MaxWalkDepth. It is why the move above keeps its source rather than
// deleting a tree that still holds something.
func TestCross_Move_AFolderTooDeepToWalkIsLeftAndKept(t *testing.T) {
	f := newCrossFixture(t, nil)
	parts := []string{"deep"}
	for i := 0; i < storage.MaxWalkDepth+1; i++ {
		parts = append(parts, "d")
	}
	writeA(t, f, strings.Join(append(append([]string(nil), parts...), "bottom.txt"), "/"), "real data")
	writeA(t, f, "deep/top.txt", "top")

	op := f.run(t, ops.OpMove, []string{"deep"}, "/")

	require.Equal(t, ops.StatusPartial, op.Status, op.Error)
	assert.Contains(t, op.Error, "more than 64 folders deep")
	assert.Contains(t, op.Error, "the source was kept")
	body, err := os.ReadFile(filepath.Join(append([]string{f.rootA}, append(parts, "bottom.txt")...)...))
	require.NoError(t, err, "the file below the walk's floor must survive the move")
	assert.Equal(t, "real data", string(body))
	assert.Equal(t, "top", readB(t, f, "deep/top.txt"))
}

// remoteLike is the shape of the sftp and ftp drivers: List reports a link as
// KindSymlink ("unresolved" — the driver does not vouch for it), and Read is
// the SERVER opening the path, which follows the link wherever it points.
type remoteLike struct {
	storage.Driver
	root  string
	reads *atomic.Int32 // Reads of a path that is a link
}

func (r remoteLike) List(ctx context.Context, p string) ([]storage.Object, error) {
	objs, err := r.Driver.List(ctx, p)
	for i := range objs {
		if objs[i].Kind == storage.KindSymlink {
			objs[i].Metadata = map[string]string{storage.MetaLinkState: storage.LinkUnresolved}
		}
	}
	return objs, err
}

func (r remoteLike) Read(_ context.Context, p string) (io.ReadCloser, error) {
	abs := filepath.Join(r.root, filepath.FromSlash(p))
	if fi, err := os.Lstat(abs); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		r.reads.Add(1)
	}
	return os.Open(abs)
}

// ⚠⚠ The leak: before the fix this copied "OUTSIDE-SECRET" into the
// destination and answered success. The rule that closes it is not "Read will
// refuse" — a remote Read will not — but "a link the driver did not resolve is
// never opened".
func TestTransfer_NeverOpensALinkTheSourceDriverDidNotResolve(t *testing.T) {
	needSymlinks(t)
	ctx := context.Background()
	f := newCrossFixture(t, nil)
	outside := outsideDir(t)
	writeA(t, f, "tree/a.txt", "a")
	linkA(t, f, filepath.Join(outside, "secret.txt"), "tree/escape-file")
	src := remoteLike{Driver: f.drvA, root: f.rootA, reads: &atomic.Int32{}}

	skipped, err := ops.Transfer(ctx, src, f.drvB, "tree", "tree", ops.TransferHooks{})

	require.NoError(t, err)
	assert.Equal(t, []ops.Skipped{{Path: "tree/escape-file", Reason: storage.LinkUnresolved}}, skipped)
	assert.Zero(t, src.reads.Load(), "the link was opened")
	files := walkB(t, f)
	assertNothingFromOutside(t, files)
	assert.Equal(t, map[string]string{"tree/a.txt": "a"}, files)
}

// When the ONE thing asked for is such a link there are no neighbours to
// spare: the transfer fails and says what it was, rather than answering
// "done" for nothing.
func TestTransfer_ALinkAskedForByItselfFails(t *testing.T) {
	needSymlinks(t)
	ctx := context.Background()
	f := newCrossFixture(t, nil)
	linkA(t, f, "nope", "broken")

	skipped, err := ops.Transfer(ctx, f.drvA, f.drvB, "broken", "broken", ops.TransferHooks{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `"broken" is a broken link`)
	assert.Empty(t, skipped)
	assert.Empty(t, walkB(t, f))
}

// The message is a toast and a column, so it names a handful and counts the
// rest instead of growing with the folder.
func TestSkipsError_NamesAFewAndCountsTheRest(t *testing.T) {
	var sk []ops.Skipped
	for i := 0; i < 8; i++ {
		sk = append(sk, ops.Skipped{Path: "tree/l" + string(rune('0'+i)), Reason: storage.LinkBroken})
	}
	msg := (&ops.SkipsError{Skipped: sk}).Error()
	assert.Contains(t, msg, "8 entries were left out")
	assert.Contains(t, msg, `"tree/l4"`)
	assert.NotContains(t, msg, `"tree/l5"`)
	assert.Contains(t, msg, "and 3 more")
}
