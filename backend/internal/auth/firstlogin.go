package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/tagname"
)

// ── The first-login rule ────────────────────────────────────────────────────
//
// A person who signs in through a provider filex has never seen before either
// gets an account or does not. That used to be decided ad hoc, driver by driver:
// OIDC and LDAP always opened one, the header driver had `auto_provision`, and
// none could say "only members of this group". One rule now, for every
// provider (OIDC, LDAP, the header proxy — and the operating-system providers,
// which take the same FirstLogin request):
//
//	the account exists (same e-mail)        → it signs in; nothing here runs
//	auto_create is off                      → refused
//	allowed_groups is set, no group matches → refused
//	otherwise                               → the account is created, homed in
//	                                          its tenant, and a new account
//	                                          whose groups name a role starts
//	                                          with it (ApplyStartingRole)
//
// Group → role is NOT a second table: it is the permission rules that already
// target an SSO group (perm.StartingRole). allowed_groups is only a door.
//
// ⚠ A refusal is ONE answer to the person — the provider answers 401 / sends
// them back to the sign-in form, exactly as for a wrong password — so nobody
// can probe which accounts or groups exist. The reason goes to the operator: a
// log line and an audit row (auth.first_login_refused).

// Reasons a first sign-in is refused. Stable strings: they are stored in audit
// rows.
const (
	ReasonAutoCreateOff    = "auto_create_off"
	ReasonGroupNotAllowed  = "group_not_allowed"
	ReasonForbiddenAccount = identity.RefuseForbidden
	ReasonInvalidName      = identity.RefuseInvalidName
)

// AuditFirstLoginRefused is the audit action of a refused first sign-in.
const AuditFirstLoginRefused = "auth.first_login_refused"

// ErrFirstLoginRefused is matched (errors.Is) by a refusal. Providers turn it
// into their one ambiguous answer; only the operator sees the reason.
var ErrFirstLoginRefused = errors.New("auth: first sign-in refused")

// FirstLoginRefusal is the error a refused first sign-in returns.
type FirstLoginRefusal struct {
	Reason string
}

func (e *FirstLoginRefusal) Error() string {
	return "auth: first sign-in refused (" + e.Reason + ")"
}

// Is makes errors.Is(err, ErrFirstLoginRefused) true.
func (e *FirstLoginRefusal) Is(target error) bool { return target == ErrFirstLoginRefused }

// FirstLoginPolicy is a provider's setting for people with no account yet.
type FirstLoginPolicy struct {
	// AutoCreate: open an account at the first sign-in. The zero value is
	// false; providers get the default (true) from FirstLoginPolicyFrom.
	AutoCreate bool
	// AllowedGroups: when non-empty, only members of at least one of these
	// groups get an account. Empty = no group condition.
	AllowedGroups []string
}

// FirstLoginPolicyFrom reads a provider configuration: `auto_create` (bool,
// default TRUE — an upgrade must not leave anybody outside), `allowed_groups`
// (comma list). `auto_provision` is read as the older name of auto_create, for
// the header proxy's configurations that predate it.
func FirstLoginPolicyFrom(cfg map[string]any) FirstLoginPolicy {
	return FirstLoginPolicy{
		AutoCreate:    CfgBoolDefault(cfg, "auto_create", CfgBoolDefault(cfg, "auto_provision", true)),
		AllowedGroups: SplitList(CfgString(cfg, "allowed_groups")),
	}
}

// Decide answers a first sign-in with the reason it is refused, or "" to let it
// through. groups are the ones the provider reported for the person.
func (p FirstLoginPolicy) Decide(groups []string) string {
	if !p.AutoCreate {
		return ReasonAutoCreateOff
	}
	if len(p.AllowedGroups) > 0 && !GroupsIntersect(groups, p.AllowedGroups) {
		return ReasonGroupNotAllowed
	}
	return ""
}

// GroupsIntersect reports whether two group lists share a member. Compared with
// tagname.Key: case, surrounding space and the Turkish dotted/dotless I fold, so
// "Yöneticiler" and "YÖNETİCİLER" are the one group.
func GroupsIntersect(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	have := make(map[string]struct{}, len(a))
	for _, g := range a {
		if k := tagname.Key(g); k != "" {
			have[k] = struct{}{}
		}
	}
	for _, g := range b {
		if _, ok := have[tagname.Key(g)]; ok {
			return true
		}
	}
	return false
}

// SplitList splits a comma list into its trimmed, non-empty members.
func SplitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// CfgBoolDefault is CfgBool with a default for a key that is absent (or an
// empty string): the settings table stores every value as a string, and "" is
// how an unset field arrives.
func CfgBoolDefault(cfg map[string]any, key string, def bool) bool {
	switch v := cfg[key].(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		switch s {
		case "":
			return def
		case "true", "1", "yes", "on":
			return true
		}
		return false
	}
	return def
}

// FirstLogin is one first sign-in: who, through which provider, with which
// groups, under which policy.
type FirstLogin struct {
	// Driver names the provider, for the log and the audit row.
	Driver string
	// Identifier is what the person typed or the provider named them by; it is
	// a label for the operator (audit/log), never used to look anything up.
	Identifier string
	Email      string
	Role       string
	// Groups are the groups the provider reported; empty when it reports none.
	Groups []string
	Policy FirstLoginPolicy
	// Homing places the account in a tenant on a multi-tenant install (host,
	// then pin). ProviderID, when non-zero, is a tenant the provider instance is
	// already bound to (a per-tenant OIDC realm) and wins over Homing.
	Homing     TenantHoming
	ProviderID int64
	// OIDCSubject is stamped on the account when the provider has one.
	OIDCSubject string
}

// auditWriter is the slice of db.Store a refusal's audit row needs. Asked of the
// store by type assertion so ProvisionStore stays the small interface it was.
type auditWriter interface {
	InsertAuditEntry(ctx context.Context, e *model.AuditEntry) error
}

// ProvisionFirstLogin applies the first-login rule to a person with no account:
// it refuses (a *FirstLoginRefusal, errors.Is ErrFirstLoginRefused) or creates
// the account through the ONE creation path — create, home in the tenant, and
// delete the row again if it cannot be homed.
//
// The caller has already looked for an existing account; this is only for the
// "no such account" branch.
func ProvisionFirstLogin(ctx context.Context, store ProvisionStore, f FirstLogin) (*model.User, error) {
	if reason := f.Policy.Decide(f.Groups); reason != "" {
		return nil, RefuseFirstLogin(ctx, store, f.Driver, f.Identifier, reason)
	}
	return provision(ctx, store, f.Homing, f.ProviderID, f.OIDCSubject, f.Driver, f.Email, f.Role)
}

// RefuseFirstLogin records why a first sign-in was refused (log + audit row)
// and returns the refusal error. Providers that refuse for a reason of their own
// — an OS login whose name is a system account — call it directly so the reason
// reaches the operator the same way.
//
// ⚠ Nothing secret goes in: the provider, the reason, the identifier the person
// gave (an e-mail or a login name) and the host they came to. Never a password,
// a token or the group list.
func RefuseFirstLogin(ctx context.Context, store any, driver, identifier, reason string) error {
	host := LoginHostFrom(ctx)
	slog.Warn(driver+": first sign-in refused",
		slog.String("reason", reason), slog.String("identifier", identifier), slog.String("host", host))
	AddAuditDetail(ctx, "first_login_refused", reason)
	if w, ok := store.(auditWriter); ok {
		meta := map[string]any{"provider": driver, "reason": reason, "identifier": identifier}
		if host != "" {
			meta["host"] = host
		}
		if err := w.InsertAuditEntry(ctx, &model.AuditEntry{
			Action: AuditFirstLoginRefused, TargetType: "login", TargetID: driver, Metadata: meta,
		}); err != nil {
			slog.Warn(driver+": could not write the audit row of a refused first sign-in", slog.String("err", err.Error()))
		}
	}
	return &FirstLoginRefusal{Reason: reason}
}

// startingRoleStore is the slice of db.Store ApplyStartingRole needs.
type startingRoleStore interface {
	ListPermissionRules(ctx context.Context) ([]*model.PermissionRule, error)
	UpdateUserRole(ctx context.Context, id int64, role string) error
	SetUserCustomRole(ctx context.Context, userID, roleID int64) error
}

// ApplyStartingRole gives a NEW account the custom role its groups name, and the
// built-in role that role implies (perm.StartingRole / perm.RoleHolder). It is
// the group → role mapping for every provider: the permission rules that target
// an SSO group are the only table. Failures are logged, not fatal: the account
// then simply starts with its default role.
//
// Only at creation: afterwards the role is the person's own, changed on their
// page like anyone else's. An administrator (the header proxy's admin role, the
// OIDC admin group) is bound by no role — callers do not call this for one.
func ApplyStartingRole(ctx context.Context, store startingRoleStore, driver string, user *model.User, groups []string) {
	if user == nil || len(groups) == 0 {
		return
	}
	rules, err := store.ListPermissionRules(ctx)
	if err != nil {
		slog.Warn(driver+": starting role: list roles", slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
		return
	}
	role := perm.StartingRole(rules, groups, user.ProviderID)
	if role == nil {
		return
	}
	want := perm.RoleHolder(role)
	if user.Role != want {
		if err := store.UpdateUserRole(ctx, user.ID, want); err != nil {
			slog.Warn(driver+": starting role: set built-in role", slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
			return
		}
		user.Role = want
	}
	if err := store.SetUserCustomRole(ctx, user.ID, role.ID); err != nil {
		slog.Warn(driver+": starting role: give role", slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
		return
	}
	perm.InvalidateFor(ctx, user.ID)
}

// FirstLoginCheck is the provider test's step for the first-login rule. Its ID
// says what the configuration does — first_login_open (every new person gets an
// account), first_login_groups (only members of allowed_groups),
// first_login_closed (nobody new does) — and the step FAILS (`first_login`,
// reason no_group_source) when the configuration cannot work: allowed_groups
// with nowhere to read groups from (groupsFrom == "", e.g. OIDC without a
// role/group claim, which would refuse everybody).
func FirstLoginCheck(cfg map[string]any, groupsFrom string) ProbeCheck {
	p := FirstLoginPolicyFrom(cfg)
	switch {
	case !p.AutoCreate:
		return Check("first_login_closed", ProbeOK)
	case len(p.AllowedGroups) > 0 && groupsFrom == "":
		return Check("first_login", ProbeFail, "reason", "no_group_source")
	case len(p.AllowedGroups) > 0:
		return Check("first_login_groups", ProbeOK, "groups", strings.Join(p.AllowedGroups, ", "), "from", groupsFrom)
	}
	return Check("first_login_open", ProbeOK)
}
