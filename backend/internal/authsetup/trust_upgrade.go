package authsetup

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/multioidc"
	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	"github.com/brf-tech/filex/backend/internal/db"
)

// ── "Trust this provider's email addresses" on upgrade (filex 0.50) ─────────
//
// 0.50 reads `email_verified`: an address the identity provider does not mark
// verified no longer signs in to an existing account that is not yet bound to
// the person's SSO identity, and opens a new account switched off
// (docs/SSO.md, "Which account an SSO sign-in opens"). Many providers never
// send the claim, so on an upgrade that alone would lock out the people of an
// install whose provider worked yesterday.
//
// The owner's decision (2026-10-02): every OIDC provider that ALREADY EXISTS
// when 0.50 first starts gets trust ON, keeping its old behaviour, and the
// page says the upgrade set it ("switch it off if your provider sends
// email_verified"); a provider made afterwards starts with trust OFF. The
// tenant boundary, the pinning, the closed cookie-less callback and the
// (issuer, sub) bind hold everywhere, whatever this setting says.
//
// The environment's OIDC (FILEX_OIDC_*) has no row to switch: unless
// FILEX_OIDC_TRUST_EMAIL says otherwise, it trusts when the database had
// accounts that came through SSO at the upgrade (EnvTrustEmail). That answer
// is taken once and kept.
//
// Migration 00079 writes the mark trustUpgradeKey=pending only on a database
// that already had accounts, so a fresh install never runs any of this.

const (
	trustUpgradeKey   = "auth.oidc_trust.upgrade"
	trustByUpgradeKey = "auth.oidc_trust.by_upgrade"
	envTrustKey       = "auth.oidc_trust.environment"
)

// TrustUpgrade is what the upgrade switched on: which providers' trust is
// still the value it set (until someone saves it).
type TrustUpgrade struct {
	// Instances are page and row instances, by slug (`oidc` is the page's
	// first OIDC).
	Instances []string `json:"instances,omitempty"`
	// Tenants are tenants whose own OIDC on their row got trust.
	Tenants []int64 `json:"tenants,omitempty"`
}

// Instance reports whether an instance's trust is the upgrade's.
func (t TrustUpgrade) Instance(slug string) bool {
	for _, s := range t.Instances {
		if s == slug {
			return true
		}
	}
	return false
}

// Tenant reports whether a tenant's own OIDC's trust is the upgrade's.
func (t TrustUpgrade) Tenant(id int64) bool {
	for _, x := range t.Tenants {
		if x == id {
			return true
		}
	}
	return false
}

// trustStore is the slice of db.Store the upgrade needs.
type trustStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	UpsertSetting(ctx context.Context, key, value string) error
}

// UpgradeOIDCTrust runs once, at the first start of 0.50 on a database that
// already had accounts (the mark migration 00079 wrote): trust ON for every
// OIDC that exists - the page's first one, every row instance (a tenant's
// own among them), every tenant's own OIDC on its row - recorded so the page
// can say the upgrade set it; and the environment OIDC's answer
// (EnvTrustEmail). Called before the environment's providers are built.
func UpgradeOIDCTrust(ctx context.Context, store db.Store, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	if v, _ := store.GetSetting(ctx, trustUpgradeKey); v != "pending" {
		return nil
	}
	var rec TrustUpgrade
	stored, err := LoadStored(ctx, store)
	if err != nil {
		return err
	}
	if s := stored["oidc"]; s != nil && s.Exists && s.Values[authoidc.TrustEmailKey] == "" {
		if err := store.UpsertSetting(ctx, settingKey("oidc", authoidc.TrustEmailKey), "true"); err != nil {
			return err
		}
		rec.Instances = append(rec.Instances, "oidc")
	}
	rows, err := store.ListAuthInstances(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if Canonical(row.Driver) != "oidc" || PrimarySlug(row.Slug, row.Driver) {
			continue
		}
		s, serr := StoredOfRow(row)
		if serr != nil || s.Values[authoidc.TrustEmailKey] != "" {
			continue
		}
		s.Values[authoidc.TrustEmailKey] = "true"
		row.ConfigJSON = RowConfigJSON(s)
		if err := store.UpdateAuthInstance(ctx, row); err != nil {
			return err
		}
		rec.Instances = append(rec.Instances, row.Slug)
	}
	tenants, err := store.ListProviders(ctx)
	if err != nil {
		return err
	}
	for _, p := range tenants {
		if !multioidc.HasOwnOIDC(p) || p.OIDCTrustEmail {
			continue
		}
		if err := store.SetProviderOIDCTrustEmail(ctx, p.ID, true); err != nil {
			return err
		}
		rec.Tenants = append(rec.Tenants, p.ID)
	}
	// The environment's OIDC: did accounts come through SSO before 0.50? An
	// account opened by an SSO sign-in has no password of its own; a
	// multi-tenant one also kept its subject. (A single-tenant install kept
	// no subject before 0.50, so "no password" is the only trace it left - an
	// LDAP or header-proxy account counts too, which errs towards keeping the
	// old behaviour, never towards locking anybody out.)
	users, err := store.ListUsers(ctx)
	if err != nil {
		return err
	}
	ssoMade := false
	for _, u := range users {
		if u.OIDCSubject != "" || strings.TrimSpace(u.PasswordHash) == "" {
			ssoMade = true
			break
		}
	}
	env := "false"
	if ssoMade {
		env = "true"
	}
	if err := store.UpsertSetting(ctx, envTrustKey, env); err != nil {
		return err
	}
	if err := writeTrustUpgrade(ctx, store, rec); err != nil {
		return err
	}
	if err := store.UpsertSetting(ctx, trustUpgradeKey, "done"); err != nil {
		return err
	}
	log.Info("auth: upgrade to 0.50 - the OIDC providers that existed keep trusting the email addresses they send; switch it off on a provider that sends email_verified (docs/SSO.md)",
		slog.Any("instances", rec.Instances), slog.Any("tenants", rec.Tenants), slog.Bool("environment", ssoMade))
	return nil
}

// ReadTrustUpgrade is what the upgrade switched on, as still recorded.
func ReadTrustUpgrade(ctx context.Context, store trustStore) TrustUpgrade {
	var rec TrustUpgrade
	if store == nil {
		return rec
	}
	if v, err := store.GetSetting(ctx, trustByUpgradeKey); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &rec)
	}
	return rec
}

func writeTrustUpgrade(ctx context.Context, store trustStore, rec TrustUpgrade) error {
	b, _ := json.Marshal(rec)
	return store.UpsertSetting(ctx, trustByUpgradeKey, string(b))
}

// ForgetTrustUpgradeInstance: an instance's trust was saved by someone, so it
// is theirs now and the page stops saying the upgrade set it.
func ForgetTrustUpgradeInstance(ctx context.Context, store trustStore, slug string) {
	rec := ReadTrustUpgrade(ctx, store)
	if !rec.Instance(slug) {
		return
	}
	out := rec.Instances[:0]
	for _, s := range rec.Instances {
		if s != slug {
			out = append(out, s)
		}
	}
	rec.Instances = out
	if err := writeTrustUpgrade(ctx, store, rec); err != nil {
		slog.Warn("auth: could not record that a provider's trust setting was saved", slog.String("provider", slug), slog.String("err", err.Error()))
	}
}

// ForgetTrustUpgradeTenant is ForgetTrustUpgradeInstance for a tenant's own
// OIDC on its row.
func ForgetTrustUpgradeTenant(ctx context.Context, store trustStore, id int64) {
	rec := ReadTrustUpgrade(ctx, store)
	if !rec.Tenant(id) {
		return
	}
	out := rec.Tenants[:0]
	for _, x := range rec.Tenants {
		if x != id {
			out = append(out, x)
		}
	}
	rec.Tenants = out
	if err := writeTrustUpgrade(ctx, store, rec); err != nil {
		slog.Warn("auth: could not record that a tenant's trust setting was saved", slog.Int64("tenant", id), slog.String("err", err.Error()))
	}
}

// EnvTrustEmail is the environment OIDC's trust: what FILEX_OIDC_TRUST_EMAIL
// (or the config file's trust_email) says when it says anything; otherwise
// the answer the upgrade to 0.50 took once - on when the database had
// accounts that came through SSO then - and off on a fresh install. byUpgrade
// reports that the upgrade's answer is the one in force, for the page.
func EnvTrustEmail(ctx context.Context, store trustStore, explicit *bool) (trust, byUpgrade bool) {
	if explicit != nil {
		return *explicit, false
	}
	if store == nil {
		return false, false
	}
	v, _ := store.GetSetting(ctx, envTrustKey)
	return v == "true", v == "true"
}
