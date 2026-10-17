package protocolsync

import (
	"context"
	"errors"
	"syscall"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// CheckRmdir is the question every protocol's RMDIR asks before the folder
// goes to the trash: rel is a folder (syscall.ENOTDIR otherwise) and holds
// nothing but filex's own entries - the keep marker, an internal folder
// (syscall.ENOTEMPTY otherwise). Errors of the storage come back as they are.
//
// ⚠ Why it is asked at all. RMDIR means "remove this EMPTY directory"
// (POSIX rmdir, SFTP's RMDIR, FTP's RMD, NFS's RMDIR): a client that sends it
// on a folder with something in it expects "directory not empty" and nothing
// gone. SFTP, FTPS and NFS answered every RMDIR with the trash taking the
// folder and everything in it - one stray `rmdir` in a script, or a client
// that tries RMDIR before it walks a folder, sent a whole tree to the trash
// (task #238). An empty folder still goes to the trash as before; a file's
// delete is not asked this.
func CheckRmdir(ctx context.Context, drv storage.Driver, rel string) error {
	obj, err := drv.Stat(ctx, rel)
	if err != nil {
		return err
	}
	if obj.Kind != storage.KindDirectory {
		return syscall.ENOTDIR
	}
	objs, err := drv.List(ctx, rel)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if !syspath.IsName(o.Name) {
			return syscall.ENOTEMPTY
		}
	}
	return nil
}

// CheckRemoveFile is the question a protocol's FILE delete asks - SFTP's
// REMOVE, FTP's DELE: rel is not a folder (syscall.EISDIR when it is, empty
// or not). A folder is removed only by RMDIR, and only when it is empty
// (CheckRmdir): REMOVE and DELE on a folder trashed it whole, contents and
// all (the owner's decision for 0.57, task #238). A path that is not there is
// not this question's (nil: the delete says "not there" in its own words); a
// Stat the storage could not answer comes back as it is, so the delete fails
// closed rather than guess that a folder is a file.
func CheckRemoveFile(ctx context.Context, drv storage.Driver, rel string) error {
	obj, err := drv.Stat(ctx, rel)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return nil
	case err != nil:
		return err
	case obj.Kind == storage.KindDirectory:
		return syscall.EISDIR
	}
	return nil
}
