package nfssrv

import (
	"os"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// What an NFS client is told about an entry.
//
// Same reasoning as SFTP and FTPS: filex has no permission bits to report, NFS
// clients act on them (the kernel refuses a write to a file it was told is
// read-only, before any request reaches this server), and a file shown as
// writable that then refuses a write is worse than either answer.
//
// ⚠ The export's read-only flag folds in here as well as at the gate. Without
// it a read-only export would show writable files that fail on write, which is
// exactly the experience the flag exists to prevent — a client that can see the
// mount is read-only will not even try.

const (
	modeDirRead  = os.ModeDir | 0o555
	modeDirWrite = os.ModeDir | 0o755
	modeFileRead = os.FileMode(0o444)
	modeFileRW   = os.FileMode(0o644)
)

type info struct {
	name string
	size int64
	mode os.FileMode
	mod  time.Time
}

func (i info) Name() string       { return i.name }
func (i info) Size() int64        { return i.size }
func (i info) Mode() os.FileMode  { return i.mode }
func (i info) ModTime() time.Time { return i.mod }
func (i info) IsDir() bool        { return i.mode.IsDir() }
func (i info) Sys() any           { return nil }

func dirInfo(name string, mod time.Time, writable bool) os.FileInfo {
	mode := modeDirRead
	if writable {
		mode = modeDirWrite
	}
	return info{name: name, mode: mode, mod: mod}
}

func storageInfo(st *model.Storage, set *acl.Set, exportReadOnly bool) os.FileInfo {
	writable := !exportReadOnly && !st.ReadOnly &&
		set != nil && set.Effective("") >= acl.LevelEditor
	return dirInfo(st.Name, st.CreatedAt, writable)
}

func objectInfo(o storage.Object, level acl.Level, exportReadOnly bool) os.FileInfo {
	writable := !exportReadOnly && level >= acl.LevelEditor
	if o.Kind == storage.KindDirectory {
		return dirInfo(o.Name, o.Mtime, writable)
	}
	mode := modeFileRead
	if writable {
		mode = modeFileRW
	}
	return info{name: o.Name, size: o.Size, mode: mode, mod: o.Mtime}
}
