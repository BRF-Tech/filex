package storage

import "context"

// Space is the room a storage has at a path, in bytes: the disk, the share or
// the account behind it.
type Space struct {
	// Total is the size of what the path is on.
	Total uint64
	// Used is what is in use on it, by anybody: a disk filex shares with other
	// programs reports their bytes too, because they take the same room.
	Used uint64
	// Free is what filex could still write there.
	Free uint64
	// Volume names the filesystem when the driver can tell, "" when it cannot.
	// Two storages with the same non-empty Volume are one disk, and a total
	// over both counts it once.
	Volume string
}

// SpaceReporter is implemented by a driver that can say how much room its
// backend has: a local disk (statfs), an SMB share, an SFTP server that speaks
// statvfs@openssh.com, a WebDAV server that answers RFC 4331's quota
// properties. An object store has no such number and does not implement it.
//
// path is storage-relative. A path that does not exist yet is answered for
// the folder that would hold it, because the question is asked before an
// upload as often as after.
type SpaceReporter interface {
	Space(ctx context.Context, path string) (Space, error)
}

// SpaceOf asks d how much room path has. ErrUnsupported when the driver cannot
// tell.
//
// A replicated storage answers with its primary: that is where every write
// lands first and what a full disk stops. Unwrapping here, rather than adding
// the interface to ReplicatedDriver.Shaped, keeps that switch at eight types
// instead of sixteen.
func SpaceOf(ctx context.Context, d Driver, path string) (Space, error) {
	if r, ok := AsReplicated(d); ok {
		d = r.Primary()
	}
	sr, ok := d.(SpaceReporter)
	if !ok {
		return Space{}, ErrUnsupported
	}
	return sr.Space(ctx, path)
}
