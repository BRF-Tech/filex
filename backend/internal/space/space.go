// Package space answers "how much room is there?" for every surface a client
// asks it through: SFTP's statvfs@openssh.com, NFS FSSTAT, FTP AVBL, WebDAV's
// RFC 4331 quota properties, and `filex mount` (the manager API's
// action=space).
//
// # Why one answer
//
// Each of those used to make up its own number: the account's quota when it
// had one, a round figure when it did not, and the figure was different on
// every surface (SFTP and the Linux mount said 1 PiB, NFS 32 TiB, FTP 1 TiB,
// the Windows mount 4 TiB half full, /dav nothing). A local storage on a RAID
// volume showed up in Windows Explorer as a 1 PB drive with nothing used
// (issue #116).
//
// # What the answer is
//
//   - A path inside a storage: what the storage itself reports for it
//     (storage.SpaceOf: the disk, the share, the server), capped by the
//     account's quota when it has one.
//   - The virtual root, the folder that lists the storages: the storages the
//     caller can see, added up, a disk two of them share counted once. A client
//     that maps the whole server as a drive asks about the root.
//   - Nothing measurable (an object store, a driver that cannot say, a backend
//     that did not answer) and no quota: Known is false and the surface answers
//     with OrStandIn, large and finite, because zero makes WinSCP and Explorer
//     report a full disk and refuse to copy.
package space

// Figures is the room at one location, in bytes.
type Figures struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	// Free is what the caller could still write.
	Free uint64 `json:"free"`
	// Known is false when nothing could be measured and no quota applies.
	Known bool `json:"known"`
}

// standIn is what a location nothing could measure reports: 1 PiB.
const standIn = uint64(1) << 50

// OrStandIn returns f, or, when f is unknown, a large disk with nothing used.
func (f Figures) OrStandIn() Figures {
	if f.Known {
		return f
	}
	return Figures{Total: standIn, Free: standIn}
}

// Blocks counts f in blocks of bs bytes the way statvfs and statfs do: blocks
// is the size, bfree what is not in use (df's Used column is blocks-bfree) and
// bavail what can still be written.
func (f Figures) Blocks(bs uint64) (blocks, bfree, bavail uint64) {
	blocks = f.Total / bs
	if f.Used < f.Total {
		bfree = (f.Total - f.Used) / bs
	}
	bavail = f.Free / bs
	// statvfs promises bavail <= bfree; a quota-capped answer can break it.
	bfree = max(bfree, bavail)
	return blocks, bfree, bavail
}
