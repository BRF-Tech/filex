package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// DraftSQL implements the drafts half of Store (migration 00064, issue #71),
// written ONCE for every engine. Each driver embeds a *DraftSQL in its Store,
// so the methods are promoted and there is no per-driver copy to drift — the
// arrangement of StorageOrderSQL and NodeDeletedBySQL, and for the same
// reason (the duplication gate in web/tests/quality).
//
// See db/migrations/sqlite/00064_drafts.sql for the model and
// internal/drafts for the rules; this only reads and writes the rows.
type DraftSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (d *DraftSQL) q(query string) string { return rebind(d.Placeholders, query) }

// draftSelect reads a draft joined to its file's node row. A LEFT JOIN: a row
// whose node went away without the cascade (an engine with foreign keys off)
// still reads, as not live. "Is the node live" is a boolean expression, not a
// CASE: every engine returns it as something database/sql scans into a bool.
const draftSelect = `SELECT d.id, d.draft_key, d.user_id, d.storage_id, d.node_id,
        d.target_dir, d.target_name, d.doc_type, d.created_at, d.updated_at,
        COALESCE(n.path, ''), COALESCE(n.size, 0), COALESCE(n.mime, ''), n.backend_mtime,
        (n.id IS NOT NULL AND n.deleted_at IS NULL)
   FROM drafts d
   LEFT JOIN nodes n ON n.id = d.node_id`

func scanDraft(r interface{ Scan(dst ...any) error }) (*model.Draft, error) {
	out := &model.Draft{}
	err := r.Scan(&out.ID, &out.Key, &out.UserID, &out.StorageID, &out.NodeID,
		&out.TargetDir, &out.TargetName, &out.DocType, &out.CreatedAt, &out.UpdatedAt,
		&out.Path, &out.Size, &out.Mime, &out.Mtime, &out.NodeLive)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CreateDraft inserts a draft row and returns it as the store reads it back.
func (d *DraftSQL) CreateDraft(ctx context.Context, in *model.Draft) (*model.Draft, error) {
	if in == nil || in.Key == "" || in.UserID == 0 || in.StorageID == 0 || in.NodeID == 0 || in.TargetName == "" {
		return nil, errors.New("draft: missing key, user, storage, node or name")
	}
	if _, err := Conn(ctx, d.Pool).ExecContext(ctx, d.q(
		`INSERT INTO drafts (draft_key, user_id, storage_id, node_id, target_dir, target_name, doc_type)
		 VALUES (?,?,?,?,?,?,?)`),
		in.Key, in.UserID, in.StorageID, in.NodeID, in.TargetDir, in.TargetName, in.DocType); err != nil {
		return nil, fmt.Errorf("insert draft: %w", err)
	}
	return d.GetDraftByKey(ctx, in.Key)
}

// GetDraftByKey returns the draft with that key, live or not.
func (d *DraftSQL) GetDraftByKey(ctx context.Context, key string) (*model.Draft, error) {
	return scanDraft(Conn(ctx, d.Pool).QueryRowContext(ctx, d.q(draftSelect+` WHERE d.draft_key=?`), key))
}

// GetDraftByNode returns the draft whose file is that node, live or not.
func (d *DraftSQL) GetDraftByNode(ctx context.Context, nodeID int64) (*model.Draft, error) {
	return scanDraft(Conn(ctx, d.Pool).QueryRowContext(ctx, d.q(draftSelect+` WHERE d.node_id=?`), nodeID))
}

// ListLiveDrafts returns a person's drafts whose file is live, the most
// recently made first (the client sorts by whatever column it likes).
func (d *DraftSQL) ListLiveDrafts(ctx context.Context, userID int64) ([]*model.Draft, error) {
	rows, err := Conn(ctx, d.Pool).QueryContext(ctx, d.q(draftSelect+
		` WHERE d.user_id=? AND n.id IS NOT NULL AND n.deleted_at IS NULL ORDER BY d.id DESC`), userID)
	if err != nil {
		return nil, fmt.Errorf("list drafts: %w", err)
	}
	defer rows.Close()
	var out []*model.Draft
	for rows.Next() {
		row, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// CountLiveDrafts counts a person's drafts whose file is live.
func (d *DraftSQL) CountLiveDrafts(ctx context.Context, userID int64) (int, error) {
	var n int
	err := Conn(ctx, d.Pool).QueryRowContext(ctx, d.q(
		`SELECT COUNT(*) FROM drafts d JOIN nodes n ON n.id = d.node_id
		  WHERE d.user_id=? AND n.deleted_at IS NULL`), userID).Scan(&n)
	return n, err
}

// DeleteDraft removes a draft row. Its file is the caller's business.
func (d *DraftSQL) DeleteDraft(ctx context.Context, id int64) error {
	_, err := Conn(ctx, d.Pool).ExecContext(ctx, d.q(`DELETE FROM drafts WHERE id=?`), id)
	return err
}

// NotInDraftsSQL is the WHERE condition "the node at column col is not inside
// the drafts area", in both path spellings the drivers store (`/a/b` and
// `a/b`). The storage's counts (StorageStats) leave drafts out the way they
// leave the trash out: a draft is somebody's unfinished document, not a file
// of the storage yet (issue #71).
//
// ⚠ Safe to splice: syspath.Drafts is plain — no `%`, `_`, `\` or quote — so
// the pattern matches only itself (syspath's tests hold every internal name to
// that, for notify's LIKE patterns).
func NotInDraftsSQL(col string) string {
	return "(" + col + " NOT LIKE '/" + syspath.Drafts + "/%' AND " +
		col + " NOT LIKE '" + syspath.Drafts + "/%')"
}
