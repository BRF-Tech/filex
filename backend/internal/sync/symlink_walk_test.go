package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// What the catalogue walk does with symlinks.
//
// ⚠⚠ Until v0.43.0 the walk typed EVERY non-directory NodeTypeFile, symlinks
// included — and that was measured, not assumed: a root holding a link to a
// file outside it produced a `file` row, the antivirus queue was handed it,
// and the scanner read the bytes on the other end of the link. Out-of-root
// content was being virus-scanned, content-indexed, version-tracked and
// quota-counted, because all four gates ask `Type == NodeTypeFile`.

// A link the driver will not follow must not be catalogued as a file, because
// "file" is the word that opens all four of those gates.
func TestWalk_AnUnfollowableLinkIsNotCataloguedAsAFile(t *testing.T) {
	ctx := context.Background()
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("SECRET BYTES"), 0o644))
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "filelink.txt")); err != nil {
		t.Skipf("this host cannot create symlinks: %v", err)
	}
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "dirlink")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ordinary.txt"), []byte("mine"), 0o644))

	sc := &cleanScanner{}
	job := avJob(store, st, drv, sc)
	syncWithAV(t, store, st, job, qd)

	for _, p := range []string{"/filelink.txt", "/dirlink"} {
		n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, p))
		require.NoError(t, err)
		require.NotNil(t, n, "the link must still be CATALOGUED — hiding it is worse than showing it")
		assert.Equal(t, model.NodeTypeSymlink, n.Type,
			"%s was catalogued as %q; every content gate asks for NodeTypeFile", p, n.Type)
	}

	// The ordinary file is untouched by all of this.
	ord, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/ordinary.txt"))
	require.NoError(t, err)
	require.NotNil(t, ord)
	require.Equal(t, model.NodeTypeFile, ord.Type)

	// ⚠ The measurement that motivated the whole branch: the scanner used to
	// read the bytes behind the link. Exactly one file is eligible now.
	assert.Equal(t, []int64{ord.ID}, pendingScans(t, qd),
		"the antivirus queue was handed a symlink; it would read whatever is on the other end")
	require.Equal(t, 1, drainScans(t, qd, job))
	assert.Equal(t, 1, sc.scanned)
}

// A link that stays INSIDE the root is followed, so the walk catalogues it as
// the thing it points at and descends into it — which is what issue #34 asks
// for.
func TestWalk_AnInRootDirectoryLinkIsWalkedAsADirectory(t *testing.T) {
	ctx := context.Background()
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	inner := filepath.Join(root, "real")
	require.NoError(t, os.MkdirAll(inner, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(inner, "deep.txt"), []byte("DEEP"), 0o644))
	if err := os.Symlink("real", filepath.Join(root, "shortcut")); err != nil {
		t.Skipf("this host cannot create symlinks: %v", err)
	}

	job := avJob(store, st, drv, &cleanScanner{})
	syncWithAV(t, store, st, job, qd)

	link, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/shortcut"))
	require.NoError(t, err)
	require.NotNil(t, link)
	assert.Equal(t, model.NodeTypeDirectory, link.Type,
		"an in-root directory link came back as %q — this is issue #34, the 0-byte file that will not open", link.Type)

	through, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/shortcut/deep.txt"))
	require.NoError(t, err)
	assert.NotNil(t, through, "the walk did not descend through the link")
}

// ⚠⚠ The prerequisite. Making symlinked directories walkable without this is
// what turns one `ln -s . loop` into a catalogue full of duplicate rows:
// measured at depth 81 / 123 visits on Linux and 127 / 192 on Windows, each
// visit re-cataloguing the same subtree under a fresh path hash.
func TestWalk_TerminatesOnASymlinkLoopWithoutDuplicatingTheTree(t *testing.T) {
	ctx := context.Background()
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	inner := filepath.Join(root, "real")
	require.NoError(t, os.MkdirAll(inner, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(inner, "f.txt"), []byte("x"), 0o644))
	if err := os.Symlink(root, filepath.Join(inner, "cycle")); err != nil {
		t.Skipf("this host cannot create symlinks: %v", err)
	}

	job := avJob(store, st, drv, &cleanScanner{})
	run := syncWithAV(t, store, st, job, qd) // fails the test if the run does not finish "ok"

	// Every extra row is the same subtree catalogued again under a path that
	// only exists because the walk went round the loop.
	total, err := store.CountNodesByStorage(ctx, st.ID)
	require.NoError(t, err)
	assert.Less(t, total, int64(30),
		"the catalogue filled up with loop duplicates: %d rows for a storage holding one directory and one file (the run reported %d objects seen)",
		total, run.SeenCount)
}

// Why a link will not open is kept with its row (migration 00098), so the
// listing the catalogue answers after the sync - every listing, once the first
// sync is done - names the reason the driver gave ("Outside storage", "Broken
// link") instead of the general "Link".
//
// RED before 0.54: the catalogue kept only the row's type; there was no
// reason to read back.
func TestWalk_AnUnfollowableLinkKeepsWhyItWillNotOpen(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)

	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(outside, "old-projects"), 0o755))
	if err := os.Symlink(filepath.Join(outside, "old-projects"), filepath.Join(root, "archive")); err != nil {
		t.Skipf("this host cannot create symlinks: %v", err)
	}
	require.NoError(t, os.Symlink(filepath.Join(outside, "not-yet.txt"), filepath.Join(root, "later")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ordinary.txt"), []byte("mine"), 0o644))

	w := newWorkerFor(t, store, st)
	require.NoError(t, w.Trigger(ctx, st.ID))

	states := linkStatesAt(t, store, st.ID, "/archive", "/later", "/ordinary.txt")
	assert.Equal(t, storage.LinkOutsideRoot, states["/archive"], "the driver said why: the target is outside the root")
	assert.Equal(t, storage.LinkBroken, states["/later"], "the driver said why: the target does not exist")
	assert.Empty(t, states["/ordinary.txt"], "an ordinary file is not a link and carries no reason")

	// The target turns up - outside the root. The next pass says so.
	require.NoError(t, os.WriteFile(filepath.Join(outside, "not-yet.txt"), []byte("x"), 0o644))
	require.NoError(t, w.Trigger(ctx, st.ID))
	assert.Equal(t, storage.LinkOutsideRoot, linkStatesAt(t, store, st.ID, "/later")["/later"],
		"a reason that changed on the storage did not change in the catalogue on the next pass")
}

// A link row catalogued before 0.54 has no reason (the column is new, NULL on
// every existing row). The next pass that lists its folder fills it in - the
// upgrade needs nothing from the operator.
func TestWalk_ALinkRowWithNoReasonGetsOneOnTheNextPass(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "archive")); err != nil {
		t.Skipf("this host cannot create symlinks: %v", err)
	}
	w := newWorkerFor(t, store, st)
	require.NoError(t, w.Trigger(ctx, st.ID))

	link, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/archive"))
	require.NoError(t, err)
	require.NotNil(t, link)
	require.Equal(t, model.NodeTypeSymlink, link.Type)
	// What an upgraded catalogue holds: the row, no reason.
	cleared, err := store.SetNodeLinkState(ctx, link.ID, "")
	require.NoError(t, err)
	require.True(t, cleared, "fixture check: the first pass recorded a reason")
	require.Empty(t, linkStatesAt(t, store, st.ID, "/archive")["/archive"])

	require.NoError(t, w.Trigger(ctx, st.ID))
	assert.Equal(t, storage.LinkOutsideRoot, linkStatesAt(t, store, st.ID, "/archive")["/archive"],
		"a link row with no reason did not get one when its folder was listed again")
}

// linkStatesAt reads the recorded reason of the rows at paths, keyed by path.
// A row with none is absent.
func linkStatesAt(t *testing.T, store db.Store, storageID int64, paths ...string) map[string]string {
	t.Helper()
	ctx := context.Background()
	pathOf := map[int64]string{}
	ids := make([]int64, 0, len(paths))
	for _, p := range paths {
		n, err := store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, p))
		require.NoError(t, err, p)
		require.NotNil(t, n, "%s was not catalogued", p)
		pathOf[n.ID] = p
		ids = append(ids, n.ID)
	}
	got, err := store.NodeLinkStates(ctx, ids)
	require.NoError(t, err)
	out := map[string]string{}
	for id, state := range got {
		out[pathOf[id]] = state
	}
	return out
}
