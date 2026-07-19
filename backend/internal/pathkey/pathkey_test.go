package pathkey

import (
	"crypto/md5"
	"encoding/hex"
	"path"
	"strings"
	"testing"
)

// legacyHash is a byte-for-byte copy of the body that used to live at each of
// the nine call sites (managerPathHash / sharePathHash / uploadPathHash /
// avPathHash / nodePathHash / pgNodePathHash / pathHash). It stays here as an
// independent oracle: if Hash ever drifts from the original algorithm, the
// equality test below fails and existing path_hash rows would be at risk.
func legacyHash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
}

// TestHashGolden pins Hash to known values derived from the pre-refactor
// algorithm (computed once from the original body, not hand-authored).
func TestHashGolden(t *testing.T) {
	cases := []struct {
		id   int64
		p    string
		want string
	}{
		{1, "foo/bar.txt", "8e2bb032a89827a5d8f9a6c81d8cf240"},
		{7, "a/b/c", "ed1e9360502802387916950462e396c8"},
		{42, "", "8b22cacd6655fc765d9b87ae5e317837"},
		{300000, "docs/report.pdf", "f5b301149eceb8c0ceb439eb5231dcf9"},
	}
	for _, c := range cases {
		if got := Hash(c.id, c.p); got != c.want {
			t.Errorf("Hash(%d, %q) = %q, want %q", c.id, c.p, got, c.want)
		}
	}
}

// TestHashNormalization proves the leading/trailing-slash canonicalisation is
// preserved: these three spellings of the same path must collide.
func TestHashNormalization(t *testing.T) {
	want := Hash(1, "foo/bar.txt")
	for _, p := range []string{"/foo/bar.txt", "foo/bar.txt/", "/foo/bar.txt/", "foo//bar.txt"} {
		if got := Hash(1, p); got != want {
			t.Errorf("Hash(1, %q) = %q, want %q (same as canonical)", p, got, want)
		}
	}
	if Hash(42, "") != Hash(42, "/") {
		t.Errorf("root path variants must collide")
	}
}

// TestHashMatchesLegacy proves Hash is byte-identical to the original inlined
// body across a spread of inputs — the core guarantee of this refactor.
func TestHashMatchesLegacy(t *testing.T) {
	ids := []int64{0, 1, 7, 42, 255, 256, 65535, 65536, 300000, 1 << 31}
	paths := []string{"", "/", "a", "a/b", "a/b/c.txt", "dir/", "/leading", "trailing/", "deep/nested/path/file.ext"}
	for _, id := range ids {
		for _, p := range paths {
			if got, want := Hash(id, p), legacyHash(id, p); got != want {
				t.Fatalf("Hash(%d, %q) = %q, legacy = %q", id, p, got, want)
			}
		}
	}
}
