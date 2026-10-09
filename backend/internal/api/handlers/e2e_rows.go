package handlers

import (
	"context"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// e2eRoots answers, for rows that arrive OUTSIDE a folder listing (Recent,
// Starred, a tag view, search hits, Shared with me, the trash, a file's own
// stat), which end-to-end encrypted folder a row sits in - and, with a vault
// finder attached (withVaults), whether that folder is a vault.
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
// One instance per request; answers are memoised per parent folder (and the
// vault answer per encrypted folder), so a page of hits from a handful of
// folders costs a handful of walks.
type e2eRoots struct {
	lk e2e.NodeByPathLookup
	// vaults says which encrypted folder is a vault (acl.Resolver.VaultRoot,
	// the vaultlock.Finder every write door asks). Nil = no vault is known,
	// and every encrypted file reads as `encrypted: "folder"`.
	vaults acl.VaultFinder
	memo   map[string]e2eRootAt
	vault  map[string]bool
}

// e2eRootAt is the encrypted folder a parent folder sits in: ok, and its path
// inside the storage ("" = the storage's root itself). A fact about the
// storage; the wire path a row names it by is `of`'s.
type e2eRootAt struct {
	rel string
	ok  bool
}

func newE2eRoots(store any) *e2eRoots {
	lk, _ := store.(e2e.NodeByPathLookup)
	return &e2eRoots{lk: lk, memo: map[string]e2eRootAt{}, vault: map[string]bool{}}
}

// withVaults attaches the vault rule (an *acl.Resolver: nil-safe, and
// answering "in no vault" while vaults are off), so encryptedOf can tell a
// vault from an ordinary encrypted folder.
func (c *e2eRoots) withVaults(v acl.VaultFinder) *e2eRoots {
	if c != nil {
		c.vaults = v
	}
	return c
}

// of returns the wire path ("<storage>://<root>") of the encrypted folder the
// node at nodePath sits in, or "" when it sits in none. The node's own name
// does not count: an encrypted folder's own row is not inside itself.
func (c *e2eRoots) of(ctx context.Context, storageID int64, storageName, nodePath string) string {
	if storageName == "" {
		return ""
	}
	at := c.find(ctx, storageID, nodePath)
	if !at.ok {
		return ""
	}
	return joinAdapterPath(storageName, at.rel)
}

// find is the encrypted folder the node at nodePath sits in (its parent's
// nearest marker, e2e.FindRoot), memoised per parent folder.
func (c *e2eRoots) find(ctx context.Context, storageID int64, nodePath string) e2eRootAt {
	if c == nil || c.lk == nil {
		return e2eRootAt{}
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
	v := e2eRootAt{}
	if root, ok := e2e.FindRoot(ctx, c.lk, storageID, parent); ok {
		v = e2eRootAt{rel: root, ok: true}
	}
	c.memo[key] = v
	return v
}

// inVault reports whether the encrypted folder `at` is a vault; false when
// at is no encrypted folder or no vault rule is attached.
func (c *e2eRoots) inVault(ctx context.Context, storageID int64, at e2eRootAt) bool {
	if c == nil || !at.ok || c.vaults == nil {
		return false
	}
	key := strconv.FormatInt(storageID, 10) + "|" + at.rel
	if v, ok := c.vault[key]; ok {
		return v
	}
	_, v := c.vaults.VaultRoot(ctx, storageID, at.rel)
	c.vault[key] = v
	return v
}

// encryptedOf is a row's `encrypted` (encryptedKind) for the node at
// nodePath: "vault", "folder", "file" or "" - the answer every row outside a
// folder listing carries, from the facts the listing has (the encrypted
// folder the node sits in, whether that folder is a vault, the file's name).
func (c *e2eRoots) encryptedOf(ctx context.Context, storageID int64, nodePath string, isFile bool) string {
	if !isFile || c == nil {
		return ""
	}
	at := c.find(ctx, storageID, nodePath)
	return encryptedKind(true, path.Base("/"+strings.Trim(nodePath, "/")), at.ok, c.inVault(ctx, storageID, at))
}

// encryptedAtDoor is the question every app door asks of a path before it
// hands an app the file or writes over it (an action's or a screen's inputs,
// an interface's call, read and save): the row rule (encryptedKind) asked of
// the path - "folder" (or "vault") for anything inside an encrypted folder, a
// subfolder too, as the doors always refused; "file" for a single encrypted
// file (`.fxe`), unless the catalogue knows the path as a folder; "" when the
// server holds it in plaintext. One rule with every row's `encrypted`.
func encryptedAtDoor(ctx context.Context, store any, storageID int64, rel string) string {
	c := newE2eRoots(store)
	rel = strings.Trim(rel, "/")
	at := c.find(ctx, storageID, rel)
	name := path.Base("/" + rel)
	if c.lk != nil && e2e.LooksEncryptedFile(name) {
		if n, err := c.lk.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, rel)); err == nil && n != nil && n.Type == model.NodeTypeDirectory {
			name = ""
		}
	}
	return encryptedKind(true, name, at.ok, false)
}

// refuseEncryptedAtDoor writes an app door's refusal of what encryptedAtDoor
// found (kind "" refuses nothing) and reports whether it did: `403
// encrypted`, the reason in the reader's language - reading it (an input, an
// interface's read) or writing over it (a save). rel is the path the person
// named, said back in the sentence.
func refuseEncryptedAtDoor(w http.ResponseWriter, r *http.Request, kind, rel string, writing bool) bool {
	if kind == "" {
		return false
	}
	said, params := "app_encrypted_read", apierr.Params{"name": rel}
	switch {
	case writing && kind == encryptedSingle:
		said = "app_encrypted_file_write"
	case writing:
		said, params = "app_encrypted_write", nil
	case kind == encryptedSingle:
		said = "app_encrypted_file_read"
	}
	writeErrorSaid(w, r, http.StatusForbidden, "encrypted", said, params)
	return true
}

// What a file row's `encrypted` says (app SDK FileInfo.encrypted, task #189).
const (
	encryptedInFolder = "folder"
	encryptedInVault  = "vault"
	encryptedSingle   = "file"
)

// encryptedKind is THE rule behind every row's `encrypted`, wherever the row
// comes from: a file inside a vault is "vault", a file inside any other end-
// to-end encrypted folder is "folder", a single encrypted file (`.fxe`,
// e2e.LooksEncryptedFile - the name the server already skips thumbnails and
// content on) outside both is "file", and a folder row or any other file says
// nothing ("", the field is left off). Each is a file whose bytes the server
// holds only as ciphertext and has no key to - what the agent API's boolean
// `encrypted` says too (mark). The explorer hands it to an app's interface as
// is: the server says it, no client guesses it from a name or a badge.
// inFolder / inVault: the file sits in an encrypted folder / that folder is a
// vault; name is the file's stored name.
func encryptedKind(isFile bool, name string, inFolder, inVault bool) string {
	switch {
	case !isFile:
		return ""
	case inFolder && inVault:
		return encryptedInVault
	case inFolder:
		return encryptedInFolder
	case e2e.LooksEncryptedFile(name):
		return encryptedSingle
	}
	return ""
}

// stampEncrypted writes a map-shaped row's `encrypted`; kind "" leaves the
// row as it was, so a plain file and a folder carry no such field. The one
// place a listing-shaped row gets it.
func stampEncrypted(row map[string]any, kind string) {
	if kind != "" {
		row["encrypted"] = kind
	}
}

// stampListingEncrypted stamps every file row of a folder listing from the
// listing's own answer: `e2e_vault_root` (the folder is in a vault) and
// `e2e_root` (it is in an encrypted folder, also the cold-cache one whose
// marker only the driver listing has seen) - and the row's own name (a
// `.fxe`).
func stampListingEncrypted(files []map[string]any, resp map[string]any) {
	folder, _ := resp["e2e_root"].(string)
	vault, _ := resp["e2e_vault_root"].(string)
	for _, entry := range files {
		name, _ := entry["basename"].(string)
		stampEncrypted(entry, encryptedKind(entry["type"] == "file", name, folder != "" || vault != "", vault != ""))
	}
}

// stampRow writes what a map-shaped row outside a folder listing says about
// end-to-end encryption: `e2e_root` (the folder it sits in) and `encrypted`
// (encryptedOf). rel is the node's path inside its storage.
func (c *e2eRoots) stampRow(ctx context.Context, storageID int64, storageName, rel string, row map[string]any) {
	if root := c.of(ctx, storageID, storageName, rel); root != "" {
		row["e2e_root"] = root
	}
	stampEncrypted(row, c.encryptedOf(ctx, storageID, rel, row["type"] == "file"))
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
	c.memo[strconv.FormatInt(storageID, 10)+"|"+dir] = e2eRootAt{rel: dir, ok: true}
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

// annotateRowsE2e stamps `e2e_root` and `encrypted` on projected listing-
// shaped rows of one storage (the manager's search answer) through roots
// (stampRow). Rows outside any encrypted folder are untouched, so a client
// that does not know the key sees exactly what it saw before.
func annotateRowsE2e(ctx context.Context, roots *e2eRoots, storageID int64, storageName string, rows []map[string]any) {
	prefix := storageName + "://"
	for _, row := range rows {
		p, _ := row["path"].(string)
		roots.stampRow(ctx, storageID, storageName, strings.TrimPrefix(p, prefix), row)
	}
}
