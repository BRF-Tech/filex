// Package diskfree probes the free space on the filesystem holding a
// directory.
//
// It exists because two subsystems now need the same number and neither
// should own it: the staged-upload guard (internal/staging), which refuses
// an upload the disk cannot hold, and the search index rebuild
// (internal/search), which builds a second copy of the index alongside the
// live one and must refuse loudly rather than fill the disk.
//
// A probe that cannot answer returns an error and callers treat that as
// "cannot measure", never as "no space" — a guard that blocks the work
// because it could not read a number is worse than no guard.
package diskfree

// Free returns the space available to this process on the filesystem
// holding dir.
func Free(dir string) (uint64, error) { return freeBytes(dir) }
