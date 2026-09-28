// Package e2e carries the server-side AWARENESS of client-side (E2E)
// encrypted folders — nothing more. The server never sees a password or a
// key and cannot decrypt anything; this package only lets the pipelines
// recognise the two artifacts the client leaves behind so they stop doing
// pointless (and potentially leaky) work:
//
//   - the folder marker `.filex-e2e.json` at an encrypted folder's root
//     (public salt + verify blob — hidden from listings, but readable via
//     the preview endpoint so the client can unlock);
//   - the `filexe2e` magic prefix every encrypted file starts with.
//
// Design + threat model: docs/E2E-ENCRYPTION.md.
package e2e

import (
	"bytes"
	"context"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// MarkerName is the folder marker filename dropped by the client at the
// root of every encrypted folder.
const MarkerName = ".filex-e2e.json"

// MagicPrefix is the 8-byte prefix of every client-encrypted file.
var MagicPrefix = []byte("filexe2e")

// HasMagicPrefix reports whether b starts with the encrypted-FOLDER-file
// magic. HasEncryptedPrefix recognises every kind of encrypted file.
func HasMagicPrefix(b []byte) bool {
	return bytes.HasPrefix(b, MagicPrefix)
}

// FileMagicPrefix is the 8-byte prefix of a single encrypted file (`.fxe`,
// docs/E2E-ENCRYPTION.md → "Single encrypted files"): a file encrypted on
// its own, outside any encrypted folder, carrying its own key slots.
var FileMagicPrefix = []byte("filexfxe")

// FileExtension is the stored-name suffix of a single encrypted file.
const FileExtension = ".fxe"

// HasEncryptedPrefix reports whether b starts with either magic — a file in
// an encrypted folder or a single encrypted file. Every pipeline that would
// read content (thumbnails, content indexing, document editing) treats the
// two the same: ciphertext it cannot, and must not try to, render.
func HasEncryptedPrefix(b []byte) bool {
	return bytes.HasPrefix(b, MagicPrefix) || bytes.HasPrefix(b, FileMagicPrefix)
}

// LooksEncryptedFile reports whether a stored NAME is a single encrypted
// file's — the check that needs no read, for skipping work before anything
// is opened. The content sniff (HasEncryptedPrefix) is the authority.
func LooksEncryptedFile(name string) bool {
	return len(name) > len(FileExtension) && strings.EqualFold(name[len(name)-len(FileExtension):], FileExtension)
}

// LongNameSuffix and SidecarSuffix are the two spellings a shortened
// encrypted name takes (docs/E2E-ENCRYPTION.md → "Encrypted names"):
// `<43 base64url>.fxl` for the item, `<43 base64url>.fxl.name` for the
// sidecar that holds its full encrypted name.
const (
	LongNameSuffix = ".fxl"
	SidecarSuffix  = ".fxl.name"
)

// minEncryptedNameLen is the shortest name the browser can produce: a 16-byte
// SIV tag plus one byte of name, base64url — 23 characters.
const minEncryptedNameLen = 23

// dirIDLen is a folder id's length in a stored folder name (`S.D`,
// `<43>.fxl.D`): 16 bytes, base64url.
const dirIDLen = 22

// LooksEncryptedName reports whether a stored name has the SHAPE of a name
// the browser encrypted: base64url with no dot and at least 23 characters, a
// shortened `<43>.fxl` item, its `.fxl.name` sidecar — or either of the first
// two followed by `.` and a folder's 22-character id (a folder's name,
// docs/E2E-ENCRYPTION.md → "Folder ids").
//
// The server holds no key and cannot tell an encrypted name from a plaintext
// one that happens to look like it, so this is used for exactly one thing:
// REFUSING to invent a name next to one (the `-copy` suffix of a colliding
// copy or move). A name the server makes up inside a folder whose names are
// encrypted is a name nobody can decrypt, so the right answer there is a
// conflict the person resolves, not a silent new name.
func LooksEncryptedName(name string) bool {
	if strings.HasSuffix(name, SidecarSuffix) {
		core := strings.TrimSuffix(name, SidecarSuffix)
		return len(core) == 43 && isB64URL(core)
	}
	core := name
	if i := strings.LastIndexByte(core, '.'); i > 0 && len(core)-i-1 == dirIDLen && isB64URL(core[i+1:]) {
		core = core[:i]
	}
	if strings.HasSuffix(core, LongNameSuffix) {
		core = strings.TrimSuffix(core, LongNameSuffix)
		return len(core) == 43 && isB64URL(core)
	}
	return len(core) >= minEncryptedNameLen && isB64URL(core)
}

func isB64URL(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return s != ""
}

// NodeByPathLookup is the narrow store surface the ancestor walk needs.
// db.Store satisfies it.
type NodeByPathLookup interface {
	GetNodeByPath(ctx context.Context, storageID int64, pathHash string) (*model.Node, error)
}

// markerAt reports whether dir (relative, any leading-slash form) contains
// a live `.filex-e2e.json` node in the DB cache.
func markerAt(ctx context.Context, lk NodeByPathLookup, storageID int64, dir string) bool {
	if lk == nil {
		return false
	}
	rel := strings.Trim(dir, "/")
	var markerPath string
	if rel == "" {
		markerPath = MarkerName
	} else {
		markerPath = rel + "/" + MarkerName
	}
	n, err := lk.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, markerPath))
	return err == nil && n != nil && n.DeletedAt == nil
}

// FindRoot walks dir and its ancestors (deepest first, storage root last)
// and returns the relative path of the nearest directory carrying an
// encrypted-folder marker. ok=false when no ancestor is marked. The
// returned root is trimmed of slashes ("" = the storage root itself).
func FindRoot(ctx context.Context, lk NodeByPathLookup, storageID int64, dir string) (string, bool) {
	rel := strings.Trim(path.Clean("/"+strings.Trim(dir, "/")), "/")
	for {
		if markerAt(ctx, lk, storageID, rel) {
			return rel, true
		}
		if rel == "" {
			return "", false
		}
		idx := strings.LastIndex(rel, "/")
		if idx == -1 {
			rel = ""
		} else {
			rel = rel[:idx]
		}
	}
}

// UnderEncrypted reports whether nodePath (a FILE or dir path, relative)
// sits inside an encrypted folder subtree — i.e. any of its ancestor
// directories carries the marker. Used by the thumb pipeline and the
// content extractor to skip work on ciphertext.
func UnderEncrypted(ctx context.Context, lk NodeByPathLookup, storageID int64, nodePath string) bool {
	rel := strings.Trim(path.Clean("/"+strings.Trim(nodePath, "/")), "/")
	parent := ""
	if idx := strings.LastIndex(rel, "/"); idx != -1 {
		parent = rel[:idx]
	}
	_, ok := FindRoot(ctx, lk, storageID, parent)
	return ok
}
