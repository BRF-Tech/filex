package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ── Testing an operating-system provider with a real account ────────────────
//
// A directory can be tested with its service account; an operating-system
// sign-in provider has no such thing — the only way to know that it works is to
// sign somebody in. So the administrator names an account and its password ON
// THE TEST REQUEST (`test_account`), and the provider's Probe uses it once.
//
// ⚠⚠ The password lives in memory for the length of that one request. It is
// never stored, never written to a log, an audit row or a probe step, and never
// sent back. TestAccount is the carrier between the HTTP handler and the
// provider's Probe (which has the fixed Prober signature): the handler stamps
// it on the context, the prober reads it and records who it turned out to be.

// TestAccount is the account an administrator gives a provider test. It travels
// as a POINTER: the prober that signs it in records who it turned out to be
// (Email) on the same value the handler reads afterwards.
type TestAccount struct {
	Username string
	Password string

	// Email is set by the prober when the account signed in and passed the
	// provider's own rules: the address the account has (or would get) in
	// filex. Empty means the test did not get that far.
	Email string
}

// String never shows the password (a %v in a log line would otherwise print it).
func (t TestAccount) String() string { return "TestAccount{" + t.Username + "}" }

// GoString keeps %#v from printing the password either.
func (t TestAccount) GoString() string { return t.String() }

type testAccountKey struct{}

// WithTestAccount stamps the test account on ctx. A nil account, or one
// without a name, is not stamped.
func WithTestAccount(ctx context.Context, t *TestAccount) context.Context {
	if t == nil || strings.TrimSpace(t.Username) == "" {
		return ctx
	}
	return context.WithValue(ctx, testAccountKey{}, t)
}

// TestAccountFrom returns the stamped test account, or nil.
func TestAccountFrom(ctx context.Context) *TestAccount {
	t, _ := ctx.Value(testAccountKey{}).(*TestAccount)
	return t
}

// ParseTestAccount reads `test_account` {username, password} from a request
// body. The name is trimmed, the password is taken as it is (a space can be
// part of one). ok is false when the body has no account with a name.
func ParseTestAccount(raw map[string]any) (*TestAccount, bool) {
	m, ok := raw["test_account"].(map[string]any)
	if !ok {
		return nil, false
	}
	u, _ := m["username"].(string)
	p, _ := m["password"].(string)
	u = strings.TrimSpace(u)
	if u == "" {
		return nil, false
	}
	return &TestAccount{Username: u, Password: p}, true
}

type probeLangKey struct{}

// WithProbeLang stamps the language a provider test words its hints in.
func WithProbeLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, probeLangKey{}, lang)
}

// ProbeLangFrom returns the language stamped by WithProbeLang, "" when none.
func ProbeLangFrom(ctx context.Context) string {
	l, _ := ctx.Value(probeLangKey{}).(string)
	return l
}

// StrictProber is a Prober whose PASSING test is a condition of switching the
// provider on: the operator cannot override a failing test with
// confirm_failed_test. It is for providers whose only proof of working is a
// real sign-in — one that was never tried would otherwise be switched on blind.
type StrictProber interface {
	Prober
	// StrictProbe reports that a failing test can never be confirmed away.
	StrictProbe() bool
}

// TestAccountGranter is a provider that makes the account its test signed in
// with a super administrator itself (created, or raised) — for one whose
// account addresses or homing are its own business. Providers without it get
// the generic GrantSuperAdmin on TestAccount.Email. The handler calls it once
// the provider is saved and running.
type TestAccountGranter interface {
	GrantTestAccount(ctx context.Context, store any, cfg map[string]any, username string) error
}

// ErrBusy is returned when a sign-in cannot even be tried because the provider
// is at its limit of simultaneous attempts. It is a temporary condition, NOT a
// judgement of the credentials: callers answer "try again", never "wrong
// password", and do not count it as a failed attempt.
var ErrBusy = errors.New("auth: the sign-in provider is busy, try again")

// ErrUndecided is matched (errors.Is) by an error that means a provider could
// not judge the credentials at all — a missing tool, a broken setup, a
// timeout. It is not ErrUnauthorized: nothing was decided.
var ErrUndecided = errors.New("auth: the sign-in provider could not decide")

// Errors of GrantSuperAdmin.
var (
	// ErrAccountDisabled: the account exists and an administrator turned it off.
	// A test must not switch a disabled account back on.
	ErrAccountDisabled = errors.New("auth: the account is disabled")
	// ErrNotSupertenant: the account belongs to a tenant, so an admin role on it
	// would be a tenant administrator, not the platform's.
	ErrNotSupertenant = errors.New("auth: the account belongs to a tenant, not to the platform")
)

// superAdminStore is the slice of db.Store GrantSuperAdmin needs.
type superAdminStore interface {
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	CreateUser(ctx context.Context, email, passwordHash, role, locale, tz string) (*model.User, error)
	UpdateUserRole(ctx context.Context, id int64, role string) error
	GetProvider(ctx context.Context, id int64) (*model.Provider, error)
}

// CheckSuperAdminCandidate says, without changing anything, whether
// GrantSuperAdmin would accept the account: nil when it does not exist yet or
// is a usable platform account, ErrAccountDisabled / ErrNotSupertenant when it
// exists and cannot be promoted.
func CheckSuperAdminCandidate(ctx context.Context, store superAdminStore, email string) error {
	u, err := store.GetUserByEmail(ctx, email)
	if err != nil || u == nil {
		return nil
	}
	if !u.Enabled {
		return ErrAccountDisabled
	}
	if u.ProviderID != nil {
		if p, perr := store.GetProvider(ctx, *u.ProviderID); perr == nil && p != nil && !p.IsSupertenant {
			return ErrNotSupertenant
		}
	}
	return nil
}

// ErrOtherTenant: the account belongs to another tenant than the one an
// operating-system provider was switched on in, so it cannot become that
// tenant's administrator.
var ErrOtherTenant = errors.New("auth: the account belongs to another tenant")

// tenantAdminStore is the slice of db.Store GrantTenantAdmin needs.
type tenantAdminStore interface {
	superAdminStore
	SetUserProvider(ctx context.Context, userID, providerID int64, oidcSubject string) error
	DeleteUser(ctx context.Context, id int64) error
}

// CheckAdminCandidate is CheckSuperAdminCandidate for the scope a test account
// signed in in (docs/TENANT-ADMIN.md): the platform's own tenant (nil, or the
// supertenant) asks CheckSuperAdminCandidate; a tenant accepts an account that
// does not exist yet or is its own and enabled (ErrAccountDisabled,
// ErrOtherTenant otherwise).
func CheckAdminCandidate(ctx context.Context, store superAdminStore, email string, tenant *model.Provider) error {
	if tenant == nil || tenant.IsSupertenant {
		return CheckSuperAdminCandidate(ctx, store, email)
	}
	u, err := store.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil || u == nil {
		return nil
	}
	if !u.Enabled {
		return ErrAccountDisabled
	}
	if u.ProviderID == nil || *u.ProviderID != tenant.ID {
		return ErrOtherTenant
	}
	return nil
}

// GrantTenantAdmin makes email an administrator of a tenant: the account is
// created in the tenant (no password - it signs in through the provider) or,
// when it is the tenant's already, given the admin role. It reports whether
// anything changed. The tenant counterpart of GrantSuperAdmin, for an
// operating-system provider switched on in a tenant for the first time
// (the owner's decision, 2026-10-01).
//
// The three beats of every creation that homes an account (auth.provision):
// create, home, and on a failure to home delete the row rather than leave a
// half-made account in the platform's own tenant.
func GrantTenantAdmin(ctx context.Context, store tenantAdminStore, email string, tenant *model.Provider) (bool, error) {
	if tenant == nil || tenant.IsSupertenant {
		return GrantSuperAdmin(ctx, store, email)
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false, errors.New("auth: no account to promote")
	}
	if err := CheckAdminCandidate(ctx, store, email, tenant); err != nil {
		return false, err
	}
	u, gerr := store.GetUserByEmail(ctx, email)
	if gerr != nil || u == nil {
		created, err := store.CreateUser(ctx, email, "", model.RoleAdmin, "en", model.TimezoneUnset)
		if err != nil {
			return false, err
		}
		if err := store.SetUserProvider(ctx, created.ID, tenant.ID, ""); err != nil {
			_ = store.DeleteUser(ctx, created.ID)
			return false, err
		}
		return true, nil
	}
	if u.Role == model.RoleAdmin {
		return false, nil
	}
	if err := store.UpdateUserRole(ctx, u.ID, model.RoleAdmin); err != nil {
		return false, err
	}
	return true, nil
}

// GrantSuperAdmin makes email a platform administrator: the account is created
// (in the supertenant, no password — it signs in through the provider) or, when
// it exists, given the admin role. It reports whether anything changed.
//
// It is the ONE deliberate exception to "auto_create is off" and to the
// first-login rule: an operating-system provider that has just proved it works
// for a person must not leave that person locked out of the instance they are
// setting up. The caller has verified the account really signed in.
func GrantSuperAdmin(ctx context.Context, store superAdminStore, email string) (changed bool, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false, errors.New("auth: no account to promote")
	}
	if err := CheckSuperAdminCandidate(ctx, store, email); err != nil {
		return false, err
	}
	u, gerr := store.GetUserByEmail(ctx, email)
	if gerr != nil || u == nil {
		if _, err := store.CreateUser(ctx, email, "", model.RoleAdmin, "en", model.TimezoneUnset); err != nil {
			return false, err
		}
		return true, nil
	}
	if u.Role == model.RoleAdmin {
		return false, nil
	}
	if err := store.UpdateUserRole(ctx, u.ID, model.RoleAdmin); err != nil {
		return false, err
	}
	return true, nil
}
