package sftpsrv

import (
	"io"
	"os"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// What an SFTP client is told about an entry.
//
// # Permission bits filex does not have
//
// `storage.Object` carries name, size, kind, mime, etag and mtime — and no
// permission bits, because most backends have none to give. SFTP clients, on
// the other hand, DRAW those bits: WinSCP greys out the delete button, `sftp`
// prints them in `ls -l`, and a file listed as read-only that then accepts a
// write is a worse experience than either answer alone.
//
// So they are SYNTHESISED, from the one thing filex does know: the caller's
// effective ACL level on that path. Viewer reads, editor writes. That makes the
// bits a true statement about what this session may do, rather than a guess
// about a filesystem that may not exist.
//
// ⚠ Never S_IFLNK. filex has no symlinks, Readlink is refused, and a client
// that saw a link mode would ask to follow something that cannot be resolved.

const (
	modeDirRead  = os.ModeDir | 0o500 // r-x
	modeDirWrite = os.ModeDir | 0o700 // rwx
	modeFileRead = os.FileMode(0o400)
	modeFileRW   = os.FileMode(0o600)
)

// info is the os.FileInfo an SFTP client receives.
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

// Uid and Gid satisfy sftp.FileInfoUidGid, so a listing does not have to invent
// numbers from the host filex happens to run on. Zero is the honest answer:
// filex has accounts, not POSIX users, and the mapping does not exist.
func (i info) Uid() uint32 { return 0 }
func (i info) Gid() uint32 { return 0 }

// rootInfo describes `/`.
func rootInfo() os.FileInfo {
	return info{name: "/", mode: modeDirRead, mod: time.Now()}
}

// storageInfo describes a storage as a directory.
func storageInfo(st *model.Storage, set *acl.Set) os.FileInfo {
	mode := modeDirRead
	if !st.ReadOnly && set != nil && set.Effective("") >= acl.LevelEditor {
		mode = modeDirWrite
	}
	return info{name: st.Name, mode: mode, mod: st.CreatedAt}
}

// objectInfo describes one entry, with the bits its ACL level justifies.
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

// listerAt adapts a slice of entries to the interface the library reads pages
// from.
type listerAtSlice []os.FileInfo

func listerAt(entries []os.FileInfo) listerAtSlice { return listerAtSlice(entries) }

func (l listerAtSlice) ListAt(out []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(out, l[off:])
	if n < len(out) {
		// Fewer copied than asked for means the end — the client stops here
		// rather than asking again for a page that will be empty.
		return n, io.EOF
	}
	return n, nil
}
