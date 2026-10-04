package db

import (
	"context"
	"database/sql"

	"github.com/brf-tech/filex/backend/internal/syspath"
)

// VanishedSQL implements Store.ListVanishedNodeIDs and Store.CountChildRows
// (issue #74), written ONCE for every engine. Each driver embeds a
// *VanishedSQL in its Store, so the methods are promoted and there is no
// per-driver copy to drift (the arrangement of NodeDeletedBySQL).
//
// A "vanished" row is one soft-deleted WHERE IT STOOD: deleted_at is set and
// its path is still where the file lived, not a key inside `.filex-trash/`.
// Its bytes were never parked in the trash — the storage sync found the file
// gone (every version up to 0.47), or the bytes were deleted outright — so it
// is not a trash entry and nothing can restore it. See trash.Vanished.
type VanishedSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (o *VanishedSQL) q(query string) string { return rebind(o.Placeholders, query) }

// vanishedNodeIDsSQL is the statement of ListVanishedNodeIDs, with `?`. It is
// a constant so that the test that reads its plan on SQLite
// (TestThePathIndexIsOnlyForStatementsThatComparePath) reads this text and
// not a copy of it.
const vanishedNodeIDsSQL = `
		SELECT id FROM nodes
		 WHERE storage_id=? AND deleted_at IS NOT NULL AND id > ?
		   AND path <> ? AND path <> ?
		   AND SUBSTR(path,1,?) <> ? AND SUBSTR(path,1,?) <> ?
		 ORDER BY id LIMIT ?`

// ListVanishedNodeIDs returns up to limit ids, above afterID and in id order,
// of storageID's vanished rows: deleted, and with a path that is neither the
// trash directory nor inside it, in either spelling the path column carries.
//
// ⚠ The trash prefix is matched with SUBSTR on the CHARACTER count (see
// PrefixChars), never LIKE. What is BELOW a folder is a byte range of path
// that an index answers (NodesUnderSQL); "not in the trash" has no range.
func (o *VanishedSQL) ListVanishedNodeIDs(ctx context.Context, storageID, afterID int64, limit int) ([]int64, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	bare := syspath.Trash
	slashed := "/" + bare
	rows, err := Conn(ctx, o.Pool).QueryContext(ctx, o.q(vanishedNodeIDsSQL),
		storageID, afterID,
		slashed, bare,
		PrefixChars(slashed+"/"), slashed+"/", PrefixChars(bare+"/"), bare+"/",
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CountChildRows counts the rows, live or deleted, whose parent_id is
// parentID — everything the parent_id cascade would take with the row.
func (o *VanishedSQL) CountChildRows(ctx context.Context, parentID int64) (int, error) {
	var n int
	err := Conn(ctx, o.Pool).QueryRowContext(ctx, o.q(`SELECT COUNT(*) FROM nodes WHERE parent_id=?`), parentID).Scan(&n)
	return n, err
}
