// Package local is a Storage Driver fronting an on-disk directory.
//
// Path safety is two layers, and they answer different questions:
//
//   - resolve is LEXICAL. It maps a wire path onto the root and refuses
//     anything that leaves it by `..`. It never touches the filesystem.
//   - contained is REAL. It asks the kernel whether the path — symlinks and
//     all — still lands inside the root.
//
// Both are needed, and until v0.43.0 only a broken version of the first
// existed. The lexical layer alone let `..\sibling` out on Windows (see
// toSlash and within); the real layer alone cannot judge a path that names
// nothing on disk yet, which is every upload.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/regfile"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// The driver's two containment refusals. They are separate sentinels because
// they have different cures: ErrEscapesRoot is a malformed request and nobody
// can turn it into a legal one, while ErrLinkLeavesRoot names a real object
// the operator may decide to allow by switching follow_symlinks on.
var (
	ErrEscapesRoot    = errors.New("local: path escapes root")
	ErrLinkLeavesRoot = errors.New("local: symlink target is outside the storage root")
)

func init() {
	storage.Register("local", func() storage.Driver { return &Driver{} })
}

// Driver implements local FS access.
type Driver struct {
	root string
	// realRoot is root with every symlink resolved — the yardstick `within`
	// measures a followed path against.
	//
	// ⚠ It is NOT root, and using root would break the ordinary case rather
	// than a corner one: point a storage at /data on a host where /data is a
	// link to /mnt/disk1/data, and the kernel answers every question with the
	// resolved form, so every path inside the operator's own storage would be
	// judged an escape.
	realRoot string
	// followSymlinks governs ONLY links whose target leaves the root. A link
	// that stays inside is always followed — its target is within the
	// boundary, so there is nothing to protect, and refusing it is what issue
	// #34 was reported about.
	followSymlinks bool
	// skipped remembers which special entries (named pipes, sockets, devices —
	// regfile.Special) this driver has already told the log about, so a
	// storage rescanned every few minutes says it ONCE per process.
	skipped regfile.Skipped
}

// skipSpecial is the one place an entry that is not a regular file, a folder
// or a symlink is turned away (issue #38): never opened, never listed, and
// said once in the server log so the operator knows why a name on disk is not
// in the explorer.
func (d *Driver) skipSpecial(abs string, mode fs.FileMode) {
	d.skipped.Report("local", d.root, abs, mode)
}

// Name implements storage.Driver.
func (d *Driver) Name() string { return "local" }

// Init validates and stores the root directory from config["path"]
// (preferred) or config["root"] (legacy). Matches storage.ValidateNonRootPath's
// own preference order — without this fallback an operator could create a
// storage with {path: "/data"} via the admin API, pass validate.go cleanly,
// and then watch the driver init silently with an empty root because it
// only looked at config["root"].
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	root, _ := cfg["path"].(string)
	if root == "" {
		root, _ = cfg["root"].(string)
	}
	if root == "" {
		return errors.New("local: config.path (or config.root) is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("local: abs root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("local: mkdir root: %w", err)
	}
	d.root = abs
	// Resolved once, here, because it cannot change for the life of the
	// driver and because EvalSymlinks is the expensive call in this package
	// (measured 1.1 ms on Windows, see contained). MkdirAll has just
	// guaranteed the directory exists, which is EvalSymlinks' precondition.
	//
	// A failure is not fatal: fall back to the lexical root so a storage on an
	// exotic mount still starts. Containment is then measured against the
	// unresolved path, which is stricter, never looser.
	if rr, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		d.realRoot = rr
	} else {
		d.realRoot = abs
	}
	d.followSymlinks, _ = cfg["follow_symlinks"].(bool)
	return nil
}

// Capabilities — local FS supports everything except Presign.
func (d *Driver) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		Read:   true,
		Range:  true,
		Write:  true,
		Move:   true,
		Copy:   true,
		Delete: true,
		Mkdir:  true,
		Watch:  true,
	}
}

// resolve joins p onto the root LEXICALLY, returning a clean absolute path or
// an error if the result escapes root. It does not touch the filesystem; see
// contained for the layer that does.
func (d *Driver) resolve(p string) (string, error) {
	p = path.Clean("/" + strings.TrimLeft(toSlash(p), "/"))
	abs := filepath.Join(d.root, filepath.FromSlash(p))
	if !within(d.root, abs) {
		return "", ErrEscapesRoot
	}
	return abs, nil
}

// toSlash rewrites the HOST's path separators to '/' so path.Clean can see
// them.
//
// ⚠⚠ path.Clean is the POSIX cleaner and does not know that '\' separates
// anything, so `..\sibling` survives it whole — and filepath.Join, which DOES
// know on Windows, then spends the `..` on the way back out of the root.
// Measured over HTTP against a live Windows instance before this existed:
//
//	GET /api/files/manager?action=index&adapter=depo&path=..\depo-gizli
//	  → 200, listing a directory outside the storage root
//	GET …&path=../depo-gizli   (forward slash)
//	  → correctly refused
//
// ⚠ Only the host's own separators are folded, which is why this is not
// strings.ReplaceAll. On Linux a backslash is an ordinary, legal character in
// a file name; folding it there would make `weird\name.txt` — which List
// happily returns — permanently unopenable, and "an entry you can see and
// cannot open" is the exact complaint issue #34 was filed about.
func toSlash(p string) string {
	if !os.IsPathSeparator('\\') || !strings.ContainsRune(p, '\\') {
		return p
	}
	return strings.ReplaceAll(p, `\`, "/")
}

// within reports whether abs is root itself or something underneath it.
//
// ⚠⚠ The test this replaces was strings.HasPrefix(abs, root), which is not a
// path test at all — it is a string test that happens to be right most of the
// time. A root of `/srv/storage1` accepted every path under `/srv/storage10`,
// so a tenant on storage1 could read storage10's file names, sizes and
// timestamps (measured on Windows through the listing endpoint). Numbered
// roots are the normal way people name them; this was not a corner case.
//
// filepath.Rel compares the way the host does: measured case-INsensitive on
// Windows (Rel(UPPER(root), root/real) == "real") and case-sensitive on Linux
// (the same call there returns a ../.. path). Neither is hardcoded here.
func within(root, abs string) bool {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// leafMode says whether the verb about to run will DEREFERENCE the leaf, which
// decides whether contained may stop at the link or must look behind it.
type leafMode bool

const (
	// derefLeaf — the verb reads or writes THROUGH the leaf. os.Open,
	// os.Create, os.Stat, os.ReadDir and os.Chtimes all follow a symlink, so
	// an escaping link at the leaf is an escape and Lstat would wave it past
	// (measured: os.Root.Lstat("filelink") returns nil for a link pointing
	// outside, because Lstat is defined not to dereference).
	derefLeaf leafMode = true
	// nodeLeaf — the verb acts on the leaf NODE and never looks behind it.
	//
	// ⚠⚠ Measured identically on Linux and Windows: os.RemoveAll on a
	// directory symlink unlinks the LINK and leaves the tree behind it
	// untouched, and os.Rename moves the link itself (the moved node is still
	// a link, the target does not move). Judging those with derefLeaf would
	// deny a user the perfectly in-root right to delete or rename a link they
	// can see — and an out-of-root link they are not allowed to follow is
	// exactly the one they will want to delete.
	nodeLeaf leafMode = false
)

// contained reports whether abs — symlinks and all — still lands inside the
// storage root. It is the only thing standing between a link planted in the
// root and the rest of the disk.
//
// ⚠⚠ What it defends against, measured at driver level before it existed: one
// `ln -s /etc <root>/escape` handed out Read, ReadRange, List, Stat, Write,
// Mkdir, Copy, Move, SetMtime and a RECURSIVE Delete over the linked tree. The
// reporter of issue #34 planted such a link deliberately and had no idea it
// gave filex deletion rights outside the storage.
//
// Mechanism, and why this one and not another:
//
//   - os.Root is the ORACLE. It is the kernel's own answer, it cannot be
//     fooled by a link, and it costs one opendir plus one stat per component
//     — measured 67 µs at depth 1 and 179 µs at depth 3 on Windows, 2–8 µs on
//     Linux, against a bare os.Open of 57 µs / 5.7 µs.
//   - filepath.EvalSymlinks is the APPEAL, never the check. It costs
//     1.1–1.8 ms per call on Windows, 20× the oracle and 26× a bare os.Open,
//     because it re-normalises the whole path every time. It is paid only
//     after the oracle has already refused, i.e. only on a path that really
//     does cross a link.
//
// The appeal exists for the one case the oracle gets wrong: os.Root refuses an
// ABSOLUTE symlink even when its target is inside the root (measured:
// `ln -s <root>/real <root>/abs` then Open("abs/inside.txt") → "path escapes
// from parent", while the relative `ln -s real rel` is allowed). Adopting that
// rule wholesale would mean `ln -s /data/files/photos /data/files/pics` breaks
// while `ln -s photos pics` keeps working — a distinction no user can explain.
//
// ⚠ It DECIDES, it does not rewrite. Every verb goes on operating on the
// lexical path. Handing back the resolved target instead would quietly turn
// Delete("dirlink") from "unlink the link" into "recursively delete the tree
// it points at", which is the single most expensive bug this file could have.
//
// ⚠ TOCTOU: the check and the operation are two syscalls, so a link swapped
// between them wins. Closing that means doing the I/O itself through os.Root,
// which would be a reimplementation of all ten verbs (including a tree copy
// built on filepath.Walk and the Windows lock-retry policy in retry.go) and
// would STILL need this appeal branch — for a measured saving of about 60 µs
// per operation on Windows. The actor who could win that race is the actor who
// can create symlinks inside the root, who already has filesystem access to
// the server; that is not the boundary this driver is defending.
func (d *Driver) contained(abs string, mode leafMode) error {
	if d.followSymlinks {
		return nil
	}
	rel, err := filepath.Rel(d.root, abs)
	if err != nil {
		return ErrEscapesRoot
	}
	if rel == "." {
		return nil
	}
	oracle, err := os.OpenRoot(d.root)
	if err != nil {
		// The root itself is gone or unreadable. The verb about to run will
		// say so in its own vocabulary; refusing here would dress "the storage
		// directory is missing" up as "you tried to escape".
		return nil //nolint:nilerr // deliberate — see comment
	}
	defer oracle.Close()
	if mode == derefLeaf {
		_, err = oracle.Stat(rel)
	} else {
		_, err = oracle.Lstat(rel)
	}
	// ⚠ A missing leaf is CONTAINED, not unknown. os.Root walks the path one
	// component at a time and reports an escape the moment a component leaves
	// the root, so reaching the leaf at all proves the chain above it was
	// clean — measured: Stat("escape/nope.txt") → "path escapes from parent",
	// while Stat("real/nope.txt") → not-exist. Write and Mkdir depend on this,
	// because their target is a name that does not exist yet.
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return d.appeal(abs)
}

// appeal is the slow, exact answer to "does this path, followed the whole way,
// stay inside the root?" — asked only once the oracle has refused.
func (d *Driver) appeal(abs string) error {
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// ⚠ The leaf may simply not exist yet — a Write or Mkdir of a new name
		// under an absolute in-root link. EvalSymlinks refuses any path whose
		// last component is missing, so resolve the PARENT and re-attach the
		// name; measured to resolve correctly through the link on both hosts.
		parent, perr := filepath.EvalSymlinks(filepath.Dir(abs))
		if perr != nil {
			// Nothing resolvable at all. Fail closed — the oracle said no and
			// no evidence has been produced to overturn it.
			return ErrLinkLeavesRoot
		}
		real = filepath.Join(parent, filepath.Base(abs))
	}
	if !within(d.realRoot, real) {
		return ErrLinkLeavesRoot
	}
	return nil
}

// resolveDeref is resolve plus containment for a verb that will read or write
// through the leaf.
func (d *Driver) resolveDeref(p string) (string, error) {
	abs, err := d.resolve(p)
	if err != nil {
		return "", err
	}
	if err := d.contained(abs, derefLeaf); err != nil {
		return "", err
	}
	return abs, nil
}

// resolveNode is resolve plus containment for a verb that acts on the leaf
// node itself and never dereferences it (Delete, and the source of a Move).
func (d *Driver) resolveNode(p string) (string, error) {
	abs, err := d.resolve(p)
	if err != nil {
		return "", err
	}
	if err := d.contained(abs, nodeLeaf); err != nil {
		return "", err
	}
	return abs, nil
}

// List implements storage.Driver.
func (d *Driver) List(_ context.Context, p string) ([]storage.Object, error) {
	abs, err := d.resolveDeref(p)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	out := make([]storage.Object, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		obj := storage.Object{
			Path:  path.Join(p, e.Name()),
			Name:  e.Name(),
			Size:  info.Size(),
			Mtime: info.ModTime(),
		}
		full := filepath.Join(abs, e.Name())
		switch {
		case e.IsDir():
			obj.Kind = storage.KindDirectory
		case info.Mode()&os.ModeSymlink != 0:
			if !d.describeLink(full, &obj) {
				continue
			}
		case info.Mode().IsRegular():
			obj.Kind = storage.KindFile
			obj.Mime = sniffMime(full)
		default:
			// ⚠⚠ A named pipe, socket or device (issue #38). This branch used
			// to be `default: it is a file` and sniffed it — and opening a
			// pipe nobody writes to never returns, so the scan hung.
			d.skipSpecial(full, info.Mode())
			continue
		}
		out = append(out, obj)
	}
	return out, nil
}

// describeLink decides what a symlink entry IS. That decision is the whole of
// issue #34: List read Lstat data and called every link a KindSymlink of size
// 0, while Stat read os.Stat data, followed the link, and called the very same
// object a directory. The explorer believes List, so a perfectly good
// directory turned up as a 0-byte file that would not open.
//
// ⚠ Everything here is gated on ModeSymlink, which os.ReadDir has already paid
// for, and that gate IS the budget. Measured on a 2000-entry directory holding
// 20 links (µs per entry, Windows / Linux):
//
//	today's List, unchanged            108   / 11.8
//	os.Stat on EVERY entry              72   /  3.7
//	EvalSymlinks on EVERY entry       1292   /  9.9   ← 2.6 s for ONE listing
//	this, on the 20 entries that are links
//	                                    23   /  3.3
//
// The ungated column is why the gate is not an optimisation to revisit later.
//
// It answers false for a link whose target is a named pipe, socket or device:
// that entry is not shown at all, exactly as the thing it points at would not
// be (issue #38).
func (d *Driver) describeLink(full string, obj *storage.Object) bool {
	target, err := os.Stat(full) // follows the link
	if err != nil {
		// A broken link — the target was moved or deleted. It is still a real
		// entry the operator should see, and saying "symlink" is the only
		// honest answer available.
		obj.Kind = storage.KindSymlink
		obj.Size = 0
		obj.Metadata = map[string]string{storage.MetaLinkState: storage.LinkBroken}
		return true
	}
	if err := d.contained(full, derefLeaf); err != nil {
		// Out of the root with following off. ⚠ Shown, never hidden: hiding it
		// would replace "a file that will not open" with "a file that is not
		// there", which is strictly worse for the person who put the link
		// there on purpose and is the reason they filed the issue.
		obj.Kind = storage.KindSymlink
		obj.Size = 0
		obj.Metadata = map[string]string{storage.MetaLinkState: storage.LinkOutsideRoot}
		return true
	}
	if regfile.Special(target.Mode()) {
		d.skipSpecial(full, target.Mode())
		return false
	}
	// Inside the root, or following is on: the link IS its target, down to the
	// size and mtime. Reporting the link's own size here is what made the
	// reported directory "0 bytes" (Windows) / "44 bytes" (Linux, the length
	// of the target path).
	obj.Size = target.Size()
	obj.Mtime = target.ModTime()
	obj.Metadata = map[string]string{storage.MetaLinkState: storage.LinkFollowed}
	if target.IsDir() {
		obj.Kind = storage.KindDirectory
		// ⚠ The resolved target is what stops a walk going round in a circle
		// (storage.CycleGuard): `real/cycle -> root` produces a fresh logical
		// path at every depth, so only a real identity collapses them. Paid
		// here and nowhere else — a DIRECTORY link is the only kind that can
		// close a loop, and EvalSymlinks is this package's expensive call.
		if real, rerr := filepath.EvalSymlinks(full); rerr == nil {
			obj.Metadata[storage.MetaLinkTarget] = real
		}
	} else {
		obj.Kind = storage.KindFile
		obj.Mime = sniffMime(full)
	}
	return true
}

// Stat implements storage.Driver.
func (d *Driver) Stat(_ context.Context, p string) (storage.Object, error) {
	abs, err := d.resolve(p)
	if err != nil {
		return storage.Object{}, err
	}
	// ⚠ Lstat FIRST, and only then the containment check. The link node itself
	// always lives inside the root, so Stat must be able to describe it even
	// when its target may not be followed — otherwise the entry List shows
	// with an explanation becomes an error the moment anything asks about it,
	// and List and Stat are back to disagreeing, which is the bug.
	if li, lerr := os.Lstat(abs); lerr == nil && li.Mode()&os.ModeSymlink != 0 {
		obj := storage.Object{
			Path:  p,
			Name:  filepath.Base(p),
			Size:  li.Size(),
			Mtime: li.ModTime(),
		}
		if !d.describeLink(abs, &obj) {
			return storage.Object{}, storage.ErrNotFound
		}
		return obj, nil
	}
	if err := d.contained(abs, derefLeaf); err != nil {
		return storage.Object{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return storage.Object{}, storage.ErrNotFound
		}
		return storage.Object{}, err
	}
	// Not a file and not a folder: List does not show it, so Stat does not
	// know it either — one answer for one object (issue #38).
	if regfile.Special(info.Mode()) {
		d.skipSpecial(abs, info.Mode())
		return storage.Object{}, storage.ErrNotFound
	}
	obj := storage.Object{
		Path:  p,
		Name:  filepath.Base(p),
		Size:  info.Size(),
		Mtime: info.ModTime(),
	}
	if info.IsDir() {
		obj.Kind = storage.KindDirectory
	} else {
		obj.Kind = storage.KindFile
		obj.Mime = sniffMime(abs)
	}
	return obj, nil
}

// openRead opens a file the driver serves. A named pipe, socket or device —
// which List and Stat never show — answers ErrNotFound, and answers it at
// once: regfile never waits on a pipe (issue #38).
func openRead(abs string) (*os.File, error) {
	f, err := regfile.Open(abs)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, regfile.ErrNotRegular) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// Read implements storage.Driver.
func (d *Driver) Read(_ context.Context, p string) (io.ReadCloser, error) {
	abs, err := d.resolveDeref(p)
	if err != nil {
		return nil, err
	}
	return openRead(abs)
}

// ReadRange implements storage.RangeReader — a plain seek on the open
// file. Seeking past EOF is legal on a POSIX file and the first Read then
// reports io.EOF, which is exactly the documented contract.
func (d *Driver) ReadRange(_ context.Context, p string, off, length int64) (io.ReadCloser, error) {
	if off < 0 {
		return nil, fmt.Errorf("local: negative range offset %d", off)
	}
	abs, err := d.resolveDeref(p)
	if err != nil {
		return nil, err
	}
	f, err := openRead(abs)
	if err != nil {
		return nil, err
	}
	if length == 0 {
		_ = f.Close()
		return storage.EmptyReadCloser(), nil
	}
	if off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return storage.LimitReadCloser(f, length), nil
}

// Write implements storage.Writer.
func (d *Driver) Write(_ context.Context, p string, r io.Reader, _ int64) error {
	// ⚠ derefLeaf, not nodeLeaf: os.Create on an existing symlink writes
	// THROUGH it. A link planted at an upload path would otherwise let an
	// ordinary upload overwrite a file outside the storage.
	abs, err := d.resolveDeref(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	f, err := createRegular(abs)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// createRegular is os.Create that refuses to write onto a named pipe, socket
// or device sitting at the path. os.Create on a pipe nobody reads blocks
// forever, and on a device it writes to the device (issue #38).
func createRegular(abs string) (*os.File, error) {
	f, err := regfile.OpenFile(abs, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
	if errors.Is(err, regfile.ErrNotRegular) {
		return nil, fmt.Errorf("%w: %q is not a regular file", storage.ErrKindConflict, filepath.Base(abs))
	}
	return f, err
}

// SetMtime implements storage.Toucher.
func (d *Driver) SetMtime(_ context.Context, p string, mtime time.Time) error {
	abs, err := d.resolveDeref(p)
	if err != nil {
		return err
	}
	if err := os.Chtimes(abs, mtime, mtime); err != nil {
		if os.IsNotExist(err) {
			return storage.ErrNotFound // same contract as Move and Copy, below
		}
		return err
	}
	return nil
}

// Move implements storage.Mover.
func (d *Driver) Move(_ context.Context, src, dst string) error {
	// ⚠ The two ends are judged differently, and measurement says they must
	// be. os.Rename moves the SOURCE NODE — renaming a symlink moves the link
	// and leaves its target where it was (measured on both hosts) — so a
	// source link is an in-root operation. The DESTINATION is a name that gets
	// created or replaced, so it is judged with the leaf followed.
	a, err := d.resolveNode(src)
	if err != nil {
		return err
	}
	b, err := d.resolveDeref(dst)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b), 0o755); err != nil {
		return err
	}
	// ⚠⚠ Retried, because on Windows a rename fails outright while ANY handle
	// is open on the file — including filex's own thumbnailer or content
	// indexer, since Go opens files without FILE_SHARE_DELETE. Uploading a file
	// and deleting it straight away returned 500 about three times in two
	// hundred (measured 2026-08-17). Every such holder releases in
	// milliseconds; see retry.go for why the budget is one second and why only
	// this class of error is retried. On Unix the retry never engages.
	if err := retryWhileLocked(func() error { return os.Rename(a, b) }); err != nil {
		// The Driver contract says a missing path is storage.ErrNotFound, and
		// Read/Stat/List all honour it — Move and Copy did not, and returned
		// the raw *fs.PathError instead.
		//
		// That is not cosmetic. internal/trash.Put asks `errors.Is(err,
		// storage.ErrNotFound)` to tell "the object is already gone" (report
		// Missing, succeed) from "the rename genuinely failed" (fail the op).
		// With the raw error it could not, so deleting a path that was no
		// longer there ended the async op as `failed` — measured 2026-08-15 as
		// `rename …: The system cannot find the path specified` on a delete
		// whose source had already moved.
		if os.IsNotExist(err) {
			return storage.ErrNotFound
		}
		return err
	}
	return nil
}

// Copy implements storage.Copier.
func (d *Driver) Copy(ctx context.Context, src, dst string) error {
	// Both ends dereference: a copy reads the bytes BEHIND the source link and
	// os.Create writes THROUGH a destination link.
	a, err := d.resolveDeref(src)
	if err != nil {
		return err
	}
	b, err := d.resolveDeref(dst)
	if err != nil {
		return err
	}
	info, err := os.Stat(a)
	if err != nil {
		if os.IsNotExist(err) {
			return storage.ErrNotFound // same contract as Move, above
		}
		return err
	}
	if info.IsDir() {
		return d.copyTree(a, b)
	}
	return copyFile(a, b)
}

// Delete implements storage.Deleter.
func (d *Driver) Delete(_ context.Context, p string) error {
	// ⚠ nodeLeaf: os.RemoveAll on a directory symlink unlinks the LINK and
	// leaves the tree behind it untouched (measured on Linux and Windows
	// alike), so deleting a link — including one whose target may not be
	// followed, which is precisely the one an operator wants rid of — never
	// leaves the root.
	abs, err := d.resolveNode(p)
	if err != nil {
		return err
	}
	// Same Windows hazard as Move: an open handle blocks the unlink too, and a
	// permanent delete failing with an internal error is the version of this
	// bug the user cannot work around by waiting, because the row is already
	// gone from their trash.
	if err := retryWhileLocked(func() error { return os.RemoveAll(abs) }); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Mkdir implements storage.Mkdirer.
func (d *Driver) Mkdir(_ context.Context, p string) error {
	abs, err := d.resolveDeref(p)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0o755)
}

// Root returns the absolute disk path — exposed so the sync worker's
// fsnotify watcher can attach to it.
func (d *Driver) Root() string { return d.root }

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := openRead(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := createRegular(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// copyTree copies a directory recursively.
//
// ⚠⚠ filepath.Walk hands back LSTAT info, so a symlink inside the tree arrives
// here as a non-directory and used to go straight to copyFile — which opens
// it, follows it, and copies whatever is on the other end INTO the storage.
// That is a read escape with no `..` and no admin involvement: one link inside
// a folder anybody copies. A directory link was not even a successful escape,
// it was a broken copy — io.Copy on an opened directory fails, so the whole
// operation died partway through with an unexplained error.
//
// The rule now: a link is copied BY VALUE when it resolves to a file the
// driver is allowed to follow, and skipped otherwise. Skipping rather than
// failing is deliberate — one unfollowable link in a thousand-file folder
// should not cost the operator the other nine hundred and ninety-nine, and the
// files that were copied are all genuinely inside the storage.
//
// ⚠ A contained DIRECTORY link is skipped too, not recursed into. Recursing
// would need this walk to carry the cycle guard the catalogue walks carry
// (`real/cycle -> root` was measured to reach depth 81 before the OS stopped
// it), and a copy is the wrong place to grow one.
func (d *Driver) copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if cerr := d.contained(p, derefLeaf); cerr != nil {
				return nil
			}
			ti, terr := os.Stat(p)
			if terr != nil || ti.IsDir() {
				return nil
			}
			if regfile.Special(ti.Mode()) {
				d.skipSpecial(p, ti.Mode())
				return nil
			}
		} else if regfile.Special(info.Mode()) {
			// A named pipe, socket or device inside a copied folder is skipped
			// like an unfollowable link: copyFile would open it, and a pipe
			// holds that open forever (issue #38).
			d.skipSpecial(p, info.Mode())
			return nil
		}
		return copyFile(p, target)
	})
}

// sniffMime peeks the first 512 bytes for magic-byte detection, then
// refines ZIP-based formats via storage.RefineOfficeMime.
//
// http.DetectContentType returns "application/zip" for every ZIP
// container, including the OOXML/ODF office formats which are just
// ZIPs with a manifest. OnlyOffice Document Server fetches the source
// bytes from filex and inspects Content-Type for sanity: when fileType
// in the JWT-signed config says "pptx" but the fetch response says
// "application/zip" the converter aborts with "Download failed."
// xlsx works only because OnlyOffice's xlsx pipeline accepts ZIP MIME
// — pptx/docx/odt do not. Setting the correct office MIME at sniff
// time keeps the downstream contract consistent for ALL office types.
func sniffMime(abs string) string {
	// ⚠ regfile, never os.Open: this runs for every file of every listing,
	// and an entry that became a pipe after the caller looked at it must not
	// hang the listing (issue #38).
	f, err := regfile.Open(abs)
	if err != nil {
		return ""
	}
	defer f.Close()
	var buf [512]byte
	n, _ := f.Read(buf[:])
	return storage.RefineOfficeMime(http.DetectContentType(buf[:n]), abs)
}
