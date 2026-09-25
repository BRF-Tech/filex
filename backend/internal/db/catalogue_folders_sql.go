package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ─────────────────── Lazy catalogue folders: the store methods ─────────────
//
// Migration 00059 (docs/LAZY-CATALOGUE.md). The catalogue_folders methods of
// db.Store, written ONCE: every engine runs the same statements, and the three
// ways they differ are the CatalogueDialect a driver hands in. Each driver
// embeds a *CatalogueFolderSQL in its Store, so the methods are promoted and
// there is no per-driver copy to drift.
//
// ⚠ It used to be two files, one per driver, with the same Go around two
// spellings of the SQL; the duplication gate (web/tests/quality) caught four
// verbatim blocks between them. Anything engine-specific belongs in the
// dialect, not in a second copy of a method.

// CatalogueDialect is what the engines disagree on for these statements.
// A nil function is the identity (SQLite's own spelling).
type CatalogueDialect struct {
	// Placeholders turns a statement written with `?` into the engine's
	// (PostgreSQL: $1, $2 …; DollarPlaceholders).
	Placeholders func(q string) string
	// Upsert rewrites SQLite's `ON CONFLICT (…) DO UPDATE SET` tail for the
	// engine (MySQL: ON DUPLICATE KEY UPDATE). PostgreSQL speaks SQLite's.
	Upsert func(q string) string
	// Time binds a timestamp (nil for NULL): CatalogueTime for SQLite and
	// MySQL, which compare stored timestamps as text; PlainTime for
	// PostgreSQL.
	Time func(t *time.Time) any
}

// CatalogueFolderSQL implements the catalogue_folders half of db.Store.
type CatalogueFolderSQL struct {
	DB      *sql.DB
	Dialect CatalogueDialect
}

func (c *CatalogueFolderSQL) q(query string) string {
	if c.Dialect.Placeholders != nil {
		return c.Dialect.Placeholders(query)
	}
	return query
}

func (c *CatalogueFolderSQL) upsert(query string) string {
	if c.Dialect.Upsert != nil {
		query = c.Dialect.Upsert(query)
	}
	return c.q(query)
}

func (c *CatalogueFolderSQL) t(v *time.Time) any {
	if c.Dialect.Time != nil {
		return c.Dialect.Time(v)
	}
	return CatalogueTime(v)
}

// DollarPlaceholders numbers every `?` of a statement as PostgreSQL's $N.
// ⚠ For statements with no `?` inside a string literal — true of every
// statement in this file.
func DollarPlaceholders(q string) string {
	var b strings.Builder
	b.Grow(len(q) + 8)
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// PlainTime binds a timestamp as the instant it is, in UTC (PostgreSQL).
func PlainTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC()
}

func (c *CatalogueFolderSQL) GetCatalogueFolder(ctx context.Context, storageID int64, pathHash string) (*model.CatalogueFolder, error) {
	f, err := ScanCatalogueFolder(Conn(ctx, c.DB).QueryRowContext(ctx,
		c.q(`SELECT `+CatalogueFolderColumns+` FROM catalogue_folders WHERE storage_id=? AND path_hash=?`),
		storageID, pathHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return f, err
}

// discoverChunk bounds one multi-row INSERT: well under every engine's
// placeholder limit, and a folder with 50 000 subfolders is still a few
// hundred statements, not 50 000.
const discoverChunk = 200

func (c *CatalogueFolderSQL) DiscoverCatalogueFolders(ctx context.Context, storageID int64, folders []model.CatalogueFolder) error {
	for start := 0; start < len(folders); start += discoverChunk {
		end := min(start+discoverChunk, len(folders))
		var b strings.Builder
		b.WriteString(`INSERT INTO catalogue_folders (storage_id, path_hash, path, depth, state) VALUES `)
		args := make([]any, 0, 5*(end-start))
		for i, f := range folders[start:end] {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("(?,?,?,?,?)")
			args = append(args, storageID, f.PathHash, f.Path, f.Depth, string(model.FolderUncatalogued))
		}
		// A no-op update rather than DO NOTHING: the MySQL rewrite knows the
		// DO UPDATE shape, and "keep the row that is there" is what every
		// engine spells with it.
		b.WriteString(` ON CONFLICT(storage_id, path_hash) DO UPDATE SET path_hash=excluded.path_hash`)
		if _, err := Conn(ctx, c.DB).ExecContext(ctx, c.upsert(b.String()), args...); err != nil {
			return err
		}
	}
	return nil
}

func (c *CatalogueFolderSQL) RecordCatalogueFolder(ctx context.Context, f *model.CatalogueFolder) error {
	_, err := Conn(ctx, c.DB).ExecContext(ctx, c.upsert(`INSERT INTO catalogue_folders
		 (storage_id, path_hash, path, depth, state, reconciled_at, visited_at, watched_at, reconcile_on_open, entries, held_back)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(storage_id, path_hash) DO UPDATE SET path=excluded.path, depth=excluded.depth,
		   state=excluded.state, reconciled_at=excluded.reconciled_at, visited_at=excluded.visited_at,
		   watched_at=excluded.watched_at, reconcile_on_open=excluded.reconcile_on_open,
		   entries=excluded.entries, held_back=excluded.held_back`),
		f.StorageID, f.PathHash, f.Path, f.Depth, string(f.State),
		c.t(f.ReconciledAt), c.t(f.VisitedAt), c.t(f.WatchedAt),
		f.ReconcileOnOpen, f.Entries, f.HeldBack)
	return err
}

func (c *CatalogueFolderSQL) SetCatalogueFolderWatch(ctx context.Context, storageID int64, pathHash string, watchedAt *time.Time, reconcileOnOpen bool) error {
	state := model.FolderCatalogued
	if watchedAt != nil {
		state = model.FolderWatched
	}
	_, err := Conn(ctx, c.DB).ExecContext(ctx,
		c.q(`UPDATE catalogue_folders SET state=?, watched_at=?, reconcile_on_open=?
		 WHERE storage_id=? AND path_hash=? AND state<>?`),
		string(state), c.t(watchedAt), reconcileOnOpen, storageID, pathHash, string(model.FolderUncatalogued))
	return err
}

func (c *CatalogueFolderSQL) TouchCatalogueFolderVisit(ctx context.Context, storageID int64, pathHash string, at time.Time) error {
	_, err := Conn(ctx, c.DB).ExecContext(ctx,
		c.q(`UPDATE catalogue_folders SET visited_at=? WHERE storage_id=? AND path_hash=?`),
		c.t(&at), storageID, pathHash)
	return err
}

func (c *CatalogueFolderSQL) ListCatalogueFolders(ctx context.Context, storageID int64, f model.CatalogueFolderFilter) ([]*model.CatalogueFolder, error) {
	q := `SELECT ` + CatalogueFolderColumns + ` FROM catalogue_folders WHERE storage_id=?`
	args := []any{storageID}
	if f.State != "" {
		q += ` AND state=?`
		args = append(args, string(f.State))
	}
	if f.Under != "" {
		if lo, hi, ok := CatalogueSubtreeRange(f.Under); ok {
			q += ` AND (path=? OR (path>? AND path<?))`
			args = append(args, CatalogueFolderPath(f.Under), lo, hi)
		}
	}
	order := ` ORDER BY depth, path`
	if f.ReconciledBefore != nil {
		q += ` AND state=? AND reconciled_at IS NOT NULL AND reconciled_at<?`
		args = append(args, string(model.FolderCatalogued), c.t(f.ReconciledBefore))
		order = ` ORDER BY reconciled_at, depth, path`
	}
	q += order
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := Conn(ctx, c.DB).QueryContext(ctx, c.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.CatalogueFolder{}
	for rows.Next() {
		row, err := ScanCatalogueFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (c *CatalogueFolderSQL) CountCatalogueFolders(ctx context.Context, storageID int64) (model.CatalogueCounts, error) {
	var out model.CatalogueCounts
	rows, err := Conn(ctx, c.DB).QueryContext(ctx,
		c.q(`SELECT state, COUNT(*), SUM(CASE WHEN held_back>0 THEN 1 ELSE 0 END) FROM catalogue_folders WHERE storage_id=? GROUP BY state`),
		storageID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			state string
			n     int64
			held  sql.NullInt64
		)
		if err := rows.Scan(&state, &n, &held); err != nil {
			return out, err
		}
		switch model.CatalogueFolderState(state) {
		case model.FolderUncatalogued:
			out.Uncatalogued = n
		case model.FolderCatalogued:
			out.Catalogued = n
		case model.FolderWatched:
			out.Watched = n
		}
		out.HeldBack += held.Int64
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	var rootState string
	err = Conn(ctx, c.DB).QueryRowContext(ctx,
		c.q(`SELECT state FROM catalogue_folders WHERE storage_id=? AND path=? AND reconciled_at IS NOT NULL`),
		storageID, "/").Scan(&rootState)
	switch {
	case err == nil:
		out.RootCatalogued = rootState != string(model.FolderUncatalogued)
	case !errors.Is(err, sql.ErrNoRows):
		return out, err
	}
	return out, nil
}

func (c *CatalogueFolderSQL) ResetCatalogueWatches(ctx context.Context, storageID int64) (int64, error) {
	res, err := Conn(ctx, c.DB).ExecContext(ctx,
		c.q(`UPDATE catalogue_folders SET state=?, watched_at=NULL, reconcile_on_open=? WHERE storage_id=? AND state=?`),
		string(model.FolderCatalogued), true, storageID, string(model.FolderWatched))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (c *CatalogueFolderSQL) DeleteCatalogueFoldersUnder(ctx context.Context, storageID int64, dir string) error {
	p := CatalogueFolderPath(dir)
	lo, hi, ok := CatalogueSubtreeRange(p)
	if !ok {
		_, err := Conn(ctx, c.DB).ExecContext(ctx, c.q(`DELETE FROM catalogue_folders WHERE storage_id=?`), storageID)
		return err
	}
	_, err := Conn(ctx, c.DB).ExecContext(ctx,
		c.q(`DELETE FROM catalogue_folders WHERE storage_id=? AND (path=? OR (path>? AND path<?))`),
		storageID, p, lo, hi)
	return err
}

func (c *CatalogueFolderSQL) HasUncataloguedUnder(ctx context.Context, storageID int64, dir string) (bool, error) {
	q := `SELECT 1 FROM catalogue_folders WHERE storage_id=? AND state=?`
	args := []any{storageID, string(model.FolderUncatalogued)}
	if lo, hi, ok := CatalogueSubtreeRange(dir); ok {
		q += ` AND path>? AND path<?`
		args = append(args, lo, hi)
	} else {
		q += ` AND path<>?`
		args = append(args, "/")
	}
	var one int
	err := Conn(ctx, c.DB).QueryRowContext(ctx, c.q(q+` LIMIT 1`), args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
