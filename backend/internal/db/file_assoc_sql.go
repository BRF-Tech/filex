package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// FileAssocSQL implements the file-association half of Store (migration
// 00077, internal/assoc): the administrator's rule per kind and capability,
// and the per-app thumbnail limits. Written ONCE for every engine and
// embedded in each driver's Store - the arrangement of PluginRequestSQL, for
// the same reason (the duplication gate in web/tests/quality).
//
// ⚠ An upsert is a delete and an insert in one transaction rather than each
// engine's own ON CONFLICT / ON DUPLICATE KEY: the three spellings differ,
// and a table this small does not need them.
type FileAssocSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
	// Time binds a timestamp: CatalogueTime for SQLite and MySQL, PlainTime
	// for PostgreSQL. Nil = CatalogueTime.
	Time func(t *time.Time) any
}

func (f *FileAssocSQL) q(query string) string { return rebind(f.Placeholders, query) }

func (f *FileAssocSQL) t(v *time.Time) any {
	if f.Time != nil {
		return f.Time(v)
	}
	return CatalogueTime(v)
}

func jsonList(s string) []string {
	out := []string{}
	if s == "" {
		return out
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

func listJSON(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// ListFileAssociations returns every rule, by capability then kind.
func (f *FileAssocSQL) ListFileAssociations(ctx context.Context) ([]*model.FileAssociation, error) {
	rows, err := Conn(ctx, f.Pool).QueryContext(ctx, f.q(
		`SELECT capability, ext, handlers_json, off_json, updated_by, updated_at FROM file_associations ORDER BY capability, ext`))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.FileAssociation{}
	for rows.Next() {
		var (
			a         model.FileAssociation
			handlers  string
			off       string
			updatedBy sql.NullInt64
		)
		if err := rows.Scan(&a.Capability, &a.Ext, &handlers, &off, &updatedBy, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.Handlers, a.Off = jsonList(handlers), jsonList(off)
		a.UpdatedBy = nullInt64Ptr(updatedBy)
		a.UpdatedAt = a.UpdatedAt.UTC()
		out = append(out, &a)
	}
	return out, rows.Err()
}

// PutFileAssociation creates or replaces the rule for (Capability, Ext).
func (f *FileAssocSQL) PutFileAssociation(ctx context.Context, a *model.FileAssociation) error {
	if a == nil || a.Capability == "" || a.Ext == "" {
		return errors.New("file association: capability and ext are required")
	}
	now := time.Now().UTC()
	return RunInTx(ctx, f.Pool, func(ctx context.Context) error {
		c := Conn(ctx, f.Pool)
		if _, err := c.ExecContext(ctx, f.q(`DELETE FROM file_associations WHERE capability=? AND ext=?`), a.Capability, a.Ext); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, f.q(
			`INSERT INTO file_associations (capability, ext, handlers_json, off_json, updated_by, updated_at) VALUES (?,?,?,?,?,?)`),
			a.Capability, a.Ext, listJSON(a.Handlers), listJSON(a.Off), int64Arg(a.UpdatedBy), f.t(&now)); err != nil {
			return fmt.Errorf("insert file association: %w", err)
		}
		return nil
	})
}

// DeleteFileAssociation removes the rule for (capability, ext): the kind
// keeps the default order again. ok = there was one.
func (f *FileAssocSQL) DeleteFileAssociation(ctx context.Context, capability, ext string) (bool, error) {
	res, err := Conn(ctx, f.Pool).ExecContext(ctx, f.q(`DELETE FROM file_associations WHERE capability=? AND ext=?`), capability, ext)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// GetAppThumbLimits answers (nil, nil) when the app has none stored.
func (f *FileAssocSQL) GetAppThumbLimits(ctx context.Context, pluginID int64) (*model.AppThumbLimits, error) {
	var l model.AppThumbLimits
	err := Conn(ctx, f.Pool).QueryRowContext(ctx, f.q(
		`SELECT plugin_id, max_input_mb, timeout_s, memory_mb, concurrency, updated_at FROM app_thumb_limits WHERE plugin_id=?`), pluginID).
		Scan(&l.PluginID, &l.MaxInputMB, &l.TimeoutS, &l.MemoryMB, &l.Concurrency, &l.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	l.UpdatedAt = l.UpdatedAt.UTC()
	return &l, nil
}

// PutAppThumbLimits creates or replaces one app's limits.
func (f *FileAssocSQL) PutAppThumbLimits(ctx context.Context, l *model.AppThumbLimits) error {
	if l == nil || l.PluginID <= 0 {
		return errors.New("thumbnail limits: plugin id is required")
	}
	now := time.Now().UTC()
	return RunInTx(ctx, f.Pool, func(ctx context.Context) error {
		c := Conn(ctx, f.Pool)
		if _, err := c.ExecContext(ctx, f.q(`DELETE FROM app_thumb_limits WHERE plugin_id=?`), l.PluginID); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, f.q(
			`INSERT INTO app_thumb_limits (plugin_id, max_input_mb, timeout_s, memory_mb, concurrency, updated_at) VALUES (?,?,?,?,?,?)`),
			l.PluginID, l.MaxInputMB, l.TimeoutS, l.MemoryMB, l.Concurrency, f.t(&now)); err != nil {
			return fmt.Errorf("insert thumbnail limits: %w", err)
		}
		return nil
	})
}

// DeleteAppThumbLimits removes one app's limits (the app is removed).
func (f *FileAssocSQL) DeleteAppThumbLimits(ctx context.Context, pluginID int64) error {
	_, err := Conn(ctx, f.Pool).ExecContext(ctx, f.q(`DELETE FROM app_thumb_limits WHERE plugin_id=?`), pluginID)
	return err
}

// ThumbnailGenerators counts the ready thumbnails of live files by who drew
// them, within storageIDs (nil: every storage, empty: none).
func (f *FileAssocSQL) ThumbnailGenerators(ctx context.Context, storageIDs []int64) (map[string]int64, error) {
	out := map[string]int64{}
	if storageIDs != nil && len(storageIDs) == 0 {
		return out, nil
	}
	q := `SELECT COALESCE(t.generator,''), COUNT(*) FROM thumbnails t JOIN nodes n ON n.id = t.node_id
		WHERE t.state = 'ready' AND n.deleted_at IS NULL`
	args := []any{}
	if storageIDs != nil {
		q += ` AND n.storage_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(storageIDs)), ",") + `)`
		for _, id := range storageIDs {
			args = append(args, id)
		}
	}
	q += ` GROUP BY COALESCE(t.generator,'')`
	rows, err := Conn(ctx, f.Pool).QueryContext(ctx, f.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g string
		var n int64
		if err := rows.Scan(&g, &n); err != nil {
			return nil, err
		}
		out[g] = n
	}
	return out, rows.Err()
}
