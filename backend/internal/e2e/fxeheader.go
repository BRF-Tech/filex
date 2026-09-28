package e2e

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"sort"
)

// A single encrypted file's header, as the SERVER sees it (docs/E2E-ENCRYPTION.md
// → "Single encrypted files"): `filexfxe` ‖ 0x01 ‖ uint32 BE length ‖ UTF-8
// JSON. The server holds no key and reads nothing it could decrypt; it can
// only tell which public fields changed between two versions of a file — the
// `.fxe` counterpart of a rewritten folder key file.

const (
	fxeFormatVersion = 1
	// FileHeaderFixedLen is magic (8) + version (1) + header length (4).
	FileHeaderFixedLen = 13
	// FileHeaderMaxLen is the largest header a writer produces or a reader
	// accepts — so FileHeaderFixedLen+FileHeaderMaxLen bytes always hold it.
	FileHeaderMaxLen = 64 * 1024
)

// ReadFileHeader returns the JSON fields of the `.fxe` header b starts with,
// or ok=false when b is not one (wrong magic or version, a length out of
// range, a header cut short, or not a JSON object).
func ReadFileHeader(b []byte) (map[string]json.RawMessage, bool) {
	if len(b) < FileHeaderFixedLen || !bytes.HasPrefix(b, FileMagicPrefix) || b[8] != fxeFormatVersion {
		return nil, false
	}
	n := binary.BigEndian.Uint32(b[9:FileHeaderFixedLen])
	if n == 0 || n > FileHeaderMaxLen || uint64(len(b)) < uint64(FileHeaderFixedLen)+uint64(n) {
		return nil, false
	}
	var h map[string]json.RawMessage
	if err := json.Unmarshal(b[FileHeaderFixedLen:FileHeaderFixedLen+int(n)], &h); err != nil || h == nil {
		return nil, false
	}
	return h, true
}

// FileHeaderDiff says what changed between two headers of the same `.fxe`.
type FileHeaderDiff struct {
	// Valid: the new bytes carry a readable `.fxe` header.
	Valid bool
	// Compared: there was a readable previous header to compare with.
	Compared bool
	// Changes, sorted: "password" (salt, iter, verify, fmk_pw), "recovery_key"
	// (rk), "escrow" (esc), "content" (dek, nonce, size, chunk), "name" (the
	// sealed original name), "other" (anything else).
	Changes []string
	// SecretChanged: the password or the recovery slot changed — the event
	// after which a copy under the old secret is a liability.
	SecretChanged bool
}

// fileHeaderClass is which change a header field belongs to.
var fileHeaderClass = map[string]string{
	"salt":   "password",
	"iter":   "password",
	"verify": "password",
	"fmk_pw": "password",
	"rk":     "recovery_key",
	"esc":    "escrow",
	"dek":    "content",
	"nonce":  "content",
	"size":   "content",
	"chunk":  "content",
	"name":   "name",
}

// DiffFileHeaders compares the header before (the version an overwrite kept,
// or nil) with the header after (the file as it is now).
func DiffFileHeaders(before, after []byte) FileHeaderDiff {
	a, ok := ReadFileHeader(after)
	if !ok {
		return FileHeaderDiff{}
	}
	d := FileHeaderDiff{Valid: true}
	b, ok := ReadFileHeader(before)
	if !ok {
		return d
	}
	d.Compared = true
	seen := map[string]bool{}
	for _, k := range unionKeys(a, b) {
		if sameJSON(a[k], b[k]) {
			continue
		}
		class, known := fileHeaderClass[k]
		if !known {
			class = "other"
		}
		seen[class] = true
	}
	for c := range seen {
		d.Changes = append(d.Changes, c)
	}
	sort.Strings(d.Changes)
	d.SecretChanged = seen["password"] || seen["recovery_key"]
	return d
}

// SameFileKey reports whether two `.fxe` headers wrap the same file key: the
// same wrapped `dek` bytes, which a writer never reproduces by chance (every
// wrap takes a fresh IV). Such a pair holds the same content under the same
// folder-master key — so the older one's password opens the newer one's body.
func SameFileKey(x, y []byte) bool {
	hx, ok := ReadFileHeader(x)
	if !ok {
		return false
	}
	hy, ok := ReadFileHeader(y)
	if !ok {
		return false
	}
	var dx, dy string
	if json.Unmarshal(hx["dek"], &dx) != nil || json.Unmarshal(hy["dek"], &dy) != nil {
		return false
	}
	return dx != "" && dx == dy
}

// RetiredSecret reports whether old holds the file key now holds under a
// password or recovery slot now no longer uses: a copy that opens the file's
// CURRENT contents with a secret that was changed. That, and only that, is
// what a password change leaves behind as a liability.
func RetiredSecret(old, now []byte) bool {
	if !SameFileKey(old, now) {
		return false
	}
	return DiffFileHeaders(old, now).SecretChanged
}

func unionKeys(a, b map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, dup := a[k]; !dup {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// sameJSON compares two JSON values by meaning (key order and spacing do not
// count). An absent field equals only another absent field.
func sameJSON(x, y json.RawMessage) bool {
	if x == nil || y == nil {
		return x == nil && y == nil
	}
	var vx, vy any
	if json.Unmarshal(x, &vx) != nil || json.Unmarshal(y, &vy) != nil {
		return bytes.Equal(x, y)
	}
	return reflect.DeepEqual(vx, vy)
}
