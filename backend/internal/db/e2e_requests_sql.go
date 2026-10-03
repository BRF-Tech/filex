package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// E2ERequestSQL implements the e2e_requests half of Store (migration 00080,
// internal/e2epolicy): a person's request to encrypt in one folder where the
// tenant's policy wants an administrator's approval first. Written ONCE for
// every engine and embedded in each driver's Store — the arrangement of
// PluginRequestSQL, whose helpers it uses (int64Arg, nullInt64Ptr, and
// catalogue_scan.go's nullTimePtr) instead of carrying copies: the
// duplication gate in web/tests/quality reads Go too.
//
// It only reads and writes rows. What a request means, and which state may
// follow which, is internal/e2epolicy's and the requests service's.
type E2ERequestSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
	// Time binds a timestamp: CatalogueTime for SQLite and MySQL (they compare
	// stored timestamps as text), PlainTime for PostgreSQL. Nil = CatalogueTime.
	Time func(t *time.Time) any
}

func (p *E2ERequestSQL) q(query string) string { return rebind(p.Placeholders, query) }

func (p *E2ERequestSQL) t(v *time.Time) any {
	if p.Time == nil {
		return CatalogueTime(v)
	}
	return p.Time(v)
}

// e2eRequestSelect reads every column but request_key, which exists only so
// CreateE2ERequest can find the row it has just written on an engine with no
// LastInsertId (PostgreSQL) and no RETURNING (MySQL).
const e2eRequestSelect = `SELECT id, provider_id, user_id, requester, storage_id, path, kind, reason, status,
        decided_by, decider, decided_at, decision_note, expires_at, used_at, created_at, updated_at
   FROM e2e_requests`

// maxE2ERequestList caps one ListE2ERequests.
const maxE2ERequestList = 500

func scanE2ERequest(row interface{ Scan(dst ...any) error }) (*model.E2ERequest, error) {
	r := &model.E2ERequest{}
	var provider, decidedBy sql.NullInt64
	var decidedAt, usedAt sql.NullTime
	if err := row.Scan(&r.ID, &provider, &r.UserID, &r.Requester, &r.StorageID, &r.Path, &r.Kind, &r.Reason, &r.Status,
		&decidedBy, &r.Decider, &decidedAt, &r.DecisionNote, &r.ExpiresAt, &usedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.ProviderID, r.DecidedBy = nullInt64Ptr(provider), nullInt64Ptr(decidedBy)
	r.DecidedAt, r.UsedAt = nullTimePtr(decidedAt), nullTimePtr(usedAt)
	r.ExpiresAt, r.CreatedAt, r.UpdatedAt = r.ExpiresAt.UTC(), r.CreatedAt.UTC(), r.UpdatedAt.UTC()
	return r, nil
}

// one reads the single request `tail` (a WHERE clause) picks, sql.ErrNoRows
// when there is none.
func (p *E2ERequestSQL) one(ctx context.Context, tail string, args ...any) (*model.E2ERequest, error) {
	return scanE2ERequest(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(e2eRequestSelect+tail), args...))
}

// CreateE2ERequest inserts a request and returns it as the store reads it
// back. A request without a status is pending; a new request is undecided,
// so the decision fields are not written (UpdateE2ERequest writes them).
func (p *E2ERequestSQL) CreateE2ERequest(ctx context.Context, in *model.E2ERequest) (*model.E2ERequest, error) {
	switch {
	case in == nil || in.UserID == 0 || in.StorageID == 0:
		return nil, errors.New("e2e request: missing person or storage")
	case in.Kind != model.E2ERequestFolder && in.Kind != model.E2ERequestFile:
		return nil, fmt.Errorf("e2e request: kind %q is neither folder nor file", in.Kind)
	case in.ExpiresAt.IsZero():
		return nil, errors.New("e2e request: missing expiry")
	}
	status := in.Status
	if status == "" {
		status = model.E2ERequestPending
	}
	key, now, expires := rand.Text(), time.Now().UTC(), in.ExpiresAt.UTC()
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(`INSERT INTO e2e_requests
		  (request_key, provider_id, user_id, requester, storage_id, path, kind, reason, status, expires_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`),
		key, int64Arg(in.ProviderID), in.UserID, in.Requester, in.StorageID, in.Path, in.Kind, in.Reason, status,
		p.t(&expires), p.t(&now), p.t(&now)); err != nil {
		return nil, fmt.Errorf("insert e2e request: %w", err)
	}
	return p.one(ctx, ` WHERE request_key=?`, key)
}

// GetE2ERequest returns one request, sql.ErrNoRows when there is none.
func (p *E2ERequestSQL) GetE2ERequest(ctx context.Context, id int64) (*model.E2ERequest, error) {
	return p.one(ctx, ` WHERE id=?`, id)
}

// ListE2ERequests returns the requests f matches, the newest first, at most
// f.Limit of them (<= 0 or over 500: 500). With f.ExpiresBefore it returns
// what is due at that instant, the longest overdue first (a tie by id): the
// order a sweep reads in, a batch at a time.
func (p *E2ERequestSQL) ListE2ERequests(ctx context.Context, f model.E2ERequestFilter) ([]*model.E2ERequest, error) {
	var conds []string
	var args []any
	where := func(cond string, v any) {
		conds = append(conds, cond)
		args = append(args, v)
	}
	if f.ProviderID != nil {
		where("provider_id=?", *f.ProviderID)
	}
	if f.UserID != nil {
		where("user_id=?", *f.UserID)
	}
	if f.Status != "" {
		where("status=?", f.Status)
	}
	order := ` ORDER BY id DESC`
	if !f.ExpiresBefore.IsZero() {
		due := f.ExpiresBefore.UTC()
		where("expires_at<=?", p.t(&due))
		order = ` ORDER BY expires_at, id`
	}
	query := e2eRequestSelect
	if len(conds) > 0 {
		query += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	limit := f.Limit
	if limit <= 0 || limit > maxE2ERequestList {
		limit = maxE2ERequestList
	}
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, p.q(query+order+` LIMIT ?`), append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("list e2e requests: %w", err)
	}
	defer rows.Close()
	out := []*model.E2ERequest{}
	for rows.Next() {
		r, err := scanE2ERequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FindApprovedE2ERequest returns an approval the person may still use for an
// encryption of kind at path on that storage: approved, and not yet expired
// at now. The one that expires first wins when there are several.
// sql.ErrNoRows when there is none. Finding it does not use it up —
// UpdateE2ERequest with onlyIfStatus approved does, once.
//
// ⚠ path is compared exactly, byte for byte (MySQL's column is binary for that
// reason): callers spell it as internal/e2epolicy.ApprovalPath does.
func (p *E2ERequestSQL) FindApprovedE2ERequest(ctx context.Context, userID, storageID int64, path, kind string, now time.Time) (*model.E2ERequest, error) {
	at := now.UTC()
	return p.one(ctx, ` WHERE user_id=? AND storage_id=? AND path=? AND kind=? AND status=? AND expires_at>?
		ORDER BY expires_at, id LIMIT 1`,
		userID, storageID, path, kind, model.E2ERequestApproved, p.t(&at))
}

// UpdateE2ERequest writes a request's state: the status, who decided and
// when, the note, the expiry (an approval's own seven days start at the
// decision) and when it was used. With onlyIfStatus the write happens only
// while the stored row is still in that state, and ok reports whether it did:
// two administrators deciding one request, or two writes spending one
// approval, cannot both win.
//
// ⚠ MySQL counts CHANGED rows, so a write that changes nothing reports false.
// Every transition changes the status, so a real one never does.
func (p *E2ERequestSQL) UpdateE2ERequest(ctx context.Context, r *model.E2ERequest, onlyIfStatus string) (bool, error) {
	if r == nil || r.ID == 0 || r.ExpiresAt.IsZero() {
		return false, errors.New("e2e request: missing id or expiry")
	}
	now, expires := time.Now().UTC(), r.ExpiresAt.UTC()
	stmt := `UPDATE e2e_requests SET status=?, decided_by=?, decider=?, decided_at=?, decision_note=?,
	           expires_at=?, used_at=?, updated_at=? WHERE id=?`
	args := []any{r.Status, int64Arg(r.DecidedBy), r.Decider, p.t(r.DecidedAt), r.DecisionNote,
		p.t(&expires), p.t(r.UsedAt), p.t(&now), r.ID}
	if onlyIfStatus != "" {
		stmt += ` AND status=?`
		args = append(args, onlyIfStatus)
	}
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(stmt), args...)
	if err != nil {
		return false, fmt.Errorf("update e2e request: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
