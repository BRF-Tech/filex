package ops

import "testing"

// The destination of a copy/move, and the one case that was broken: the
// storage ROOT.
//
// Pasting into the top of a storage is what FileExplorer sends as
// `<storage>://`; the handler turned that into an empty dest, and Submit
// refuses an empty dest for copy/move — so "paste into the root" answered
// `ops: dest required` for every driver, built-in ones included (measured on
// a local storage, 2026-08-19). The root now arrives here as "/", and this
// test pins what that must produce: the source's own basename, with NO
// leading slash. A leading slash is harmless on a local disk and a real
// object on S3 — a key whose first segment is empty, which no listing shows
// where the user expects it.
func TestJoinIntoDir(t *testing.T) {
	cases := []struct{ dest, src, want string }{
		// The storage root.
		{"/", "sub/f.txt", "f.txt"},
		{"/", "f.txt", "f.txt"},
		{"/", "a/b/c.txt", "c.txt"},
		{"/", "dir/", "dir"},
		// An ordinary directory keeps its trailing-slash meaning.
		{"sub/", "f.txt", "sub/f.txt"},
		{"sub/", "other/f.txt", "sub/f.txt"},
		{"a/b/", "x/y/z.bin", "a/b/z.bin"},
		// No trailing slash is a literal target (a rename), untouched.
		{"sub/new.txt", "old.txt", "sub/new.txt"},
		// An empty dest leaves the source alone (delete ops carry none).
		{"", "keep/me.txt", "keep/me.txt"},
	}
	for _, c := range cases {
		if got := joinIntoDir(c.dest, c.src); got != c.want {
			t.Errorf("joinIntoDir(%q, %q) = %q, want %q", c.dest, c.src, got, c.want)
		}
	}
}
