package handlers

import (
	"context"
	"path"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// e2eRoots answers, for rows that arrive OUTSIDE a folder listing (Recent,
// Starred, a tag view, search hits, the trash), which end-to-end encrypted
// folder a row sits in.
//
// Why the server says it: a folder listing already carries `e2e_root` for the
// whole listing (annotateE2e), so the client knows to show the lock screen
// and, since encrypted names, to decrypt the names. These other views mix
// rows from anywhere, and a row inside an encrypted folder whose NAMES are
// encrypted arrives with a ciphertext name. Without the root the client
// cannot tell that row from an ordinary file that happens to have an odd
// name, so it could neither decrypt it (folder unlocked) nor say honestly
// "🔒 Encrypted item" (folder locked), and opening it handed a viewer
// ciphertext. The server still reads nothing: this is the same marker-path
// lookup every other e2e awareness uses (e2e.FindRoot), and the root's own
// name is already in the clear — it lives in an unencrypted folder.
//
// One instance per request; answers are memoised per parent folder, so a
// page of hits from a handful of folders costs a handful of walks.
type e2eRoots struct {
	lk   e2e.NodeByPathLookup
	memo map[string]string
}

func newE2eRoots(store any) *e2eRoots {
	lk, _ := store.(e2e.NodeByPathLookup)
	return &e2eRoots{lk: lk, memo: map[string]string{}}
}

// of returns the wire path ("<storage>://<root>") of the encrypted folder the
// node at nodePath sits in, or "" when it sits in none. The node's own name
// does not count: an encrypted folder's own row is not inside itself.
func (c *e2eRoots) of(ctx context.Context, storageID int64, storageName, nodePath string) string {
	if c == nil || c.lk == nil || storageName == "" {
		return ""
	}
	rel := strings.Trim(nodePath, "/")
	parent := ""
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		parent = rel[:i]
	}
	key := strconv.FormatInt(storageID, 10) + "|" + parent
	if v, ok := c.memo[key]; ok {
		return v
	}
	v := ""
	if root, ok := e2e.FindRoot(ctx, c.lk, storageID, parent); ok {
		v = joinAdapterPath(storageName, root)
	}
	c.memo[key] = v
	return v
}

// mark is what a row of the AI surface (file_list, file_info, file_search and
// their /api/ai twins) says about end-to-end encryption, from the same three
// rules the explorer's rows use, none of them reading a byte of content:
//
//   - a folder that holds a marker is an encrypted folder (e2e.IsRoot, the
//     listing's `e2e: true` badge): encrypted, and its own root;
//   - anything inside one sits in that root (of, the `e2e_root` of Recent,
//     Starred, search hits and the trash): encrypted, root named;
//   - a single encrypted file (`.fxe`, e2e.LooksEncryptedFile, the name test
//     the thumbnail pipeline skips on): encrypted, no root (it carries its
//     own key slots).
//
// encrypted is the server's honest "this is ciphertext I hold no key for"; it
// is what makes file_read answer E2E_ENCRYPTED instead of bytes.
func (c *e2eRoots) mark(ctx context.Context, storageID int64, storageName, rel string, isDir bool) (encrypted bool, root string) {
	rel = strings.Trim(rel, "/")
	if c == nil {
		return false, ""
	}
	if isDir && rel != "" && c.lk != nil && e2e.IsRoot(ctx, c.lk, storageID, rel) {
		return true, joinAdapterPath(storageName, rel)
	}
	if root = c.of(ctx, storageID, storageName, rel); root != "" {
		return true, root
	}
	return !isDir && e2e.LooksEncryptedFile(path.Base(rel)), ""
}

// rootIs records that dir is an encrypted folder before the catalogue knows
// it: its marker is right there in a driver listing (e2eMarkerAmong) but no
// row has been written for it yet. Every child of dir then sits in it.
func (c *e2eRoots) rootIs(storageID int64, storageName, dir string) {
	if c == nil {
		return
	}
	dir = strings.Trim(dir, "/")
	c.memo[strconv.FormatInt(storageID, 10)+"|"+dir] = joinAdapterPath(storageName, dir)
}

// e2eMarkerAmong reports whether a driver listing holds an encrypted folder's
// marker - the cold-cache case of a folder encrypted seconds ago, before the
// sync has catalogued the marker. The explorer's listing and the AI listing
// both ask it.
func e2eMarkerAmong(objs []storage.Object) bool {
	for _, o := range objs {
		if o.Name == e2e.MarkerName && o.Kind != storage.KindDirectory {
			return true
		}
	}
	return false
}

// annotateRowsE2e stamps `e2e_root` on projected listing-shaped rows (the
// manager's search answer). Rows outside any encrypted folder are untouched,
// so a client that does not know the key sees exactly what it saw before.
func annotateRowsE2e(ctx context.Context, store any, storageID int64, storageName string, rows []map[string]any) {
	roots := newE2eRoots(store)
	prefix := storageName + "://"
	for _, row := range rows {
		p, _ := row["path"].(string)
		rel := strings.TrimPrefix(p, prefix)
		if root := roots.of(ctx, storageID, storageName, rel); root != "" {
			row["e2e_root"] = root
		}
	}
}
