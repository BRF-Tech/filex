// Package pam is the Linux operating-system sign-in provider: a person signs in
// to filex with the login they use on the machine filex runs on, and PAM — the
// same door SSH and su use — judges the password.
//
// # How it asks PAM (and why not another way)
//
// filex is not root and has no cgo. It runs a command the administrator has
// set up: by default
//
//	sudo -n /usr/bin/pamtester filex <user> authenticate acct_mgmt
//
// with the password on STANDARD INPUT. `authenticate` checks the password;
// `acct_mgmt` checks the account (expired, locked, not permitted to log in).
// pamtester was chosen over a checkpassword-style helper because sudo closes
// every file descriptor above 2 — a password handed over on fd 3 never arrives.
//
// ⚠⚠ The argv is BUILT here, never composed from a template string and never
// through a shell: the user name is one argv element, checked to be a plain
// login name that does not start with a dash; the command path and the PAM
// service name are configuration, checked to be plain too. There is no place an
// administrator's or a visitor's text can become a shell command. The password
// is written to the pipe and nowhere else — not argv (visible in `ps`), not the
// environment, not a log line.
//
// # Three answers, kept apart
//
//   - yes  — PAM accepted the password and the account;
//   - no   — PAM refused (wrong password, unknown user, locked or expired
//     account): auth.ErrUnauthorized;
//   - could not decide — pamtester is missing, sudo wants a password, PAM
//     itself is failing, the command timed out: an error that is NOT
//     ErrUnauthorized (auth.ErrUndecided), so the operator's log shows it and a
//     misconfigured server never looks like a wrong password.
//
// Setup is checked before the provider may be switched on (probe.go) and again
// every time filex starts.
package pam

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Name is the provider's name.
const Name = "pam"

// Defaults.
const (
	DefaultPamtester     = "/usr/bin/pamtester"
	DefaultService       = "filex"
	DefaultSudo          = "/usr/bin/sudo"
	DefaultTimeout       = 10 * time.Second
	DefaultMaxConcurrent = 4
)

// AuditProviderUnavailable is the audit action written when the provider's
// setup is found broken at start-up and it is left out of the running set.
const AuditProviderUnavailable = "auth.provider_unavailable"

// AuditOSAdminGranted is the audit action of the test account becoming a super
// administrator.
const AuditOSAdminGranted = "auth.os_admin_granted"

func init() {
	auth.Register(Name, func() auth.Driver { return &Driver{} })
}

var (
	// pathRe: an absolute path of plain characters — no space, quote, glob or
	// dollar sign for a sudoers line or a log to trip over.
	pathRe = regexp.MustCompile(`^/[A-Za-z0-9._+/-]+$`)
	// serviceRe: a PAM service name, i.e. a file name in /etc/pam.d.
	serviceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// userRe: the only shape of user name that reaches an argv. Starts with a
	// letter (never `-`, so it cannot be read as an option), then plain
	// characters. identity.CheckOSLogin already narrows further; this is the
	// last line of defence at the place the argv is built.
	userRe = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
)

// settings is the command the administrator set up.
type settings struct {
	pamtester string // absolute path
	service   string // PAM service name
	useSudo   bool
	sudo      string // absolute path of sudo
	timeout   time.Duration
	maxConc   int
}

func defaultSettings() settings {
	return settings{pamtester: DefaultPamtester, service: DefaultService, useSudo: true, sudo: DefaultSudo,
		timeout: DefaultTimeout, maxConc: DefaultMaxConcurrent}
}

// parseSettings reads and VALIDATES the command settings.
func parseSettings(cfg map[string]any) (settings, error) {
	s := defaultSettings()
	if v := auth.CfgString(cfg, "pamtester_path"); v != "" {
		s.pamtester = v
	}
	if v := auth.CfgString(cfg, "service"); v != "" {
		s.service = v
	}
	if v := auth.CfgString(cfg, "sudo_path"); v != "" {
		s.sudo = v
	}
	s.useSudo = auth.CfgBoolDefault(cfg, "use_sudo", true)
	if v := auth.CfgString(cfg, "timeout_seconds"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 3 || n > 60 {
			return s, errors.New("pam: timeout_seconds must be a whole number from 3 to 60")
		}
		s.timeout = time.Duration(n) * time.Second
	}
	if v := auth.CfgString(cfg, "max_concurrent"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 64 {
			return s, errors.New("pam: max_concurrent must be a whole number from 1 to 64")
		}
		s.maxConc = n
	}
	if !pathRe.MatchString(s.pamtester) || strings.Contains(s.pamtester, "..") {
		return s, errors.New("pam: pamtester_path must be an absolute path of plain characters")
	}
	if s.useSudo && (!pathRe.MatchString(s.sudo) || strings.Contains(s.sudo, "..")) {
		return s, errors.New("pam: sudo_path must be an absolute path of plain characters")
	}
	if !serviceRe.MatchString(s.service) {
		return s, errors.New("pam: service must be a PAM service name (letters, digits, dot, dash, underscore)")
	}
	return s, nil
}

// wrap puts the sudo prefix in front of a command when sudo is in use.
func (s settings) wrap(argv ...string) []string {
	if !s.useSudo {
		return argv
	}
	return append([]string{s.sudo, "-n"}, argv...)
}

// authArgv is THE argument vector of a sign-in: a fixed shape with the user
// name in one slot. An error means the name is not one that may reach a
// command line.
func (s settings) authArgv(user string) ([]string, error) {
	if !userRe.MatchString(user) {
		return nil, fmt.Errorf("pam: %q is not a plain login name", user)
	}
	return s.wrap(s.pamtester, s.service, user, "authenticate", "acct_mgmt"), nil
}

// Driver is the PAM sign-in provider.
type Driver struct {
	store db.Store
	set   settings

	firstLogin auth.FirstLoginPolicy
	// tellRefusal: show_refusal_reason (auth.RefusedAfterPassword), off by
	// default.
	tellRefusal bool
	homing      auth.TenantHoming
	// emailToken / emailDomain decide the address a login name gets: `alex`
	// is `alex@<email_domain>`, or `alex@<token>` with no domain.
	emailToken  string
	emailDomain string

	run runner
	sem chan struct{}

	// getent / idTool are the account-database tools; tests point them at
	// scripts.
	getent string
	idTool string
}

// New constructs an empty driver — Init must be called.
func New(store db.Store) *Driver {
	return &Driver{store: store, set: defaultSettings(), run: defaultRunner, getent: "getent", idTool: "id"}
}

// Name implements auth.Driver.
func (d *Driver) Name() string { return Name }

// Capabilities implements auth.Driver.
func (d *Driver) Capabilities() auth.Capabilities {
	return auth.Capabilities{SignIn: true, Logout: true}
}

// Authenticate is a no-op, as the LDAP driver's: the session cookie is
// resolved by the local driver.
func (d *Driver) Authenticate(_ *http.Request) (*model.User, error) {
	return nil, auth.ErrUnauthorized
}

// StrictProbe implements auth.StrictProber: this provider cannot be switched
// on past a failing test.
func (d *Driver) StrictProbe() bool { return true }

// policyFrom is the first-login policy of this provider: like the others,
// except that auto_create defaults to OFF — an operating-system login must not
// open an account for everyone who has a Unix account on the box.
func policyFrom(cfg map[string]any) auth.FirstLoginPolicy {
	return auth.FirstLoginPolicy{
		AutoCreate:    auth.CfgBoolDefault(cfg, "auto_create", false),
		AllowedGroups: auth.SplitList(auth.CfgString(cfg, "allowed_groups")),
	}
}

// load reads a configuration into the driver without touching the system, so
// Probe tests a configuration exactly the way the running driver would use it.
func (d *Driver) load(cfg map[string]any) error {
	s, err := parseSettings(cfg)
	if err != nil {
		return err
	}
	d.set = s
	d.firstLogin = policyFrom(cfg)
	d.tellRefusal = auth.TellsRefusal(cfg)
	d.homing.MultiTenant = auth.CfgBool(cfg, "multi_tenant")
	d.homing.Pin = auth.CfgString(cfg, "provider")
	tok, err := identity.EmailToken(auth.CfgString(cfg, "email_token"))
	if err != nil {
		return fmt.Errorf("pam: email_token: %w", err)
	}
	d.emailToken = tok
	d.emailDomain = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(auth.CfgString(cfg, "email_domain"), "@")))
	d.sem = make(chan struct{}, s.maxConc)
	if d.run == nil {
		d.run = defaultRunner
	}
	if d.getent == "" {
		d.getent = "getent"
	}
	if d.idTool == "" {
		d.idTool = "id"
	}
	return nil
}

// Init configures the driver AND re-checks the machine's setup (steps 1-4 of
// the provider test). A setup that has broken since it was switched on — the
// package was removed, sudoers was edited — keeps THIS provider out of the
// running set, with the reason in the error, the log and an audit row; every
// other sign-in path is unaffected.
func (d *Driver) Init(ctx context.Context, cfg map[string]any) error {
	if d.store == nil {
		return errors.New("pam: nil store")
	}
	if err := d.load(cfg); err != nil {
		return err
	}
	checks := d.setupChecks(ctx)
	for _, c := range checks {
		if c.Status != auth.ProbeFail {
			continue
		}
		reason := c.Params["reason"]
		slog.Error("pam: the operating-system sign-in is switched on but its setup no longer works; it stays out of the running set until it does",
			slog.String("step", c.ID), slog.String("reason", reason), slog.String("detail", c.Params["detail"]))
		if err := d.store.InsertAuditEntry(ctx, &model.AuditEntry{
			Action: AuditProviderUnavailable, TargetType: "login", TargetID: Name,
			Metadata: map[string]any{"provider": Name, "step": c.ID, "reason": reason},
		}); err != nil {
			slog.Warn("pam: could not write the audit row of a broken setup", slog.String("err", err.Error()))
		}
		return fmt.Errorf("pam: setup check %q failed (%s): %s", c.ID, reason, c.Params["hint"])
	}
	return nil
}

// Login verifies the credentials and mints a browser session.
func (d *Driver) Login(ctx context.Context, identifier, password string) (*model.User, string, error) {
	return authlocal.LoginWith(ctx, d.store, d.VerifyPassword, identifier, password)
}

// Logout revokes a session minted by Login.
func (d *Driver) Logout(ctx context.Context, token string) error {
	return authlocal.RevokeSession(ctx, d.store, token)
}

// osName turns what was typed — `alex`, or `alex@<domain>` — into the operating
// system's login name. An address of some other domain is not ours.
//
// With email_domain the address is `alex@<email_domain>` in every realm; without
// it, the address this provider makes up — `alex@<token>`, or
// `alex@<realm>.<token>` in a tenant's realm — is read back by the one
// derivation every provider shares (identity.LoginNameOf).
func (d *Driver) osName(identifier, realm string) (string, bool) {
	id := identity.Normalize(identifier)
	i := strings.LastIndex(id, "@")
	if i < 0 {
		return id, id != ""
	}
	if d.emailDomain != "" {
		if id[i+1:] != d.emailDomain || i == 0 {
			return "", false
		}
		return id[:i], true
	}
	return identity.LoginNameOf(id, realm, d.emailToken)
}

// email is the account address of a login name in a realm ("" = the
// platform's own tenant, or a single-tenant install).
func (d *Driver) email(name, realm string) string {
	return identity.DeriveEmail(name, d.emailDomain, realm, d.emailToken)
}

// VerifyPassword is the sign-in — name checks, PAM, groups, then the account —
// and the protocol path (WebDAV, SFTP, FTPS, S3): the password is judged, no
// session is minted. The resolver hands over either the typed login name or the
// account's e-mail.
func (d *Driver) VerifyPassword(ctx context.Context, identifier, password string) (*model.User, error) {
	if d.sem == nil {
		return nil, fmt.Errorf("%w: pam: not initialised", auth.ErrUndecided)
	}
	// An empty password is refused up front (PAM stacks with nullok would
	// accept it), and so is one holding a line break or NUL: the password is
	// written to a pipe one line at a time, and a second line would answer a
	// prompt the person was never shown.
	if password == "" || strings.TrimSpace(identifier) == "" || strings.ContainsAny(password, "\r\n\x00") {
		return nil, auth.ErrUnauthorized
	}
	// The realm the account's address is made in (multi-tenant): the tenant
	// the sign-in named, else the one the pin homes it in.
	realm, home, viaPin := d.homing.DirectoryRealm(ctx, d.store)
	name, ok := d.osName(identifier, realm)
	if !ok {
		return nil, auth.ErrUnauthorized
	}
	// The shape that may reach a command line, before anything is run. The
	// account rules below are looser (identity.Check allows a leading dash or
	// dot); a name like that is simply not a login this provider can ask about.
	if !userRe.MatchString(name) {
		_ = auth.RefuseFirstLogin(ctx, d.store, Name, identifier, identity.RefuseInvalidName)
		return nil, auth.ErrUnauthorized
	}
	// A limit on simultaneous attempts: each is a process (and PAM waits a
	// couple of seconds after a failure on purpose). Full → answer "busy" at
	// once rather than queueing without end.
	select {
	case d.sem <- struct{}{}:
		defer func() { <-d.sem }()
	default:
		slog.Warn("pam: too many sign-ins at once; this one was turned away with a try-again answer",
			slog.Int("limit", cap(d.sem)))
		return nil, auth.ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, d.set.timeout)
	defer cancel()

	// Forbidden accounts first, before PAM is ever asked about them.
	chk := &systemChecker{d: d}
	name, refusal := identity.CheckOSLogin(ctx, name, chk)
	if refusal != "" && chk.err != nil {
		// The operating system could not be asked (no getent, a timeout): the
		// account is refused, as the rule says, but this is a setup problem the
		// operator must see, not a forbidden account.
		slog.Warn("pam: could not tell whether the account is a system account; refused",
			slog.String("err", chk.err.Error()))
		return nil, fmt.Errorf("%w: pam: getent", auth.ErrUndecided)
	}
	if refusal != "" {
		_ = auth.RefuseFirstLogin(ctx, d.store, Name, identifier, refusal)
		return nil, auth.ErrUnauthorized
	}

	argv, err := d.set.authArgv(name)
	if err != nil {
		_ = auth.RefuseFirstLogin(ctx, d.store, Name, identifier, identity.RefuseInvalidName)
		return nil, auth.ErrUnauthorized
	}
	res, rerr := d.run.Run(ctx, argv, password+"\n")
	out := classify(res, rerr)
	switch out.V {
	case verdictDenied:
		slog.Debug("pam: refused", slog.String("user", name), slog.String("pam", out.Detail))
		return nil, auth.ErrUnauthorized
	case verdictUndecided:
		slog.Warn("pam: could not decide a sign-in; the setup needs attention (Admin → Identity providers → pam → Test now)",
			slog.String("user", name), slog.String("reason", out.Reason), slog.String("detail", out.Detail))
		return nil, fmt.Errorf("%w: pam: %s", auth.ErrUndecided, out.Reason)
	}

	groups, err := d.groupsOf(ctx, name)
	if err != nil {
		slog.Warn("pam: the password was right but the account's groups could not be read",
			slog.String("user", name), slog.String("err", err.Error()))
		return nil, fmt.Errorf("%w: pam: groups", auth.ErrUndecided)
	}

	em := d.email(name, realm)
	created := false
	user, gerr := d.store.GetUserByEmail(ctx, em)
	if gerr == nil && user != nil {
		// ⚠ An address with email_domain is the same in every realm, so it can
		// be another tenant's account: never signed in to — or written to —
		// through this realm.
		auth.NoteHome(ctx, home, viaPin)
		if !auth.LoginRealmAdmits(ctx, user) {
			slog.Warn("pam: the account is in another tenant than the sign-in's realm; refused", slog.Int64("user_id", user.ID))
			return nil, auth.ErrUnauthorized
		}
	}
	if gerr != nil || user == nil {
		user, err = auth.ProvisionFirstLogin(ctx, d.store, auth.FirstLogin{
			Driver: Name, Identifier: name, Email: em, Role: model.RoleUser,
			Groups: groups, Policy: d.firstLogin, Homing: d.homing,
		})
		if err != nil {
			if errors.Is(err, auth.ErrFirstLoginRefused) {
				// PAM said yes: the password was right. The wrong-password
				// answer, unless the operator chose to tell why.
				return nil, auth.RefusedAfterPassword(d.tellRefusal, err)
			}
			return nil, err
		}
		created = true
		slog.Info("pam: provisioned an operating-system account", slog.String("email", em))
		auth.NoteHome(ctx, home, viaPin)
	}
	// The machine is the authority on membership: recorded and REPLACED at
	// every sign-in, with the starting role of a new account and the filex
	// groups linked to these (auth.RecordSignInGroups, one rule for every
	// provider). A failure is loud, not fatal.
	return auth.RecordSignInGroups(ctx, d.store, Name, user, groups, created), nil
}

// groupsOf lists the operating-system groups of a login: `id -Gn -- <name>`.
func (d *Driver) groupsOf(ctx context.Context, name string) ([]string, error) {
	if !userRe.MatchString(name) {
		return nil, fmt.Errorf("not a plain login name")
	}
	res, err := d.run.Run(ctx, []string{d.idTool, "-Gn", "--", name}, "")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("id exited %d", res.ExitCode)
	}
	return strings.Fields(res.Stdout), nil
}

// systemChecker asks the operating system whether a login is a system or
// service account (identity.SystemAccountChecker): `getent passwd <name>`, then
// UID below 1000 or a nologin / false shell. /etc/passwd is not read by hand —
// getent also sees sssd, LDAP and every other source the machine uses.
type systemChecker struct {
	d *Driver
	// err is the last failure to ask (not a "yes, it is a service account").
	err error
}

func (c *systemChecker) IsSystemAccount(ctx context.Context, name string) (bool, error) {
	sys, err := c.ask(ctx, name)
	if err != nil {
		c.err = err
	}
	return sys, err
}

func (c *systemChecker) ask(ctx context.Context, name string) (bool, error) {
	if !userRe.MatchString(name) {
		return false, fmt.Errorf("not a plain login name")
	}
	res, err := c.d.run.Run(ctx, []string{c.d.getent, "passwd", name}, "")
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
	case 2:
		// getent's "no such entry": nothing to be a service account. PAM will
		// say no to a login it does not know.
		return false, nil
	default:
		return false, fmt.Errorf("getent exited %d", res.ExitCode)
	}
	return isSystemEntry(res.Stdout)
}

// isSystemEntry judges one passwd line: name:x:uid:gid:gecos:home:shell.
func isSystemEntry(line string) (bool, error) {
	line = strings.TrimSpace(firstLine(line))
	f := strings.Split(line, ":")
	if len(f) < 7 {
		return false, fmt.Errorf("unreadable passwd entry")
	}
	uid, err := strconv.Atoi(f[2])
	if err != nil {
		return false, fmt.Errorf("unreadable passwd entry")
	}
	if uid < 1000 {
		return true, nil
	}
	switch path.Base(strings.TrimSpace(f[6])) {
	case "nologin", "false":
		return true, nil
	}
	return false, nil
}

// GrantTestAccount implements auth.TestAccountGranter: the account the setup
// test signed in with becomes a super administrator of this filex — created
// when it has no account, raised when it has one. It is the ONE place this
// provider makes an administrator, and it does so only for the account whose
// password just passed a real PAM sign-in in the same request.
func (d *Driver) GrantTestAccount(ctx context.Context, store any, cfg map[string]any, username string) error {
	st, ok := store.(db.Store)
	if !ok || st == nil {
		return errors.New("pam: no store")
	}
	p := &Driver{}
	if err := p.load(cfg); err != nil {
		return err
	}
	name := identity.Normalize(username)
	// The platform's own tenant: the test account becomes ITS administrator.
	em := p.email(name, "")
	created := false
	u, err := st.GetUserByEmail(ctx, em)
	if err != nil || u == nil {
		// CreateUser homes the account in the default (super) tenant.
		u, err = st.CreateUser(ctx, em, "", model.RoleAdmin, "en", model.TimezoneUnset)
		if err != nil {
			return fmt.Errorf("pam: create the administrator account: %w", err)
		}
		created = true
	} else if u.Role != model.RoleAdmin {
		if err := st.UpdateUserRole(ctx, u.ID, model.RoleAdmin); err != nil {
			return fmt.Errorf("pam: raise the account to administrator: %w", err)
		}
	}
	if super, serr := st.GetSupertenant(ctx); serr == nil && super != nil && (u.ProviderID == nil || *u.ProviderID != super.ID) {
		if err := st.SetUserProvider(ctx, u.ID, super.ID, u.OIDCSubject); err != nil {
			return fmt.Errorf("pam: home the administrator in the platform tenant: %w", err)
		}
	}
	if err := st.InsertAuditEntry(ctx, &model.AuditEntry{
		Action: AuditOSAdminGranted, TargetType: "user", TargetID: strconv.FormatInt(u.ID, 10),
		Metadata: map[string]any{"provider": Name, "account": name, "created": created},
	}); err != nil {
		slog.Warn("pam: could not write the audit row of the administrator grant", slog.String("err", err.Error()))
	}
	return nil
}

var (
	_ auth.Driver                   = (*Driver)(nil)
	_ auth.LoginDriver              = (*Driver)(nil)
	_ auth.StrictProber             = (*Driver)(nil)
	_ auth.TestAccountGranter       = (*Driver)(nil)
	_ identity.SystemAccountChecker = (*systemChecker)(nil)
)
