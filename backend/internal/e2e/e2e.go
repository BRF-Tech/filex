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
	"crypto/md5"
	"encoding/hex"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
)

// MarkerName is the folder marker filename dropped by the client at the
// root of every encrypted folder.
const MarkerName = ".filex-e2e.json"

// MagicPrefix is the 8-byte prefix of every client-encrypted file.
var MagicPrefix = []byte("filexe2e")

// HasMagicPrefix reports whether b starts with the encrypted-file magic.
func HasMagicPrefix(b []byte) bool {
	return bytes.HasPrefix(b, MagicPrefix)
}

// NodeByPathLookup is the narrow store surface the ancestor walk needs.
// db.Store satisfies it.
type NodeByPathLookup interface {
	GetNodeByPath(ctx context.Context, storageID int64, pathHash string) (*model.Node, error)
}

// pathHash mirrors handlers.managerPathHash / sync.pathHash (unexported
// there — same duplication precedent as internal/dav/dbsync.go) so the
// marker lookup collides with the rows the sync worker + manager write.
func pathHash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
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
	n, err := lk.GetNodeByPath(ctx, storageID, pathHash(storageID, markerPath))
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
