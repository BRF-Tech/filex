package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/brf-tech/filex/backend/internal/model"
)

// OIDCIdentitySQL implements the account lookups and writes an OIDC sign-in
// makes (migration 00079, docs/SSO.md "Which account an SSO sign-in opens"),
// written ONCE for every engine. Each driver embeds a *OIDCIdentitySQL in its
// Store, so the methods are promoted and there is no per-driver copy to drift.
//
// ⚠⚠ Every lookup here stays inside ONE tenant (UserTenant). An OIDC sign-in
// used to find its account by e-mail across the whole platform
// (Store.GetUserByEmail), so an identity provider bound to one tenant could
// sign a person in to another tenant's account that had the same address.
// There is deliberately no unscoped variant.
type OIDCIdentitySQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
	// GetUser reads one account with the driver's own column list.
	GetUser func(ctx context.Context, id int64) (*model.User, error)
}

func (o *OIDCIdentitySQL) q(query string) string { return rebind(o.Placeholders, query) }

// UserTenant is the tenant an account lookup stays inside: the tenant's row,
// and Main for the platform's own tenant, which also owns an account with no
// tenant at all (provider_id NULL, a bootstrap or legacy row; the same rule as
// identity.Tenant.Owns).
type UserTenant struct {
	ProviderID int64
	Main       bool
}

// where is the tenant condition and its argument.
func (t UserTenant) where() (string, []any) {
	if t.Main {
		return `(provider_id=? OR provider_id IS NULL)`, []any{t.ProviderID}
	}
	return `provider_id=?`, []any{t.ProviderID}
}

// GetUserByOIDCIdentity is the account of the tenant bound to the identity
// (issuer, subject), or (nil, nil) when none is. The comparison is exact on
// every engine: an identity provider's `sub` is case-sensitive (on MySQL the
// columns are utf8mb4_bin for it, migration 00079).
func (o *OIDCIdentitySQL) GetUserByOIDCIdentity(ctx context.Context, t UserTenant, issuer, subject string) (*model.User, error) {
	if issuer == "" || subject == "" {
		return nil, nil
	}
	cond, args := t.where()
	return o.one(ctx, `SELECT id FROM users WHERE oidc_issuer=? AND oidc_subject=? AND `+cond,
		append([]any{issuer, subject}, args...)...)
}

// GetUserInTenantByEmail is the account of the tenant with this e-mail
// address, or (nil, nil) when the tenant has none: an account of another
// tenant with the address is not found.
func (o *OIDCIdentitySQL) GetUserInTenantByEmail(ctx context.Context, t UserTenant, email string) (*model.User, error) {
	if email == "" {
		return nil, nil
	}
	cond, args := t.where()
	return o.one(ctx, `SELECT id FROM users WHERE email=? AND `+cond, append([]any{email}, args...)...)
}

func (o *OIDCIdentitySQL) one(ctx context.Context, query string, args ...any) (*model.User, error) {
	var id int64
	err := Conn(ctx, o.Pool).QueryRowContext(ctx, o.q(query), args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return o.GetUser(ctx, id)
}

// SetUserOIDCIdentity binds an account to the identity (issuer, subject).
// The unique index (provider_id, oidc_issuer, oidc_subject) refuses a second
// account of the same tenant bound to the same identity.
func (o *OIDCIdentitySQL) SetUserOIDCIdentity(ctx context.Context, userID int64, issuer, subject string) error {
	if issuer == "" || subject == "" {
		return errors.New("db: an SSO identity needs an issuer and a subject")
	}
	_, err := Conn(ctx, o.Pool).ExecContext(ctx, o.q(
		`UPDATE users SET oidc_issuer=?, oidc_subject=? WHERE id=?`), issuer, subject, userID)
	return err
}

// ClearUserOIDCIdentity removes an account's SSO bind (issuer and subject),
// and reports whether it had one: its next SSO sign-in is matched by its
// address again, as a first sign-in is (docs/SSO.md, "Removing an account's
// SSO bind"). An administrator's repair when the identity provider gave the
// person a new identity, or a new issuer address.
func (o *OIDCIdentitySQL) ClearUserOIDCIdentity(ctx context.Context, userID int64) (bool, error) {
	res, err := Conn(ctx, o.Pool).ExecContext(ctx, o.q(
		`UPDATE users SET oidc_issuer=NULL, oidc_subject=NULL WHERE id=? AND ((oidc_issuer IS NOT NULL AND oidc_issuer <> '') OR (oidc_subject IS NOT NULL AND oidc_subject <> ''))`), userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SetProviderOIDCTrustEmail is a tenant's own OIDC's "trust this provider's
// email addresses" (providers.oidc_trust_email, migration 00079). Its own
// statement: UpdateProvider never writes it, so a caller that rebuilds a
// provider from a form cannot switch it by accident.
func (o *OIDCIdentitySQL) SetProviderOIDCTrustEmail(ctx context.Context, providerID int64, trust bool) error {
	lit := "FALSE"
	if trust {
		lit = "TRUE"
	}
	_, err := Conn(ctx, o.Pool).ExecContext(ctx, o.q(
		`UPDATE providers SET oidc_trust_email=`+lit+` WHERE id=?`), providerID)
	return err
}

// SetUserDisabledReason switches an account off and records why the server
// did (model.DisabledPendingApproval). An administrator switching the account
// on or off (Store.SetUserEnabled) clears the reason.
func (o *OIDCIdentitySQL) SetUserDisabledReason(ctx context.Context, userID int64, reason string) error {
	if reason == "" {
		return errors.New("db: a disabled reason is needed")
	}
	// FALSE is a keyword all three engines read (SQLite and MySQL as 0, which
	// is what their `enabled` column holds); a bound Go bool is not.
	_, err := Conn(ctx, o.Pool).ExecContext(ctx, o.q(
		`UPDATE users SET enabled=FALSE, disabled_reason=? WHERE id=?`), reason, userID)
	return err
}
