package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
)

// BootstrapAdminSetting names the settings row holding the id of the
// administrator the installation created for itself on first run — the
// `admin@local` account, or the one FILEX_ADMIN_EMAIL named.
const BootstrapAdminSetting = "auth.bootstrap_admin_id"

// RecoveryLoginName is the chain name of the recovery sign-in driver.
const RecoveryLoginName = "recovery"

// RecoveryLogin lets ONE account sign in with its password when password
// sign-in is otherwise switched off: the bootstrap administrator.
//
// # Why it exists
//
// An installation that signs people in through an identity provider alone
// (`FILEX_AUTH_DRIVERS=oidc`) is locked the moment that provider is down, its
// client secret expires or its realm is misconfigured — and the one person who
// could fix filex's side of it cannot get in to do so. The owner's ruling
// (2026-09-14): the administrator created at installation must always be able
// to sign in, for recovery purposes only.
//
// # What it deliberately does not do
//
//   - It is added only when no `local` driver is enabled; with `local` in the
//     list every local account already signs in and this adds nothing.
//   - It accepts nobody but the bootstrap administrator. Every other account —
//     including another administrator with a local password — is refused
//     exactly as if the driver were not there, so turning password sign-in off
//     still means what it says for everyone else.
//   - Two-factor still applies: the login handler checks TOTP after any login
//     driver, this one included.
//   - An operator whose policy forbids any password sign-in turns it off with
//     `FILEX_AUTH_RECOVERY_LOGIN=false`.
//
// Every recovery sign-in is logged at WARN, because on an SSO-only server it
// is by definition an event somebody should know happened.
type RecoveryLogin struct {
	d     *Driver
	store db.Store
}

// NewRecoveryLogin returns the recovery sign-in driver over store.
func NewRecoveryLogin(store db.Store) *RecoveryLogin {
	return &RecoveryLogin{d: New(store), store: store}
}

// Name reports the driver's chain name.
func (r *RecoveryLogin) Name() string { return RecoveryLoginName }

// Login implements auth.LoginDriver for the bootstrap administrator only.
func (r *RecoveryLogin) Login(ctx context.Context, identifier, password string) (*model.User, string, error) {
	adminID, err := BootstrapAdminID(ctx, r.store)
	if err != nil {
		return nil, "", fmt.Errorf("recovery: read bootstrap administrator: %w", err)
	}
	if adminID == 0 {
		slog.Warn("auth: recovery sign-in refused",
			slog.String("reason", "no bootstrap administrator is recorded"),
			slog.String("identifier", identifier))
		return nil, "", auth.ErrUnauthorized
	}
	u, err := identity.Resolve(ctx, r.store, identifier)
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			return nil, "", auth.ErrUnauthorized
		}
		return nil, "", fmt.Errorf("recovery: user lookup: %w", err)
	}
	if u.ID != adminID {
		// Not an error and not worth a WARN: this is what password sign-in
		// being off looks like for everybody else.
		slog.Debug("auth: recovery sign-in refused",
			slog.String("reason", "only the bootstrap administrator may use it"),
			slog.String("identifier", identifier))
		return nil, "", auth.ErrUnauthorized
	}
	user, tok, err := r.d.Login(ctx, identifier, password)
	if err != nil {
		return nil, "", err
	}
	slog.Warn("auth: recovery sign-in as the bootstrap administrator",
		slog.Int64("user_id", user.ID),
		slog.String("identifier", identifier),
		slog.String("note", "password sign-in is off on this server; this account alone may still use it"))
	return user, tok, nil
}

// Logout implements auth.LoginDriver.
func (r *RecoveryLogin) Logout(ctx context.Context, token string) error {
	return RevokeSession(ctx, r.store, token)
}

// BootstrapAdminID reads the recorded bootstrap administrator. Zero, with no
// error, means none is recorded.
func BootstrapAdminID(ctx context.Context, store db.Store) (int64, error) {
	v, err := store.GetSetting(ctx, BootstrapAdminSetting)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(v) == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("setting %s holds %q, not a user id", BootstrapAdminSetting, v)
	}
	return id, nil
}

// RecordBootstrapAdmin stores id as the bootstrap administrator.
func RecordBootstrapAdmin(ctx context.Context, store db.Store, id int64) error {
	return store.UpsertSetting(ctx, BootstrapAdminSetting, strconv.FormatInt(id, 10))
}

// EnsureBootstrapAdmin records the bootstrap administrator on an installation
// that predates the setting, and reports which account it is.
//
// First run records it as it creates the account. An installation created
// before that has only `first_run_at`, so the account is recovered the way it
// was made: the OLDEST administrator that holds a local password. An account a
// provider created just in time has no password and is skipped — it could not
// use a password recovery anyway. Nothing is chosen when no administrator has a
// password; recovery then refuses everyone, and says why in the log.
func EnsureBootstrapAdmin(ctx context.Context, store db.Store) (*model.User, error) {
	id, err := BootstrapAdminID(ctx, store)
	if err != nil {
		return nil, err
	}
	if id != 0 {
		// A recorded account that is gone stays gone. Deleting admin@local is
		// a common hardening step on an SSO-only server, and re-picking some
		// other administrator here would quietly hand recovery to an account
		// the operator never chose.
		u, err := store.GetUser(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return u, err
	}
	users, err := store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	var pick *model.User
	for _, u := range users {
		if u.Role != model.RoleAdmin || u.PasswordHash == "" {
			continue
		}
		if pick == nil || u.ID < pick.ID {
			pick = u
		}
	}
	if pick == nil {
		return nil, nil
	}
	if err := RecordBootstrapAdmin(ctx, store, pick.ID); err != nil {
		return nil, err
	}
	return pick, nil
}
