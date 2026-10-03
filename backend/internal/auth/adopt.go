package auth

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ── Adopting the account an older sign-in path opened ───────────────────────
//
// Until the e-mail rule, the LDAP driver keyed a directory entry that carries no
// e-mail attribute by whatever the person typed: `alex` signed in to an account
// whose e-mail is `alex`. The rule now gives that person `alex@<token>` —
// `alex@<realm>.<token>` in a tenant's realm on a multi-tenant install
// (identity.DeriveEmail; the realm is the tenant the account is homed in, so
// acme's old `alex` becomes `alex@acme.local`). Left there, the first sign-in after the upgrade would
// find no `alex@local`, open a SECOND account beside the old one, and leave the
// files, shares, permissions and role on an account nobody can reach again.
//
// AdoptAccount finds the old account — the one whose e-mail is exactly the
// login name, no `@`: only the old directory path ever opened such a row, every
// other way of making an account asks for an address — and settles it. Nobody
// is refused and nobody has anything to do by hand (the maintainer's decision,
// 2026-09-30):
//
//	the old account is in another tenant   left alone — the tenant boundary is
//	(multi-tenant: the sign-in's tenant    not crossed. The platform operator is
//	was told, the account's is another)    told ONCE per old account; the person
//	                                       goes on to the first-login rule in
//	                                       their own tenant
//	the sign-in's tenant cannot be told    left alone, nothing adopted (a
//	(multi-tenant, no host, no pin)        file-protocol sign-in); the
//	                                       first-login rule refuses to create
//	it is bound to SSO (an OIDC subject)   signed in to AS IT IS: its e-mail is
//	                                       not changed, or the OIDC provider's
//	                                       own lookup would lose it
//	otherwise — with or without a local    re-keyed to the new address in place:
//	password                               same ID, so files, shares,
//	                                       permissions and role stay; a local
//	                                       password stays exactly as it was, so
//	                                       both ways in keep working
//
// ⚠ An adoption counts as "the account exists": the first-login rule
// (auto_create, allowed_groups) does not judge it, as it judges no existing
// account.

// AuditAccountAdopted is the audit action of an older account taken over:
// re-keyed to the address the e-mail rule gives it (`renamed: true`), or signed
// in to as it is (`renamed: false`, `reason: sso_account`).
const AuditAccountAdopted = "auth.account_adopted"

// AuditAccountNotAdopted is the audit action of an older account that was left
// alone. The row names no user, so only the platform operator reads it.
const AuditAccountNotAdopted = "auth.account_not_adopted"

// AuditLegacyAccountElsewhere is written once per older account found in
// another tenant than the sign-in's — the mark that the platform operator has
// been told (SetLegacyAccountAlarm). No user on the row: a tenant administrator
// never reads another tenant's name from it.
const AuditLegacyAccountElsewhere = "auth.legacy_account_elsewhere"

// Why an older account is not adopted, or adopted without being renamed.
// Stable strings: they are stored in audit rows.
const (
	NotAdoptedOtherTenant = "other_tenant"
	NotAdoptedNoTenant    = "no_tenant"
	AdoptedSSOAccount     = "sso_account"
)

// Adoption is one sign-in that may take over an older account.
type Adoption struct {
	// Driver names the provider, for the log and the audit row.
	Driver string
	// LoginName is the name the provider knew the person by — what the older
	// path used as the e-mail. One that contains `@` adopts nothing: the older
	// path kept an address as it was, so there is no older key to find.
	LoginName string
	// Email is the address the account has under the rule.
	Email string
	// Homing is the provider's tenant policy: on a multi-tenant install only an
	// account in the sign-in's tenant is adopted.
	Homing TenantHoming
}

// AdoptStore is the slice of db.Store AdoptAccount needs.
type AdoptStore interface {
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	GetUser(ctx context.Context, id int64) (*model.User, error)
	UpdateUserEmail(ctx context.Context, id int64, email string) error
	GetProvider(ctx context.Context, id int64) (*model.Provider, error)
	GetProviderByHost(ctx context.Context, host string) (*model.Provider, error)
	GetProviderBySlug(ctx context.Context, slug string) (*model.Provider, error)
	InsertAuditEntry(ctx context.Context, e *model.AuditEntry) error
	ListAuditFiltered(ctx context.Context, userID *int64, action string, from, to *time.Time, limit, offset int) ([]*db.AuditEntryWithUser, int64, error)
}

// LegacyAccountElsewhere is what the platform operator is told about an older
// account found in another tenant than the sign-in's. Names only — no password,
// no token, no group.
type LegacyAccountElsewhere struct {
	// Driver is the provider the person signed in through.
	Driver string
	// Account is the login name — the older account's e-mail.
	Account string
	// UserID is the older account.
	UserID int64
	// AccountTenant is the older account's tenant (its slug); LoginTenant the
	// tenant the sign-in was for.
	AccountTenant string
	LoginTenant   string
}

var legacyAlarm atomic.Pointer[func(context.Context, LegacyAccountElsewhere)]

// SetLegacyAccountAlarm registers how the platform operator hears about an
// older account in another tenant (server.go: a notification only a platform
// administrator's bell carries). It is called once per older account, ever —
// the audit row AuditLegacyAccountElsewhere is the mark. nil unregisters.
func SetLegacyAccountAlarm(f func(context.Context, LegacyAccountElsewhere)) {
	if f == nil {
		legacyAlarm.Store(nil)
		return
	}
	legacyAlarm.Store(&f)
}

// AdoptAccount returns the older account the person signs in to — re-keyed to
// a.Email, or (bound to SSO) as it is — or nil (and no error) when there is
// none to adopt. The caller has already looked for an account at a.Email and
// found none; on nil it goes on to the first-login rule.
//
// The unique index on e-mail is the arbiter of a race: if re-keying fails and
// an account holds the new address by then (another sign-in of the same person,
// an administrator), that account is returned — never a second one opened.
func AdoptAccount(ctx context.Context, store AdoptStore, a Adoption) (*model.User, error) {
	old := identity.Normalize(a.LoginName)
	email := identity.Normalize(a.Email)
	if old == "" || email == "" || old == email || strings.Contains(old, "@") {
		return nil, nil
	}
	u, err := store.GetUserByEmail(ctx, old)
	if err != nil || u == nil {
		return nil, nil
	}

	// The tenant boundary first: nothing below may touch another tenant's row.
	if a.Homing.MultiTenant {
		p, err := a.Homing.resolveProvider(ctx, store)
		if err != nil || p == nil {
			notAdopted(ctx, store, a, u, old, email, NotAdoptedNoTenant)
			return nil, nil
		}
		if u.ProviderID == nil || *u.ProviderID != p.ID {
			notAdopted(ctx, store, a, u, old, email, NotAdoptedOtherTenant)
			tellOperatorOnce(ctx, store, a, u, old, p)
			return nil, nil
		}
	}

	// Bound to SSO: signed in to as it is. Its e-mail is what the OIDC
	// provider finds it by; changing it would hand that provider a new,
	// empty account at its next sign-in.
	if u.OIDCSubject != "" {
		if !audited(ctx, store, &u.ID, AuditAccountAdopted, "") {
			slog.Info(a.Driver+": signed in to an older SSO-bound account as it is",
				slog.Int64("user_id", u.ID), slog.String("old_email", old))
			id := u.ID
			writeAudit(ctx, store, a.Driver, &model.AuditEntry{
				UserID: &id, Action: AuditAccountAdopted, TargetType: "user", TargetID: strconv.FormatInt(u.ID, 10),
				Metadata: adoptMeta(ctx, a.Driver, old, email, map[string]any{"renamed": false, "reason": AdoptedSSOAccount}),
			})
		}
		return u, nil
	}

	hadPassword := u.PasswordHash != ""
	if err := store.UpdateUserEmail(ctx, u.ID, email); err != nil {
		if cur, gerr := store.GetUserByEmail(ctx, email); gerr == nil && cur != nil {
			return cur, nil
		}
		return nil, fmt.Errorf("%s: adopt account %d as %s: %w", a.Driver, u.ID, email, err)
	}
	if fresh, gerr := store.GetUser(ctx, u.ID); gerr == nil && fresh != nil {
		u = fresh
	} else {
		u.Email = email
	}
	slog.Info(a.Driver+": adopted an older account at its new address",
		slog.Int64("user_id", u.ID), slog.String("old_email", old), slog.String("new_email", email),
		slog.Bool("had_local_password", hadPassword))
	AddAuditDetail(ctx, "account_adopted", u.ID)
	extra := map[string]any{"renamed": true}
	if hadPassword {
		// The local password is untouched; the flag tells the operator the
		// account has two ways in now, the directory's and its own.
		extra["had_local_password"] = true
	}
	id := u.ID
	writeAudit(ctx, store, a.Driver, &model.AuditEntry{
		UserID: &id, Action: AuditAccountAdopted, TargetType: "user", TargetID: strconv.FormatInt(u.ID, 10),
		Metadata: adoptMeta(ctx, a.Driver, old, email, extra),
	})
	return u, nil
}

// notAdopted records an older account left alone: a log line and an audit row
// with no user on it (supertenant-only — it may name another tenant's host).
func notAdopted(ctx context.Context, store AdoptStore, a Adoption, u *model.User, old, email, reason string) {
	slog.Warn(a.Driver+": an older account with this login name was left alone; the first sign-in rule decides",
		slog.String("reason", reason), slog.Int64("user_id", u.ID),
		slog.String("old_email", old), slog.String("new_email", email))
	writeAudit(ctx, store, a.Driver, &model.AuditEntry{
		Action: AuditAccountNotAdopted, TargetType: "user", TargetID: strconv.FormatInt(u.ID, 10),
		Metadata: adoptMeta(ctx, a.Driver, old, email, map[string]any{"reason": reason}),
	})
}

// tellOnce serialises the look-then-mark of tellOperatorOnce inside one
// process, so two sign-ins at the same moment cannot both tell.
var tellOnce sync.Mutex

// tellOperatorOnce tells the platform operator, once per older account ever,
// that it sits in another tenant than a sign-in by the same name. The audit row
// AuditLegacyAccountElsewhere (no user on it) is both the record and the mark.
func tellOperatorOnce(ctx context.Context, store AdoptStore, a Adoption, u *model.User, old string, login *model.Provider) {
	tellOnce.Lock()
	defer tellOnce.Unlock()
	target := strconv.FormatInt(u.ID, 10)
	if audited(ctx, store, nil, AuditLegacyAccountElsewhere, target) {
		return
	}
	var owner *model.Provider
	if u.ProviderID != nil {
		if p, err := store.GetProvider(ctx, *u.ProviderID); err == nil {
			owner = p
		}
	}
	e := LegacyAccountElsewhere{
		Driver: a.Driver, Account: old, UserID: u.ID,
		AccountTenant: tenantLabel(owner), LoginTenant: tenantLabel(login),
	}
	slog.Warn(a.Driver+": an older account with this login name belongs to another tenant; it was left alone and the platform operator is told",
		slog.Int64("user_id", u.ID), slog.String("account", old),
		slog.String("account_tenant", e.AccountTenant), slog.String("login_tenant", e.LoginTenant))
	meta := map[string]any{"provider": a.Driver, "account": old,
		"account_tenant": e.AccountTenant, "login_tenant": e.LoginTenant}
	if host := LoginHostFrom(ctx); host != "" {
		meta["host"] = host
	}
	writeAudit(ctx, store, a.Driver, &model.AuditEntry{
		Action: AuditLegacyAccountElsewhere, TargetType: "user", TargetID: target, Metadata: meta,
	})
	if f := legacyAlarm.Load(); f != nil {
		(*f)(ctx, e)
	}
}

// audited reports whether an audit row of action exists — for userID when it
// is given, and with targetID when that is given.
func audited(ctx context.Context, store AdoptStore, userID *int64, action, targetID string) bool {
	const page = 200
	for offset := 0; ; offset += page {
		rows, total, err := store.ListAuditFiltered(ctx, userID, action, nil, nil, page, offset)
		if err != nil {
			return false
		}
		for _, r := range rows {
			if r != nil && r.Entry != nil && r.Entry.Action == action && (targetID == "" || r.Entry.TargetID == targetID) {
				return true
			}
		}
		if len(rows) < page || int64(offset+page) >= total {
			return false
		}
	}
}

// tenantLabel names a tenant in the operator's notice: its slug (the short
// name it is known by), else its name.
func tenantLabel(p *model.Provider) string {
	switch {
	case p == nil:
		return "-"
	case strings.TrimSpace(p.Slug) != "":
		return p.Slug
	case strings.TrimSpace(p.Name) != "":
		return p.Name
	}
	return "#" + strconv.FormatInt(p.ID, 10)
}

// adoptMeta is an adoption row's metadata: the provider, the old and the new
// address, the host — never a password, a token or a group list.
func adoptMeta(ctx context.Context, driver, old, email string, extra map[string]any) map[string]any {
	meta := map[string]any{"provider": driver, "old_email": old, "new_email": email}
	if host := LoginHostFrom(ctx); host != "" {
		meta["host"] = host
	}
	for k, v := range extra {
		meta[k] = v
	}
	return meta
}

// writeAudit writes an audit row; a failure is logged, not fatal — the sign-in
// it describes has already happened.
func writeAudit(ctx context.Context, store AdoptStore, driver string, e *model.AuditEntry) {
	if err := store.InsertAuditEntry(ctx, e); err != nil {
		slog.Warn(driver+": could not write an audit row", slog.String("action", e.Action), slog.String("err", err.Error()))
	}
}
