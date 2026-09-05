package writehook

import "context"

// OverwriteGuardFunc runs BEFORE a destructive write and may refuse it.
//
// The mirror image of OnFileWritten: that hook fans out the side effects of a
// write that already happened; this one is the last moment at which the bytes
// about to be replaced still exist. `rel` is the storage-relative path of the
// file that is about to be written, in whatever spelling the caller has —
// leading slash optional, since pathkey.Hash cleans the path it is given.
//
// Returning an error MUST abort the write. Versioning is the only guard today,
// and losing history is not a reason to lose the file: if the snapshot cannot
// be taken, the surface answers 503 rather than overwriting unrecoverably.
type OverwriteGuardFunc func(ctx context.Context, storageID int64, rel string) error

// overwriteGuard stays nil until ConfigureOverwriteGuard wires it at startup,
// so an unwired deployment (and every existing test) behaves exactly as before.
// Package-level, like the Configure/OnFileWritten pair above it: this package
// is already the process-wide seam every write surface reaches for without
// threading a dependency through half the handler tree.
var overwriteGuard OverwriteGuardFunc

// ConfigureOverwriteGuard installs the process-wide pre-write guard. Call once
// at boot, after the versioning service exists. nil disables it.
func ConfigureOverwriteGuard(g OverwriteGuardFunc) { overwriteGuard = g }

// BeforeOverwrite runs the configured guard. Safe to call unconditionally from
// any write surface; a nil guard is a no-op returning nil.
func BeforeOverwrite(ctx context.Context, storageID int64, rel string) error {
	if overwriteGuard == nil {
		return nil
	}
	return overwriteGuard(ctx, storageID, rel)
}
