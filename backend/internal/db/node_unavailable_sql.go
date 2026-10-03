package db

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// NodeUnavailableSQL implements Store.MarkNodeUnavailable,
// Store.ClearNodeUnavailable and Store.UnavailableAt (migration 00078, issue
// #104), written ONCE for every engine. Each driver embeds a
// *NodeUnavailableSQL in its Store, so the methods are promoted and there is no
// per-driver copy to drift (the arrangement of NodeDeletedBySQL).
//
// An "unavailable" row is a live row the storage could not answer for: the
// sync asked whether its object still exists and got neither "yes" nor "not
// found". It stays in the catalogue, is listed with a warning, and nothing may
// be done to it or below it until the storage answers again.
type NodeUnavailableSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (o *NodeUnavailableSQL) q(query string) string { return rebind(o.Placeholders, query) }

// UnavailableReasonMax bounds what is kept of a storage's answer: it is shown
// to a person and written to a log, and a backend error can be a page long.
const UnavailableReasonMax = 500

// MarkNodeUnavailable records reason on the live row id and reports whether
// that changed anything: false when the row already carried the same reason
// (the sync asks on every pass, and only a change is worth a log line), or is
// not a live row.
func (o *NodeUnavailableSQL) MarkNodeUnavailable(ctx context.Context, id int64, reason string) (bool, error) {
	reason = clipReason(reason)
	res, err := Conn(ctx, o.Pool).ExecContext(ctx, o.q(`
		UPDATE nodes SET unavailable_reason=?, unavailable_at=CURRENT_TIMESTAMP
		 WHERE id=? AND deleted_at IS NULL
		   AND (unavailable_reason IS NULL OR unavailable_reason <> ?)`),
		reason, id, reason)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ClearNodeUnavailable lifts the mark from row id and reports whether it
// carried one.
func (o *NodeUnavailableSQL) ClearNodeUnavailable(ctx context.Context, id int64) (bool, error) {
	res, err := Conn(ctx, o.Pool).ExecContext(ctx, o.q(`
		UPDATE nodes SET unavailable_reason=NULL, unavailable_at=NULL
		 WHERE id=? AND unavailable_reason IS NOT NULL`), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UnavailableEntry is the row that makes a path unavailable: the path itself,
// or the folder above it that the storage could not answer for.
type UnavailableEntry struct {
	NodeID int64
	Path   string
	Reason string
	Since  *time.Time
}

// UnavailableAt answers whether rel, on storageID, is unavailable: whether its
// own live row, or the live row of any folder above it, carries a mark. The
// shallowest such row is returned, nil when there is none.
//
// One indexed query, whatever the depth: every ancestor is looked up by its
// path hash ((storage_id, path_hash) is unique for live rows), which is also
// how both spellings a path column carries ("/a/b", "a/b") are matched.
func (o *NodeUnavailableSQL) UnavailableAt(ctx context.Context, storageID int64, rel string) (*UnavailableEntry, error) {
	clean := strings.Trim(path.Clean("/"+strings.TrimSpace(rel)), "/")
	if clean == "" || clean == "." {
		return nil, nil
	}
	parts := strings.Split(clean, "/")
	args := []any{storageID}
	marks := make([]string, 0, len(parts))
	for i := range parts {
		args = append(args, pathkey.Hash(storageID, "/"+strings.Join(parts[:i+1], "/")))
		marks = append(marks, "?")
	}
	rows, err := Conn(ctx, o.Pool).QueryContext(ctx, o.q(`
		SELECT id, path, unavailable_reason, unavailable_at FROM nodes
		 WHERE storage_id=? AND path_hash IN (`+strings.Join(marks, ",")+`)
		   AND deleted_at IS NULL AND unavailable_reason IS NOT NULL`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var best *UnavailableEntry
	for rows.Next() {
		var e UnavailableEntry
		var since sql.NullTime
		if err := rows.Scan(&e.NodeID, &e.Path, &e.Reason, &since); err != nil {
			return nil, err
		}
		if since.Valid {
			t := since.Time
			e.Since = &t
		}
		if best == nil || depthOf(e.Path) < depthOf(best.Path) {
			best = &e
		}
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return best, nil
}

func depthOf(p string) int { return strings.Count(strings.Trim(p, "/"), "/") }

// clipReason keeps the first UnavailableReasonMax characters of reason, on a
// character boundary.
func clipReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "the storage gave no answer"
	}
	r := []rune(reason)
	if len(r) <= UnavailableReasonMax {
		return reason
	}
	return string(r[:UnavailableReasonMax-3]) + "..."
}
