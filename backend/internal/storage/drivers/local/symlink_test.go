package local

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// Symlink containment and classification.
//
// ⚠⚠ The escape these tests pin was complete: before the fix, one
// `ln -s /etc <root>/escape` gave Read, ReadRange, List, Stat, Write, Mkdir,
// Copy, Move, SetMtime and a RECURSIVE Delete over the linked tree, measured
// on Linux AND Windows. Planting the link needs filesystem access to the
// server — it cannot be done through the app — but the reporter of issue #34
// planted one on purpose and had no idea what it granted.

// linkFixture builds the standard shape every test below needs: a root with a
// real in-root directory, a RELATIVE in-root link, an ABSOLUTE in-root link, a
// directory link that leaves the root, a file link that leaves the root, and a
// broken link.
type linkFixture struct {
	root, outside string
	d             *Driver
}

func newLinkFixture(t *testing.T, cfg ...map[string]any) linkFixture {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "storage")
	outside := filepath.Join(base, "outside")
	real := filepath.Join(root, "real")
	for _, dir := range []string{root, filepath.Join(outside, "sub"), real} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(real, "inside.txt"), []byte("INSIDE"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("SECRET"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "sub", "deep.txt"), []byte("DEEP"), 0o644))

	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		// Windows needs Developer Mode or elevation to create one. A skipped
		// test is honest; a test that silently does not exercise the fix is not.
		t.Skipf("this host cannot create symlinks: %v", err)
	}
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escapefile")))
	require.NoError(t, os.Symlink("real", filepath.Join(root, "rel")))
	require.NoError(t, os.Symlink(real, filepath.Join(root, "abs")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "gone.txt"), filepath.Join(root, "broken")))

	return linkFixture{root: root, outside: outside, d: rootedAt(t, root, cfg...)}
}

func (f linkFixture) entry(t *testing.T, name string) storage.Object {
	t.Helper()
	objs, err := f.d.List(context.Background(), "/")
	require.NoError(t, err)
	for _, o := range objs {
		if o.Name == name {
			return o
		}
	}
	t.Fatalf("List did not return %q", name)
	return storage.Object{}
}

// ---------------------------------------------------------------------------
// Phase 2 — containment
// ---------------------------------------------------------------------------

// Every verb the driver advertises, through a directory link that leaves the
// root. This is the escape itself; each sub-test failed before the fix.
func TestSymlink_EveryVerbRefusesToLeaveTheRoot(t *testing.T) {
	ctx := context.Background()
	f := newLinkFixture(t)

	t.Run("List", func(t *testing.T) {
		_, err := f.d.List(ctx, "escape")
		require.ErrorIs(t, err, ErrLinkLeavesRoot)
	})
	t.Run("Read", func(t *testing.T) {
		_, err := f.d.Read(ctx, "escape/secret.txt")
		require.ErrorIs(t, err, ErrLinkLeavesRoot)
	})
	t.Run("Read through a FILE link", func(t *testing.T) {
		// ⚠ The file link is the case an Lstat-based check waves straight
		// through: Lstat does not dereference, so the link node looks
		// perfectly in-root. Measured: os.Root.Lstat("escapefile") → nil.
		_, err := f.d.Read(ctx, "escapefile")
		require.ErrorIs(t, err, ErrLinkLeavesRoot)
	})
	t.Run("ReadRange", func(t *testing.T) {
		_, err := f.d.ReadRange(ctx, "escape/secret.txt", 1, 3)
		require.ErrorIs(t, err, ErrLinkLeavesRoot)
	})
	t.Run("Write", func(t *testing.T) {
		err := f.d.Write(ctx, "escape/planted.txt", bytes.NewReader([]byte("x")), 1)
		require.ErrorIs(t, err, ErrLinkLeavesRoot)
		_, serr := os.Stat(filepath.Join(f.outside, "planted.txt"))
		assert.True(t, os.IsNotExist(serr), "bytes were written outside the storage root")
	})
	t.Run("Write THROUGH an existing file link", func(t *testing.T) {
		err := f.d.Write(ctx, "escapefile", bytes.NewReader([]byte("OVERWRITTEN")), 11)
		require.ErrorIs(t, err, ErrLinkLeavesRoot)
		b, rerr := os.ReadFile(filepath.Join(f.outside, "secret.txt"))
		require.NoError(t, rerr)
		assert.Equal(t, "SECRET", string(b), "an upload overwrote a file outside the storage root")
	})
	t.Run("Mkdir", func(t *testing.T) {
		err := f.d.Mkdir(ctx, "escape/newdir")
		require.ErrorIs(t, err, ErrLinkLeavesRoot)
		_, serr := os.Stat(filepath.Join(f.outside, "newdir"))
		assert.True(t, os.IsNotExist(serr))
	})
	t.Run("SetMtime", func(t *testing.T) {
		err := f.d.SetMtime(ctx, "escape/secret.txt", time.Unix(1000000, 0))
		require.ErrorIs(t, err, ErrLinkLeavesRoot)
	})
	t.Run("Copy source", func(t *testing.T) {
		err := f.d.Copy(ctx, "escape/secret.txt", "stolen.txt")
		require.ErrorIs(t, err, ErrLinkLeavesRoot)
		_, serr := os.Stat(filepath.Join(f.root, "stolen.txt"))
		assert.True(t, os.IsNotExist(serr), "outside bytes were copied into the storage")
	})
	t.Run("Copy destination", func(t *testing.T) {
		require.NoError(t, f.d.Write(ctx, "ordinary.txt", bytes.NewReader([]byte("x")), 1))
		err := f.d.Copy(ctx, "ordinary.txt", "escape/copied.txt")
		require.ErrorIs(t, err, ErrLinkLeavesRoot)
	})
	t.Run("Move destination", func(t *testing.T) {
		require.NoError(t, f.d.Write(ctx, "movable.txt", bytes.NewReader([]byte("x")), 1))
		err := f.d.Move(ctx, "movable.txt", "escape/moved.txt")
		require.ErrorIs(t, err, ErrLinkLeavesRoot)
	})
	t.Run("Delete THROUGH the link", func(t *testing.T) {
		err := f.d.Delete(ctx, "escape/sub")
		require.ErrorIs(t, err, ErrLinkLeavesRoot)
		_, serr := os.Stat(filepath.Join(f.outside, "sub", "deep.txt"))
		require.NoError(t, serr, "a recursive delete reached outside the storage root")
	})
}

// A link whose target is inside the root is ALWAYS followed, whatever
// follow_symlinks says. There is nothing to protect — the target is within the
// boundary — and refusing it is what issue #34 was filed about.
func TestSymlink_InRootLinksAreAlwaysUsable(t *testing.T) {
	ctx := context.Background()
	f := newLinkFixture(t)

	for _, link := range []string{"rel", "abs"} {
		t.Run(link, func(t *testing.T) {
			objs, err := f.d.List(ctx, link)
			require.NoError(t, err, "an in-root link must be listable")
			names := make([]string, 0, len(objs))
			for _, o := range objs {
				names = append(names, o.Name)
			}
			assert.Contains(t, names, "inside.txt", "listing through %q did not reach the target", link)

			rc, err := f.d.Read(ctx, link+"/inside.txt")
			require.NoError(t, err)
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			assert.Equal(t, "INSIDE", string(b))

			require.NoError(t, f.d.Write(ctx, link+"/written-"+link+".txt", bytes.NewReader([]byte("W")), 1))
			require.NoError(t, f.d.Mkdir(ctx, link+"/newdir-"+link))
			require.NoError(t, f.d.Delete(ctx, link+"/written-"+link+".txt"))
			assert.NoFileExists(t, filepath.Join(f.root, "real", "written-"+link+".txt"))
			assert.DirExists(t, filepath.Join(f.root, "real", "newdir-"+link),
				"the write landed somewhere other than the link's target")
		})
	}
}

// ⚠⚠ The absolute in-root link is the reason os.Root is an oracle here and not
// the rule. os.Root refuses `ln -s <root>/real <root>/abs` outright while
// allowing the relative `ln -s real rel` (measured on both hosts), and shipping
// that would mean `ln -s /data/files/photos /data/files/pics` breaks while
// `ln -s photos pics` works — a distinction no user can explain.
func TestSymlink_AbsoluteInRootLinkWorksJustLikeARelativeOne(t *testing.T) {
	ctx := context.Background()
	f := newLinkFixture(t)

	oracle, err := os.OpenRoot(f.root)
	require.NoError(t, err)
	_, oracleErr := oracle.Stat(filepath.Join("abs", "inside.txt"))
	_ = oracle.Close()
	require.Error(t, oracleErr,
		"precondition: os.Root is expected to refuse the absolute in-root link — if it no longer does, the appeal branch can be simplified")

	rc, err := f.d.Read(ctx, "abs/inside.txt")
	require.NoError(t, err, "the driver must overturn the oracle for a target that IS inside the root")
	_ = rc.Close()

	// And the appeal must also carry a name that does not exist yet, which is
	// every upload.
	require.NoError(t, f.d.Write(ctx, "abs/brand-new.txt", bytes.NewReader([]byte("N")), 1))
	assert.FileExists(t, filepath.Join(f.root, "real", "brand-new.txt"))
}

// The link NODE is inside the root even when its target is not, so an operator
// must be able to get rid of it. Measured on both hosts: RemoveAll on a
// directory link unlinks the link and leaves the tree behind it alone.
func TestSymlink_TheLinkItselfCanBeDeletedAndRenamed(t *testing.T) {
	ctx := context.Background()
	f := newLinkFixture(t)

	require.NoError(t, f.d.Delete(ctx, "escape"), "the operator cannot remove a link they are not allowed to follow")
	_, err := os.Lstat(filepath.Join(f.root, "escape"))
	assert.True(t, os.IsNotExist(err), "the link node survived its own deletion")
	require.FileExists(t, filepath.Join(f.outside, "sub", "deep.txt"),
		"deleting the LINK destroyed the tree it pointed at")

	require.NoError(t, f.d.Move(ctx, "escapefile", "renamed-link"))
	fi, err := os.Lstat(filepath.Join(f.root, "renamed-link"))
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the rename replaced the link with its target's bytes")
	b, err := os.ReadFile(filepath.Join(f.outside, "secret.txt"))
	require.NoError(t, err, "renaming the link moved the file it pointed at")
	assert.Equal(t, "SECRET", string(b))
}

// ⚠⚠ A sibling directory whose name merely EXTENDS the root's is the case a
// strings.HasPrefix containment test accepts. It is reachable through a link,
// which is why `within` exists.
func TestSymlink_TargetInASiblingSharingTheRootsNamePrefixIsRefused(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	root := filepath.Join(base, "storage1")
	sibling := filepath.Join(base, "storage10")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(sibling, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sibling, "tenant.txt"), []byte("other tenant"), 0o644))
	if err := os.Symlink(sibling, filepath.Join(root, "neighbour")); err != nil {
		t.Skipf("this host cannot create symlinks: %v", err)
	}
	d := rootedAt(t, root)

	_, err := d.List(ctx, "neighbour")
	require.ErrorIs(t, err, ErrLinkLeavesRoot,
		"storage1 listed storage10 — the containment test is comparing strings, not paths")
	_, err = d.Read(ctx, "neighbour/tenant.txt")
	require.ErrorIs(t, err, ErrLinkLeavesRoot)
}

// With the option on, the operator has said yes and everything works again.
func TestSymlink_FollowSymlinksReopensTheDoorDeliberately(t *testing.T) {
	ctx := context.Background()
	f := newLinkFixture(t, map[string]any{"follow_symlinks": true})

	objs, err := f.d.List(ctx, "escape")
	require.NoError(t, err)
	names := make([]string, 0, len(objs))
	for _, o := range objs {
		names = append(names, o.Name)
	}
	assert.Contains(t, names, "secret.txt")

	rc, err := f.d.Read(ctx, "escape/secret.txt")
	require.NoError(t, err)
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	assert.Equal(t, "SECRET", string(b))

	// And the listing reports the link as its target, not as an unusable node.
	assert.Equal(t, storage.KindDirectory, f.entry(t, "escape").Kind)
}

// A `..` payload is refused whatever follow_symlinks says: the option is about
// links, not about traversal.
func TestSymlink_FollowSymlinksDoesNotReopenTraversal(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "storage1")
	sibling := filepath.Join(base, "storage10")
	require.NoError(t, os.MkdirAll(sibling, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sibling, "tenant.txt"), []byte("x"), 0o644))
	d := rootedAt(t, root, map[string]any{"follow_symlinks": true})

	for _, p := range []string{`..\storage10`, "../storage10"} {
		if abs, err := d.resolve(p); err == nil {
			assert.True(t, within(root, abs), "resolve(%q) = %q left the root with following on", p, abs)
		}
	}
}

// ---------------------------------------------------------------------------
// Phase 3 — issue #34 itself: List must agree with Stat
// ---------------------------------------------------------------------------

// The reported bug. A directory symlink listed as a 0-byte KindSymlink while
// Stat on the very same object said KindDirectory, so the explorer drew a file
// that would not open.
func TestList_DirectorySymlinkAgreesWithStat(t *testing.T) {
	ctx := context.Background()
	f := newLinkFixture(t)

	for _, link := range []string{"rel", "abs"} {
		listed := f.entry(t, link)
		stat, err := f.d.Stat(ctx, link)
		require.NoError(t, err)

		assert.Equal(t, storage.KindDirectory, listed.Kind,
			"List called the directory link %q a %s — this is issue #34", link, listed.Kind)
		assert.Equal(t, stat.Kind, listed.Kind, "List and Stat disagree about %q", link)
		assert.Equal(t, storage.LinkFollowed, listed.Metadata[storage.MetaLinkState])
	}
}

// A file link inside the root reports the TARGET's size, not the length of the
// target path — which is where "0 bytes" (Windows) and "44 bytes" (Linux) came
// from.
func TestList_InRootFileSymlinkReportsTheTargetsSize(t *testing.T) {
	ctx := context.Background()
	f := newLinkFixture(t)
	require.NoError(t, os.Symlink(filepath.Join(f.root, "real", "inside.txt"), filepath.Join(f.root, "filelink")))

	listed := f.entry(t, "filelink")
	assert.Equal(t, storage.KindFile, listed.Kind)
	assert.EqualValues(t, len("INSIDE"), listed.Size, "the link's own size was reported instead of the target's")

	stat, err := f.d.Stat(ctx, "filelink")
	require.NoError(t, err)
	assert.Equal(t, listed.Kind, stat.Kind)
	assert.Equal(t, listed.Size, stat.Size)
}

// An out-of-root link is SHOWN with a reason, never hidden. Hiding would
// replace "a file that will not open" with "a file that is not there", which
// is worse for the person who put the link there.
func TestList_OutOfRootLinkIsShownWithAnExplanation(t *testing.T) {
	ctx := context.Background()
	f := newLinkFixture(t)

	for _, name := range []string{"escape", "escapefile"} {
		listed := f.entry(t, name)
		assert.Equal(t, storage.KindSymlink, listed.Kind, "%q", name)
		assert.Equal(t, storage.LinkOutsideRoot, listed.Metadata[storage.MetaLinkState],
			"%q came back unusable with no reason attached", name)

		// ⚠ Stat must say the same thing rather than erroring, or the entry
		// the listing explains becomes a 500 the moment anything asks about
		// it — and List and Stat are back to disagreeing, which is the bug.
		st, err := f.d.Stat(ctx, name)
		require.NoError(t, err, "Stat(%q) must describe the link, not refuse it", name)
		assert.Equal(t, storage.KindSymlink, st.Kind)
		assert.Equal(t, storage.LinkOutsideRoot, st.Metadata[storage.MetaLinkState])
	}
}

func TestList_BrokenLinkSaysSo(t *testing.T) {
	f := newLinkFixture(t)
	listed := f.entry(t, "broken")
	assert.Equal(t, storage.KindSymlink, listed.Kind)
	assert.Equal(t, storage.LinkBroken, listed.Metadata[storage.MetaLinkState])
}

// ---------------------------------------------------------------------------
// copyTree
// ---------------------------------------------------------------------------

// A folder copy must not pull outside bytes in through a link it finds inside
// the tree. filepath.Walk reports Lstat info, so the link used to go straight
// to copyFile, which opens it and follows it.
func TestCopy_TreeDoesNotPullOutsideBytesInThroughALink(t *testing.T) {
	ctx := context.Background()
	f := newLinkFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "folder"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "folder", "ok.txt"), []byte("OK"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(f.outside, "secret.txt"), filepath.Join(f.root, "folder", "sneaky.txt")))
	require.NoError(t, os.Symlink(f.outside, filepath.Join(f.root, "folder", "sneakydir")))

	require.NoError(t, f.d.Copy(ctx, "folder", "copy"),
		"one unfollowable link must not cost the operator the rest of the folder")
	assert.FileExists(t, filepath.Join(f.root, "copy", "ok.txt"))
	assert.NoFileExists(t, filepath.Join(f.root, "copy", "sneaky.txt"),
		"outside bytes were copied into the storage through a link inside the tree")
	assert.NoFileExists(t, filepath.Join(f.root, "copy", "sneakydir"))
}
