package local

// A mount under a storage root (issue #133). These stand in for the kernel's
// answer, so they run on every host and as any user; the real boundary is
// measured in internal/trash/put_crossdevice_test.go.
//
// The invariant is the one a rename gives for free and a copy has to earn: a
// move that fails has changed nothing, and a move that succeeds has lost
// nothing.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// mounted builds a storage root with `gdrive` playing the mount: a rename in or
// out of it answers what a rename across filesystems answers.
func mounted(t *testing.T) (*Driver, string) {
	t.Helper()
	root := t.TempDir()
	mount := filepath.Join(root, "gdrive")
	require.NoError(t, os.MkdirAll(mount, 0o755))
	real := rename
	rename = func(a, b string) error {
		if within(mount, a) != within(mount, b) {
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: errCrossDevice}
		}
		return real(a, b)
	}
	t.Cleanup(func() { rename = real })
	return rootedAt(t, root), root
}

// failRemoving makes the removal of the named sources answer err.
func failRemoving(t *testing.T, err error, names ...string) {
	t.Helper()
	real := removeNode
	removeNode = func(p string) error {
		for _, n := range names {
			if filepath.Base(p) == n {
				return err
			}
		}
		return real(p)
	}
	t.Cleanup(func() { removeNode = real })
}

func put(t *testing.T, p, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
}

func body(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return string(b)
}

// noStage fails on a staged copy left behind.
func noStage(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		require.NoError(t, err)
		for _, e := range entries {
			assert.False(t, strings.HasPrefix(e.Name(), ".filex-move-"), "%s left in %s", e.Name(), dir)
		}
	}
}

func TestMove_AFileLeavesTheMountWithItsBytesAndTimes(t *testing.T) {
	d, root := mounted(t)
	src := filepath.Join(root, "gdrive", "doc.pdf")
	put(t, src, "the only copy")
	when := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	require.NoError(t, os.Chmod(src, 0o640))
	require.NoError(t, os.Chtimes(src, when, when))

	require.NoError(t, d.Move(context.Background(), "gdrive/doc.pdf", ".filex-trash/key__doc.pdf"))

	dst := filepath.Join(root, ".filex-trash", "key__doc.pdf")
	assert.Equal(t, "the only copy", body(t, dst))
	assert.NoFileExists(t, src)
	info, err := os.Stat(dst)
	require.NoError(t, err)
	assert.True(t, info.ModTime().Equal(when), "a rename keeps the time, and the scan compares it: %v", info.ModTime())
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	}
	noStage(t, filepath.Join(root, ".filex-trash"))
}

func TestMove_AFolderLeavesTheMountWhole(t *testing.T) {
	d, root := mounted(t)
	src := filepath.Join(root, "gdrive", "photos")
	put(t, filepath.Join(src, "a.jpg"), "A")
	put(t, filepath.Join(src, "deep", "er", "b.jpg"), "B")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "empty"), 0o755))

	tally := &storage.Tally{}
	ctx := storage.WithTally(context.Background(), tally)
	require.NoError(t, d.Move(ctx, "gdrive/photos", "archive/photos"))

	dst := filepath.Join(root, "archive", "photos")
	assert.Equal(t, "A", body(t, filepath.Join(dst, "a.jpg")))
	assert.Equal(t, "B", body(t, filepath.Join(dst, "deep", "er", "b.jpg")))
	assert.DirExists(t, filepath.Join(dst, "empty"), "an empty folder is something the person made")
	assert.NoDirExists(t, src)
	done, found := tally.Load()
	assert.Equal(t, int64(2), found, "a folder across a mount takes minutes: its progress has to move")
	assert.Equal(t, int64(2), done)
	noStage(t, filepath.Join(root, "archive"))
}

func TestMove_BackOntoTheMountRestores(t *testing.T) {
	d, root := mounted(t)
	put(t, filepath.Join(root, ".filex-trash", "key__doc.pdf"), "restored")

	require.NoError(t, d.Move(context.Background(), ".filex-trash/key__doc.pdf", "gdrive/Documents/doc.pdf"))

	assert.Equal(t, "restored", body(t, filepath.Join(root, "gdrive", "Documents", "doc.pdf")))
	assert.NoFileExists(t, filepath.Join(root, ".filex-trash", "key__doc.pdf"))
}

func TestMove_AMissingSourceStaysNotFound(t *testing.T) {
	d, _ := mounted(t)

	err := d.Move(context.Background(), "gdrive/gone.pdf", ".filex-trash/key__gone.pdf")

	require.ErrorIs(t, err, storage.ErrNotFound, "trash.Put reads this as nothing left to trash")
}

// ⚠⚠ What a copy-then-delete would lose. copyTree skips a link it may not
// follow, so with a delete behind it the link would simply be gone, and
// following one would read outside the root.
func TestMove_LinksTravelAsLinksAndAreNeverFollowed(t *testing.T) {
	d, root := mounted(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	put(t, outside, "SECRET")
	folder := filepath.Join(root, "gdrive", "folder")
	put(t, filepath.Join(folder, "real.txt"), "REAL")
	if err := os.Symlink(outside, filepath.Join(folder, "leaves")); err != nil {
		t.Skipf("this host cannot create symlinks: %v", err)
	}
	require.NoError(t, os.Symlink("real.txt", filepath.Join(folder, "near")))
	require.NoError(t, os.Symlink(filepath.Dir(outside), filepath.Join(folder, "dirlink")))

	require.NoError(t, d.Move(context.Background(), "gdrive/folder", ".filex-trash/key__folder"))

	dst := filepath.Join(root, ".filex-trash", "key__folder")
	for name, want := range map[string]string{
		"leaves":  outside,
		"near":    "real.txt",
		"dirlink": filepath.Dir(outside),
	} {
		got, err := os.Readlink(filepath.Join(dst, name))
		require.NoError(t, err, "%s has to arrive as a link", name)
		assert.Equal(t, want, got)
	}
	assert.Equal(t, "SECRET", body(t, outside), "the target stays where it was")
	assert.NoDirExists(t, folder)
}

func TestMove_ALinkOnItsOwnMovesAsTheLink(t *testing.T) {
	d, root := mounted(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	put(t, outside, "SECRET")
	link := filepath.Join(root, "gdrive", "leaves")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("this host cannot create symlinks: %v", err)
	}

	require.NoError(t, d.Move(context.Background(), "gdrive/leaves", ".filex-trash/key__leaves"))

	got, err := os.Readlink(filepath.Join(root, ".filex-trash", "key__leaves"))
	require.NoError(t, err)
	assert.Equal(t, outside, got)
	_, err = os.Lstat(link)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// ⚠⚠ A folder takes minutes on a cloud mount. What lands in it meanwhile was
// never carried, and removing the source as a whole would destroy the only
// copy of it.
func TestMove_WhatArrivesMeanwhileStays(t *testing.T) {
	d, root := mounted(t)
	src := filepath.Join(root, "gdrive", "shared")
	put(t, filepath.Join(src, "old.txt"), "OLD")
	real := removeNode
	removeNode = func(p string) error {
		if filepath.Base(p) == "old.txt" {
			put(t, filepath.Join(src, "new.txt"), "NEW")
		}
		return real(p)
	}
	t.Cleanup(func() { removeNode = real })

	require.NoError(t, d.Move(context.Background(), "gdrive/shared", ".filex-trash/key__shared"))

	assert.Equal(t, "OLD", body(t, filepath.Join(root, ".filex-trash", "key__shared", "old.txt")))
	assert.NoFileExists(t, filepath.Join(src, "old.txt"))
	assert.Equal(t, "NEW", body(t, filepath.Join(src, "new.txt")))
}

// ⚠⚠ A folder is built under its final name, so a failure removes what it
// built. A name that was already taken is not what it built: the folder there
// belongs to somebody, and cleaning up after the refusal would delete it.
func TestMove_ATakenFolderNameIsRefusedAndLeftAlone(t *testing.T) {
	d, root := mounted(t)
	src := filepath.Join(root, "gdrive", "folder")
	put(t, filepath.Join(src, "a.txt"), "A")
	taken := filepath.Join(root, "archive", "folder")
	put(t, filepath.Join(taken, "theirs.txt"), "THEIRS")

	err := d.Move(context.Background(), "gdrive/folder", "archive/folder")

	require.ErrorIs(t, err, storage.ErrMoveAcross)
	assert.Equal(t, "THEIRS", body(t, filepath.Join(taken, "theirs.txt")))
	assert.NoFileExists(t, filepath.Join(taken, "a.txt"))
	assert.Equal(t, "A", body(t, filepath.Join(src, "a.txt")))
}

// A folder must never reach its name through a rename: see crossdevice.go for
// what that did on a real rclone mount.
func TestMove_AFolderIsNeverRenamedIntoPlace(t *testing.T) {
	d, root := mounted(t)
	put(t, filepath.Join(root, "gdrive", "folder", "deep", "a.txt"), "A")
	across := rename
	rename = func(a, b string) error {
		if strings.HasPrefix(filepath.Base(a), ".filex-move-") {
			t.Errorf("a staged rename while carrying a folder: %s", a)
		}
		return across(a, b)
	}
	t.Cleanup(func() { rename = across })

	require.NoError(t, d.Move(context.Background(), "gdrive/folder", ".filex-trash/key__folder"))

	assert.Equal(t, "A", body(t, filepath.Join(root, ".filex-trash", "key__folder", "deep", "a.txt")))
}

func TestMove_AnAbandonedCopyChangesNothing(t *testing.T) {
	d, root := mounted(t)
	src := filepath.Join(root, "gdrive", "folder")
	put(t, filepath.Join(src, "a.txt"), "A")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := d.Move(ctx, "gdrive/folder", ".filex-trash/key__folder")

	require.ErrorIs(t, err, storage.ErrMoveAcross)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, "A", body(t, filepath.Join(src, "a.txt")))
	assert.NoDirExists(t, filepath.Join(root, ".filex-trash", "key__folder"))
	noStage(t, filepath.Join(root, ".filex-trash"))
}

// A read-only mount: everything copies and nothing can be removed. A copy left
// at the destination would be one more full copy in the trash on every attempt.
func TestMove_ASourceThatCannotBeRemovedTakesTheCopyBack(t *testing.T) {
	refused := errors.New("read-only file system")

	t.Run("file", func(t *testing.T) {
		d, root := mounted(t)
		src := filepath.Join(root, "gdrive", "doc.pdf")
		put(t, src, "the only copy")
		failRemoving(t, refused, "doc.pdf")

		err := d.Move(context.Background(), "gdrive/doc.pdf", ".filex-trash/key__doc.pdf")

		require.ErrorIs(t, err, refused)
		assert.Equal(t, "the only copy", body(t, src))
		assert.NoFileExists(t, filepath.Join(root, ".filex-trash", "key__doc.pdf"))
	})

	t.Run("folder", func(t *testing.T) {
		d, root := mounted(t)
		src := filepath.Join(root, "gdrive", "folder")
		put(t, filepath.Join(src, "a.txt"), "A")
		put(t, filepath.Join(src, "b.txt"), "B")
		failRemoving(t, refused, "a.txt", "b.txt")

		err := d.Move(context.Background(), "gdrive/folder", ".filex-trash/key__folder")

		require.ErrorIs(t, err, refused)
		assert.Equal(t, "A", body(t, filepath.Join(src, "a.txt")))
		assert.Equal(t, "B", body(t, filepath.Join(src, "b.txt")))
		assert.NoDirExists(t, filepath.Join(root, ".filex-trash", "key__folder"))
	})
}

// ⚠⚠ Once part of the source is gone the destination holds the only copy of
// that part: taking the copy back here would be the data loss.
func TestMove_APartlyRemovedFolderKeepsEveryByte(t *testing.T) {
	d, root := mounted(t)
	src := filepath.Join(root, "gdrive", "folder")
	put(t, filepath.Join(src, "a.txt"), "A")
	put(t, filepath.Join(src, "b.txt"), "B")
	refused := errors.New("input/output error")
	failRemoving(t, refused, "b.txt")

	err := d.Move(context.Background(), "gdrive/folder", ".filex-trash/key__folder")

	require.ErrorIs(t, err, refused)
	dst := filepath.Join(root, ".filex-trash", "key__folder")
	assert.NoFileExists(t, filepath.Join(src, "a.txt"))
	assert.Equal(t, "A", body(t, filepath.Join(dst, "a.txt")))
	assert.Equal(t, "B", body(t, filepath.Join(dst, "b.txt")))
	assert.Equal(t, "B", body(t, filepath.Join(src, "b.txt")))
}

func TestMove_NeverIntoItselfAcrossAMount(t *testing.T) {
	_, root := mounted(t)
	put(t, filepath.Join(root, "top", "a.txt"), "A")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "top", "mnt"), 0o755))

	err := moveAcross(context.Background(), filepath.Join(root, "top"), filepath.Join(root, "top", "mnt", "top"))

	require.ErrorIs(t, err, storage.ErrMoveAcross)
	assert.Equal(t, "A", body(t, filepath.Join(root, "top", "a.txt")))
	noStage(t, filepath.Join(root, "top", "mnt"))
}

// Only that one refusal copies. Every other refusal is an answer, and copying
// past it would turn "permission denied" into a slow surprise.
func TestMove_OtherRefusalsAreNotACopy(t *testing.T) {
	d, root := mounted(t)
	src := filepath.Join(root, "gdrive", "doc.pdf")
	put(t, src, "bytes")
	real := rename
	rename = func(a, b string) error {
		return &os.LinkError{Op: "rename", Old: a, New: b, Err: syscall.EACCES}
	}
	t.Cleanup(func() { rename = real })

	err := d.Move(context.Background(), "gdrive/doc.pdf", ".filex-trash/key__doc.pdf")

	require.ErrorIs(t, err, syscall.EACCES)
	require.NotErrorIs(t, err, storage.ErrMoveAcross)
	assert.Equal(t, "bytes", body(t, src))
	assert.NoFileExists(t, filepath.Join(root, ".filex-trash", "key__doc.pdf"))
}
