package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// ErrKindConflict is returned when a write target already exists as the OTHER
// kind: a file written onto an existing directory, or a directory created onto
// an existing file.
//
// On a real filesystem the OS refuses this for us — os.Create on a directory
// fails, and the error surfaces. An object store has no such rule: keys are
// flat, so `X` (an object) and `X/y` (an object under a prefix) can both exist
// and the write succeeds silently. Hetzner Object Storage accepts it; MinIO,
// which is directory-backed, cannot represent it.
//
// What that cost us (2026-08-06, brkip DR mirror): the mirror could never
// settle the colliding prefix, so `mc mirror` re-copied it every run — 2760
// syncs in 24h, 1016 versions of a single PNG, a 43 MiB folder occupying 45 GB,
// and the disk at 96%. Worse and quieter: the colliding object made everything
// under that prefix unlistable, so 314 objects had no DR backup at all and
// nothing reported it.
var ErrKindConflict = errors.New("storage: path exists with a different kind")

// EnsureFileTarget refuses to let a FILE be written at p when p already exists
// as a directory. Call it before every user-facing write.
//
// This check only became expressible in v0.9.0: before it, the S3 driver's
// Stat answered ErrNotFound for a bare prefix, so "is this a folder?" had no
// answer on an object store. Stat now resolves a prefix to KindDirectory.
func EnsureFileTarget(ctx context.Context, d Driver, p string) error {
	o, err := statForGuard(ctx, d, p)
	if err != nil || o == nil {
		return err
	}
	if o.Kind == KindDirectory {
		return fmt.Errorf("%w: %q already exists as a folder", ErrKindConflict, p)
	}
	return nil
}

// EnsureDirTarget refuses to create a DIRECTORY at p when p already exists as
// a file — the same collision approached from the other side.
func EnsureDirTarget(ctx context.Context, d Driver, p string) error {
	o, err := statForGuard(ctx, d, p)
	if err != nil || o == nil {
		return err
	}
	if o.Kind != KindDirectory {
		return fmt.Errorf("%w: %q already exists as a file", ErrKindConflict, p)
	}
	return nil
}

// statForGuard returns the object at p, or (nil, nil) when the guard cannot
// reach a verdict.
//
// It FAILS OPEN on anything other than a clean "not found": a transient driver
// error must not start refusing uploads. Nothing is lost by doing so — a
// backend too unwell to answer Stat is about to fail the write anyway — whereas
// failing closed would turn one flaky listing into an outage. The miss is
// logged so a guard that has quietly stopped guarding is still visible.
func statForGuard(ctx context.Context, d Driver, p string) (*Object, error) {
	if d == nil || p == "" {
		return nil, nil
	}
	o, err := d.Stat(ctx, p)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			slog.Debug("kind guard: stat inconclusive, allowing write",
				slog.String("path", p), slog.String("err", err.Error()))
		}
		return nil, nil
	}
	return &o, nil
}
