package local

// A move that crosses a filesystem boundary inside one storage (issue #133).
//
// A storage root can hold other filesystems: an rclone mount of a cloud drive,
// an NFS or SMB share, a second disk, a Docker volume bound under the root.
// os.Rename cannot cross any of them, and the trash lives at the storage root,
// so EVERY delete under such a mount is a rename across the boundary. Measured
// with a mount under a real root:
//
//	os.Rename(root/gdrive/doc.pdf, root/moved.pdf)
//	  → invalid cross-device link
//
// A move across the boundary is therefore a copy and a delete, and it destroys
// the source, which is what makes it different from Copy:
//
//   - ⚠⚠ copyTree is NOT reusable here. It skips what it may not follow (a link
//     leaving the root, a pipe, a directory link) and that is right for a copy,
//     because the source stays. Followed by a delete, every skipped entry would
//     be gone for good. A link travels as a link, by value and never followed,
//     which is what a rename does; a pipe, socket or device cannot travel at
//     all, so it refuses the whole move before anything is removed.
//   - Nothing is removed until everything has arrived, so a failure leaves the
//     source whole, and what was delivered so far is taken back.
//   - ⚠⚠ A folder is built under its final name and never renamed into place.
//     Measured on a real rclone mount with a cache (v1.60.1, every mode that
//     has one): a folder staged and renamed while its files were still
//     uploading came out with them unreadable or EMPTY, with the source already
//     removed. tmpfs shows nothing of the kind. A lone file, staged and renamed,
//     came through intact in every mode.
//   - Only what was carried is removed, never the source as a whole. A folder
//     on a cloud mount takes minutes to carry; a file that arrives in it
//     meanwhile was not carried, and os.RemoveAll would destroy the only copy.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/brf-tech/filex/backend/internal/regfile"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// rename and removeNode are os.Rename and os.Remove. A test stands in for them
// to answer what a second filesystem answers, on hosts where it cannot mount
// one, and what a refusing one answers when the tests run as root.
var (
	rename     = os.Rename
	removeNode = os.Remove
)

// crossDevice reports whether err is a rename refused for crossing filesystems.
func crossDevice(err error) bool { return errors.Is(err, errCrossDevice) }

// carriedNode is one source moveAcross has delivered.
type carriedNode struct {
	path string
	dir  bool
}

// moveAcross is Move for two ends on different filesystems.
//
// ⚠ Every failure past the first Lstat is storage.ErrMoveAcross, on purpose,
// and for two readers. Move answers ErrNotFound for a bare not-exist, and a
// caller reads that as "the source is gone" and drops its catalogue rows
// (ops.runOne): a file vanishing from a folder half way through says nothing
// of the kind about the folder. And the callers that retry a failed folder
// object by object must not: see ErrMoveAcross.
func moveAcross(ctx context.Context, src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if err := deliver(ctx, src, dst, info); err != nil {
		return fmt.Errorf("%w: %w", storage.ErrMoveAcross, err)
	}
	return nil
}

// deliver puts src at dst and removes the source.
func deliver(ctx context.Context, src, dst string, info fs.FileInfo) error {
	// A rename answers this itself on one filesystem. Here the walk would find
	// what it is writing inside what it is carrying and never finish.
	if within(src, dst) {
		return fmt.Errorf("cannot move %q into itself", filepath.Base(src))
	}
	var carried []carriedNode
	if info.IsDir() {
		// ⚠ Before anything can fail with something of ours to clean up: a
		// taken name answers here, and the folder holding it is not ours to
		// remove. A rename would have refused one with anything in it too.
		if err := os.Mkdir(dst, 0o700); err != nil {
			return err
		}
		if err := carryFolder(ctx, src, dst, info, &carried); err != nil {
			_ = os.RemoveAll(dst)
			return err
		}
	} else if err := carryStaged(ctx, src, dst, info, &carried); err != nil {
		return err
	}
	gone, err := removeCarried(carried)
	if err != nil {
		// With nothing removed the source is whole, so taking the copy back
		// leaves things as they were: a read-only mount would otherwise leave
		// one more full copy in the trash on every attempt. With part of it
		// removed the destination holds the only copy of that part, and both
		// stay, so every byte still has a home.
		if gone == 0 {
			_ = os.RemoveAll(dst)
		}
		return fmt.Errorf("remove source: %w", err)
	}
	return nil
}

// carryStaged delivers a file or a link beside dst and renames it into place,
// so a failure leaves no half-written object under dst's name, and a name that
// is taken gets the answer a plain rename gives.
func carryStaged(ctx context.Context, src, dst string, info fs.FileInfo, carried *[]carriedNode) error {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	stage := filepath.Join(filepath.Dir(dst), ".filex-move-"+hex.EncodeToString(b[:]))
	if err := carry(ctx, src, stage, info, carried); err != nil {
		_ = os.Remove(stage)
		return err
	}
	if err := rename(stage, dst); err != nil {
		_ = os.Remove(stage)
		return err
	}
	return nil
}

// carryFolder fills dst, a folder that exists, with what is in src, and records
// src after what it held, which is the order the sources can be removed in.
func carryFolder(ctx context.Context, src, dst string, info fs.FileInfo, carried *[]carriedNode) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		child, err := e.Info()
		// ⚠ Only this one not-exist is skipped: the entry left between the
		// listing and the look, so there are no bytes to carry, and a cloud
		// mount's cached listing keeps naming deleted files for minutes. One
		// from further down may be the DESTINATION failing, and skipping that
		// would remove a source that never arrived.
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := carry(ctx, filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), child, carried); err != nil {
			return err
		}
	}
	keepMeta(dst, info)
	*carried = append(*carried, carriedNode{path: src, dir: true})
	return nil
}

// carry delivers one entry to dst, a name nothing holds yet, and records it.
func carry(ctx context.Context, src, dst string, info fs.FileInfo, carried *[]carriedNode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	mode := info.Mode()
	switch {
	case info.IsDir():
		// Writable while it fills up: a read-only folder gets its mode at the end.
		if err := os.Mkdir(dst, 0o700); err != nil {
			return err
		}
		return carryFolder(ctx, src, dst, info, carried)
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.Symlink(target, dst); err != nil {
			return err
		}
	case regfile.Special(mode):
		return fmt.Errorf("%w: %s %q cannot leave its filesystem",
			storage.ErrKindConflict, regfile.KindOf(mode), filepath.Base(src))
	default:
		tally := storage.TallyOf(ctx)
		tally.Found(1)
		if err := copyWhole(ctx, src, dst, info.Size()); err != nil {
			return err
		}
		tally.Done(1)
		keepMeta(dst, info)
	}
	*carried = append(*carried, carriedNode{path: src})
	return nil
}

// keepMeta gives dst the mode and time a rename would have kept. Best effort:
// rclone, SMB or FAT may hold neither, and a refusal there says nothing about
// the bytes, which have arrived.
func keepMeta(dst string, info fs.FileInfo) {
	_ = os.Chmod(dst, info.Mode().Perm())
	_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
}

// copyWhole copies one regular file and answers only once its bytes are safe,
// because the next step removes the source.
//
// ⚠ Not copyFile: that one ignores what Close says, and a network filesystem
// reports a failed write exactly there (an rclone mount uploads on close).
//
// ⚠ Fewer bytes than promised fails, more does not. A short read is a truncated
// copy; a size that undersells is an rclone mount showing a Google Doc as 0
// bytes until it is read, and refusing that would make such a file undeletable.
func copyWhole(ctx context.Context, src, dst string, size int64) error {
	in, err := regfile.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := createRegular(dst)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, ctxReader{ctx: ctx, r: in})
	if err == nil && n < size {
		err = fmt.Errorf("%q: read %d of %d bytes", filepath.Base(src), n, size)
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// ctxReader ends a copy when its context does: one file on a cloud mount can
// take longer than anybody is willing to wait for a job to stop.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// removeCarried removes the delivered sources, children first, and says how
// many are gone.
//
// ⚠ A folder that will not go is not a failure: everything carried out of it
// is safe at the destination, so what is left is something that arrived after
// the walk passed, and it stays where its owner put it.
func removeCarried(carried []carriedNode) (gone int, err error) {
	for _, c := range carried {
		rerr := retryWhileLocked(func() error { return removeNode(c.path) })
		switch {
		case rerr == nil, errors.Is(rerr, fs.ErrNotExist):
			gone++
		case c.dir:
			slog.Warn("local: moved folder left behind", slog.String("path", c.path), slog.String("err", rerr.Error()))
		default:
			return gone, rerr
		}
	}
	return gone, nil
}
