package ftpsrv

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	ftpserver "github.com/fclairamb/ftpserverlib"
	"github.com/spf13/afero"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// One session's view of the tree, in the shape ftpserverlib asks for.
//
// The path model is the one every other protocol uses: the first segment names
// a STORAGE, the rest is storage-relative. Four protocols disagreeing about
// what a storage is called is how somebody ends up unable to find, over one,
// the folder they made over another.
//
// ⚠ The library's ClientDriver is an `afero.Fs`, which is a filesystem
// interface with more verbs than FTP has. Only the ones an FTP client can
// actually reach are implemented; the rest return ErrUnsupported rather than a
// plausible-looking approximation of a filesystem filex does not have.

// hiddenNames are the filex-internal buckets no protocol exposes.
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
	ctx       context.Context

	mu   sync.Mutex
	sets map[int64]*acl.Set
}

func newFS(srv *Server, p *protocolauth.Principal) *fs {
	return &fs{srv: srv, principal: p, ctx: p.WithContext(context.Background()), sets: map[int64]*acl.Set{}}
}

func (f *fs) Name() string { return "filex" }

// ─────────────────────────── resolution ───────────────────────────

type target struct {
	Storage *model.Storage
	Set     *acl.Set
	Rel     string
}

func (t target) isRoot() bool { return t.Storage == nil }

func splitPath(p string) (string, string) {
	rest := strings.Trim(path.Clean("/"+p), "/")
	if rest == "" || rest == "." {
		return "", ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i], acl.CleanRel(rest[i+1:])
	}
	return rest, ""
}

// resolve applies the tenant boundary, the grants and the confinement.
//
// ⚠ os.ErrNotExist for anything the caller may not see — never a permission
// error. FTP reports 550 for both, but the MESSAGE differs and "permission
// denied" on a path that does not exist confirms that it does. Same rule as
// every other protocol here.
func (f *fs) resolve(p string) (target, error) {
	name, rel := splitPath(p)
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

func (f *fs) aclSet(st *model.Storage) (*acl.Set, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sets[st.ID]; ok {
		return s, nil
	}
	s, err := f.principal.ACL(f.ctx, st)
	if err != nil {
		return nil, err
	}
	f.sets[st.ID] = s
	return s, nil
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
	if t.Storage == nil || t.Storage.ReadOnly {
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

// ─────────────────────────── listing ───────────────────────────

// ReadDir implements ClientDriverExtensionFileList — the listing verb.
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
			out = append(out, storageInfo(st, set))
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
		return nil, mapStorageErr(err)
	}
	out := make([]os.FileInfo, 0, len(objs))
	for _, o := range objs {
		rel := path.Join(t.Rel, o.Name)
		if hiddenPath(rel) {
			continue
		}
		// ⚠ FILTER, never reject: a listing that fails because one entry is out
		// of reach hides the whole directory from somebody who can see the rest.
		if !t.Set.CanSee(rel) {
			continue
		}
		out = append(out, objectInfo(o, t.Set.Effective(rel)))
	}
	return out, nil
}

// Stat answers the size/type questions a client asks before a transfer.
func (f *fs) Stat(name string) (os.FileInfo, error) {
	t, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	if t.isRoot() {
		return rootInfo(), nil
	}
	if !f.canTraverse(t) {
		return nil, os.ErrNotExist
	}
	if t.Rel == "" {
		return storageInfo(t.Storage, t.Set), nil
	}
	drv, err := f.driverFor(t)
	if err != nil {
		return nil, err
	}
	// The shared read door: an object whose bytes are still in the staging area
	// of an upload has to stat as the file it will be, or a client sees a file
	// it cannot download.
	src, err := f.srv.cfg.Body.Resolve(f.ctx, drv, t.Storage.ID, t.Rel, nil)
	if err != nil {
		return nil, mapStorageErr(err)
	}
	obj, err := src.Stat(f.ctx)
	if err != nil {
		return nil, mapStorageErr(err)
	}
	obj.Name = path.Base(t.Rel)
	return objectInfo(obj, t.Set.Effective(t.Rel)), nil
}

// ─────────────────────────── mutation ───────────────────────────

func (f *fs) Mkdir(name string, _ os.FileMode) error {
	t, err := f.resolve(name)
	if err != nil {
		return err
	}
	if t.isRoot() || t.Rel == "" {
		// Creating a storage needs a driver, a path and credentials; MKD
		// cannot express any of that.
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
		return storage.ErrUnsupported
	}
	if err := mk.Mkdir(f.ctx, t.Rel); err != nil {
		return mapStorageErr(err)
	}
	f.srv.syncer.Mkdir(f.ctx, t.Storage, t.Rel)
	return nil
}

// MkdirAll is afero's recursive form. FTP has no such command, but the library
// asks for the interface; making it the same thing keeps the two honest.
func (f *fs) MkdirAll(name string, perm os.FileMode) error { return f.Mkdir(name, perm) }

// Remove deletes a file — to the TRASH, like every other surface.
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
			return storage.ErrUnsupported
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

// RemoveDir implements ClientDriverExtensionRemoveDir, so RMD and DELE are
// told apart. They do the same thing here (both trash), but a client that
// sends RMD on a file expects an error rather than a deletion.
func (f *fs) RemoveDir(name string) error { return f.Remove(name) }

// RemoveAll is afero's recursive form; the trash already takes the subtree.
func (f *fs) RemoveAll(name string) error { return f.Remove(name) }

func (f *fs) Rename(oldname, newname string) error {
	src, err := f.resolve(oldname)
	if err != nil {
		return err
	}
	dst, err := f.resolve(newname)
	if err != nil {
		return err
	}
	if src.isRoot() || dst.isRoot() || src.Rel == "" || dst.Rel == "" {
		return os.ErrPermission
	}
	// ⚠ Both ends: a rename is a delete of one path and a create of another,
	// so checking only the destination would let a caller move a file OUT of a
	// subtree they may not write.
	if !f.canWrite(src) || !f.canWrite(dst) {
		return os.ErrPermission
	}
	if src.Storage.ID != dst.Storage.ID {
		// Across storages this is a copy plus a delete — an async operation in
		// filex. Refusing is honest; answering before any bytes moved is not.
		return storage.ErrUnsupported
	}
	drv, err := f.driverFor(src)
	if err != nil {
		return err
	}
	mover, ok := drv.(storage.Mover)
	if !ok {
		return storage.ErrUnsupported
	}
	if err := mover.Move(f.ctx, src.Rel, dst.Rel); err != nil {
		return mapStorageErr(err)
	}
	f.srv.syncer.Move(f.ctx, src.Storage, src.Rel, dst.Rel)
	return nil
}

// Chtimes carries the MFMT command — the one attribute filex can keep, and the
// one a sync tool needs kept or it copies everything again next run.
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
		return storage.ErrUnsupported
	}
	if err := toucher.SetMtime(f.ctx, t.Rel, mtime); err != nil {
		return mapStorageErr(err)
	}
	f.srv.syncer.Touch(f.ctx, t.Storage, t.Rel, mtime)
	return nil
}

// Chmod and Chown have no meaning here.
//
// ⚠ They return nil rather than an error: clients (and `lftp` in particular)
// chmod after an upload out of habit, and a failure there turns a completed
// transfer into a reported error. The permission bits filex reports are
// synthesised from the ACL and were never the client's to set — see fileinfo.go.
func (f *fs) Chmod(string, os.FileMode) error { return nil }
func (f *fs) Chown(string, int, int) error    { return nil }

// ─────────────────────────── transfers ───────────────────────────

// GetHandle implements ClientDriverExtentionFileTransfer: one door for both
// directions, which is where the read and write paths live (see transfer.go).
func (f *fs) GetHandle(name string, flags int, offset int64) (ftpserver.FileTransfer, error) {
	if flags&(os.O_WRONLY|os.O_RDWR) != 0 {
		return f.openWrite(name, flags, offset)
	}
	return f.openRead(name, offset)
}

// GetAvailableSpace answers AVBL from the account's quota, so a client can see
// before a transfer what it would otherwise learn at the end of one.
func (f *fs) GetAvailableSpace(string) (int64, error) {
	if u := userFrom(f.ctx); u != nil && f.srv.cfg.Quota != nil {
		if snap, err := f.srv.cfg.Quota.Get(f.ctx, u.ID); err == nil && !snap.Unlimited && snap.QuotaBytes > 0 {
			free := snap.QuotaBytes - snap.UsedBytes
			if free < 0 {
				free = 0
			}
			return free, nil
		}
	}
	// Unlimited as far as the quota is concerned. ⚠ Zero would make clients
	// report a full disk and refuse to start.
	return 1 << 40, nil
}

// ─────────────────────────── afero leftovers ───────────────────────────

// The library takes an afero.Fs, but with the FileTransfer and FileList
// extensions implemented above it never calls these. They refuse rather than
// approximate: a half-built afero.File would be a second, untested read path
// for the same bytes.

func (f *fs) Create(string) (afero.File, error) { return nil, storage.ErrUnsupported }
func (f *fs) Open(string) (afero.File, error)   { return nil, storage.ErrUnsupported }
func (f *fs) OpenFile(string, int, os.FileMode) (afero.File, error) {
	return nil, storage.ErrUnsupported
}

// mapStorageErr turns a driver error into what the library expects.
func mapStorageErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, storage.ErrNotFound), errors.Is(err, os.ErrNotExist):
		return os.ErrNotExist
	case errors.Is(err, storage.ErrReadOnly):
		return os.ErrPermission
	default:
		return err
	}
}
