package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TenantAuthSQL implements the tenant self-service half of Store (migration
// 00076, docs/TENANT-ADMIN.md): sign-in provider instances, which tenant
// signs in through which, a tenant's own domains, and the operator's
// per-tenant switch for insecure providers. Written ONCE for every engine and
// embedded in each driver's Store, the arrangement of GroupSQL.
//
// Every statement runs on Conn(ctx, pool), so a caller's RunInTx holds it
// too; nothing here opens a transaction of its own except where two
// statements must agree (BindAuthInstance), and that one joins the caller's.
type TenantAuthSQL struct {
	conn *sql.DB
	// dollar rebinds `?` to `$n` and binds booleans and timestamps the way
	// PostgreSQL takes them.
	dollar bool
}

// NewTenantAuthSQL returns the store for conn; dollar for PostgreSQL.
func NewTenantAuthSQL(conn *sql.DB, dollar bool) *TenantAuthSQL {
	return &TenantAuthSQL{conn: conn, dollar: dollar}
}

func (s *TenantAuthSQL) q(query string) string {
	if s.dollar {
		return DollarPlaceholders(query)
	}
	return query
}

func (s *TenantAuthSQL) db(ctx context.Context) Querier { return Conn(ctx, s.conn) }

func (s *TenantAuthSQL) b(v bool) any {
	if s.dollar {
		return v
	}
	if v {
		return 1
	}
	return 0
}

func (s *TenantAuthSQL) t(v *time.Time) any {
	if s.dollar {
		return PlainTime(v)
	}
	return CatalogueTime(v)
}

func (s *TenantAuthSQL) now() string {
	if s.dollar {
		return "NOW()"
	}
	return "CURRENT_TIMESTAMP"
}

func (s *TenantAuthSQL) insert(ctx context.Context, stmt string, args ...any) (int64, error) {
	ex := s.db(ctx)
	if s.dollar {
		var id int64
		err := ex.QueryRowContext(ctx, s.q(stmt+" RETURNING id"), args...).Scan(&id)
		return id, err
	}
	res, err := ex.ExecContext(ctx, stmt, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ── sign-in provider instances ──────────────────────────────────────────────

const authInstanceColumns = `id, slug, driver, label, origin, owner_provider_id, enabled, legacy, config_json, promoted_json, created_by, created_at, updated_at`

func scanAuthInstance(r RowScanner) (*model.AuthInstance, error) {
	a := &model.AuthInstance{}
	var owner, createdBy sql.NullInt64
	var promoted string
	if err := r.Scan(&a.ID, &a.Slug, &a.Driver, &a.Label, &a.Origin, &owner, &a.Enabled, &a.Legacy,
		&a.ConfigJSON, &promoted, &createdBy, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.OwnerProviderID = nullInt(owner)
	a.CreatedBy = nullInt(createdBy)
	if promoted != "" {
		if err := json.Unmarshal([]byte(promoted), &a.Promoted); err != nil {
			return nil, fmt.Errorf("auth instance %d promoted_json: %w", a.ID, err)
		}
	}
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	return a, nil
}

func encodePromoted(p []int64) string {
	if len(p) == 0 {
		return "[]"
	}
	cp := append([]int64(nil), p...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	b, _ := json.Marshal(cp)
	return string(b)
}

func configOrEmpty(c string) string {
	if strings.TrimSpace(c) == "" {
		return "{}"
	}
	return c
}

// ListAuthInstances returns every instance, in id order.
func (s *TenantAuthSQL) ListAuthInstances(ctx context.Context) ([]*model.AuthInstance, error) {
	rows, err := s.db(ctx).QueryContext(ctx, `SELECT `+authInstanceColumns+` FROM auth_instances ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.AuthInstance
	for rows.Next() {
		a, err := scanAuthInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAuthInstance returns one instance, or (nil, nil).
func (s *TenantAuthSQL) GetAuthInstance(ctx context.Context, id int64) (*model.AuthInstance, error) {
	a, err := scanAuthInstance(s.db(ctx).QueryRowContext(ctx, s.q(`SELECT `+authInstanceColumns+` FROM auth_instances WHERE id=?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// GetAuthInstanceBySlug returns one instance by its slug, or (nil, nil).
func (s *TenantAuthSQL) GetAuthInstanceBySlug(ctx context.Context, slug string) (*model.AuthInstance, error) {
	a, err := scanAuthInstance(s.db(ctx).QueryRowContext(ctx, s.q(`SELECT `+authInstanceColumns+` FROM auth_instances WHERE slug=?`), slug))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// CreateAuthInstance inserts an instance. A slug another instance has is the
// unique index's refusal.
func (s *TenantAuthSQL) CreateAuthInstance(ctx context.Context, a *model.AuthInstance) (*model.AuthInstance, error) {
	if a == nil || strings.TrimSpace(a.Slug) == "" || strings.TrimSpace(a.Driver) == "" {
		return nil, errors.New("auth instance: slug and driver are required")
	}
	origin := a.Origin
	if origin == "" {
		origin = model.AuthOriginPage
	}
	var owner, createdBy any
	if a.OwnerProviderID != nil {
		owner = *a.OwnerProviderID
	}
	if a.CreatedBy != nil {
		createdBy = *a.CreatedBy
	}
	id, err := s.insert(ctx, `INSERT INTO auth_instances (slug, driver, label, origin, owner_provider_id, enabled, legacy, config_json, promoted_json, created_by) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		a.Slug, a.Driver, a.Label, origin, owner, s.b(a.Enabled), s.b(a.Legacy), configOrEmpty(a.ConfigJSON), encodePromoted(a.Promoted), createdBy)
	if err != nil {
		return nil, err
	}
	return s.GetAuthInstance(ctx, id)
}

// UpdateAuthInstance writes what may change about an instance: its label,
// switch, legacy mark, configuration and promoted scopes. ⚠ Never the slug,
// the driver, the origin or the owner: an instance is what it was made as,
// and a tenant's own instance never becomes another tenant's.
func (s *TenantAuthSQL) UpdateAuthInstance(ctx context.Context, a *model.AuthInstance) error {
	if a == nil || a.ID == 0 {
		return errors.New("auth instance: no id")
	}
	_, err := s.db(ctx).ExecContext(ctx, s.q(`UPDATE auth_instances SET label=?, enabled=?, legacy=?, config_json=?, promoted_json=?, updated_at=`+s.now()+` WHERE id=?`),
		a.Label, s.b(a.Enabled), s.b(a.Legacy), configOrEmpty(a.ConfigJSON), encodePromoted(a.Promoted), a.ID)
	return err
}

// DeleteAuthInstance removes an instance; its bindings go with it.
func (s *TenantAuthSQL) DeleteAuthInstance(ctx context.Context, id int64) error {
	_, err := s.db(ctx).ExecContext(ctx, s.q(`DELETE FROM auth_instances WHERE id=?`), id)
	return err
}

// ListAuthBindings returns every binding, by tenant then instance.
func (s *TenantAuthSQL) ListAuthBindings(ctx context.Context) ([]*model.AuthBinding, error) {
	rows, err := s.db(ctx).QueryContext(ctx, `SELECT provider_id, instance_id, source, created_at FROM provider_auth_instances ORDER BY provider_id, instance_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.AuthBinding
	for rows.Next() {
		b := &model.AuthBinding{}
		if err := rows.Scan(&b.ProviderID, &b.InstanceID, &b.Source, &b.CreatedAt); err != nil {
			return nil, err
		}
		b.CreatedAt = b.CreatedAt.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

// BindAuthInstance binds a tenant to an instance. Binding twice is not an
// error, and the first binding's source is kept.
//
// Check, then insert, inside one transaction (the caller's when it has one):
// no upsert clause, so nothing differs per engine.
func (s *TenantAuthSQL) BindAuthInstance(ctx context.Context, providerID, instanceID int64, source string) error {
	if source == "" {
		source = model.AuthBindExplicit
	}
	return RunInTx(ctx, s.conn, func(ctx context.Context) error {
		var n int
		if err := s.db(ctx).QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM provider_auth_instances WHERE provider_id=? AND instance_id=?`),
			providerID, instanceID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		_, err := s.db(ctx).ExecContext(ctx, s.q(`INSERT INTO provider_auth_instances (provider_id, instance_id, source) VALUES (?,?,?)`),
			providerID, instanceID, source)
		return err
	})
}

// UnbindAuthInstance removes one binding (nothing when there is none).
func (s *TenantAuthSQL) UnbindAuthInstance(ctx context.Context, providerID, instanceID int64) error {
	_, err := s.db(ctx).ExecContext(ctx, s.q(`DELETE FROM provider_auth_instances WHERE provider_id=? AND instance_id=?`), providerID, instanceID)
	return err
}

// SetProviderAllowInsecureAuth is the platform operator's per-tenant switch
// (providers.allow_insecure_auth). Its own statement: UpdateProvider never
// writes the column, so a tenant edit cannot carry it along.
func (s *TenantAuthSQL) SetProviderAllowInsecureAuth(ctx context.Context, providerID int64, allow bool) error {
	_, err := s.db(ctx).ExecContext(ctx, s.q(`UPDATE providers SET allow_insecure_auth=?, updated_at=`+s.now()+` WHERE id=?`), s.b(allow), providerID)
	return err
}

// ── a tenant's own domains ──────────────────────────────────────────────────

const providerDomainColumns = `id, provider_id, domain, status, last_error, last_error_code, last_error_params, checked_at, active_since, tls_cert_pem, tls_key_sealed, tls_not_after, created_by, created_at`

func scanProviderDomain(r RowScanner) (*model.ProviderDomain, error) {
	d := &model.ProviderDomain{}
	var checked, since, notAfter sql.NullTime
	var createdBy sql.NullInt64
	var params string
	if err := r.Scan(&d.ID, &d.ProviderID, &d.Domain, &d.Status, &d.LastError, &d.LastErrorCode, &params, &checked, &since,
		&d.TLSCertPEM, &d.TLSKeySealed, &notAfter, &createdBy, &d.CreatedAt); err != nil {
		return nil, err
	}
	if params != "" && params != "{}" {
		if err := json.Unmarshal([]byte(params), &d.LastErrorParams); err != nil {
			return nil, fmt.Errorf("provider domain %d last_error_params: %w", d.ID, err)
		}
	}
	d.CheckedAt, d.ActiveSince, d.TLSNotAfter = nullTimePtr(checked), nullTimePtr(since), nullTimePtr(notAfter)
	d.CreatedBy = nullInt(createdBy)
	d.CreatedAt = d.CreatedAt.UTC()
	return d, nil
}

// ListProviderDomains returns a tenant's domains (every tenant's for 0), by
// domain.
func (s *TenantAuthSQL) ListProviderDomains(ctx context.Context, providerID int64) ([]*model.ProviderDomain, error) {
	query, args := `SELECT `+providerDomainColumns+` FROM provider_domains ORDER BY domain`, []any{}
	if providerID != 0 {
		query, args = `SELECT `+providerDomainColumns+` FROM provider_domains WHERE provider_id=? ORDER BY domain`, []any{providerID}
	}
	rows, err := s.db(ctx).QueryContext(ctx, s.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.ProviderDomain
	for rows.Next() {
		d, err := scanProviderDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetProviderDomain returns the row of a domain (lower case), or (nil, nil).
func (s *TenantAuthSQL) GetProviderDomain(ctx context.Context, domain string) (*model.ProviderDomain, error) {
	d, err := scanProviderDomain(s.db(ctx).QueryRowContext(ctx, s.q(`SELECT `+providerDomainColumns+` FROM provider_domains WHERE domain=?`),
		strings.ToLower(strings.TrimSpace(domain))))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// GetProviderDomainByID returns one row, or (nil, nil).
func (s *TenantAuthSQL) GetProviderDomainByID(ctx context.Context, id int64) (*model.ProviderDomain, error) {
	d, err := scanProviderDomain(s.db(ctx).QueryRowContext(ctx, s.q(`SELECT `+providerDomainColumns+` FROM provider_domains WHERE id=?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// CreateProviderDomain adds a domain to a tenant, pending. A domain any
// tenant already has is the unique index's refusal (one tenant at a time).
func (s *TenantAuthSQL) CreateProviderDomain(ctx context.Context, d *model.ProviderDomain) (*model.ProviderDomain, error) {
	if d == nil || d.ProviderID == 0 || strings.TrimSpace(d.Domain) == "" {
		return nil, errors.New("provider domain: tenant and domain are required")
	}
	status := d.Status
	if status == "" {
		status = model.DomainPending
	}
	var createdBy any
	if d.CreatedBy != nil {
		createdBy = *d.CreatedBy
	}
	id, err := s.insert(ctx, `INSERT INTO provider_domains (provider_id, domain, status, created_by) VALUES (?,?,?,?)`,
		d.ProviderID, strings.ToLower(strings.TrimSpace(d.Domain)), status, createdBy)
	if err != nil {
		return nil, err
	}
	return s.GetProviderDomainByID(ctx, id)
}

// SetProviderDomainStatus records a check: the state it left the domain in,
// what it found when that was not the CNAME (the sentence, and its code and
// names for a screen to say), and when. active_since is set when the domain
// becomes active and kept while it stays so; it is cleared when it stops
// being active.
func (s *TenantAuthSQL) SetProviderDomainStatus(ctx context.Context, id int64, status string, why model.DomainCheck, checkedAt time.Time) error {
	at := checkedAt.UTC()
	if status == model.DomainActive {
		_, err := s.db(ctx).ExecContext(ctx, s.q(`UPDATE provider_domains SET status=?, last_error='', last_error_code='', last_error_params='{}', checked_at=?, active_since=COALESCE(active_since, ?) WHERE id=?`),
			status, s.t(&at), s.t(&at), id)
		return err
	}
	params := "{}"
	if len(why.Params) > 0 {
		b, err := json.Marshal(why.Params)
		if err != nil {
			return err
		}
		params = string(b)
	}
	_, err := s.db(ctx).ExecContext(ctx, s.q(`UPDATE provider_domains SET status=?, last_error=?, last_error_code=?, last_error_params=?, checked_at=?, active_since=NULL WHERE id=?`),
		status, why.Text, why.Code, params, s.t(&at), id)
	return err
}

// SetProviderDomainCert stores (or, with empty values, removes) the
// certificate a tenant brought for its domain. The key arrives sealed.
func (s *TenantAuthSQL) SetProviderDomainCert(ctx context.Context, id int64, certPEM, keySealed string, notAfter *time.Time) error {
	_, err := s.db(ctx).ExecContext(ctx, s.q(`UPDATE provider_domains SET tls_cert_pem=?, tls_key_sealed=?, tls_not_after=? WHERE id=?`),
		certPEM, keySealed, s.t(notAfter), id)
	return err
}

// DeleteProviderDomain removes a domain from its tenant.
func (s *TenantAuthSQL) DeleteProviderDomain(ctx context.Context, id int64) error {
	_, err := s.db(ctx).ExecContext(ctx, s.q(`DELETE FROM provider_domains WHERE id=?`), id)
	return err
}

// ProviderIDByActiveDomain returns the tenant an ACTIVE own domain routes to,
// 0 for none. It does not read the providers row itself: the engine's
// GetProviderByHost does, so the row is scanned the engine's way and its
// `enabled` is judged there.
func (s *TenantAuthSQL) ProviderIDByActiveDomain(ctx context.Context, domain string) (int64, error) {
	var id int64
	err := s.db(ctx).QueryRowContext(ctx, s.q(`SELECT provider_id FROM provider_domains WHERE domain=? AND status=?`),
		strings.ToLower(strings.TrimSpace(domain)), model.DomainActive).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}
