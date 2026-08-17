package trash

// Put is the single place bytes are moved into `.filex-trash/`.
//
// Every deletion surface (web UI, WebDAV, AI/REST, MCP, the async ops worker,
// and any protocol added later) calls this instead of minting a trash key and
// driving the driver itself. Before it existed, four surfaces each carried
// their own copy of "mint key → Move → walk the prefix when the object store
// has no folder object", and they had already drifted: one of them permanently
// deleted a folder's contents and still reported it as trashed.
//
// Put NEVER destroys data. When it cannot trash (a driver with neither Mover
// nor Copier) it reports ErrUnsupported and touches nothing, leaving the caller
// to decide — and to fire the hook that matches what actually happened.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// ErrUnsupported is returned when the driver can neither Move nor Copy, so
// there is no way to preserve the bytes. Callers that choose to hard-delete
// anyway MUST fire writehook.OnFileDeleted, not OnFileTrashed.
var ErrUnsupported = errors.New("trash: driver supports neither Move nor Copy")

// maxWalkEntries caps the per-folder walk so a pathological tree cannot pin
// the request goroutine. Mirrors the cap the DAV walk already used.
const maxWalkEntries = 50000

// maxWalkDepth caps recursion for the same reason.
const maxWalkDepth = 64

// Outcome describes what Put did to the bytes.
type Outcome struct {
	// Key is the storage-relative `.filex-trash/...` location the bytes now
	// live under. Empty unless Trashed is true.
	Key string
	// Trashed is true when the bytes are parked in the trash and restorable.
	// It is the caller's signal to fire OnFileTrashed (and only then).
	Trashed bool
	// Missing is true when there was nothing at `rel` to trash — a stale
	// index row or an out-of-band delete. No bytes were touched and no
	// content was lost, so this is not an error.
	Missing bool
	// Files is the number of objects moved. 1 for a plain file; for a folder
	// on an object store it is the number of contained objects.
	Files int
}

// Put moves whatever lives at `rel` into a fresh trash key on drv.
//
// It handles the three shapes a deletion takes in practice:
//
//   - a plain file, or a folder the driver can rename in one call (local FS,
//     SFTP, FTP, WebDAV): a single Move;
//   - a folder on an object store, where no object exists at the prefix and a
//     Move of it 404s: every contained object is moved individually, keeping
//     the sub-structure under the trash key so Restore rebuilds the tree;
//   - a driver with no Mover at all: the same two shapes, done as Copy then
//     Delete, so the bytes still survive.
//
// Folder-marker objects (`<prefix>` / `<prefix>/`, which filex's Mkdir writes
// on S3) are cleaned up best-effort once the contents are safe.
func Put(ctx context.Context, drv storage.Driver, rel string) (Outcome, error) {
	rel = strings.TrimRight(rel, "/")
	if rel == "" {
		return Outcome{}, errors.New("trash: empty path")
	}
	if IsTrashPath(rel) {
		// Already in the trash — trashing it again would bury the original
		// path and make Restore point at a key inside the trash.
		return Outcome{}, errors.New("trash: path is already in the trash")
	}
	base := path.Base(rel)
	if base == "" || base == "." || base == "/" {
		return Outcome{}, fmt.Errorf("trash: bad base for %q", rel)
	}
	key := NewKey(base)

	mover, hasMover := drv.(storage.Mover)
	copier, hasCopier := drv.(storage.Copier)
	deleter, hasDeleter := drv.(storage.Deleter)
	if !hasMover && !(hasCopier && hasDeleter) {
		return Outcome{}, ErrUnsupported
	}

	// Single-call rename first — the cheap path, and the only one that is
	// atomic for a whole folder on a real filesystem.
	if hasMover {
		err := mover.Move(ctx, rel, key)
		if err == nil {
			return Outcome{Key: key, Trashed: true, Files: 1}, nil
		}
		if !errors.Is(err, storage.ErrNotFound) {
			// Could still be an object-store folder: no object sits at the
			// prefix, so the rename fails while the contents are perfectly
			// real. Fall through to the per-object walk and only surface
			// this error if there is nothing underneath either.
			out, werr := putChildren(ctx, drv, rel, key, mover, copier, deleter)
			if werr != nil {
				return Outcome{}, werr
			}
			if out.Files > 0 {
				cleanupMarkers(ctx, deleter, hasDeleter, rel)
				return out, nil
			}
			return Outcome{}, err
		}
		// ErrNotFound: either an empty folder marker or genuinely gone.
		out, werr := putChildren(ctx, drv, rel, key, mover, copier, deleter)
		if werr != nil {
			return Outcome{}, werr
		}
		if out.Files > 0 {
			cleanupMarkers(ctx, deleter, hasDeleter, rel)
			return out, nil
		}
		cleanupMarkers(ctx, deleter, hasDeleter, rel)
		return Outcome{Missing: true}, nil
	}

	// No Mover. Copy+Delete keeps the bytes instead of destroying them.
	if err := copyThenDelete(ctx, copier, deleter, rel, key); err == nil {
		return Outcome{Key: key, Trashed: true, Files: 1}, nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		out, werr := putChildren(ctx, drv, rel, key, nil, copier, deleter)
		if werr != nil {
			return Outcome{}, werr
		}
		if out.Files > 0 {
			cleanupMarkers(ctx, deleter, hasDeleter, rel)
			return out, nil
		}
		return Outcome{}, err
	}
	out, werr := putChildren(ctx, drv, rel, key, nil, copier, deleter)
	if werr != nil {
		return Outcome{}, werr
	}
	if out.Files > 0 {
		cleanupMarkers(ctx, deleter, hasDeleter, rel)
		return out, nil
	}
	cleanupMarkers(ctx, deleter, hasDeleter, rel)
	return Outcome{Missing: true}, nil
}

// putChildren moves every object under `rel` into `key`, preserving the
// relative sub-path so a folder restores with its tree intact.
func putChildren(ctx context.Context, drv storage.Driver, rel, key string,
	mover storage.Mover, copier storage.Copier, deleter storage.Deleter) (Outcome, error) {
	files, err := walkFiles(ctx, drv, rel, true)
	if err != nil {
		return Outcome{}, err
	}
	if len(files) == 0 {
		return Outcome{}, nil
	}
	prefix := strings.TrimRight(rel, "/") + "/"
	moved := 0
	for _, fp := range files {
		dst := key + "/" + strings.TrimPrefix(fp, prefix)
		if mover != nil {
			if err := mover.Move(ctx, fp, dst); err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					continue
				}
				return Outcome{}, fmt.Errorf("trash %q: %w", fp, err)
			}
		} else {
			if err := copyThenDelete(ctx, copier, deleter, fp, dst); err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					continue
				}
				return Outcome{}, fmt.Errorf("trash %q: %w", fp, err)
			}
		}
		moved++
	}
	if moved == 0 {
		return Outcome{}, nil
	}
	return Outcome{Key: key, Trashed: true, Files: moved}, nil
}

// TakeBack is the inverse of Put: it returns the bytes parked under a trash
// key to their original path. Restore uses it so the two directions stay
// symmetric — a driver that could only trash via Copy+Delete must be able to
// come back the same way, or trashing would be a one-way trip on that backend.
//
// Like Put it handles both a single rename and the per-object walk an object
// store needs, and it never deletes anything it has not first copied.
func TakeBack(ctx context.Context, drv storage.Driver, trashKey, origPath string) error {
	trashKey = strings.TrimRight(trashKey, "/")
	origPath = strings.TrimRight(origPath, "/")
	if trashKey == "" || origPath == "" {
		return errors.New("trash: empty path")
	}
	err := relocate(ctx, drv, trashKey, origPath)
	if err == nil {
		return nil
	}
	// Either genuinely missing, or a folder that only exists as its contents.
	files, werr := walkFiles(ctx, drv, trashKey, false)
	if werr != nil || len(files) == 0 {
		return err
	}
	prefix := trashKey + "/"
	for _, fp := range files {
		dst := origPath + "/" + strings.TrimPrefix(fp, prefix)
		if rerr := relocate(ctx, drv, fp, dst); rerr != nil && !errors.Is(rerr, storage.ErrNotFound) {
			return fmt.Errorf("restore %q: %w", fp, rerr)
		}
	}
	if deleter, ok := drv.(storage.Deleter); ok {
		_ = deleter.Delete(ctx, trashKey)
		_ = deleter.Delete(ctx, trashKey+"/")
	}
	return nil
}

// relocate moves a single object, preferring a native rename and falling back
// to Copy+Delete for drivers without Mover.
func relocate(ctx context.Context, drv storage.Driver, src, dst string) error {
	if mv, ok := drv.(storage.Mover); ok {
		return mv.Move(ctx, src, dst)
	}
	copier, _ := drv.(storage.Copier)
	deleter, _ := drv.(storage.Deleter)
	return copyThenDelete(ctx, copier, deleter, src, dst)
}

// copyThenDelete is the no-Mover equivalent of a rename. The delete only runs
// once the copy has succeeded, so a failure leaves the original in place.
func copyThenDelete(ctx context.Context, copier storage.Copier, deleter storage.Deleter, src, dst string) error {
	if copier == nil || deleter == nil {
		return ErrUnsupported
	}
	if err := copier.Copy(ctx, src, dst); err != nil {
		return err
	}
	if err := deleter.Delete(ctx, src); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	return nil
}

// cleanupMarkers drops the empty folder-marker objects an object store keeps
// at a prefix. Best-effort: the contents are already safe by this point.
func cleanupMarkers(ctx context.Context, deleter storage.Deleter, ok bool, rel string) {
	if !ok || deleter == nil {
		return
	}
	_ = deleter.Delete(ctx, rel)
	_ = deleter.Delete(ctx, strings.TrimRight(rel, "/")+"/")
}

// walkFiles returns every FILE object under root, recursively.
//
// skipTrash controls whether filex's own trash bucket is stepped over. Put
// passes true so a delete can never drag the trash into the trash; TakeBack
// passes false because it is deliberately walking INSIDE the trash — with the
// skip unconditional, restoring a folder found nothing and silently gave up.
func walkFiles(ctx context.Context, drv storage.Driver, root string, skipTrash bool) ([]string, error) {
	var out []string
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if depth > maxWalkDepth {
			return errors.New("trash: directory tree too deep")
		}
		objs, err := drv.List(ctx, dir)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return nil
			}
			return err
		}
		for _, o := range objs {
			p := strings.Trim(o.Path, "/")
			if p == "" {
				p = path.Join(dir, o.Name)
			}
			if skipTrash && IsTrashPath(p) {
				continue
			}
			if o.Kind == storage.KindDirectory {
				if err := walk(p, depth+1); err != nil {
					return err
				}
				continue
			}
			if len(out) >= maxWalkEntries {
				return fmt.Errorf("trash: more than %d entries under %q", maxWalkEntries, root)
			}
			out = append(out, p)
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// IsTrashPath reports whether rel is the trash bucket or lives inside it.
func IsTrashPath(rel string) bool {
	clean := strings.Trim(rel, "/")
	return clean == Prefix || strings.HasPrefix(clean, Prefix+"/")
}
