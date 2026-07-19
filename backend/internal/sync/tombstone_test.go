package sync

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// TestGuardOK_ColdStart — when the previous run had 0 seen nodes (first
// ever sync), the guard MUST allow the tombstone pass to proceed regardless
// of the current count.
func TestGuardOK_ColdStart(t *testing.T) {
	assert.True(t, guardOK(0, 0), "fresh storage: 0 prev, 0 seen → must pass")
	assert.True(t, guardOK(1, 0), "fresh storage: 0 prev, any seen → must pass")
	assert.True(t, guardOK(1000, 0), "still cold start with 1000 nodes")
}

// TestGuardOK_AbortOnSharpDrop — when seen drops > 30% vs prev the
// tombstone pass must NOT proceed (returns false).
func TestGuardOK_AbortOnSharpDrop(t *testing.T) {
	cases := []struct {
		seen, prev int
		want       bool
		desc       string
	}{
		// 30% threshold: seen >= prev * 0.7 must return true (proceed).
		{seen: 100, prev: 100, want: true, desc: "no change"},
		{seen: 70, prev: 100, want: true, desc: "exactly 30% drop = exactly threshold"},
		{seen: 71, prev: 100, want: true, desc: "29% drop is fine"},
		{seen: 69, prev: 100, want: false, desc: "31% drop trips guard"},
		{seen: 50, prev: 100, want: false, desc: "50% drop trips guard"},
		{seen: 0, prev: 100, want: false, desc: "100% drop is the textbook abort case"},
		{seen: 200, prev: 100, want: true, desc: "growth never trips the guard"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, guardOK(c.seen, c.prev), c.desc)
	}
}

// TestPathHash_Stable — the same (storage, path) pair must produce the
// same hash across calls; different storages must differ.
func TestPathHash_Stable(t *testing.T) {
	a := pathkey.Hash(1, "/foo/bar.txt")
	b := pathkey.Hash(1, "/foo/bar.txt")
	assert.Equal(t, a, b)

	c := pathkey.Hash(2, "/foo/bar.txt")
	assert.NotEqual(t, a, c, "different storage IDs must hash to different values")

	d := pathkey.Hash(1, "/foo/baz.txt")
	assert.NotEqual(t, a, d, "different paths must hash to different values")
}

// TestPathHash_PathNormalization — trailing slash + leading slash should
// produce the same hash (path.Clean takes care of it).
func TestPathHash_PathNormalization(t *testing.T) {
	assert.Equal(t,
		pathkey.Hash(1, "/foo/bar/"),
		pathkey.Hash(1, "/foo/bar"),
		"trailing slash should not affect hash")
}
