package db

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// NodeDeletedBySQL implements Store.SetNodeDeletedBy (migration 00061), written
// ONCE for every engine. Each driver embeds a *NodeDeletedBySQL in its Store,
// so the method is promoted and there is no per-driver copy to drift — the
// arrangement of StorageOrderSQL and CatalogueFolderSQL (lesson #499). PR #64
// wrote it twice, identical but for the placeholders, and only the duplication
// gate's driver-pair entry kept that green.
type NodeDeletedBySQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

// SetNodeDeletedBy names who put a trashed row in the trash — the row, and for
// a folder in the trash every trashed row under its path that names nobody
// yet: its contents, which SoftDeleteAndRetag moved under the same trash key.
// A live row is left alone, and so is a row another delete already named.
//
// ⚠ The contents only when the folder's path IS a trash key
// (syspath.InTrash). A folder soft-deleted where it stood — a driver that could
// not trash it, so its bytes were deleted outright and its row kept its path —
// took none of its rows along: what is trashed under that path is older
// tombstones (the scanner found those files gone, nobody deleted them), and
// naming this person on them credited them with deletes they never made.
//
// ⚠ The subtree is matched with SUBSTR on the CHARACTER count, not LIKE: a `%`
// or `_` in a folder name is data, and see PrefixChars for why len() is wrong.
func (o *NodeDeletedBySQL) SetNodeDeletedBy(ctx context.Context, nodeID int64, by *int64) error {
	c := Conn(ctx, o.Pool)
	var storageID int64
	var nodeType, p string
	err := c.QueryRowContext(ctx, o.q(`SELECT storage_id, type, path FROM nodes WHERE id=? AND deleted_at IS NOT NULL`), nodeID).
		Scan(&storageID, &nodeType, &p)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, o.q(`UPDATE nodes SET deleted_by=? WHERE id=?`), by, nodeID); err != nil {
		return err
	}
	if nodeType != string(model.NodeTypeDirectory) || !syspath.InTrash(p) {
		return nil
	}
	for _, pfx := range SubtreePrefixVariants([]string{p}) {
		if _, err := c.ExecContext(ctx, o.q(`
			UPDATE nodes SET deleted_by=?
			WHERE storage_id=? AND deleted_at IS NOT NULL AND deleted_by IS NULL AND SUBSTR(path,1,?)=?`),
			by, storageID, PrefixChars(pfx), pfx); err != nil {
			return err
		}
	}
	return nil
}

func (o *NodeDeletedBySQL) q(query string) string { return rebind(o.Placeholders, query) }

// rebind writes a `?` statement in the engine's placeholders: ph when it has
// its own (PostgreSQL), the statement as it is otherwise.
func rebind(ph func(string) string, query string) string {
	if ph != nil {
		return ph(query)
	}
	return query
}

// PrefixChars is the length SUBSTR needs for a path prefix: SQL counts
// CHARACTERS (SQLite, MySQL and PostgreSQL alike), Go's len counts bytes.
// Passing len() made every folder whose path is not plain ASCII match none of
// its own rows — "/Müşteri/" is 9 characters and 11 bytes — so the folder went
// to the trash and its contents stayed live, and a restore left them in the
// trash.
func PrefixChars(p string) int { return utf8.RuneCountInString(p) }

// SubtreePrefixVariants normalizes candidate folder paths into the two on-disk
// conventions the nodes.path column historically mixes (with and without a
// leading slash), each with a trailing "/" so only strict descendants match.
// The storage root is never a subtree.
func SubtreePrefixVariants(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		norm := strings.TrimRight(path.Clean("/"+strings.Trim(p, "/")), "/")
		if norm == "" || norm == "/" {
			continue
		}
		for _, v := range []string{norm + "/", strings.TrimPrefix(norm, "/") + "/"} {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

// SubtreeSuffix strips the first of prefixes p starts with off p ("" when none
// matches).
func SubtreeSuffix(p string, prefixes []string) string {
	for _, pfx := range prefixes {
		if strings.HasPrefix(p, pfx) {
			return strings.TrimPrefix(p, pfx)
		}
	}
	return ""
}
