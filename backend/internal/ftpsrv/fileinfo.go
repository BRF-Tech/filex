package ftpsrv

import (
	"os"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// What an FTP client is told about an entry.
//
// Same reasoning as the SFTP surface: `storage.Object` has no permission bits,
// FTP clients DRAW them (FileZilla shows them in a column, `ls -l` prints
// them), and a file shown as writable that then refuses a write is worse than
// either answer alone. So they are synthesised from the caller's effective ACL
// level, which makes them a true statement about what this session may do.

const (
	modeDirRead  = os.ModeDir | 0o500
	modeDirWrite = os.ModeDir | 0o700
	modeFileRead = os.FileMode(0o400)
	modeFileRW   = os.FileMode(0o600)
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

func rootInfo() os.FileInfo {
	return info{name: "/", mode: modeDirRead, mod: time.Now()}
}

func storageInfo(st *model.Storage, set *acl.Set) os.FileInfo {
	mode := modeDirRead
	if !st.ReadOnly && set != nil && set.Effective("") >= acl.LevelEditor {
		mode = modeDirWrite
	}
	return info{name: st.Name, mode: mode, mod: st.CreatedAt}
}

func objectInfo(o storage.Object, level acl.Level) os.FileInfo {
	if o.Kind == storage.KindDirectory {
		mode := modeDirRead
		if level >= acl.LevelEditor {
			mode = modeDirWrite
		}
		return info{name: o.Name, mode: mode, mod: o.Mtime}
	}
	mode := modeFileRead
	if level >= acl.LevelEditor {
		mode = modeFileRW
	}
	return info{name: o.Name, size: o.Size, mode: mode, mod: o.Mtime}
}
