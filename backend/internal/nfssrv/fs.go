package nfssrv

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	billy "github.com/go-git/go-billy/v5"
	nfs "github.com/willscott/go-nfs"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/protoperm"
	"github.com/brf-tech/filex/backend/internal/space"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storageref"
	"github.com/brf-tech/filex/backend/internal/syspath"
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

// ⚠ filex's own directories (trash, version history, thumbnails, the desktop
// app's open-with working area) are judged by syspath.InDir, the one list
// every surface shares. This file used to carry its own three-name copy that
// did not know `.filex-open`, so an NFS mount was shown the desktop's
// working copies beside the person's own files (2026-09-21).

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
//
// ⚠⚠ It is also the one place a NAME the client sent is still a name. go-nfs
// joins the file name of a CREATE, MKDIR, RENAME, REMOVE, RMDIR, LOOKUP, LINK
// or SYMLINK onto the folder's path segments through this method and hands
// the file system only the joined path - and its handlers are package-private
// (nfs.HandleFunc takes an unexported type), so filex cannot put its own in
// front of them. A name holding a `/` would become folders on the way (a
// CREATE of `a/b` made `a`), and `.` or `..` the folder itself or its parent.
// The segments go-nfs passes otherwise are names that already passed here, so
// none of them legitimately is either: such a join answers refusedPath, which
// resolve refuses (errRefusedName, NFS3ERR_ACCES - go-nfs reaches no other
// refusal; a LOOKUP finds nothing). Task #238.
func (f *fs) Join(elem ...string) string {
	for _, e := range elem {
		if e == "." || e == ".." || strings.Contains(e, "/") {
			return refusedPath
		}
	}
	return path.Join(elem...)
}

// refusedPath is what Join answers for a name that is not one: no path.Join
// result starts with two slashes, so nothing else can be it.
const refusedPath = "//refused-name"

// errRefusedName is resolve's answer to refusedPath: a permission error, the
// one refusal every go-nfs handler passes through (NFS3ERR_ACCES).
var errRefusedName = &os.PathError{Op: "name", Path: "", Err: os.ErrPermission}

// Chroot is part of billy.Filesystem and is not something NFS asks for.
// Refusing keeps one path model rather than two.
func (f *fs) Chroot(string) (billy.Filesystem, error) { return nil, billy.ErrNotSupported }

// ─────────────────────────── resolution ───────────────────────────

type target struct {
	Storage *model.Storage
	Set     *acl.Set
	Rel     string
	// Mount is the export's own root: for an export rooted inside a folder,
	// Rel is that folder. It is read and listed like any folder and is never
	// the object of a change - see changeable.
	Mount bool
}

func (t target) isRoot() bool { return t.Storage == nil }

// changeable reports whether t may be created, removed or renamed: not the
// virtual root, not a storage's root, and not the export's own root.
//
// ⚠ The last one is why this exists. go-nfs joins the name a client sends
// onto the folder's path and hands this file system the joined path, so a
// REMOVE, RMDIR or RENAME of `.` or `..` on the mount's root would arrive
// here as the root itself; for an export of `main/projects/acme` that is the
// folder `projects/acme`, which Rel alone cannot tell from a subfolder's.
// Join refuses those names first (refusedPath); this is the second guard,
// for any path that cleans to the mount's root. No client sends it on purpose
// (the Linux kernel never does), and none may: the mount is the folder it was
// given, not a folder in it (task #238).
func (t target) changeable() bool { return !t.isRoot() && t.Rel != "" && !t.Mount }

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
	if p == refusedPath {
		return target{}, errRefusedName
	}
	name, rel := f.split(p)
	if name == "" {
		return target{}, nil
	}
	if syspath.InDir(rel) {
		return target{}, os.ErrNotExist
	}
	st, err := storageref.Resolve(f.ctx, f.srv.cfg.Store, name)
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
	mount := strings.Trim(path.Clean("/"+p), "/") == ""
	return target{Storage: st, Set: set, Rel: rel, Mount: mount}, nil
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

// The gates below ask two things: what the ACCOUNT may do here (the grant
// set) and what the CREDENTIAL may do (Principal.HasScope). An export minted
// from an API token inherits that token, its verbs included: `read` to list and
// read, `write` to create, change and move, `delete` to remove. An export with
// no parent token carries every verb, as before.

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
	return f.principal.HasScope(auth.VerbRead) && t.Set != nil && t.Set.CanSee(t.Rel)
}

// canRead is the door for a file's CONTENT: viewer on the path itself, never
// inherited from being on the way to a grant. canTraverse above is what lets a
// caller walk to their folder; this is what lets them open what is in it.
//
// Reading a file's bytes over this protocol IS a download, so it also needs
// files.download (internal/perm): there is no preview here to leave open.
func (f *fs) canRead(t target) bool {
	return f.principal.HasScope(auth.VerbRead) && protoperm.CanRead(t.Set, t.Rel)
}

// canDo is the door for every change: the export and storage are writable,
// no app lock refuses the path, and the caller may take action p there — the
// level it needs (≥editor for every mutation) AND the per-user permission.
func (f *fs) canDo(t target, p perm.Perm) bool {
	// ⚠ The export's own read-only flag comes FIRST and cannot be argued with:
	// an operator who exported a folder read-only to a media player has said
	// something about the mount, not about the account.
	return !f.readOnly && f.principal.HasScope(protoperm.VerbFor(p)) && protoperm.CanDo(f.ctx, t.Set, t.Storage, f.srv.cfg.ACL, t.Rel, p)
}

// writeNeed is what writing t's bytes is: files.modify when something is
// there to replace, files.create when not.
func (f *fs) writeNeed(t target) perm.Perm {
	drv, err := f.driverFor(t)
	return protoperm.WriteNeed(f.ctx, drv, err, t.Rel)
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
		if syspath.InDir(rel) {
			continue
		}
		// ⚠ Filter, never reject — one unreachable entry must not hide the
		// whole directory.
		if !t.Set.CanSee(rel) {
			continue
		}
		if !listable(o.Name) {
			continue
		}
		out = append(out, objectInfo(o, t.Set.Effective(rel), f.readOnly))
	}
	return out, nil
}

// listable reports whether a name can stand in an NFSv3 directory listing.
//
// ⚠ NFSv3 names are at most 255 bytes (go-nfs refuses a longer one with
// NFS3ERR_NAMETOOLONG on every verb that takes one), and the Linux client
// rejects a READDIR reply holding a longer name as a whole: one long name on
// an object store - which holds names a local disk refuses - would make the
// whole folder unreadable. A NUL ends the name for every C client. Such an
// entry is left out, like one the caller may not see; it cannot be opened
// over NFS anyway, and the other surfaces still show it (task #238).
func listable(name string) bool {
	return len(name) <= nfs.PathNameMax && strings.IndexByte(name, 0) < 0
}

// refuseName asks the one rule (internal/leafname) of the name rel would give
// an entry, before anything is written: the list's refused names - a
// backslash, a NUL, a control character, `...` - are refused on every door,
// so a file never exists on one surface that the next cannot open (task
// #238). A file that already has such a name is not asked: it stays
// readable, writable and removable. (A `/`, `.` or `..` never gets this far:
// Join refuses it.)
//
// ⚠ A permission error, NFS3ERR_ACCES: go-nfs answers every failed CREATE
// and MKDIR with ACCES whatever the error, and a RENAME's only other answer
// is NFS3ERR_IO, which tells a client to retry; NFS3ERR_INVAL cannot be
// reached through a billy file system.
func refuseName(rel string) error {
	if err := leafname.Check(path.Base(rel)); err != nil {
		slog.Debug("nfs: name refused", slog.String("reason", leafname.Reason(err)))
		return &os.PathError{Op: "name", Path: leafname.Reason(err), Err: os.ErrPermission}
	}
	return nil
}

// ─────────────────────────── mutation ───────────────────────────

func (f *fs) MkdirAll(name string, _ os.FileMode) error {
	t, err := f.resolve(name)
	if err != nil {
		return err
	}
	if !t.changeable() {
		return os.ErrPermission
	}
	if !f.canDo(t, perm.FilesCreate) {
		return os.ErrPermission
	}
	if err := refuseName(t.Rel); err != nil {
		return err
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
	if !t.changeable() {
		return os.ErrPermission
	}
	if !f.canDo(t, perm.FilesDelete) {
		return os.ErrPermission
	}
	drv, err := f.driverFor(t)
	if err != nil {
		return err
	}
	// A folder goes only when it is empty (protocolsync.CheckRmdir). go-nfs
	// sends REMOVE and RMDIR to this one method, and RMDIR of a folder with
	// something in it is "directory not empty", never the trash taking the
	// tree; a file's REMOVE is not asked. ⚠ The client reads NFS3ERR_IO:
	// go-nfs answers every error of a REMOVE or RMDIR but "not there" and
	// "not allowed" with IO, and NFS3ERR_NOTEMPTY cannot be reached through a
	// billy file system.
	if err := protocolsync.CheckRmdir(f.ctx, drv, t.Rel); errors.Is(err, syscall.ENOTEMPTY) {
		return err
	}
	// The bytes and the rows under the storage's row gate (Syncer.Discard,
	// Purge; issue #201): a storage scan never judges the catalogue between
	// the two.
	out, terr := f.srv.syncer.Discard(f.ctx, t.Storage, drv, t.Rel)
	switch {
	case terr == nil && out.Trashed:
		return nil
	case terr == nil && out.Missing:
		return os.ErrNotExist
	case errors.Is(terr, trash.ErrUnsupported):
		// No trash on this storage: the delete is for good — files.purge.
		if !f.canDo(t, perm.FilesPurge) {
			return os.ErrPermission
		}
		del, ok := drv.(storage.Deleter)
		if !ok {
			return billy.ErrNotSupported
		}
		if err := f.srv.syncer.Purge(f.ctx, t.Storage, t.Rel, func(ctx context.Context) error {
			if derr := del.Delete(ctx, t.Rel); derr != nil && !errors.Is(derr, storage.ErrNotFound) {
				return derr
			}
			return nil
		}); err != nil {
			return mapErr(err)
		}
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
	if !src.changeable() || !dst.changeable() {
		return os.ErrPermission
	}
	// ⚠ Both ends: a rename moves a file OUT of one subtree and INTO another.
	// A new name in the same folder is files.rename, another folder
	// files.move, both at once needs both (internal/perm); replacing what is
	// at the destination is also files.modify there.
	for _, p := range perm.RelocateNeeds(src.Storage.ID, src.Rel, dst.Storage.ID, dst.Rel) {
		if !f.canDo(src, p) || !f.canDo(dst, p) {
			return os.ErrPermission
		}
	}
	if f.writeNeed(dst) == perm.FilesModify && !f.canDo(dst, perm.FilesModify) {
		return os.ErrPermission
	}
	// A NEW name is asked of the one rule (internal/leafname); moving an item
	// into another folder under the name it already has gives it nothing new.
	if path.Base(dst.Rel) != path.Base(src.Rel) {
		if err := refuseName(dst.Rel); err != nil {
			return err
		}
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
	// A RENAME puts the source's bytes at the new path: not where they would
	// become downloadable while they are not where they are
	// (protoperm.TreeDownloadLifted - a folder by every file below it).
	_, lifted, lerr := protoperm.TreeDownloadLifted(f.ctx, drv, src.Set, src.Rel, dst.Set, dst.Rel)
	if lerr != nil {
		return mapErr(lerr)
	}
	if lifted {
		return os.ErrPermission
	}
	// Who may encrypt (internal/e2epolicy): a RENAME onto a key file's or a
	// `.fxe`'s name is asked as the CREATE of the file there would be (onto a
	// file it replaces it, onto nothing or a folder it creates one), unless
	// it is free: a folder, a `.fxe` that stays a `.fxe`, a key file that
	// stays its own folder's (protoperm.RenameEncryptionAllowed). A rule that
	// could not be decided is not a refusal: go-nfs answers it NFS3ERR_IO,
	// the refusal NFS3ERR_ACCES.
	switch protoperm.RenameEncryptionAllowed(f.ctx, f.srv.cfg.E2EPolicy, drv, src.Storage, src.Rel, dst.Rel) {
	case protoperm.EncryptionRefused:
		return os.ErrPermission
	case protoperm.EncryptionUndecided:
		return protoperm.ErrEncryptionUndecided
	}
	// The bytes, then the rows, under the storage's row gate
	// (Syncer.Relocate, issue #201), waited for on the server's context; once
	// held, the rename runs to its end (sec055).
	if err := f.srv.syncer.Relocate(f.ctx, src.Storage, src.Rel, dst.Rel, func(ctx context.Context) error {
		return mover.Move(ctx, src.Rel, dst.Rel)
	}); err != nil {
		return mapErr(err)
	}
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
	// Clients set times right after every upload: either half of writing is
	// enough to touch a timestamp.
	if t.isRoot() || (!f.canDo(t, perm.FilesModify) && !f.canDo(t, perm.FilesCreate)) {
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

// fsStat fills in what `df` and a file manager's status bar show.
//
// The figures are internal/space's, the ones every surface gives: an export
// rooted in one storage is that storage's disk, an export of the storage list
// is the storages added up; either capped by the account's quota. Only a
// location nothing could measure gets the stand-in, a large empty disk: zero
// makes clients report a full disk and refuse to copy anything.
//
// The mount's own context carries the account; the call's only its deadline.
func (f *fs) fsStat(_ context.Context, st *nfs.FSStat) error {
	t, err := f.resolve("/")
	if err != nil {
		return err
	}
	var fig space.Figures
	if t.isRoot() {
		fig = f.srv.cfg.Space.Root(f.ctx, f.visibleStorages())
	} else {
		fig = f.srv.cfg.Space.Storage(f.ctx, t.Storage, t.Rel)
	}
	fig = fig.OrStandIn()
	st.TotalSize, st.AvailableSize = fig.Total, fig.Free
	// FSSTAT's "free" counts what root could still write; df's Used column is
	// Total minus it.
	st.FreeSize = max(fig.Total-min(fig.Used, fig.Total), fig.Free)
	st.TotalFiles, st.FreeFiles, st.AvailableFiles = 1<<30, 1<<30, 1<<30
	st.CacheHint = 0
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
