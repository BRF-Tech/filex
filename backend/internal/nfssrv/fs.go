package nfssrv

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"
	"time"

	billy "github.com/go-git/go-billy/v5"
	nfs "github.com/willscott/go-nfs"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// The billy.Filesystem one mount sees.
//
// The path model is the one every other protocol uses: the first segment names
// a STORAGE, the rest is storage-relative. When the export is confined to one
// storage the mount is rooted INSIDE it instead, because that is what somebody
// who exported `main/projects/acme` means — `mount server:/x/<secret> /mnt` and
// then `ls /mnt` should show what is in acme, not a directory called `main`.

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

type fs struct {
	srv       *Server
	principal *protocolauth.Principal
	export    *model.NFSExport
	ctx       context.Context
	readOnly  bool
	// rootStorage is set when the export names one storage: the mount is then
	// rooted inside it and the storage name never appears in a path.
	rootStorage string
	rootPrefix  string
	// live is this mount's entry in the revocation registry.
	//
	// ⚠⚠ An NFS mount cannot be hung up on. There is no session and no
	// connection the server owns — the client holds a file handle and keeps
	// sending RPCs — so revoking a credential here cannot close anything. What
	// it can do is make every operation start refusing, which is what
	// live.Revoked() is consulted for below. The client sees an access error
	// and its mount goes stale, which is the correct end state.
	live *protocolauth.LiveSession
}

// revoked reports whether the export this mount was authenticated with has been
// taken away. Checked at the two doors every operation passes through:
// resolve (anything naming a storage) and visibleStorages (the virtual root).
func (f *fs) revoked() bool { return f.live.Revoked() }

func newFS(srv *Server, p *protocolauth.Principal, e *model.NFSExport) *fs {
	return &fs{
		srv:         srv,
		principal:   p,
		export:      e,
		ctx:         p.WithContext(context.Background()),
		readOnly:    e.ReadOnly,
		rootStorage: e.StorageName,
		rootPrefix:  e.Prefix,
	}
}

func (f *fs) Root() string { return "/" }

// Join is billy's path join. Kept to `path` (not `filepath`) so a server
// running on Windows still speaks the protocol's own separator.
func (f *fs) Join(elem ...string) string { return path.Join(elem...) }

// Chroot is part of billy.Filesystem and is not something NFS asks for.
// Refusing keeps one path model rather than two.
func (f *fs) Chroot(string) (billy.Filesystem, error) { return nil, billy.ErrNotSupported }

// ─────────────────────────── resolution ───────────────────────────

type target struct {
	Storage *model.Storage
	Set     *acl.Set
	Rel     string
}

func (t target) isRoot() bool { return t.Storage == nil }

// split turns a mount-relative path into a storage name and a relative path,
// honouring the rooted-inside-one-storage case.
func (f *fs) split(p string) (string, string) {
	rest := strings.Trim(path.Clean("/"+p), "/")
	if rest == "." {
		rest = ""
	}
	if f.rootStorage != "" {
		// Rooted inside a storage: everything is relative to the export root.
		rel := rest
		if f.rootPrefix != "" {
			rel = path.Join(f.rootPrefix, rest)
		}
		return f.rootStorage, acl.CleanRel(rel)
	}
	if rest == "" {
		return "", ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i], acl.CleanRel(rest[i+1:])
	}
	return rest, ""
}

// resolve applies the tenant boundary, the grants and the confinement.
//
// ⚠ os.ErrNotExist for anything out of reach, never a permission error — the
// same no-existence-oracle rule the other protocols follow.
func (f *fs) resolve(p string) (target, error) {
	if f.revoked() {
		// ⚠ A permission error, not os.ErrNotExist. Everywhere else in this
		// file "out of reach" answers "does not exist" so the endpoint is not an
		// existence oracle — but here the client already mounted successfully
		// and has been reading these paths, so there is nothing left to hide,
		// and telling it the files vanished would make a revocation look like
		// data loss.
		return target{}, os.ErrPermission
	}
	name, rel := f.split(p)
	if name == "" {
		return target{}, nil
	}
	if hiddenPath(rel) {
		return target{}, os.ErrNotExist
	}
	st, err := f.srv.cfg.Store.GetStorageByName(f.ctx, name)
	if err != nil || st == nil || !st.Enabled {
		return target{}, os.ErrNotExist
	}
	if scope, _ := tenant.FromContext(f.ctx); !scope.CanAccessStorage(st.ID) {
		return target{}, os.ErrNotExist
	}
	if c := f.principal.Confine; c != nil {
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

// aclSet is the principal's own cached lookup, and nothing more.
//
// ⚠ There used to be a second map here, holding each set for the life of the
// mount. It made the Principal's TTL meaningless: a grant taken away at 09:00
// would keep serving files until the client unmounted, which for an NFS mount
// can be weeks. One cache, one expiry, one answer to "when does it stop
// working".
func (f *fs) aclSet(st *model.Storage) (*acl.Set, error) {
	return f.principal.ACL(f.ctx, st)
}

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
	// ⚠ The export's own read-only flag comes FIRST and cannot be argued with:
	// an operator who exported a folder read-only to a media player has said
	// something about the mount, not about the account.
	if f.readOnly || t.Storage == nil || t.Storage.ReadOnly {
		return false
	}
	return t.Set != nil && t.Set.Effective(t.Rel) >= acl.LevelEditor
}

func (f *fs) driverFor(t target) (storage.Driver, error) {
	if t.Storage == nil {
		return nil, os.ErrInvalid
	}
	return f.srv.cfg.Resolver(t.Storage.ID)
}

func (f *fs) visibleStorages() []*model.Storage {
	if f.revoked() {
		return nil
	}
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
		if c := f.principal.Confine; c != nil && c.Adapter != "" && c.Adapter != st.Name {
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

// ─────────────────────────── reading the tree ───────────────────────────

func (f *fs) Stat(name string) (os.FileInfo, error) {
	t, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	if t.isRoot() {
		// ⚠⚠ A STABLE timestamp, never time.Now(). The virtual root has no
		// mtime of its own, and the obvious stand-in is a trap: an NFS client
		// re-reads a directory until the mtime it got from GETATTR equals the
		// one in the "." entry of READDIRPLUS (go-nfs-client target.go:490).
		// With a moving clock those two can never agree, so the client spins
		// for ever — measured here as 135,915 ReadDir calls in twenty seconds,
		// each one a database query, from a single `ls` (2026-08-16).
		return dirInfo(path.Base(orSlash(name)), f.srv.started, !f.readOnly), nil
	}
	if !f.canTraverse(t) {
		return nil, os.ErrNotExist
	}
	if t.Rel == "" {
		set, _ := f.aclSet(t.Storage)
		return storageInfo(t.Storage, set, f.readOnly), nil
	}
	drv, err := f.driverFor(t)
	if err != nil {
		return nil, err
	}
	// The shared read door, so a file whose bytes are still staged stats as the
	// file it will be rather than as missing.
	src, err := f.srv.cfg.Body.Resolve(f.ctx, drv, t.Storage.ID, t.Rel, nil)
	if err != nil {
		return nil, mapErr(err)
	}
	obj, err := src.Stat(f.ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	obj.Name = path.Base(t.Rel)
	return objectInfo(obj, t.Set.Effective(t.Rel), f.readOnly), nil
}

// Lstat is Stat: filex has no symlinks, so the two cannot differ.
func (f *fs) Lstat(name string) (os.FileInfo, error) { return f.Stat(name) }

func (f *fs) ReadDir(name string) ([]os.FileInfo, error) {
	t, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	if t.isRoot() {
		sts := f.visibleStorages()
		out := make([]os.FileInfo, 0, len(sts))
		for _, st := range sts {
			set, _ := f.aclSet(st)
			out = append(out, storageInfo(st, set, f.readOnly))
		}
		return out, nil
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
		return nil, mapErr(err)
	}
	out := make([]os.FileInfo, 0, len(objs))
	for _, o := range objs {
		rel := path.Join(t.Rel, o.Name)
		if hiddenPath(rel) {
			continue
		}
		// ⚠ Filter, never reject — one unreachable entry must not hide the
		// whole directory.
		if !t.Set.CanSee(rel) {
			continue
		}
		out = append(out, objectInfo(o, t.Set.Effective(rel), f.readOnly))
	}
	return out, nil
}

// ─────────────────────────── mutation ───────────────────────────

func (f *fs) MkdirAll(name string, _ os.FileMode) error {
	t, err := f.resolve(name)
	if err != nil {
		return err
	}
	if t.isRoot() || t.Rel == "" {
		return os.ErrPermission
	}
	if !f.canWrite(t) {
		return os.ErrPermission
	}
	drv, err := f.driverFor(t)
	if err != nil {
		return err
	}
	mk, ok := drv.(storage.Mkdirer)
	if !ok {
		return billy.ErrNotSupported
	}
	if err := mk.Mkdir(f.ctx, t.Rel); err != nil {
		return mapErr(err)
	}
	f.srv.syncer.Mkdir(f.ctx, t.Storage, t.Rel)
	return nil
}

// Remove deletes — to the TRASH, like every other surface.
func (f *fs) Remove(name string) error {
	t, err := f.resolve(name)
	if err != nil {
		return err
	}
	if t.isRoot() || t.Rel == "" {
		return os.ErrPermission
	}
	if !f.canWrite(t) {
		return os.ErrPermission
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
			return billy.ErrNotSupported
		}
		if derr := del.Delete(f.ctx, t.Rel); derr != nil && !errors.Is(derr, storage.ErrNotFound) {
			return mapErr(derr)
		}
		f.srv.syncer.Delete(f.ctx, t.Storage, t.Rel)
		return nil
	default:
		return mapErr(terr)
	}
}

func (f *fs) Rename(oldpath, newpath string) error {
	src, err := f.resolve(oldpath)
	if err != nil {
		return err
	}
	dst, err := f.resolve(newpath)
	if err != nil {
		return err
	}
	if src.isRoot() || dst.isRoot() || src.Rel == "" || dst.Rel == "" {
		return os.ErrPermission
	}
	// ⚠ Both ends: a rename moves a file OUT of one subtree and INTO another.
	if !f.canWrite(src) || !f.canWrite(dst) {
		return os.ErrPermission
	}
	if src.Storage.ID != dst.Storage.ID {
		return billy.ErrNotSupported
	}
	drv, err := f.driverFor(src)
	if err != nil {
		return err
	}
	mover, ok := drv.(storage.Mover)
	if !ok {
		return billy.ErrNotSupported
	}
	if err := mover.Move(f.ctx, src.Rel, dst.Rel); err != nil {
		return mapErr(err)
	}
	f.srv.syncer.Move(f.ctx, src.Storage, src.Rel, dst.Rel)
	return nil
}

// ─────────────────────────── billy.Change ───────────────────────────

// Chmod has nothing to change: the bits filex reports are synthesised from the
// ACL and were never the client's to set.
//
// ⚠ It returns nil rather than an error. NFS clients chmod as a matter of
// course (`cp -p`, rsync, an editor writing a temp file), and a failure there
// turns a completed copy into a reported error.
func (f *fs) Chmod(string, os.FileMode) error { return nil }

// Lchown/Chown likewise: filex has accounts, not POSIX users.
func (f *fs) Lchown(string, int, int) error { return nil }
func (f *fs) Chown(string, int, int) error  { return nil }

// Chtimes carries the one attribute filex can genuinely keep — and the one
// `cp -p` and rsync need kept, or every sync copies everything again.
func (f *fs) Chtimes(name string, _ time.Time, mtime time.Time) error {
	t, err := f.resolve(name)
	if err != nil {
		return err
	}
	if t.isRoot() || !f.canWrite(t) {
		return os.ErrPermission
	}
	drv, err := f.driverFor(t)
	if err != nil {
		return err
	}
	toucher, ok := drv.(storage.Toucher)
	if !ok {
		return billy.ErrNotSupported
	}
	if err := toucher.SetMtime(f.ctx, t.Rel, mtime); err != nil {
		return mapErr(err)
	}
	f.srv.syncer.Touch(f.ctx, t.Storage, t.Rel, mtime)
	return nil
}

// ─────────────────────────── symlinks ───────────────────────────

// filex has no symlinks, and inventing them would mean a client following a
// link to somewhere that does not exist.
func (f *fs) Symlink(string, string) error    { return billy.ErrNotSupported }
func (f *fs) Readlink(string) (string, error) { return "", billy.ErrNotSupported }
func (f *fs) TempFile(string, string) (billy.File, error) {
	// NFS never asks for this — it creates real files with real names — and a
	// temp file with no name in filex's tree would be a node nobody can see.
	return nil, billy.ErrNotSupported
}

// ─────────────────────────── free space ───────────────────────────

// fsStat fills in what a file manager shows in its status bar.
//
// ⚠ An unlimited account still needs a plausible number: zero makes clients
// report a full disk and refuse to copy anything.
func (f *fs) fsStat(ctx context.Context, st *nfs.FSStat) error {
	const unlimited = uint64(1) << 45 // 32 TiB
	st.TotalSize, st.FreeSize, st.AvailableSize = unlimited, unlimited, unlimited
	st.TotalFiles, st.FreeFiles, st.AvailableFiles = 1<<30, 1<<30, 1<<30
	st.CacheHint = 0

	if u := auth.UserFrom(f.ctx); u != nil && f.srv.cfg.Quota != nil {
		if snap, err := f.srv.cfg.Quota.Get(ctx, u.ID); err == nil && !snap.Unlimited && snap.QuotaBytes > 0 {
			free := snap.QuotaBytes - snap.UsedBytes
			if free < 0 {
				free = 0
			}
			st.TotalSize = uint64(snap.QuotaBytes)
			st.FreeSize, st.AvailableSize = uint64(free), uint64(free)
		}
	}
	return nil
}

func orSlash(p string) string {
	if strings.Trim(p, "/") == "" {
		return "/"
	}
	return p
}

// mapErr turns a driver error into what billy and go-nfs expect.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, storage.ErrNotFound), errors.Is(err, os.ErrNotExist):
		return os.ErrNotExist
	case errors.Is(err, storage.ErrReadOnly):
		return os.ErrPermission
	case errors.Is(err, storage.ErrUnsupported):
		return billy.ErrNotSupported
	default:
		return err
	}
}
