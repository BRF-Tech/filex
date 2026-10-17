package storage

import (
	"errors"
	"syscall"
)

// ErrNameTooLong is a driver's "this name is longer than I can hold", for a
// driver whose backend does not answer with the operating system's errno (an
// object store's KeyTooLongError, a remote server's refusal). A local disk
// answers syscall.ENAMETOOLONG and need not wrap it.
var ErrNameTooLong = errors.New("storage: name too long")

// IsNameTooLong reports a write or a rename the storage refused because a
// name, or the whole path, is longer than it holds: 255 bytes of UTF-8 in one
// name on ext4, XFS and APFS (the hostile names' bytes-256 and
// chars-under-bytes-over, task #238), 1024 bytes in an S3 key.
//
// ⚠ Every door answers it as the CLIENT's problem - "name too long", a 4xx,
// the protocol's own code where it has one - and never with the driver's
// text: that text is `open /srv/<storage root>/...: file name too long`, the
// server's own path. Before 0.57 the explorer answered it 500, S3 500 with
// the path in the message, FTPS 550 with the path in the reply.
//
// filex does not refuse a long name up front: an object store holds names a
// local disk refuses, and which limit to impose everywhere is a decision of
// its own (docs/FILE-NAMES.md).
func IsNameTooLong(err error) bool {
	return err != nil && (errors.Is(err, ErrNameTooLong) || errors.Is(err, syscall.ENAMETOOLONG))
}
