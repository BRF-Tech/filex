package db

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/listorder"
)

// UserMetaPageSQL implements Store.UserNodeMetaPage and Store.UserNodeMetaAt,
// written ONCE for every engine (the arrangement of NodeLinkStateSQL): each
// driver embeds a *UserMetaPageSQL in its Store.
//
// ⚠ Why (filex 0.54, audit D3 + D4). Recent and Starred were read with
// ListNodesByUserMeta: the newest N rows and nothing else - no total, no
// offset, no other order, and no WHEN on the row. The explorer asked for 50
// recents and 200 stars, could not say the list was cut, and drew "Recent" in
// the file's modification order because the opening time never reached it.
// And to put a star on a listing row it fetched the first 500 stars and
// matched ids in the browser, so a person's 501st star was never drawn.
//
// The page reads only ids and times; the caller loads the rows it returns
// (GetNode), so there is no per-driver node scan to keep in step here.
type UserMetaPageSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (o *UserMetaPageSQL) q(query string) string { return rebind(o.Placeholders, query) }

// UserMetaEntry is one (node, time) of a person's per-node flag: when they
// opened it (last_opened) or starred it (starred).
type UserMetaEntry struct {
	NodeID int64
	At     time.Time
}

// userMetaOrderSQL is the ORDER BY for one listorder.Order. Every clause ends
// on the node id so a page boundary never splits or repeats a tie.
func userMetaOrderSQL(o listorder.Order) string {
	dir := " ASC"
	if o.Desc {
		dir = " DESC"
	}
	foldersFirst := "CASE WHEN n.type='dir' THEN 0 ELSE 1 END, "
	switch o.Key {
	case listorder.KeyName:
		return foldersFirst + "LOWER(n.name)" + dir + ", n.id" + dir
	case listorder.KeyModified:
		return foldersFirst + "COALESCE(n.backend_mtime, n.created_at)" + dir + ", n.id" + dir
	case listorder.KeySize:
		return foldersFirst + "n.size" + dir + ", n.id" + dir
	case listorder.KeyType:
		return foldersFirst + "LOWER(n.name)" + dir + ", n.id" + dir
	}
	return "m.updated_at" + dir + ", n.id" + dir
}

// UserNodeMetaPage returns one page of the live nodes userID flagged with
// key, in order o (listorder.Newest when the caller asked for none), with
// the time each was flagged, and how many there are in all.
func (o *UserMetaPageSQL) UserNodeMetaPage(ctx context.Context, userID int64, key string, order listorder.Order, limit, offset int) ([]UserMetaEntry, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	c := Conn(ctx, o.Pool)
	var total int
	if err := c.QueryRowContext(ctx, o.q(`
		SELECT COUNT(*) FROM user_node_meta m
		 INNER JOIN nodes n ON n.id = m.node_id
		 WHERE m.user_id=? AND m.meta_key=? AND n.deleted_at IS NULL`), userID, key).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := c.QueryContext(ctx, o.q(`
		SELECT m.node_id, m.updated_at FROM user_node_meta m
		 INNER JOIN nodes n ON n.id = m.node_id
		 WHERE m.user_id=? AND m.meta_key=? AND n.deleted_at IS NULL
		 ORDER BY `+userMetaOrderSQL(order)+`
		 LIMIT ? OFFSET ?`), userID, key, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []UserMetaEntry{}
	for rows.Next() {
		var e UserMetaEntry
		if err := rows.Scan(&e.NodeID, &e.At); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// userMetaAtBatch keeps one UserNodeMetaAt query under every engine's bind
// parameter limit (SQLite's oldest is 999).
const userMetaAtBatch = 500

// UserNodeMetaAt returns, for the ids userID flagged with key, when each was
// flagged. An id the person never flagged is absent from the map - which is
// how a listing learns, in one query per page, which of its rows are starred.
func (o *UserMetaPageSQL) UserNodeMetaAt(ctx context.Context, userID int64, key string, ids []int64) (map[int64]time.Time, error) {
	out := map[int64]time.Time{}
	if userID <= 0 {
		return out, nil
	}
	for start := 0; start < len(ids); start += userMetaAtBatch {
		end := start + userMetaAtBatch
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		marks := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)+2)
		args = append(args, userID, key)
		for i, id := range chunk {
			marks[i] = "?"
			args = append(args, id)
		}
		rows, err := Conn(ctx, o.Pool).QueryContext(ctx, o.q(`
			SELECT node_id, updated_at FROM user_node_meta
			 WHERE user_id=? AND meta_key=? AND node_id IN (`+strings.Join(marks, ",")+`)`), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var at time.Time
			if err := rows.Scan(&id, &at); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[id] = at
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
