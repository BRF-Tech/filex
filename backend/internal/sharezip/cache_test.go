package sharezip

import (
	"testing"
	"time"
)

func TestSignature_OrderIndependentAndContentSensitive(t *testing.T) {
	a := []File{
		{Rel: "b.txt", Size: 2, Mtime: time.Unix(20, 0)},
		{Rel: "a.txt", Size: 1, Mtime: time.Unix(10, 0)},
	}
	b := []File{
		{Rel: "a.txt", Size: 1, Mtime: time.Unix(10, 0)},
		{Rel: "b.txt", Size: 2, Mtime: time.Unix(20, 0)},
	}
	if signature(a) != signature(b) {
		t.Fatal("signature must not depend on file ordering")
	}

	// A size change must change the signature (cache invalidation).
	c := []File{
		{Rel: "a.txt", Size: 1, Mtime: time.Unix(10, 0)},
		{Rel: "b.txt", Size: 3, Mtime: time.Unix(20, 0)},
	}
	if signature(a) == signature(c) {
		t.Fatal("a size change must change the signature")
	}

	// An mtime change must change the signature too.
	d := []File{
		{Rel: "a.txt", Size: 1, Mtime: time.Unix(11, 0)},
		{Rel: "b.txt", Size: 2, Mtime: time.Unix(20, 0)},
	}
	if signature(a) == signature(d) {
		t.Fatal("an mtime change must change the signature")
	}
}

func TestGenPercent(t *testing.T) {
	g := &Gen{Total: 4, finished: make(chan struct{})}
	if got := g.Percent(); got != 0 {
		t.Fatalf("initial percent = %d, want 0", got)
	}
	g.done.Store(2)
	if got := g.Percent(); got != 50 {
		t.Fatalf("half percent = %d, want 50", got)
	}
	// 100%% is reserved for a finished file on disk — Percent caps at 99.
	g.done.Store(4)
	if got := g.Percent(); got != 99 {
		t.Fatalf("full percent = %d, want capped 99", got)
	}

	// Zero-file folder never divides by zero.
	empty := &Gen{Total: 0, finished: make(chan struct{})}
	if got := empty.Percent(); got != 99 {
		t.Fatalf("empty percent = %d, want 99", got)
	}
}
