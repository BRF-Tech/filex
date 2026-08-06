package storage

import (
	"context"
	"errors"
	"testing"
)

// A colliding name is visible in ONE listing: S3 returns `X` in Contents and
// `X/` in CommonPrefixes, so the driver yields two entries with the same name
// and different kinds. That is what the scan looks for.
func scanFixture() *fakeDriver {
	d := newFakeDriver("s3")
	d.listings = map[string][]Object{
		"": {
			{Path: "projects", Name: "projects", Kind: KindDirectory},
			{Path: "readme.txt", Name: "readme.txt", Kind: KindFile},
		},
		"projects": {
			// The collision: `ss` exists as both an object and a prefix.
			{Path: "projects/ss", Name: "ss", Kind: KindDirectory},
			{Path: "projects/ss", Name: "ss", Kind: KindFile},
			{Path: "projects/notes.md", Name: "notes.md", Kind: KindFile},
		},
		"projects/ss": {
			{Path: "projects/ss/shot.png", Name: "shot.png", Kind: KindFile},
		},
	}
	return d
}

func TestScanKindCollisions_FindsTheCollidingName(t *testing.T) {
	got, err := ScanKindCollisions(context.Background(), scanFixture(), "")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one collision, got %d (%+v)", len(got), got)
	}
	if got[0].Path != "projects/ss" {
		t.Fatalf("want projects/ss, got %q", got[0].Path)
	}
	if got[0].Dir != "projects" {
		t.Fatalf("want dir=projects, got %q", got[0].Dir)
	}
}

func TestScanKindCollisions_CleanStorageReportsNothing(t *testing.T) {
	d := newFakeDriver("s3")
	d.listings = map[string][]Object{
		"":     {{Path: "docs", Name: "docs", Kind: KindDirectory}},
		"docs": {{Path: "docs/a.txt", Name: "a.txt", Kind: KindFile}},
	}
	got, err := ScanKindCollisions(context.Background(), d, "")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a clean storage must report nothing, got %+v", got)
	}
}

// An unlistable directory must not abort the scan: a colliding prefix is
// exactly the thing that can refuse to list, and stopping there would hide
// every collision that comes after it.
func TestScanKindCollisions_UnlistableDirDoesNotAbort(t *testing.T) {
	d := scanFixture()
	d.listErr = errors.New("prefix unreadable")

	got, err := ScanKindCollisions(context.Background(), d, "")
	if err != nil {
		t.Fatalf("an unlistable directory must not fail the scan: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("nothing is knowable when listing fails, got %+v", got)
	}
}

// The walker must terminate even if a driver reports a directory that loops
// back on itself.
func TestScanKindCollisions_TerminatesOnSelfReferentialListing(t *testing.T) {
	d := newFakeDriver("s3")
	d.listings = map[string][]Object{
		"":     {{Path: "loop", Name: "loop", Kind: KindDirectory}},
		"loop": {{Path: "loop/loop", Name: "loop", Kind: KindDirectory}},
	}
	// loop/loop lists nothing → walk ends. The visited set is what stops a
	// driver that returns the same child forever.
	done := make(chan struct{})
	go func() {
		_, _ = ScanKindCollisions(context.Background(), d, "")
		close(done)
	}()
	select {
	case <-done:
	case <-context.Background().Done():
	}
}
