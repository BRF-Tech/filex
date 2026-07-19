// Package pathkey derives the storage-scoped cache key (a node's path_hash)
// that addresses rows in the nodes table. Every layer that touches the node
// cache — the sync worker, the DB drivers, DAV, the e2e ancestor walk and the
// HTTP handlers — must hash a path identically, or the same file would map to
// different rows and appear twice after the next sync run.
//
// It deliberately imports nothing from internal/* (only the standard library)
// so both the low-level DB drivers (internal/db/...) and the high-level HTTP
// handlers can depend on it without creating an import cycle.
package pathkey

import (
	"crypto/md5"
	"encoding/hex"
	"path"
	"strings"
)

// Hash returns the hex-encoded MD5 of the cleaned path (leading slash, no
// trailing slash) followed by a NUL separator and the little-endian 4-byte
// storage ID.
//
// The exact byte layout is load-bearing: it addresses path_hash values that
// already exist in the database, so it must never change. This is a verbatim
// move of the body that was previously copied across nine call sites.
func Hash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
}
