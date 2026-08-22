package handlers

import "testing"

// The guard exists for two opposite reasons, so the table holds both: real
// filenames that contain ".." and must pass (an invoice named "… Gaz..pdf" was
// stored and then refused by every read path), and every spelling of a
// traversal that must still be refused.
func TestPathHasDotDot(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		// filenames, not traversals
		{"Fatura A.Ş..pdf", false},
		{"2026/Tic. Sic. Gaz..pdf", false},
		{"Report..pdf", false},
		{"a/..b/c", false},
		{"a/b../c", false},
		{"...three dots", false},
		{"", false},
		{"plain/path/file.txt", false},

		// traversals
		{"..", true},
		{"../etc/passwd", true},
		{"a/../../b", true},
		{"a/..", true},
		{`a\..\b`, true},
		{`..\x`, true},
		{"a/.. /b", true},
		{"a/.../b", true},
		{"a/ ../b", true},
	}
	for _, c := range cases {
		if got := pathHasDotDot(c.rel); got != c.want {
			t.Errorf("pathHasDotDot(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}
