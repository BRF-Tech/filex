package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ErrTemplateChanged: a template save named a revision that is no longer the
// saved one - somebody else saved in between (PutTemplate's ifRevision).
var ErrTemplateChanged = errors.New("the template was changed since it was opened")

// TemplateSQL implements the templates half of Store (migration 00137,
// internal/templates), written ONCE for every engine and embedded in each
// driver's Store - the arrangement of CaptchaMarkSQL. Times are unix seconds,
// so no engine's timestamp spelling is involved.
//
// ⚠ Every statement names tenant_id: a tenant's administrator reads and
// writes the rows of their own tenant, and the instance's rows are tenant 0.
// There is no method that answers rows across tenants.
type TemplateSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (p *TemplateSQL) q(query string) string { return rebind(p.Placeholders, query) }

const templateColumns = `id, tenant_id, tkey, subject, body, text_body, revision, updated_by, updated_at, last_error, last_error_at`

func scanTemplate(r RowScanner) (*model.Template, error) {
	var (
		t         model.Template
		by        sql.NullInt64
		at, errAt int64
	)
	if err := r.Scan(&t.ID, &t.TenantID, &t.Key, &t.Subject, &t.Body, &t.TextBody, &t.Revision, &by, &at, &t.LastError, &errAt); err != nil {
		return nil, err
	}
	if by.Valid {
		v := by.Int64
		t.UpdatedBy = &v
	}
	t.UpdatedAt = time.Unix(at, 0).UTC()
	if errAt > 0 {
		e := time.Unix(errAt, 0).UTC()
		t.LastErrorAt = &e
	}
	return &t, nil
}

// ListTemplates answers the templates one scope replaced (tenant 0: the
// install's), by name.
func (p *TemplateSQL) ListTemplates(ctx context.Context, tenantID int64) ([]*model.Template, error) {
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, p.q(
		`SELECT `+templateColumns+` FROM templates WHERE tenant_id=? ORDER BY tkey`), tenantID)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	defer rows.Close()
	var out []*model.Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("scan template: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTemplate answers one scope's replacement of key; (nil, nil) when it has
// none.
func (p *TemplateSQL) GetTemplate(ctx context.Context, tenantID int64, key string) (*model.Template, error) {
	t, err := scanTemplate(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT `+templateColumns+` FROM templates WHERE tenant_id=? AND tkey=?`), tenantID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get template: %w", err)
	}
	return t, nil
}

// PutTemplate saves t (TenantID, Key, Subject, Body, TextBody, UpdatedBy) and
// answers the saved row. ifRevision > 0 is the revision the editor opened:
// when the saved one is another, nothing is written and the answer is
// ErrTemplateChanged. ifRevision 0 saves whatever is there. A save clears the
// row's last error: it is a new template.
//
// ⚠ UPDATE-then-INSERT and not an upsert, because the upsert spellings differ
// per engine and this file is written once; two first saves racing on one
// name: one INSERT wins on the unique index, the other is told the template
// changed.
func (p *TemplateSQL) PutTemplate(ctx context.Context, t *model.Template, ifRevision int64) (*model.Template, error) {
	if t == nil || strings.TrimSpace(t.Key) == "" {
		return nil, errors.New("template: empty name")
	}
	now := time.Now().Unix()
	var by any
	if t.UpdatedBy != nil {
		by = *t.UpdatedBy
	}
	cur, err := p.GetTemplate(ctx, t.TenantID, t.Key)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		if ifRevision > 0 {
			return nil, ErrTemplateChanged
		}
		if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
			`INSERT INTO templates (tenant_id, tkey, subject, body, text_body, revision, updated_by, updated_at, last_error, last_error_at)
			 VALUES (?,?,?,?,?,1,?,?,'',0)`),
			t.TenantID, t.Key, t.Subject, t.Body, t.TextBody, by, now); err != nil {
			if again, gerr := p.GetTemplate(ctx, t.TenantID, t.Key); gerr == nil && again != nil {
				return nil, ErrTemplateChanged
			}
			return nil, fmt.Errorf("insert template: %w", err)
		}
		return p.GetTemplate(ctx, t.TenantID, t.Key)
	}
	if ifRevision > 0 && cur.Revision != ifRevision {
		return nil, ErrTemplateChanged
	}
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE templates SET subject=?, body=?, text_body=?, revision=?, updated_by=?, updated_at=?, last_error='', last_error_at=0
		 WHERE tenant_id=? AND tkey=? AND revision=?`),
		t.Subject, t.Body, t.TextBody, cur.Revision+1, by, now, t.TenantID, t.Key, cur.Revision)
	if err != nil {
		return nil, fmt.Errorf("update template: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrTemplateChanged
	}
	return p.GetTemplate(ctx, t.TenantID, t.Key)
}

// DeleteTemplate takes one scope's replacement away ("back to the default");
// ok = there was one.
func (p *TemplateSQL) DeleteTemplate(ctx context.Context, tenantID int64, key string) (bool, error) {
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM templates WHERE tenant_id=? AND tkey=?`), tenantID, key)
	if err != nil {
		return false, fmt.Errorf("delete template: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteTenantTemplates takes every replacement of one tenant away (the
// tenant is being deleted). Tenant 0, the install's own, is refused: that is
// not a tenant.
func (p *TemplateSQL) DeleteTenantTemplates(ctx context.Context, tenantID int64) (int64, error) {
	if tenantID <= 0 {
		return 0, errors.New("templates: not a tenant")
	}
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`DELETE FROM templates WHERE tenant_id=?`), tenantID)
	if err != nil {
		return 0, fmt.Errorf("delete tenant templates: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// NoteTemplateFailure records that revision of a template failed to render
// at `at` (the default was used instead). A row saved again since is not
// touched: its failure was the old text's.
func (p *TemplateSQL) NoteTemplateFailure(ctx context.Context, tenantID int64, key string, revision int64, msg string, at time.Time) error {
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	_, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE templates SET last_error=?, last_error_at=? WHERE tenant_id=? AND tkey=? AND revision=?`),
		msg, at.Unix(), tenantID, key, revision)
	if err != nil {
		return fmt.Errorf("note template failure: %w", err)
	}
	return nil
}
