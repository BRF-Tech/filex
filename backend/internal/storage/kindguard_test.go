package storage

import (
	"context"
	"errors"
	"testing"
)

// The guard exists because an OBJECT store will happily accept a file written
// onto a folder's key — a real filesystem refuses that at the OS level, so the
// local driver never showed the bug. fakeDriver (replicated_test.go) models the
// object-store behaviour: whatever Stat says is the whole truth.

func TestEnsureFileTarget_RefusesDirectory(t *testing.T) {
	d := newFakeDriver("s3")
	d.stats["reports"] = Object{Path: "reports", Name: "reports", Kind: KindDirectory}

	err := EnsureFileTarget(context.Background(), d, "reports")
	if !errors.Is(err, ErrKindConflict) {
		t.Fatalf("writing a file onto a folder must be refused, got %v", err)
	}
}

func TestEnsureFileTarget_AllowsFreeAndExistingFile(t *testing.T) {
	d := newFakeDriver("s3")
	d.stats["notes.txt"] = Object{Path: "notes.txt", Name: "notes.txt", Kind: KindFile}

	if err := EnsureFileTarget(context.Background(), d, "brand-new.txt"); err != nil {
		t.Fatalf("a free path must be writable: %v", err)
	}
	// Overwriting a file is a normal upload, not a conflict.
	if err := EnsureFileTarget(context.Background(), d, "notes.txt"); err != nil {
		t.Fatalf("overwriting an existing file must stay allowed: %v", err)
	}
}

func TestEnsureDirTarget_RefusesFile(t *testing.T) {
	d := newFakeDriver("s3")
	d.stats["archive"] = Object{Path: "archive", Name: "archive", Kind: KindFile}

	err := EnsureDirTarget(context.Background(), d, "archive")
	if !errors.Is(err, ErrKindConflict) {
		t.Fatalf("a folder created onto a file must be refused, got %v", err)
	}
}

func TestEnsureDirTarget_AllowsFreeAndExistingDir(t *testing.T) {
	d := newFakeDriver("s3")
	d.stats["photos"] = Object{Path: "photos", Name: "photos", Kind: KindDirectory}

	if err := EnsureDirTarget(context.Background(), d, "new-folder"); err != nil {
		t.Fatalf("a free path must be creatable: %v", err)
	}
	if err := EnsureDirTarget(context.Background(), d, "photos"); err != nil {
		t.Fatalf("mkdir on an existing folder is idempotent, not a conflict: %v", err)
	}
}

// A backend too unwell to answer Stat must not start refusing uploads: the
// guard is a safety net, and one flaky listing turning into an upload outage
// would be a worse failure than the collision it prevents. The write that
// follows will fail on its own if the backend really is down.
func TestKindGuard_FailsOpenOnInconclusiveStat(t *testing.T) {
	d := newFakeDriver("s3")
	d.statErr = errors.New("503 slow down")

	if err := EnsureFileTarget(context.Background(), d, "anything"); err != nil {
		t.Fatalf("an inconclusive Stat must not block the write, got %v", err)
	}
	if err := EnsureDirTarget(context.Background(), d, "anything"); err != nil {
		t.Fatalf("an inconclusive Stat must not block mkdir, got %v", err)
	}
}

// A nil driver / empty path reaches the guard from callers that resolve lazily;
// it must be inert rather than panic.
func TestKindGuard_NilAndEmptyAreInert(t *testing.T) {
	if err := EnsureFileTarget(context.Background(), nil, "x"); err != nil {
		t.Fatalf("nil driver must be inert, got %v", err)
	}
	if err := EnsureFileTarget(context.Background(), newFakeDriver("s3"), ""); err != nil {
		t.Fatalf("empty path must be inert, got %v", err)
	}
}
