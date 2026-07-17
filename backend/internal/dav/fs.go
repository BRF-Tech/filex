package dav

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"

	"golang.org/x/net/webdav"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// hiddenNames are filex-internal buckets never exposed over WebDAV.
var hiddenNames = map[string]bool{
	".filex-trash": true,
	".versions":    true,
	".thumbs":      true,
}

// hiddenPath reports whether any segment of rel is an internal bucket.
func hiddenPath(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if hiddenNames[seg] {
			return true
		}
	}
	return false
}

// davFS is the composite webdav.FileSystem: the first path segment picks a
// storage, the rest is storage-relative. One instance lives per request and
// carries the authenticated principal; ACL sets are cached per storage.
type davFS struct {
	h *Handler
	p *principal

	sets map[int64]*acl.Set
}

func newFS(h *Handler, p *principal) *davFS {
	return &davFS{h: h, p: p, sets: map[int64]*acl.Set{}}
}

// split normalizes a webdav path into (storage-name, rel). Empty name means
// the /dav virtual root.
func (f *davFS) split(name string) (string, string) {
	rest := strings.Trim(path.Clean("/"+name), "/")
	if rest == "" || rest == "." {
		return "", ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i], acl.CleanRel(rest[i+1:])
	}
	return rest, ""
}

// storageByName resolves an enabled storage the caller may see, plus its ACL
// set. Invisible/unknown storages yield os.ErrNotExist (no exists-oracle).
func (f *davFS) storageByName(ctx context.Context, name string) (*model.Storage, *acl.Set, error) {
	st, err := f.h.cfg.Store.GetStorageByName(ctx, name)
	if err != nil || st == nil || !st.Enabled {
		return nil, nil, os.ErrNotExist
	}
	set, err := f.aclSet(ctx, st)
	if err != nil {
		return nil, nil, err
	}
	if !set.StorageVisible() {
		return nil, nil, os.ErrNotExist
	}
	return st, set, nil
}

func (f *davFS) aclSet(ctx context.Context, st *model.Storage) (*acl.Set, error) {
	if set, ok := f.sets[st.ID]; ok {
		return set, nil
	}
	set, err := f.h.cfg.ACL.LoadSet(ctx, f.p.user, st)
	if err != nil {
		return nil, err
	}
	f.sets[st.ID] = set
	return set, nil
}

// canWrite is the FileSystem-level twin of Handler.gateWrite (defense in
// depth: even if a request slips past the pre-gate, mutations are refused
// here with os.ErrPermission).
func (f *davFS) canWrite(ctx context.Context, st *model.Storage, set *acl.Set, rel string) error {
	if st.ReadOnly {
		return os.ErrPermission
	}
	if set.Effective(rel) < acl.LevelEditor {
		return os.ErrPermission
	}
	return nil
}

// mapErr converts storage driver errors into fs-flavored ones the webdav
// library understands.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, storage.ErrNotFound):
		return os.ErrNotExist
	case errors.Is(err, storage.ErrReadOnly):
		return os.ErrPermission
	case errors.Is(err, storage.ErrUnsupported):
		return os.ErrPermission
	default:
		return err
	}
}

// ───────────────────────── webdav.FileSystem ──────────────────────────────

func (f *davFS) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	sname, rel := f.split(name)
	if sname == "" || hiddenPath(rel) {
		return os.ErrPermission
	}
	if rel == "" {
		return os.ErrExist // the storage root always exists
	}
	st, set, err := f.storageByName(ctx, sname)
	if err != nil {
		return err
	}
	if err := f.canWrite(ctx, st, set, rel); err != nil {
		return err
	}
	drv, err := f.h.cfg.Resolver(st.ID)
	if err != nil {
		return err
	}
	mk, ok := drv.(storage.Mkdirer)
	if !ok {
		return storage.ErrUnsupported
	}
	// RFC 4918: MKCOL with a missing intermediate collection is 409 — the
	// webdav handler derives that from os.ErrNotExist.
	if parent := parentOf(rel); parent != "" {
		if obj, err := drv.Stat(ctx, parent); err != nil {
			return os.ErrNotExist
		} else if obj.Kind != storage.KindDirectory {
			return os.ErrNotExist
		}
	}
	if obj, err := drv.Stat(ctx, rel); err == nil && obj.Path != "" {
		return os.ErrExist
	}
	if err := mk.Mkdir(ctx, rel); err != nil {
		return mapErr(err)
	}
	f.h.syncMkdir(ctx, st, rel)
	return nil
}

func (f *davFS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	sname, rel := f.split(name)
	wantsWrite := flag&(os.O_WRONLY|os.O_RDWR) != 0

	// Virtual root: list the visible storages.
	if sname == "" {
		if wantsWrite {
			return nil, os.ErrPermission
		}
		return f.rootDir(ctx)
	}
	if hiddenPath(rel) {
		return nil, os.ErrNotExist
	}
	st, set, err := f.storageByName(ctx, sname)
	if err != nil {
		return nil, err
	}
	drv, err := f.h.cfg.Resolver(st.ID)
	if err != nil {
		return nil, err
	}

	if wantsWrite {
		if rel == "" {
			return nil, os.ErrPermission
		}
		if err := f.canWrite(ctx, st, set, rel); err != nil {
			return nil, err
		}
		if _, ok := drv.(storage.Writer); !ok {
			return nil, storage.ErrUnsupported
		}
		// PUT is O_RDWR|O_CREATE|O_TRUNC, the LOCK-create path is
		// O_RDWR|O_CREATE — either way the upload spools to a temp file and
		// lands on the driver at Close (drivers write whole objects).
		if flag&os.O_CREATE == 0 {
			if _, err := drv.Stat(ctx, rel); err != nil {
				return nil, mapErr(err)
			}
		}
		if parent := parentOf(rel); parent != "" {
			if obj, err := drv.Stat(ctx, parent); err != nil || obj.Kind != storage.KindDirectory {
				return nil, os.ErrNotExist // → 409 Conflict on PUT
			}
		}
		return newWriteFile(ctx, f.h, st, drv, rel)
	}

	// Read side.
	if rel == "" {
		return f.storageDir(ctx, st, set, drv, rel)
	}
	if !set.CanSee(rel) {
		return nil, os.ErrNotExist
	}
	obj, err := drv.Stat(ctx, rel)
	if err != nil {
		return nil, mapErr(err)
	}
	if obj.Kind == storage.KindDirectory {
		return f.storageDir(ctx, st, set, drv, rel)
	}
	return newReadFile(ctx, drv, rel, newFileInfo(obj)), nil
}

func (f *davFS) RemoveAll(ctx context.Context, name string) error {
	sname, rel := f.split(name)
	if sname == "" || rel == "" || hiddenPath(rel) {
		return os.ErrPermission // never delete the root or a whole storage
	}
	st, set, err := f.storageByName(ctx, sname)
	if err != nil {
		return err
	}
	if !set.CanSee(rel) {
		return os.ErrNotExist
	}
	if err := f.canWrite(ctx, st, set, rel); err != nil {
		return err
	}
	drv, err := f.h.cfg.Resolver(st.ID)
	if err != nil {
		return err
	}
	del, ok := drv.(storage.Deleter)
	if !ok {
		return storage.ErrUnsupported
	}
	obj, err := drv.Stat(ctx, rel)
	if err != nil {
		return mapErr(err)
	}
	if obj.Kind == storage.KindDirectory {
		// Per-file walk: object stores have no real "directory" object, a
		// single Delete of the prefix is driver-dependent. Files first, then
		// best-effort marker cleanup.
		files, werr := walkFiles(ctx, drv, rel)
		if werr != nil {
			return mapErr(werr)
		}
		for _, fp := range files {
			if err := del.Delete(ctx, fp); err != nil && !errors.Is(err, storage.ErrNotFound) {
				return mapErr(err)
			}
		}
		_ = del.Delete(ctx, rel)
		_ = del.Delete(ctx, strings.TrimRight(rel, "/")+"/")
	} else {
		if err := del.Delete(ctx, rel); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return mapErr(err)
		}
	}
	f.h.syncDelete(ctx, st, rel)
	return nil
}

func (f *davFS) Rename(ctx context.Context, oldName, newName string) error {
	sSrc, relSrc := f.split(oldName)
	sDst, relDst := f.split(newName)
	if sSrc == "" || relSrc == "" || sDst == "" || relDst == "" {
		return os.ErrPermission
	}
	if hiddenPath(relSrc) || hiddenPath(relDst) {
		return os.ErrPermission
	}
	if sSrc != sDst {
		return os.ErrPermission // cross-storage MOVE unsupported (pre-gated too)
	}
	st, set, err := f.storageByName(ctx, sSrc)
	if err != nil {
		return err
	}
	if !set.CanSee(relSrc) {
		return os.ErrNotExist
	}
	if err := f.canWrite(ctx, st, set, relSrc); err != nil {
		return err
	}
	if err := f.canWrite(ctx, st, set, relDst); err != nil {
		return err
	}
	drv, err := f.h.cfg.Resolver(st.ID)
	if err != nil {
		return err
	}
	mv, ok := drv.(storage.Mover)
	if !ok {
		return storage.ErrUnsupported
	}
	obj, err := drv.Stat(ctx, relSrc)
	if err != nil {
		return mapErr(err)
	}
	if err := mv.Move(ctx, relSrc, relDst); err != nil {
		if obj.Kind != storage.KindDirectory {
			return mapErr(err)
		}
		// Directory Move failed (typical for object stores) — move each
		// file under the prefix instead.
		files, werr := walkFiles(ctx, drv, relSrc)
		if werr != nil {
			return mapErr(err)
		}
		prefix := strings.TrimRight(relSrc, "/") + "/"
		for _, fp := range files {
			if merr := mv.Move(ctx, fp, relDst+"/"+strings.TrimPrefix(fp, prefix)); merr != nil {
				return mapErr(merr)
			}
		}
		if del, ok := drv.(storage.Deleter); ok {
			_ = del.Delete(ctx, relSrc)
			_ = del.Delete(ctx, prefix)
		}
	}
	f.h.syncMove(ctx, st, relSrc, relDst)
	return nil
}

func (f *davFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	sname, rel := f.split(name)
	if sname == "" {
		return syntheticDirInfo("/"), nil
	}
	if hiddenPath(rel) {
		return nil, os.ErrNotExist
	}
	st, set, err := f.storageByName(ctx, sname)
	if err != nil {
		return nil, err
	}
	if rel == "" {
		// Storage root — synthesized: not every driver can Stat "".
		return syntheticDirInfo(st.Name), nil
	}
	if !set.CanSee(rel) {
		return nil, os.ErrNotExist
	}
	drv, err := f.h.cfg.Resolver(st.ID)
	if err != nil {
		return nil, err
	}
	obj, err := drv.Stat(ctx, rel)
	if err != nil {
		return nil, mapErr(err)
	}
	return newFileInfo(obj), nil
}

// ─────────────────────────── directory listings ───────────────────────────

// rootDir lists the storages visible to the caller as collections.
func (f *davFS) rootDir(ctx context.Context) (webdav.File, error) {
	storages, err := f.h.cfg.Store.ListEnabledStorages(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]os.FileInfo, 0, len(storages))
	for _, st := range storages {
		set, err := f.aclSet(ctx, st)
		if err != nil || !set.StorageVisible() {
			continue
		}
		infos = append(infos, syntheticDirInfo(st.Name))
	}
	return newDirFile(syntheticDirInfo("/"), infos), nil
}

// storageDir lists one directory of a storage, filtered by ACL visibility
// and the internal-bucket blocklist.
func (f *davFS) storageDir(ctx context.Context, st *model.Storage, set *acl.Set, drv storage.Driver, rel string) (webdav.File, error) {
	if rel != "" && !set.CanSee(rel) {
		return nil, os.ErrNotExist
	}
	objs, err := drv.List(ctx, rel)
	if err != nil {
		return nil, mapErr(err)
	}
	infos := make([]os.FileInfo, 0, len(objs))
	for _, o := range objs {
		if hiddenNames[o.Name] {
			continue
		}
		childRel := acl.CleanRel(o.Path)
		if childRel == "" {
			childRel = acl.CleanRel(path.Join(rel, o.Name))
		}
		if !set.CanSee(childRel) {
			continue
		}
		infos = append(infos, newFileInfo(o))
	}
	fiName := st.Name
	if rel != "" {
		fiName = path.Base(rel)
	}
	return newDirFile(syntheticDirInfo(fiName), infos), nil
}

// ────────────────────────────── helpers ───────────────────────────────────

// parentOf returns the parent of a clean rel path ("" at storage root).
func parentOf(rel string) string {
	dir := path.Dir(rel)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

// walkFiles collects every FILE path under dir (recursive), bounded to keep
// a runaway listing from pinning the process.
func walkFiles(ctx context.Context, drv storage.Driver, dir string) ([]string, error) {
	const maxEntries = 50000
	var out []string
	var walk func(string, int) error
	walk = func(d string, depth int) error {
		if depth > 64 {
			return errors.New("dav: directory tree too deep")
		}
		objs, err := drv.List(ctx, d)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return nil
			}
			return err
		}
		for _, o := range objs {
			p := o.Path
			if acl.CleanRel(p) == "" {
				p = path.Join(d, o.Name)
			}
			if o.Kind == storage.KindDirectory {
				if err := walk(p, depth+1); err != nil {
					return err
				}
				continue
			}
			out = append(out, acl.CleanRel(p))
			if len(out) > maxEntries {
				return errors.New("dav: too many entries")
			}
		}
		return nil
	}
	if err := walk(dir, 0); err != nil {
		return nil, err
	}
	return out, nil
}
