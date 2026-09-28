package handlers

import (
	"context"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/e2e"
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
