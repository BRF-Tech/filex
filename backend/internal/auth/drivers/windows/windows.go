// Package windows signs people in with their Windows account: a local account
// of the machine filex runs on, or a domain account of the domain it is joined
// to.
//
// The check is the operating system's own — LogonUserW, called straight from Go
// (no cgo, no helper program, nothing to install) with a NETWORK logon, which
// judges the password and takes no interactive-session rights. What comes back
// is the account's SID and the groups of its token; the password is used for
// that one call and is never kept, logged, audited or put on a command line.
//
// On any other operating system the provider is registered (so the admin page
// lists it) but refuses to start, with a clear reason — it can never be
// "enabled" on a machine that cannot judge a Windows password.
//
// # Names
//
// `alex`, `CORP\alex`, `.\alex` and `alex@corp.example` all sign in; the
// filex username is the bare account name in lower case (`alex`), and the
// account's e-mail is derived once (identity.DeriveEmail) so every spelling
// finds the same account. See names.go.
//
// # The first sign-in
//
// The account exists → it signs in. Otherwise the ONE first-login rule applies
// (auth.ProvisionFirstLogin): auto_create — OFF by default for this provider —
// and allowed_groups, judged against the groups of the Windows token. System
// and service accounts are refused in every case (identity.CheckOSLogin, plus
// the Windows service identities and the built-in accounts by SID).
//
// # Not here
//
// Doing file operations AS the Windows user (impersonation) is deliberately out
// of scope: the person signs in as a filex account, and filex's own storage
// permissions decide what they can touch.
package windows

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
)

// DriverName is the name the provider is known by (registry, page, settings).
const DriverName = "windows"

func init() {
	auth.Register(DriverName, func() auth.Driver { return &Driver{} })
}

// logonTimeout bounds one sign-in: a domain controller that does not answer
// must not hold a login form (or a protocol connection) for as long as the
// operating system's own timeouts.
const logonTimeout = 20 * time.Second

// Driver is the Windows sign-in provider.
type Driver struct {
	store db.Store
	// domain is the operator's default domain ("" = the machine's own accounts).
	domain string
	// emailToken is the installation's e-mail token, normalised.
	emailToken string
	// firstLogin is what happens to a person with no account yet.
	firstLogin auth.FirstLoginPolicy
	// tellRefusal: show_refusal_reason (auth.RefusedAfterPassword), off by
	// default.
	tellRefusal bool
	homing      auth.TenantHoming

	// logon is swapped in tests; nil means the operating system's.
	logon logonFunc
}

// New constructs an empty driver — Init must be called.
func New(store db.Store) *Driver { return &Driver{store: store} }

// Name implements auth.Driver.
func (d *Driver) Name() string { return DriverName }

// Init configures the driver and checks that this machine can run it.
//
// ⚠ It is also the "is it still all right" check the admin page's providers get
// every time they are (re)loaded: a provider that cannot start is left out of
// the running set with its reason, and the operator is told.
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	if d.store == nil {
		return errors.New("windows: nil store")
	}
	if err := d.load(cfg); err != nil {
		return err
	}
	return d.ready()
}

// ready makes sure there is a sign-in call to make.
func (d *Driver) ready() error {
	if d.logon == nil {
		d.logon = defaultLogon
	}
	if d.logon == nil {
		return errNotWindows
	}
	return nil
}

// load reads a configuration into the driver. It needs no store and no
// operating system, so Probe tests a configuration exactly as the running
// driver would read it.
func (d *Driver) load(cfg map[string]any) error {
	d.domain = auth.CfgString(cfg, "domain")
	if d.domain != "" && !validDomain(d.domain) {
		return fmt.Errorf("windows: domain %q is not a domain name (letters, digits, dots, dashes and underscores)", d.domain)
	}
	d.firstLogin = auth.FirstLoginPolicyFrom(cfg)
	// ⚠ Off unless the operator says otherwise: an operating-system provider
	// must not turn every account of the machine into a filex account by
	// itself. (An account that already exists signs in either way.)
	d.firstLogin.AutoCreate = auth.CfgBoolDefault(cfg, "auto_create", false)
	d.tellRefusal = auth.TellsRefusal(cfg)
	d.homing.MultiTenant = auth.CfgBool(cfg, "multi_tenant")
	d.homing.Pin = auth.CfgString(cfg, "provider")
	d.emailToken = ""
	if raw := auth.CfgString(cfg, "email_token"); raw != "" {
		tok, err := identity.EmailToken(raw)
		if err != nil {
			return fmt.Errorf("windows: email_token: %w", err)
		}
		d.emailToken = tok
	}
	return nil
}

func validDomain(s string) bool {
	if len(s) > 255 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// Capabilities implements auth.Driver.
func (d *Driver) Capabilities() auth.Capabilities {
	return auth.Capabilities{SignIn: true, Logout: true}
}

// Authenticate is a no-op: a Windows account's requests carry the ordinary
// session cookie, which the local driver resolves (see the LDAP driver for why
// being "enabled" is not enough to be reachable — Login is reached through the
// login chain).
func (d *Driver) Authenticate(_ *http.Request) (*model.User, error) {
	return nil, auth.ErrUnauthorized
}

// Login verifies the credentials with Windows and mints a browser session for
// the resulting account.
func (d *Driver) Login(ctx context.Context, identifier, password string) (*model.User, string, error) {
	return authlocal.LoginWith(ctx, d.store, d.verify, identifier, password)
}

// Logout revokes a session minted by Login (and makes *Driver an
// auth.LoginDriver, so it can be placed in the login chain at all).
func (d *Driver) Logout(ctx context.Context, token string) error {
	return authlocal.RevokeSession(ctx, d.store, token)
}

// VerifyPassword checks a password with Windows and returns the local account,
// WITHOUT minting a session — the file protocols (WebDAV, SFTP, FTPS, S3, NFS)
// present the password on every request. See internal/protocolauth.
func (d *Driver) VerifyPassword(ctx context.Context, identifier, password string) (*model.User, error) {
	return d.verify(ctx, identifier, password)
}

// IsSystemAccount implements identity.SystemAccountChecker for the Windows
// service identities (machine accounts).
func (d *Driver) IsSystemAccount(_ context.Context, name string) (bool, error) {
	return strings.HasSuffix(strings.TrimSpace(name), "$"), nil
}

// verify signs the account in, applies the account rules and returns (or
// creates) the filex account.
//
// ⚠ Every refusal the person can hear is auth.ErrUnauthorized, the same as a
// wrong password, unless the operator switched show_refusal_reason on: then
// the two that come after the password (the first-login rule, a system SID)
// carry their reason (auth.RefusedAfterPassword). The reason goes to the operator: the log, and for the name
// rules an audit row. Only "the OS could not be asked" is a different error, so
// the login chain does not mistake it for a wrong password.
func (d *Driver) verify(ctx context.Context, identifier, password string) (*model.User, error) {
	// An empty password is refused up front: some sign-in paths treat one as a
	// null-session logon.
	if password == "" || strings.TrimSpace(identifier) == "" {
		return nil, auth.ErrUnauthorized
	}
	if err := d.ready(); err != nil {
		return nil, err
	}
	// The realm the account's address is made in (multi-tenant): the tenant
	// the sign-in named, else the one the pin homes it in.
	realm, home, viaPin := d.homing.DirectoryRealm(ctx, d.store)
	a, ok := parseAccount(identifier, d.domain, realm, d.emailToken)
	if !ok {
		return nil, auth.ErrUnauthorized
	}
	if _, refusal := d.checkName(ctx, a); refusal != "" {
		// Before any password is tried: a service account's or Administrator's
		// password is never even offered to the OS.
		_ = auth.RefuseFirstLogin(ctx, d.store, DriverName, identifier, refusal)
		return nil, auth.ErrUnauthorized
	}

	res, err := d.signIn(ctx, a, password)
	if err != nil {
		return nil, d.answer(ctx, identifier, err)
	}
	if sidIsSystem(res.SID) {
		// The built-in Administrator under a renamed or localised name, or any
		// other identity that is not a person: the name did not say so, the SID
		// does.
		// The SID is known only once the password was accepted: told when the
		// operator chose so (auth.RefusedAfterPassword).
		return nil, auth.RefusedAfterPassword(d.tellRefusal,
			auth.RefuseFirstLogin(ctx, d.store, DriverName, identifier, identity.RefuseForbidden))
	}

	created := false
	user, gerr := d.store.GetUserByEmail(ctx, a.Email)
	if gerr == nil && user != nil {
		// ⚠ A domain account's address (`alex@corp`) is the same in every
		// realm, so it can be another tenant's account: never signed in to —
		// or written to — through this realm.
		auth.NoteHome(ctx, home, viaPin)
		if !auth.LoginRealmAdmits(ctx, user) {
			slog.Warn("windows: the account is in another tenant than the sign-in's realm; refused",
				slog.Int64("user_id", user.ID))
			return nil, auth.ErrUnauthorized
		}
	}
	if gerr != nil || user == nil {
		var perr error
		user, perr = auth.ProvisionFirstLogin(ctx, d.store, auth.FirstLogin{
			Driver: DriverName, Identifier: identifier, Email: a.Email, Role: model.RoleUser,
			Groups: res.Groups, Policy: d.firstLogin, Homing: d.homing,
		})
		if perr != nil {
			if errors.Is(perr, auth.ErrFirstLoginRefused) {
				return nil, auth.RefusedAfterPassword(d.tellRefusal, perr)
			}
			return nil, perr
		}
		created = true
		slog.Info("windows: provisioned an account", slog.String("email", a.Email))
		auth.NoteHome(ctx, home, viaPin)
	}
	// The token is the authority on membership, recorded at every sign-in and
	// REPLACED (as for LDAP and OIDC), with the starting role of a new account
	// and the filex groups linked to these (auth.RecordSignInGroups, one rule
	// for every provider). A failure is loud, not fatal.
	return auth.RecordSignInGroups(ctx, d.store, DriverName, user, res.Groups, created), nil
}

// checkName applies the account-name rules: Windows service identities, then
// identity.CheckOSLogin (forbidden names, the protocol-safe character set).
// It returns the filex username, or the refusal reason.
func (d *Driver) checkName(ctx context.Context, a account) (name, refusal string) {
	if isServiceName(a) {
		return "", identity.RefuseForbidden
	}
	return identity.CheckOSLogin(ctx, a.Name, d)
}

// signIn calls the operating system, bounded by the request and logonTimeout.
func (d *Driver) signIn(ctx context.Context, a account, password string) (*Logon, error) {
	type result struct {
		l   *Logon
		err error
	}
	ch := make(chan result, 1)
	go func() {
		l, err := d.logon(a.LogonUser, a.LogonDomain, password)
		ch <- result{l, err}
	}()
	timer := time.NewTimer(logonTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		if r.err == nil && r.l == nil {
			return nil, errors.New("windows: the sign-in call returned nothing")
		}
		return r.l, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errors.New("windows: the sign-in call did not answer in time")
	}
}

// answer turns a failed sign-in into what the caller is told, and tells the
// operator the reason.
//
// ⚠ Only the identifier and the reason are logged: never the password.
func (d *Driver) answer(ctx context.Context, identifier string, err error) error {
	var le *LogonError
	if !errors.As(err, &le) {
		slog.Warn("windows: the sign-in could not be judged",
			slog.String("identifier", identifier), slog.String("err", err.Error()))
		return fmt.Errorf("windows: could not judge the sign-in: %w", err)
	}
	switch v, reason := judge(le.Code); v {
	case verdictWrong:
		slog.Debug("windows: sign-in refused: wrong password or unknown account",
			slog.String("identifier", identifier))
		return auth.ErrUnauthorized
	case verdictRefused:
		slog.Warn("windows: sign-in refused by the operating system",
			slog.String("identifier", identifier), slog.String("reason", reason),
			slog.String("host", auth.LoginHostFrom(ctx)))
		return auth.ErrUnauthorized
	}
	slog.Warn("windows: the sign-in could not be judged",
		slog.String("identifier", identifier), slog.Uint64("win32_error", uint64(le.Code)))
	return fmt.Errorf("windows: could not judge the sign-in (Win32 error %d)", le.Code)
}

var (
	_ auth.LoginDriver              = (*Driver)(nil)
	_ identity.SystemAccountChecker = (*Driver)(nil)
	_ auth.StrictProber             = (*Driver)(nil)
)
