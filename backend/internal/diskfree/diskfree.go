// Package diskfree measures the filesystem holding a directory: how big it is,
// how much of it is in use and how much this process could still write.
//
// It exists because several subsystems need the same numbers and none of them
// should own them: the staged-upload guard (internal/staging), which refuses
// an upload the disk cannot hold; the search index rebuild (internal/search),
// which builds a second copy of the index alongside the live one and must
// refuse loudly rather than fill the disk; and the local storage driver, whose
// answer is what `df` shows over SFTP, NFS, /dav and `filex mount`
// (internal/space).
//
// A probe that cannot answer returns an error and callers treat that as
// "cannot measure", never as "no space" — a guard that blocks the work
// because it could not read a number is worse than no guard.
package diskfree

// Usage is one filesystem, in bytes.
type Usage struct {
	// Total is the size of the filesystem.
	Total uint64
	// Used is what is in use, counted the way df counts it: Total minus every
	// free block, the ones kept back for root included.
	Used uint64
	// Free is what this process could still write. On a filesystem that keeps
	// blocks back for root it is less than Total-Used.
	Free uint64
	// Volume names the filesystem, "" when the platform cannot tell. Two
	// directories with the same Volume are on one disk, so their space is one
	// space, not two.
	Volume string
}

// Measure returns the filesystem holding dir.
func Measure(dir string) (Usage, error) { return measure(dir) }

// Free returns the space available to this process on the filesystem
// holding dir.
func Free(dir string) (uint64, error) {
	u, err := measure(dir)
	return u.Free, err
}
