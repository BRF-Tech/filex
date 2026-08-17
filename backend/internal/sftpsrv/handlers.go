package sftpsrv

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// The SFTP verbs, mapped onto filex.
//
// The path model is the one /dav already uses and the one the S3 endpoint
// mirrors: the first segment names a STORAGE, the rest is storage-relative.
// Three protocols disagreeing about what a storage is called is how a user ends
// up unable to find, over one, the folder they made over another.
//
//	/                     the storages this caller may see
//	/photos               that storage's root
//	/photos/2026/img.jpg  an object

// hiddenNames are filex-internal buckets that no protocol exposes.
var hiddenNames = map[string]bool{
	".filex-trash": true,
	".versions":    true,
	".thumbs":      true,
}

func hiddenPath(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if hiddenNames[seg] {
			return true
		}
	}
	return false
}

// fs is one session's view of the tree. One per SFTP session, so the ACL sets
// are resolved once per storage rather than once per packet.
type fs struct {
	srv  *Server
	sess *session
	ctx  context.Context

	mu   sync.Mutex
	sets map[int64]*acl.Set
}

// handlers builds the handler set for a session.
func (s *Server) handlers(sess *session) sftp.Handlers {
	f := &fs{srv: s, sess: sess, ctx: s.ctxFor(sess), sets: map[int64]*acl.Set{}}
	return sftp.Handlers{FileGet: f, FilePut: f, FileCmd: f, FileList: f}
}

// ─────────────────────────── path resolution ───────────────────────────

// target is a resolved request path.
type target struct {
	// Storage is nil for the virtual root.
	Storage *model.Storage
	Set     *acl.Set
	// Rel is the storage-relative path, "" at a storage root.
	Rel string
}

func (t target) isRoot() bool { return t.Storage == nil }

// split turns an SFTP path into a storage name and a relative path.
func split(p string) (string, string) {
	rest := strings.Trim(path.Clean("/"+p), "/")
	if rest == "" || rest == "." {
		return "", ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i], acl.CleanRel(rest[i+1:])
	}
	return rest, ""
}

// resolve maps a request path onto a storage and a relative path, applying the
// tenant boundary, the caller's grants and the credential's confinement.
//
// ⚠ It answers os.ErrNotExist — never a permission error — for anything the
// caller may not see. An SFTP client shows "permission denied" and "no such
// file" differently, and the first one confirms that a path exists; across
// tenants that is an existence oracle. The same rule /dav and the S3 endpoint
// follow.
func (f *fs) resolve(p string) (target, error) {
	name, rel := split(p)
	if name == "" {
		return target{}, nil // the virtual root
	}
	if hiddenPath(rel) {
		return target{}, os.ErrNotExist
	}
	st, err := f.srv.cfg.Store.GetStorageByName(f.ctx, name)
	if err != nil || st == nil || !st.Enabled {
		return target{}, os.ErrNotExist
	}
	// ⚠ A by-name lookup is not tenant-scoped by the store (only the list
	// methods are), so the boundary has to be applied right here.
	if scope, _ := tenant.FromContext(f.ctx); !scope.CanAccessStorage(st.ID) {
		return target{}, os.ErrNotExist
	}
	// A confined credential sees exactly one storage, and only inside its root.
	if c := f.sess.principal.Confine; c != nil {
		if c.Adapter != "" && c.Adapter != st.Name {
			return target{}, os.ErrNotExist
		}
		if c.Rel != "" && rel != c.Rel && !strings.HasPrefix(rel, c.Rel+"/") {
			return target{}, os.ErrNotExist
		}
	}
	set, err := f.aclSet(st)
	if err != nil {
		return target{}, err
	}
	if !set.StorageVisible() {
		return target{}, os.ErrNotExist
	}
	return target{Storage: st, Set: set, Rel: rel}, nil
}

func (f *fs) aclSet(st *model.Storage) (*acl.Set, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sets[st.ID]; ok {
		return s, nil
	}
	s, err := f.sess.principal.ACL(f.ctx, st)
	if err != nil {
		return nil, err
	}
	f.sets[st.ID] = s
	return s, nil
}

// canRead / canWrite are the two gates every verb goes through.

// canTraverse is the LISTING and STAT door, and it is deliberately not
// canRead.
//
// ⚠⚠ A grant is per-FOLDER, so a caller can hold viewer on `main/projects/acme`
// and nothing at all on `main` or `main/projects`. Asking for viewer on the
// ancestor refuses the two levels ABOVE the grant — which means the folder the
// caller was actually given cannot be reached, because there is no way to `cd`
// into it. acl.Set.CanSee exists for exactly this: it answers true for an
// ancestor of a granted path so it renders as a traversal node, and the
// per-entry filter in the listing keeps the caller's siblings out of view.
// (Measured 2026-08-16: `ls /main` over SFTP, FTPS and NFS answered "no such
// file" for a user holding a subfolder grant. The web explorer showed the same
// tree correctly, because it has always used CanSee.)
//
// Reading a file's BYTES still requires viewer on the file itself — see
// canRead — so traversal never becomes access.
func (f *fs) canTraverse(t target) bool {
	return t.Set != nil && t.Set.CanSee(t.Rel)
}

// canRead is the door for a file's CONTENT: viewer on the path itself, never
// inherited from being on the way to a grant. canTraverse above is what lets a
// caller walk to their folder; this is what lets them open what is in it.
func (f *fs) canRead(t target) bool {
	return t.Set != nil && t.Set.Effective(t.Rel) >= acl.LevelViewer
}

func (f *fs) canWrite(t target) bool {
	if t.Storage == nil || t.Storage.ReadOnly {
		return false
	}
	return t.Set != nil && t.Set.Effective(t.Rel) >= acl.LevelEditor
}

// driverFor returns the live driver for a target.
func (f *fs) driverFor(t target) (storage.Driver, error) {
	if t.Storage == nil {
		return nil, os.ErrInvalid
	}
	return f.srv.cfg.Resolver(t.Storage.ID)
}

// visibleStorages lists the storages this session may see, in name order.
func (f *fs) visibleStorages() []*model.Storage {
	all, err := f.srv.cfg.Store.ListEnabledStorages(f.ctx)
	if err != nil {
		return nil
	}
	scope, _ := tenant.FromContext(f.ctx)
	out := make([]*model.Storage, 0, len(all))
	for _, st := range all {
		if !scope.CanAccessStorage(st.ID) {
			continue
		}
		if c := f.sess.principal.Confine; c != nil && c.Adapter != "" && c.Adapter != st.Name {
			continue
		}
		set, err := f.aclSet(st)
		if err != nil || !set.StorageVisible() {
			continue
		}
		out = append(out, st)
	}
	return out
}

// ─────────────────────────── Filelist ───────────────────────────

// Filelist serves List, Stat and Readlink.
func (f *fs) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	switch r.Method {
	case "List":
		return f.list(r.Filepath)
	case "Stat":
		return f.stat(r.Filepath)
	case "Readlink":
		// filex has no symlinks and must not invent one: a client that follows
		// a fabricated link ends up reading a different file than it asked for.
		return nil, sftp.ErrSSHFxOpUnsupported
	default:
		return nil, sftp.ErrSSHFxOpUnsupported
	}
}

// Lstat is Stat: there are no symlinks, so the two cannot differ.
func (f *fs) Lstat(r *sftp.Request) (sftp.ListerAt, error) { return f.stat(r.Filepath) }

// RealPath answers `SSH_FXP_REALPATH`.
//
// ⚠⚠ Mandatory, and mandatory to answer with an ABSOLUTE path. OpenSSH calls
// `fatal("Need cwd")` if it fails; WinSCP and FileZilla both call it on
// connect, and WinSCP uses REALPATH "/" as its keepalive — so it must also stay
// O(1) and never touch a backend. A relative answer gives clients `work/work/f`
// and a `cd ..` that never terminates.
func (f *fs) RealPath(p string) (string, error) {
	if p == "" || p == "." {
		return "/", nil
	}
	clean := path.Clean("/" + strings.TrimPrefix(p, "/"))
	return clean, nil
}

func (f *fs) list(p string) (sftp.ListerAt, error) {
	t, err := f.resolve(p)
	if err != nil {
		return nil, err
	}
	if t.isRoot() {
		sts := f.visibleStorages()
		out := make([]os.FileInfo, 0, len(sts))
		for _, st := range sts {
			set, _ := f.aclSet(st)
			out = append(out, storageInfo(st, set))
		}
		return listerAt(out), nil
	}
	if !f.canTraverse(t) {
		return nil, os.ErrNotExist
	}
	drv, err := f.driverFor(t)
	if err != nil {
		return nil, err
	}
	objs, err := drv.List(f.ctx, t.Rel)
	if err != nil {
		return nil, mapStorageErr(err)
	}
	out := make([]os.FileInfo, 0, len(objs))
	for _, o := range objs {
		rel := path.Join(t.Rel, o.Name)
		if hiddenPath(rel) {
			continue
		}
		// ⚠ FILTER, never reject. A listing that fails because one entry is
		// out of reach hides the whole directory; S3 and /dav both learned
		// this the same way.
		if !t.Set.CanSee(rel) {
			continue
		}
		out = append(out, objectInfo(o, t.Set.Effective(rel)))
	}
	return listerAt(out), nil
}

func (f *fs) stat(p string) (sftp.ListerAt, error) {
	t, err := f.resolve(p)
	if err != nil {
		return nil, err
	}
	if t.isRoot() {
		return listerAt([]os.FileInfo{rootInfo()}), nil
	}
	if !f.canTraverse(t) {
		return nil, os.ErrNotExist
	}
	if t.Rel == "" {
		return listerAt([]os.FileInfo{storageInfo(t.Storage, t.Set)}), nil
	}
	drv, err := f.driverFor(t)
	if err != nil {
		return nil, err
	}
	// The shared read door, not the driver: an object whose bytes are still in
	// the staging area of an upload has to stat as the file it will be, or a
	// client sees a file it cannot open.
	src, err := f.srv.cfg.Body.Resolve(f.ctx, drv, t.Storage.ID, t.Rel, nil)
	if err != nil {
		return nil, mapStorageErr(err)
	}
	obj, err := src.Stat(f.ctx)
	if err != nil {
		return nil, mapStorageErr(err)
	}
	obj.Name = path.Base(t.Rel)
	return listerAt([]os.FileInfo{objectInfo(obj, t.Set.Effective(t.Rel))}), nil
}

// ─────────────────────────── Filecmd ───────────────────────────

// Filecmd serves the verbs that change the tree.
func (f *fs) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Setstat":
		// ⚠ Must succeed. Clients chmod and utime after EVERY upload; object
		// storage has no such concept, and an error here gives WinSCP a warning
		// dialog on every single file. Answering "fine" for a permission bit
		// filex does not keep is honest — the bits it reports are synthesised
		// from the ACL and were never the client's to set.
		return f.setstat(r)
	case "Rename":
		// Plain v3 RENAME must fail when the destination exists.
		return f.rename(r, false)
	case "PosixRename":
		// ⚠ This is the one that matters: every write-temp-then-rename tool
		// (rclone, backups, the desktop sync) breaks on its SECOND run without
		// an overwriting rename.
		return f.rename(r, true)
	case "Mkdir":
		return f.mkdir(r)
	case "Rmdir", "Remove":
		return f.remove(r)
	case "Link", "Symlink":
		// ⚠ Refused rather than faked. `Symlink`'s Filepath is the ONE path the
		// library does not clean, so a link is also the one place a traversal
		// could be smuggled in — and a filesystem with no links has no such
		// question to answer.
		return sftp.ErrSSHFxOpUnsupported
	default:
		return sftp.ErrSSHFxOpUnsupported
	}
}

// PosixRename is the overwriting rename, and it has to be a METHOD.
//
// ⚠⚠ Handling "PosixRename" inside Filecmd's switch is NOT enough: the library
// dispatches posix-rename@openssh.com by asserting the handler to
// `sftp.PosixRenameFileCmder`, and without this method every posix-rename
// arrives as a plain v3 Rename — which must refuse an existing destination. The
// symptom is that rclone, backups and the desktop sync work once and fail on
// their SECOND run, when the temp-then-rename destination already exists.
// (Measured 2026-08-16: the test failed with "file already exists" while the
// switch case above was right there.)
func (f *fs) PosixRename(r *sftp.Request) error { return f.rename(r, true) }

func (f *fs) setstat(r *sftp.Request) error {
	t, err := f.resolve(r.Filepath)
	if err != nil {
		return err
	}
	if t.isRoot() {
		return sftp.ErrSSHFxPermissionDenied
	}
	// Even a no-op needs the write gate: a viewer must not be able to touch a
	// file's timestamp, and answering "fine" to everyone would say they can.
	if !f.canWrite(t) {
		return sftp.ErrSSHFxPermissionDenied
	}
	attrs := r.Attributes()
	// The one attribute filex can actually keep. `x-amz-meta-mtime` does the
	// same job on the S3 endpoint, and a sync that sets timestamps must settle
	// on both or it copies everything again on the next run.
	if r.AttrFlags().Acmodtime && attrs != nil {
		drv, derr := f.driverFor(t)
		if derr != nil {
			return nil
		}
		if toucher, ok := drv.(storage.Toucher); ok {
			mtime := time.Unix(int64(attrs.Mtime), 0).UTC()
			if err := toucher.SetMtime(f.ctx, t.Rel, mtime); err == nil {
				f.srv.syncer.Touch(f.ctx, t.Storage, t.Rel, mtime)
			}
		}
	}
	return nil
}

func (f *fs) mkdir(r *sftp.Request) error {
	t, err := f.resolve(r.Filepath)
	if err != nil {
		return err
	}
	if t.isRoot() || t.Rel == "" {
		// Creating a storage is an administrative act with a driver, a path and
		// credentials behind it; `mkdir /newthing` cannot express any of that.
		return sftp.ErrSSHFxPermissionDenied
	}
	if !f.canWrite(t) {
		return sftp.ErrSSHFxPermissionDenied
	}
	drv, err := f.driverFor(t)
	if err != nil {
		return err
	}
	mk, ok := drv.(storage.Mkdirer)
	if !ok {
		return sftp.ErrSSHFxOpUnsupported
	}
	if err := mk.Mkdir(f.ctx, t.Rel); err != nil {
		return mapStorageErr(err)
	}
	f.srv.syncer.Mkdir(f.ctx, t.Storage, t.Rel)
	return nil
}

func (f *fs) rename(r *sftp.Request, overwrite bool) error {
	src, err := f.resolve(r.Filepath)
	if err != nil {
		return err
	}
	dst, err := f.resolve(r.Target)
	if err != nil {
		return err
	}
	if src.isRoot() || dst.isRoot() || src.Rel == "" || dst.Rel == "" {
		return sftp.ErrSSHFxPermissionDenied
	}
	// ⚠ Both ends, with their own gate. A rename is a delete of one path and a
	// create of another; checking only the destination would let a caller move
	// a file OUT of a subtree they may not write.
	if !f.canWrite(src) || !f.canWrite(dst) {
		return sftp.ErrSSHFxPermissionDenied
	}
	if src.Storage.ID != dst.Storage.ID {
		// Across storages this is a copy plus a delete, which is an async
		// operation in filex — refusing is honest; pretending it is a rename
		// would return before any bytes had moved.
		return sftp.ErrSSHFxOpUnsupported
	}
	drv, err := f.driverFor(src)
	if err != nil {
		return err
	}
	mover, ok := drv.(storage.Mover)
	if !ok {
		return sftp.ErrSSHFxOpUnsupported
	}
	if !overwrite {
		if _, serr := drv.Stat(f.ctx, dst.Rel); serr == nil {
			// v3 RENAME semantics: refuse an existing destination.
			return os.ErrExist
		}
	}
	if err := mover.Move(f.ctx, src.Rel, dst.Rel); err != nil {
		return mapStorageErr(err)
	}
	f.srv.syncer.Move(f.ctx, src.Storage, src.Rel, dst.Rel)
	return nil
}

// remove serves both Remove and Rmdir.
//
// ⚠ To the TRASH, like every other surface. A delete over a protocol is still
// a delete by the owner, and "it was gone forever because I used WinSCP" is not
// a rule anyone can hold in their head.
func (f *fs) remove(r *sftp.Request) error {
	t, err := f.resolve(r.Filepath)
	if err != nil {
		return err
	}
	if t.isRoot() || t.Rel == "" {
		return sftp.ErrSSHFxPermissionDenied
	}
	if !f.canWrite(t) {
		return sftp.ErrSSHFxPermissionDenied
	}
	drv, err := f.driverFor(t)
	if err != nil {
		return err
	}
	out, terr := trash.Put(f.ctx, drv, t.Rel)
	switch {
	case terr == nil && out.Trashed:
		f.srv.syncer.Trash(f.ctx, t.Storage, t.Rel, out.Key)
		return nil
	case terr == nil && out.Missing:
		f.srv.syncer.Delete(f.ctx, t.Storage, t.Rel)
		return os.ErrNotExist
	case errors.Is(terr, trash.ErrUnsupported):
		del, ok := drv.(storage.Deleter)
		if !ok {
			return sftp.ErrSSHFxOpUnsupported
		}
		if derr := del.Delete(f.ctx, t.Rel); derr != nil && !errors.Is(derr, storage.ErrNotFound) {
			return mapStorageErr(derr)
		}
		f.srv.syncer.Delete(f.ctx, t.Storage, t.Rel)
		return nil
	default:
		return mapStorageErr(terr)
	}
}

// ─────────────────────────── errors ───────────────────────────

// mapStorageErr turns a driver error into the SFTP status a client can act on.
func mapStorageErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, storage.ErrNotFound), errors.Is(err, os.ErrNotExist):
		return os.ErrNotExist
	case errors.Is(err, storage.ErrReadOnly):
		return sftp.ErrSSHFxPermissionDenied
	case errors.Is(err, storage.ErrUnsupported):
		return sftp.ErrSSHFxOpUnsupported
	case errors.Is(err, io.EOF):
		return io.EOF
	default:
		return sftp.ErrSSHFxFailure
	}
}

// principalOf is a small accessor used by the file handles.
func (f *fs) principal() *protocolauth.Principal { return f.sess.principal }

// StatVFS answers `statvfs@openssh.com`.
//
// It is optional in the protocol and worth having: it lights up WinSCP's
// free-space indicator and `sftp df`, and the numbers come from the same quota
// the web app shows. A client that cannot see the space left is a client whose
// user finds out at the end of a large upload.
//
// ⚠ An unlimited account still needs a plausible answer — reporting zero blocks
// makes WinSCP show a full disk and refuse to start the transfer. It reports a
// large-but-finite figure in that case, which is the truth as far as the quota
// is concerned; the storage behind it has its own limits filex cannot see.
func (f *fs) StatVFS(r *sftp.Request) (*sftp.StatVFS, error) {
	const blockSize = 4096
	// A ceiling for the unlimited case: 1 PiB in 4 KiB blocks.
	const unlimitedBlocks = uint64(1) << 38

	total, free := unlimitedBlocks, unlimitedBlocks
	if u := auth.UserFrom(f.ctx); u != nil && f.srv.cfg.Quota != nil {
		if snap, err := f.srv.cfg.Quota.Get(f.ctx, u.ID); err == nil && !snap.Unlimited && snap.QuotaBytes > 0 {
			total = uint64(snap.QuotaBytes) / blockSize
			remaining := snap.QuotaBytes - snap.UsedBytes
			if remaining < 0 {
				remaining = 0
			}
			free = uint64(remaining) / blockSize
		}
	}
	return &sftp.StatVFS{
		Bsize:   blockSize,
		Frsize:  blockSize,
		Blocks:  total,
		Bfree:   free,
		Bavail:  free,
		Files:   1 << 30,
		Ffree:   1 << 30,
		Favail:  1 << 30,
		Namemax: 255,
	}, nil
}
