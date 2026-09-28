package versioning

import "context"

// wiring:e2 convert — a write whose replaced bytes must NOT be kept.
//
// Encrypting a folder that already exists, in place, writes each file's
// ciphertext over its plaintext. The pre-write guard would keep the plaintext
// as a version — the very copy the conversion exists to remove — so a
// conversion write carries this mark and GuardOverwrite takes no snapshot.
// Only the handlers set it, and only for a write they have checked is one
// (api/handlers/e2e_convert.go: inside an encrypted folder whose key file says
// a conversion is under way, plaintext replaced by ciphertext).
type skipKey struct{}

// WithoutSnapshot marks ctx: the overwrite it guards keeps no version.
func WithoutSnapshot(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipKey{}, true)
}

// SnapshotSkipped reports whether ctx carries WithoutSnapshot.
func SnapshotSkipped(ctx context.Context) bool {
	v, _ := ctx.Value(skipKey{}).(bool)
	return v
}
