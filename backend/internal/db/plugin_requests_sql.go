package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// PluginRequestSQL implements the plugin_requests half of Store (migration
// 00070, internal/pluginreq), written ONCE for every engine and embedded in
// each driver's Store — the arrangement of DraftSQL, for the same reason (the
// duplication gate in web/tests/quality).
//
// It only reads and writes rows; what a request means, and when one may move
// from one state to another, is internal/pluginreq's.
type PluginRequestSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
	// Time binds a timestamp: CatalogueTime for SQLite and MySQL (they compare
	// stored timestamps as text), PlainTime for PostgreSQL. Nil = CatalogueTime.
	Time func(t *time.Time) any
}

func (p *PluginRequestSQL) q(query string) string { return rebind(p.Placeholders, query) }

func (p *PluginRequestSQL) t(v *time.Time) any {
	if p.Time != nil {
		return p.Time(v)
	}
	return CatalogueTime(v)
}

const pluginRequestCols = `id, request_key, kind, op, name, plugin_id, source_kind, source_json, source_key,
        version, from_version, manifest_json, review_json, sha256, manifest_sha256, permissions_json,
        requested_by, requester, token_id, token_label, reason, status,
        decided_by, decider, decided_at, decision_note, result_json, expires_at, created_at, updated_at`

func scanPluginRequest(r interface{ Scan(dst ...any) error }) (*model.PluginRequest, error) {
	var (
		out                          model.PluginRequest
		pluginID, by, tokID, decided sql.NullInt64
		decidedAt                    sql.NullTime
	)
	if err := r.Scan(&out.ID, &out.Key, &out.Kind, &out.Op, &out.Name, &pluginID, &out.SourceKind, &out.SourceJSON, &out.SourceKey,
		&out.Version, &out.FromVersion, &out.ManifestJSON, &out.ReviewJSON, &out.SHA256, &out.ManifestSHA256, &out.PermissionsJSON,
		&by, &out.Requester, &tokID, &out.TokenLabel, &out.Reason, &out.Status,
		&decided, &out.Decider, &decidedAt, &out.DecisionNote, &out.ResultJSON, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return nil, err
	}
	out.PluginID = nullInt64Ptr(pluginID)
	out.RequestedBy = nullInt64Ptr(by)
	out.TokenID = nullInt64Ptr(tokID)
	out.DecidedBy = nullInt64Ptr(decided)
	out.DecidedAt = nullTimePtr(decidedAt)
	out.ExpiresAt = out.ExpiresAt.UTC()
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return &out, nil
}

func nullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

func int64Arg(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// CreatePluginRequest inserts a request and returns it as the store reads it
// back. A request without a key is given one.
func (p *PluginRequestSQL) CreatePluginRequest(ctx context.Context, in *model.PluginRequest) (*model.PluginRequest, error) {
	if in == nil || in.Kind == "" || in.Op == "" || in.SourceKind == "" || in.SourceKey == "" || in.ExpiresAt.IsZero() {
		return nil, errors.New("plugin request: missing kind, op, source or expiry")
	}
	key := in.Key
	if key == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		key = hex.EncodeToString(b)
	}
	status := in.Status
	if status == "" {
		status = model.PluginRequestPending
	}
	perms := in.PermissionsJSON
	if perms == "" {
		perms = "[]"
	}
	src := in.SourceJSON
	if src == "" {
		src = "{}"
	}
	now := time.Now().UTC()
	expires := in.ExpiresAt.UTC()
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO plugin_requests (request_key, kind, op, name, plugin_id, source_kind, source_json, source_key,
		   version, from_version, manifest_json, review_json, sha256, manifest_sha256, permissions_json,
		   requested_by, requester, token_id, token_label, reason, status, expires_at, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
		key, in.Kind, in.Op, in.Name, int64Arg(in.PluginID), in.SourceKind, src, in.SourceKey,
		in.Version, in.FromVersion, in.ManifestJSON, in.ReviewJSON, in.SHA256, in.ManifestSHA256, perms,
		int64Arg(in.RequestedBy), in.Requester, int64Arg(in.TokenID), in.TokenLabel, in.Reason, status,
		p.t(&expires), p.t(&now), p.t(&now)); err != nil {
		return nil, fmt.Errorf("insert plugin request: %w", err)
	}
	return scanPluginRequest(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT `+pluginRequestCols+` FROM plugin_requests WHERE request_key=?`), key))
}

// GetPluginRequest returns one request, sql.ErrNoRows when there is none.
func (p *PluginRequestSQL) GetPluginRequest(ctx context.Context, id int64) (*model.PluginRequest, error) {
	return scanPluginRequest(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT `+pluginRequestCols+` FROM plugin_requests WHERE id=?`), id))
}

// ListPluginRequests returns the requests in one state ("" = every state), the
// newest first, at most limit of them (<= 0: 500).
func (p *PluginRequestSQL) ListPluginRequests(ctx context.Context, status string, limit int) ([]*model.PluginRequest, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	query := `SELECT ` + pluginRequestCols + ` FROM plugin_requests`
	args := []any{}
	if status != "" {
		query += ` WHERE status=?`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, p.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.PluginRequest{}
	for rows.Next() {
		r, err := scanPluginRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PendingPluginRequestBySource returns the pending request for a source key,
// sql.ErrNoRows when there is none. The newest wins if a race left two.
func (p *PluginRequestSQL) PendingPluginRequestBySource(ctx context.Context, sourceKey string) (*model.PluginRequest, error) {
	return scanPluginRequest(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT `+pluginRequestCols+` FROM plugin_requests WHERE source_key=? AND status=? ORDER BY id DESC LIMIT 1`),
		sourceKey, model.PluginRequestPending))
}

// UpdatePluginRequest writes a request's decision fields — status, who
// decided and when, the note and the result. With onlyIfPending the write
// happens only while the stored row is still pending, and ok reports whether
// it did: two administrators closing one request cannot both win.
func (p *PluginRequestSQL) UpdatePluginRequest(ctx context.Context, r *model.PluginRequest, onlyIfPending bool) (bool, error) {
	if r == nil || r.ID == 0 {
		return false, errors.New("plugin request: missing id")
	}
	now := time.Now().UTC()
	query := `UPDATE plugin_requests SET status=?, decided_by=?, decider=?, decided_at=?, decision_note=?, result_json=?, updated_at=?
	           WHERE id=?`
	args := []any{r.Status, int64Arg(r.DecidedBy), r.Decider, p.t(r.DecidedAt), r.DecisionNote, r.ResultJSON, p.t(&now), r.ID}
	if onlyIfPending {
		query += ` AND status=?`
		args = append(args, model.PluginRequestPending)
	}
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(query), args...)
	if err != nil {
		return false, fmt.Errorf("update plugin request: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
