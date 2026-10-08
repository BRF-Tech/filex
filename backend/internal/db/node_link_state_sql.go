package db

import (
	"context"
	"database/sql"
	"strings"
)

// NodeLinkStateSQL implements Store.SetNodeLinkState and Store.NodeLinkStates
// (migration 00098), written ONCE for every engine. Each driver embeds a
// *NodeLinkStateSQL in its Store, so the methods are promoted and there is no
// per-driver copy to drift (the arrangement of NodeUnavailableSQL).
//
// A symlink row (model.NodeTypeSymlink) is a link the storage's driver will
// not follow, and the driver says why when it lists it
// (storage.MetaLinkState: outside_root, broken, unresolved). The sync records
// that reason here, so a listing answered by the catalogue says what a listing
// read from the storage says - not just "a link", which is all the row's type
// can tell.
//
// ⚠ The column is read only through NodeLinkStates, for the rows a listing is
// about to return; it is NOT in the drivers' common node column list. Several
// upgrade tests build a catalogue at an older schema with the ordinary store
// methods (CreateNode, ListNodesUnder) before migrating it, and a column every
// node read and write names would make each of them fail on a database that
// does not have it yet.
type NodeLinkStateSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (o *NodeLinkStateSQL) q(query string) string { return rebind(o.Placeholders, query) }

// LinkStateMax bounds what is kept of a driver's reason. The three filex's own
// drivers give are a dozen characters; a storage plugin could say anything,
// and the reason travels to every listing that shows the row.
const LinkStateMax = 64

// SetNodeLinkState records state as the reason row id is a link filex will not
// follow, and reports whether that changed the row. An empty state clears it:
// the driver listed the link and gave no reason. Nothing is written when the
// row already carries the same reason - the sync asks on every pass.
func (o *NodeLinkStateSQL) SetNodeLinkState(ctx context.Context, id int64, state string) (bool, error) {
	state = clipLinkState(state)
	var (
		res sql.Result
		err error
	)
	if state == "" {
		res, err = Conn(ctx, o.Pool).ExecContext(ctx, o.q(`
			UPDATE nodes SET link_state=NULL WHERE id=? AND link_state IS NOT NULL`), id)
	} else {
		res, err = Conn(ctx, o.Pool).ExecContext(ctx, o.q(`
			UPDATE nodes SET link_state=?
			 WHERE id=? AND (link_state IS NULL OR link_state <> ?)`), state, id, state)
	}
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// linkStateBatch keeps one NodeLinkStates query under every engine's bind
// parameter limit (SQLite's oldest is 999).
const linkStateBatch = 500

// NodeLinkStates returns the recorded reason of every row in ids that carries
// one. A row with none (not a link, catalogued before migration 00098, or a
// driver that gave no reason) is absent from the map.
func (o *NodeLinkStateSQL) NodeLinkStates(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	for start := 0; start < len(ids); start += linkStateBatch {
		end := start + linkStateBatch
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		marks := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			marks[i], args[i] = "?", id
		}
		rows, err := Conn(ctx, o.Pool).QueryContext(ctx, o.q(`
			SELECT id, link_state FROM nodes
			 WHERE id IN (`+strings.Join(marks, ",")+`) AND link_state IS NOT NULL`), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var state string
			if err := rows.Scan(&id, &state); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if state != "" {
				out[id] = state
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// clipLinkState keeps the first LinkStateMax characters of state, on a
// character boundary.
func clipLinkState(state string) string {
	state = strings.TrimSpace(state)
	r := []rune(state)
	if len(r) <= LinkStateMax {
		return state
	}
	return string(r[:LinkStateMax])
}
