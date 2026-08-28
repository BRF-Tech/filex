package ops

// Cross-storage copy/move — the "copy in one depo, paste into another" gesture.
//
// The same-storage path hands the whole job to the driver: `Copier.Copy` and
// `Mover.Move` are one call each, and the driver does it server-side. Two
// storages have no such call — an S3 bucket cannot rename a file into an SFTP
// host — so the bytes have to travel through filex, and every question the
// driver used to answer for us has to be answered here instead:
//
//   - a directory is walked and rebuilt on the far side, one file at a time,
//     because `Copy` on a tree is a driver-side concept;
//   - the destination name is de-collided against the DESTINATION driver, not
//     the source (which is what `uniqueCopyDest` is doing with an empty `src`:
//     a self-copy is impossible across two storages, only a name clash is);
//   - the file's own mtime is carried over when the target driver can set one,
//     so a moved tree does not read as "everything changed just now" to the
//     next sync run;
//   - the write is verified by size before the source is touched, so a move
//     can never delete an original whose copy did not fully arrive.
//
// ⚠ A cross-storage MOVE deletes the source outright — it does not go through
// the trash. That is Burak's call (2026-08-29): the point of moving between
// depolar is usually to free the first one, and a trashed copy would keep the
// bytes (and the quota) until the trash is emptied. The delete only runs after
// the destination has been written AND stat-verified.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// skipNames are filex's own bookkeeping directories. They are storage-local by
// definition — a copied trash is not the destination's trash, and thumbnails
// are rebuilt on demand — so a tree walk steps over them.
var skipNames = map[string]bool{
	".filex-trash": true,
	".thumbs":      true,
}

// TransferHooks let the caller mirror each finished step into whatever
// catalogue it keeps. Both are optional and are called only after the bytes
// are on the far side and verified.
//
// This exists so the agent/MCP surface and the queue can share ONE transfer
// engine. A second implementation of "walk a tree between two drivers" would
// drift — and the half that drifts is always the one nobody watches.
type TransferHooks struct {
	OnDir  func(src, dst string)
	OnFile func(src, dst string, size int64)
}

// Transfer copies `src` (a file or a whole directory) from one storage driver
// to `dst` on another, verifying every file on arrival.
//
// It does not delete anything: a move is this call followed by the caller's own
// delete, which is the order that makes a failed transfer harmless.
func Transfer(ctx context.Context, srcDrv, dstDrv storage.Driver, src, dst string, hooks TransferHooks) error {
	wr, ok := dstDrv.(storage.Writer)
	if !ok {
		return errors.New("destination storage is not writable")
	}
	stat, err := srcDrv.Stat(ctx, src)
	if err != nil {
		return err
	}
	if stat.Kind == storage.KindDirectory {
		return transferDir(ctx, srcDrv, dstDrv, wr, src, dst, hooks)
	}
	return transferFile(ctx, srcDrv, dstDrv, wr, src, dst, stat, hooks)
}

// UniqueDest resolves a non-colliding destination on `drv` — `name-copy`,
// `name-copy-2`, … — so a paste never overwrites what is already there.
func UniqueDest(ctx context.Context, drv storage.Driver, dst string) string {
	return uniqueCopyDest(ctx, drv, "", dst)
}

// isCross reports whether this op's two ends live in different storages.
func (s *Service) isCross(op *Op) bool {
	return op.DestStorageID != 0 && op.DestStorageID != op.StorageID
}

// crossTransfer runs one queued source through Transfer, mirrors the result
// into the DB cache, and — when `move` is set — removes the source afterwards.
func (s *Service) crossTransfer(ctx context.Context, srcDrv, dstDrv storage.Driver, op *Op, src string, move bool) error {
	dst := UniqueDest(ctx, dstDrv, joinIntoDir(op.Dest, src))
	hooks := TransferHooks{}
	if s.dbsync != nil {
		hooks.OnDir = func(a, b string) { s.dbsync.SyncCopyAcross(ctx, op.StorageID, a, op.DestStorageID, b) }
		hooks.OnFile = func(a, b string, _ int64) { s.dbsync.SyncCopyAcross(ctx, op.StorageID, a, op.DestStorageID, b) }
	}
	if err := Transfer(ctx, srcDrv, dstDrv, src, dst, hooks); err != nil {
		return err
	}

	if !move {
		return nil
	}
	// The bytes are on the far side and verified; the original goes away.
	del, ok := srcDrv.(storage.Deleter)
	if !ok {
		return fmt.Errorf("copied to destination, but the source storage cannot delete %q — remove it by hand", src)
	}
	if err := del.Delete(ctx, src); err != nil {
		return fmt.Errorf("copied to destination, but deleting the source failed: %w", err)
	}
	if s.dbsync != nil {
		s.dbsync.SyncHardDelete(ctx, op.StorageID, src)
	}
	return nil
}

// transferDir rebuilds a whole subtree on the destination driver.
//
// Directories are created before their contents (an object store's Mkdir may
// be a no-op, which is fine — the file writes create the prefix) and empty
// folders survive the trip, because a folder the user made is part of what
// they are moving.
func transferDir(ctx context.Context, srcDrv, dstDrv storage.Driver, wr storage.Writer, srcDir, dstDir string, hooks TransferHooks) error {
	if mk, ok := dstDrv.(storage.Mkdirer); ok {
		if err := mk.Mkdir(ctx, dstDir); err != nil && !errors.Is(err, storage.ErrUnsupported) {
			return fmt.Errorf("mkdir %q: %w", dstDir, err)
		}
	}
	if hooks.OnDir != nil {
		hooks.OnDir(srcDir, dstDir)
	}
	objs, err := srcDrv.List(ctx, srcDir)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if skipNames[o.Name] {
			continue
		}
		// ⚠ Drivers differ on whether Object.Path is storage-relative or bare;
		// rebuild it from the directory we asked for so both shapes agree.
		childSrc := path.Join(srcDir, o.Name)
		childDst := path.Join(dstDir, o.Name)
		if o.Kind == storage.KindDirectory {
			if err := transferDir(ctx, srcDrv, dstDrv, wr, childSrc, childDst, hooks); err != nil {
				return err
			}
			continue
		}
		if err := transferFile(ctx, srcDrv, dstDrv, wr, childSrc, childDst, o, hooks); err != nil {
			return err
		}
	}
	return nil
}

// transferFile streams one file's bytes to the destination driver and verifies
// what landed there.
func transferFile(ctx context.Context, srcDrv, dstDrv storage.Driver, wr storage.Writer, src, dst string, stat storage.Object, hooks TransferHooks) error {
	rc, err := srcDrv.Read(ctx, src)
	if err != nil {
		return fmt.Errorf("read %q: %w", src, err)
	}
	werr := wr.Write(ctx, dst, rc, stat.Size)
	cerr := rc.Close()
	if werr != nil {
		return fmt.Errorf("write %q: %w", dst, werr)
	}
	if cerr != nil && !errors.Is(cerr, io.EOF) {
		return fmt.Errorf("read %q: %w", src, cerr)
	}

	// Verify before anyone deletes anything. A driver that accepted the write
	// and stored fewer bytes is exactly the failure a move must not turn into
	// data loss, and it is cheap to catch: one Stat.
	got, serr := dstDrv.Stat(ctx, dst)
	if serr != nil {
		return fmt.Errorf("wrote %q but could not verify it: %w", dst, serr)
	}
	if stat.Size > 0 && got.Size != stat.Size {
		return fmt.Errorf("destination %q is %d bytes, source is %d — transfer incomplete", dst, got.Size, stat.Size)
	}

	// Carry the file's own timestamp where the target can hold one. Best
	// effort by design: a driver without SetMtime is not a failed transfer.
	if t, ok := dstDrv.(storage.Toucher); ok && !stat.Mtime.IsZero() {
		_ = t.SetMtime(ctx, dst, stat.Mtime)
	}

	if hooks.OnFile != nil {
		hooks.OnFile(src, dst, got.Size)
	}
	return nil
}
