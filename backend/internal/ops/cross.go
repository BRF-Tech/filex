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
//     can never delete an original whose copy did not fully arrive;
//   - a link the source driver could not follow is left behind and NAMED,
//     never read, and a folder link that leads back into the tree being
//     carried is walked once and then refused (Skipped, below).
//
// ⚠ A cross-storage MOVE deletes the source outright — it does not go through
// the trash. That is Burak's call (2026-08-29): the point of moving between
// depolar is usually to free the first one, and a trashed copy would keep the
// bytes (and the quota) until the trash is emptied. The delete only runs after
// the destination has been written AND stat-verified — and not at all when the
// transfer left anything behind (crossTransfer): a move deletes only what it
// carried.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// skipName reports the entries a tree walk steps over: filex's own
// directories (syspath.IsDirName). They are storage-local by definition — a
// copied trash is not the destination's trash, version history is keyed by the
// SOURCE storage's node ids (`.versions/<id>/<n>`) and means nothing anywhere
// else, thumbnails are rebuilt on demand, and the desktop's open-with working
// copies belong to an editing session on one storage.
//
// ⚠ This used to be a two-name map of its own (trash, thumbs), so copying a
// storage root carried `.versions/` and `.filex-open/` across. The keep marker
// is still copied on purpose: on a blob-store destination it is what keeps a
// copied EMPTY folder in existence.
func skipName(name string) bool { return syspath.IsDirName(name) }

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
	// OnBytes is told how many new bytes were read from a source file, while
	// they stream (issue #27). Unlike the two above it fires before
	// verification — it measures motion, not completion.
	OnBytes func(n int64)
}

// Skipped is one entry inside a transferred folder that was deliberately NOT
// carried, and why.
//
// ⚠⚠ Why a transfer skips at all, measured 2026-09-21 against this code before
// the rule existed (a `local` source, follow_symlinks off, copying a folder
// holding one link of each kind to a second storage):
//
//	broken link            the WHOLE copy failed: read "tree/broken": not found
//	link out of the root   the WHOLE copy failed: symlink target is outside…
//	`cycle -> .`           41 nested duplicate folders written into the
//	                       destination, THEN failed: too many levels of links
//	unresolved link, on a  "ok" — and the bytes of the file the link pointed
//	source whose Read      at, outside the storage, sitting in the destination
//	follows it (sftp, ftp)
//
// The local driver's Read refuses an out-of-root link, which is why the second
// row failed instead of leaking; a remote driver's Read is the SERVER opening
// the path, and a server follows links wherever its account can read. So the
// rule cannot be "Read will say no": a link the driver did not resolve is
// never opened here at all, whatever the driver would do with it.
//
// Skip, don't fail — the rule the same-storage folder copy already follows
// (local.Driver.copyTree): one link nobody can follow must not cost the other
// nine hundred and ninety-nine files. Unlike that copy, this one SAYS what it
// left behind — the op row ends `partial` with the list (Service.execute), and
// a move keeps its source (crossTransfer).
type Skipped struct {
	// Path is the entry's source path, storage-relative.
	Path string `json:"path"`
	// Reason is the source driver's own link state (storage.LinkBroken,
	// LinkOutsideRoot, LinkUnresolved), or SkipCycle / SkipTooDeep.
	Reason string `json:"reason"`
}

// Reasons a transfer gives that are not a driver's link state.
const (
	// SkipCycle — a folder link whose target this walk has already carried
	// once (storage.CycleGuard). Its content is at the destination, reached
	// through the first link; walking it again would only nest copies.
	SkipCycle = "cycle"
	// SkipTooDeep — a folder more than storage.MaxWalkDepth levels below the
	// one being carried. ⚠ This one CAN hold real data nobody linked, which
	// is the reason a move keeps its source when anything was skipped.
	SkipTooDeep = "too_deep"
	// skipLink is a KindSymlink a driver reported without saying why.
	skipLink = "link"
)

// SkipsError is a transfer that finished and left entries behind. It is an
// error so that a caller cannot mistake it for a clean success by ignoring
// it; errors.As tells it apart from a failure.
type SkipsError struct {
	Skipped []Skipped
	// SourceKept is set by a MOVE that did not delete its source because of
	// the entries above (crossTransfer).
	SourceKept bool
}

// skipsShown bounds how many entries the message names; the rest are counted.
// The message is the op row's error column and a toast — a thousand broken
// links must not become a thousand-line toast.
const skipsShown = 5

func (e *SkipsError) Error() string {
	var b strings.Builder
	n := len(e.Skipped)
	noun := "entries were"
	if n == 1 {
		noun = "entry was"
	}
	if e.SourceKept {
		fmt.Fprintf(&b, "copied, but the source was kept: %d %s not carried, and a move deletes only what it carried — ", n, noun)
	} else {
		fmt.Fprintf(&b, "copied, but %d %s left out: ", n, noun)
	}
	for i, s := range e.Skipped {
		if i == skipsShown {
			fmt.Fprintf(&b, ", and %d more", n-skipsShown)
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q (%s)", s.Path, skipReasonText(s.Reason))
	}
	return b.String()
}

// skipReasonText is the reason in the words the person reads.
func skipReasonText(reason string) string {
	switch reason {
	case storage.LinkBroken:
		return "a broken link"
	case storage.LinkOutsideRoot:
		return "a link pointing outside the storage"
	case storage.LinkUnresolved:
		return "a link this storage does not follow"
	case SkipCycle:
		return "a folder link back into what was already copied"
	case SkipTooDeep:
		return fmt.Sprintf("more than %d folders deep", storage.MaxWalkDepth)
	default:
		return "a link filex cannot follow"
	}
}

// Transfer copies `src` (a file or a whole directory) from one storage driver
// to `dst` on another, verifying every file on arrival. What it deliberately
// did not carry comes back in the list (see Skipped); an error means the
// transfer itself failed.
//
// It does not delete anything: a move is this call followed by the caller's own
// delete, which is the order that makes a failed transfer harmless — and a
// caller that deletes must not do so while the list is non-empty.
func Transfer(ctx context.Context, srcDrv, dstDrv storage.Driver, src, dst string, hooks TransferHooks) ([]Skipped, error) {
	wr, ok := dstDrv.(storage.Writer)
	if !ok {
		return nil, errors.New("destination storage is not writable")
	}
	stat, err := srcDrv.Stat(ctx, src)
	if err != nil {
		return nil, err
	}
	switch stat.Kind {
	case storage.KindDirectory:
		var skipped []Skipped
		err := transferDir(ctx, srcDrv, dstDrv, wr, src, dst, hooks, storage.NewCycleGuard(), 0, &skipped)
		return skipped, err
	case storage.KindSymlink:
		// ⚠ The ONE thing asked for is a link nobody can follow. Skipping it
		// would answer "done" for a transfer that carried nothing; "skip,
		// don't fail" is about the neighbours, and here there are none.
		return nil, fmt.Errorf("%q is %s and was not copied", src, skipReasonText(linkReason(stat)))
	}
	return nil, transferFile(ctx, srcDrv, dstDrv, wr, src, dst, stat, hooks)
}

// linkReason is the driver's own word for why an entry stayed a link.
func linkReason(o storage.Object) string {
	if r := o.Metadata[storage.MetaLinkState]; r != "" {
		return r
	}
	return skipLink
}

// UniqueDest resolves a non-colliding destination on `drv` — `name-copy`,
// `name-copy-2`, … — so a paste never overwrites what is already there.
// It fails with ErrNoFreeName rather than ever returning a taken name; taken
// adds checks the driver cannot make (see Taken).
func UniqueDest(ctx context.Context, drv storage.Driver, dst string, taken ...Taken) (string, error) {
	return uniqueCopyDest(ctx, drv, "", dst, taken...)
}

// MoveDest is where a same-storage move of `src` to `dst` lands: `dst` when
// nothing holds it, a free name beside it when something does, and `src`
// itself when the move would put the item back where it already is.
//
// ⚠⚠ The one rule for every same-storage move — the queued worker and the
// manager's synchronous `?action=move` both call it. A driver's Move onto an
// occupied path REPLACES what is there (a local rename does, and so does an
// object store's copy-then-delete), and neither caller checked: measured
// 2026-09-14, moving `a.txt` into a folder holding an `a.txt` finished `ok`
// and the file that had been there was gone, not even in the trash. A copy and
// a cross-storage move already kept both; this makes the same-storage move
// agree with them rather than invent a third behaviour.
func MoveDest(ctx context.Context, drv storage.Driver, src, dst string, taken ...Taken) (string, error) {
	if normOpPath(src) == normOpPath(dst) {
		return src, nil
	}
	return uniqueCopyDest(ctx, drv, src, dst, taken...)
}

// isCross reports whether this op's two ends live in different storages.
func (s *Service) isCross(op *Op) bool {
	return op.DestStorageID != 0 && op.DestStorageID != op.StorageID
}

// crossTransfer runs one queued source through Transfer, mirrors the result
// into the DB cache, and — when `move` is set — removes the source afterwards.
func (s *Service) crossTransfer(ctx context.Context, srcDrv, dstDrv storage.Driver, op *Op, src string, move bool) error {
	dst, err := UniqueDest(ctx, dstDrv, joinIntoDir(op.Dest, src))
	if err != nil {
		return err
	}
	hooks := TransferHooks{}
	if s.dbsync != nil {
		hooks.OnDir = func(a, b string) { s.dbsync.SyncCopyAcross(ctx, op.StorageID, a, op.DestStorageID, b) }
		hooks.OnFile = func(a, b string, _ int64) { s.dbsync.SyncCopyAcross(ctx, op.StorageID, a, op.DestStorageID, b) }
	}
	if lp := s.liveFor(op.ID); lp != nil {
		hooks.OnBytes = func(n int64) { lp.done.Add(n) }
	}
	skipped, err := Transfer(ctx, srcDrv, dstDrv, src, dst, hooks)
	if err != nil {
		return err
	}
	if len(skipped) > 0 {
		// ⚠⚠ A move deletes only what it carried, and the delete below is one
		// call on the whole tree — there is no "everything except these". A
		// piecemeal delete is NOT the way out: removing the carried files one
		// by one would follow the in-root folder links the walk went through
		// and delete the files at their REAL location, outside the tree being
		// moved. So the whole source stays and the row says why; the copy at
		// the destination is complete, and deleting the source is the
		// person's call once they have seen the list. (A skipped link carries
		// no bytes of its own, but a SkipTooDeep folder does.)
		return &SkipsError{Skipped: skipped, SourceKept: move}
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
//
// depth is srcDir's depth below the folder Transfer was asked to carry (0).
// What the walk refuses goes into *skipped; see Skipped for the rules.
func transferDir(ctx context.Context, srcDrv, dstDrv storage.Driver, wr storage.Writer, srcDir, dstDir string, hooks TransferHooks, guard *storage.CycleGuard, depth int, skipped *[]Skipped) error {
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
		if skipName(o.Name) {
			continue
		}
		// ⚠ Drivers differ on whether Object.Path is storage-relative or bare;
		// rebuild it from the directory we asked for so both shapes agree.
		childSrc := path.Join(srcDir, o.Name)
		childDst := path.Join(dstDir, o.Name)
		switch o.Kind {
		case storage.KindSymlink:
			// ⚠⚠ Never Read. A driver reports an entry as KindSymlink precisely
			// when it did NOT resolve it — broken, out of the root, or a remote
			// link it will not vouch for — and a remote driver's Read follows
			// it anyway, server side (see Skipped for what that pulled in).
			*skipped = append(*skipped, Skipped{Path: childSrc, Reason: linkReason(o)})
			continue
		case storage.KindDirectory:
			// The same guard every other recursive walk carries (sync/poll.go,
			// storage/kindscan.go, progress.go): a followed folder link names
			// its real target, so `cycle -> .` is walked once and then refused
			// instead of nesting a copy per level until the OS gives up.
			if !guard.Enter(o, depth+1) {
				reason := SkipCycle
				if depth+1 >= storage.MaxWalkDepth {
					reason = SkipTooDeep
				}
				*skipped = append(*skipped, Skipped{Path: childSrc, Reason: reason})
				continue
			}
			if err := transferDir(ctx, srcDrv, dstDrv, wr, childSrc, childDst, hooks, guard, depth+1, skipped); err != nil {
				return err
			}
			continue
		}
		// A file — including a link the driver FOLLOWED (in-root, or
		// follow_symlinks on), which it reports as the file it points at and
		// which is carried by value, as the same-storage copy does.
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
	rc = countBytes(rc, hooks.OnBytes)
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
