// Package ldap implements simple-bind LDAP/Active Directory authentication.
//
// On a successful bind the account is upserted into the local users table, so
// RBAC grants, shares and quotas treat a directory user like any other. The
// browser session that follows is the ordinary `filex_session` cookie — minted
// through internal/auth/drivers/local so there is exactly one definition of a
// session's shape.
//
// # Two entry points, deliberately
//
//   - Login is the browser path: verify, then mint a session.
//   - VerifyPassword is the protocol path (WebDAV, SFTP, FTPS, S3, NFS): verify
//     and nothing else. Those protocols present the password on EVERY request;
//     minting a session per request would fill the sessions table with rows
//     nobody can ever use or revoke.
//
// Both share verify(), so the directory is consulted in exactly one way.
package ldap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/netguard"
)

func init() {
	auth.Register("ldap", func() auth.Driver { return &Driver{} })
}

// conn is the slice of *ldap.Conn this driver uses. It exists so the search
// and bind sequence can be tested without a directory: the failure this driver
// shipped with was in the CALL PATH, not in the LDAP protocol, and a test that
// needs a live AD to run is a test nobody runs.
type conn interface {
	StartTLS(*tls.Config) error
	Bind(username, password string) error
	Search(*ldap.SearchRequest) (*ldap.SearchResult, error)
	SearchWithPaging(*ldap.SearchRequest, uint32) (*ldap.SearchResult, error)
	Close() error
}

// Driver is the LDAP/AD auth driver — one directory. An install may have
// several (Admin → Identity providers → Add a provider): each is its own
// instance, and directory is its slug.
type Driver struct {
	store db.Store
	// directory is the instance's slug ("ldap" for the first; authsetup
	// sets it). The accounts it makes are its own (users.auth_directory),
	// and so are its people's permanent ids and its sync. emailDomains, when
	// set, are the only e-mail domains it signs in or opens accounts for.
	directory    string
	emailDomains []string
	// ownerTenant is the tenant a tenant's own instance belongs to (its
	// administrator added it; authsetup sets owner_tenant), 0 for the
	// platform's. Directory sync opens its groups in that tenant and reaches
	// that tenant's accounts only.
	ownerTenant int64
	url         string // ldap:// or ldaps://
	bindDN      string // service account
	bindPass    string
	baseDN      string
	userFilter  string // e.g. "(mail=%s)"
	emailAttr   string // e.g. "mail"
	startTLS    bool
	caFile      string // optional PEM bundle for a private CA
	// caPEM is a private CA pasted as PEM text: a tenant's own directory
	// (docs/TENANT-ADMIN.md) names no file on the server.
	caPEM string
	// guarded: a tenant's own directory. The connection goes through
	// internal/netguard (no loopback, private, link-local or overlay address,
	// judged on the resolved address at connect time) and must be encrypted
	// (ldaps:// or StartTLS): the bind password crosses a network the platform
	// operator does not own. Set by authsetup, never by a form.
	guarded bool
	// homing decides which tenant a just-in-time account lands in. Zero value
	// (MultiTenant=false) is the single-tenant install and does nothing.
	homing auth.TenantHoming
	// firstLogin is what happens to a directory person with no account yet
	// (auth.ProvisionFirstLogin). Default: open one, as this driver always did.
	firstLogin auth.FirstLoginPolicy
	// tellRefusal: show_refusal_reason, OFF by default. On, a person whose
	// directory password was right but whom the first-login rule refuses is
	// told why (auth.RefusedAfterPassword); off, they get the wrong-password
	// answer.
	tellRefusal bool
	// groupAttr is the entry attribute that lists a person's groups; "" = groups
	// are neither read nor recorded (the behaviour before the first-login rule).
	// Set by `group_attr`, or to memberOf when allowed_groups needs them.
	groupAttr string
	// emailToken is the installation's e-mail token (FILEX_OS_LOGIN_EMAIL_TOKEN,
	// identity.EmailToken; never empty after load — unset is `local`): an entry
	// that names no e-mail, signed in to by a bare name, is `<name>@<token>`.
	// The account an older build keyed by the bare name is adopted
	// (auth.AdoptAccount), not doubled.
	emailToken string
	// LDAP links (docs/LDAP.md → Groups): groupFilter, when set, finds a
	// person's groups by a search — (member=%s) with %s their DN, or
	// (memberUid=%u) with %u the name they signed in with — under groupBaseDN
	// (base_dn when empty) instead of reading group_attr (memberOf when
	// unset) off their entry.
	groupFilter string
	groupBaseDN string
	// Directory sync (sync.go): how often (0 = only when asked), the search
	// that lists every person (empty = user_filter with "*"), and whether an
	// account the directory stopped listing is switched off.
	syncInterval       time.Duration
	syncFilterRaw      string
	syncDisableMissing bool
	// importGroupsOn brings every directory group in as a filex group
	// (sync_groups.go) — on unless sync_groups is false; syncGroupFilterRaw
	// picks which (empty: every group).
	importGroupsOn     bool
	syncGroupFilterRaw string

	// dial is swapped in tests. Nil means the real dialer.
	dial func(ctx context.Context) (conn, error)
}

// New constructs an empty driver — Init must be called.
func New(store db.Store) *Driver {
	return &Driver{store: store, emailAttr: "mail", userFilter: "(mail=%s)"}
}

// Name implements auth.Driver.
func (d *Driver) Name() string { return "ldap" }

// Directory is the instance's slug: "ldap" for the first.
func (d *Driver) Directory() string {
	if d.directory == "" {
		return model.MainDirectory
	}
	return d.directory
}

// Init configures the driver.
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	if d.store == nil {
		return errors.New("ldap: nil store")
	}
	return d.load(cfg)
}

// load reads a configuration into the driver — everything Init does except
// needing a store, so Probe (probe.go) tests a configuration exactly the way
// the running driver would use it.
//
// ⚠ start_tls and multi_tenant are read as bools OR as the strings the
// settings table stores ("true"); a bare type assertion to bool read the
// panel's saved "true" as false.
func (d *Driver) load(cfg map[string]any) error {
	d.directory = auth.CfgString(cfg, "directory")
	d.ownerTenant = 0
	switch v := cfg[OwnerTenantKey].(type) {
	case int64:
		d.ownerTenant = v
	case int:
		d.ownerTenant = int64(v)
	case float64:
		d.ownerTenant = int64(v)
	case string:
		d.ownerTenant, _ = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	}
	d.emailDomains = nil
	for _, dom := range strings.FieldsFunc(strings.ToLower(auth.CfgString(cfg, "email_domains")), func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	}) {
		d.emailDomains = append(d.emailDomains, strings.TrimPrefix(dom, "@"))
	}
	d.url = auth.CfgString(cfg, "url")
	d.bindDN = auth.CfgString(cfg, "bind_dn")
	d.bindPass, _ = cfg["bind_password"].(string)
	d.baseDN = auth.CfgString(cfg, "base_dn")
	if d.userFilter == "" {
		d.userFilter = "(mail=%s)"
	}
	if d.emailAttr == "" {
		d.emailAttr = "mail"
	}
	if v := auth.CfgString(cfg, "user_filter"); v != "" {
		d.userFilter = v
	}
	if v := auth.CfgString(cfg, "email_attr"); v != "" {
		d.emailAttr = v
	}
	d.startTLS = auth.CfgBool(cfg, "start_tls")
	d.caFile = auth.CfgString(cfg, "ca_file")
	d.caPEM, _ = cfg["ca_pem"].(string)
	d.guarded = auth.CfgBool(cfg, "guarded")
	d.homing.MultiTenant = auth.CfgBool(cfg, "multi_tenant")
	d.homing.Pin = auth.CfgString(cfg, "provider")
	d.firstLogin = auth.FirstLoginPolicyFrom(cfg)
	d.tellRefusal = auth.TellsRefusal(cfg)
	d.groupAttr = auth.CfgString(cfg, "group_attr")
	if d.groupAttr == "" && len(d.firstLogin.AllowedGroups) > 0 {
		d.groupAttr = defaultGroupAttr
	}
	d.groupFilter = auth.CfgString(cfg, "group_filter")
	d.groupBaseDN = auth.CfgString(cfg, "group_base_dn")
	d.syncFilterRaw = auth.CfgString(cfg, "sync_filter")
	d.syncDisableMissing = auth.CfgBool(cfg, "sync_disable_missing")
	d.syncGroupFilterRaw = auth.CfgString(cfg, "sync_group_filter")
	d.importGroupsOn = true
	if v, ok := cfg["sync_groups"]; ok && v != nil && fmt.Sprint(v) != "" {
		d.importGroupsOn = auth.CfgBool(cfg, "sync_groups")
	}
	d.syncInterval = 0
	if v := strings.TrimSpace(auth.CfgString(cfg, "sync_interval")); v != "" && v != "0" {
		iv, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("ldap: sync_interval %q: a duration such as 30m or 6h", v)
		}
		if iv < minSyncInterval {
			return fmt.Errorf("ldap: sync_interval %s is shorter than %s", iv, minSyncInterval)
		}
		d.syncInterval = iv
	}
	tok, err := identity.EmailToken(auth.CfgString(cfg, "email_token"))
	if err != nil {
		return fmt.Errorf("ldap: email_token: %w", err)
	}
	d.emailToken = tok
	if d.url == "" || d.baseDN == "" {
		return errors.New("ldap: url and base_dn required")
	}
	if d.guarded && !d.startTLS && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(d.url)), "ldaps://") {
		return errors.New("ldap: a tenant's own directory needs ldaps:// or StartTLS - its bind password crosses a network")
	}
	if d.caFile != "" || strings.TrimSpace(d.caPEM) != "" {
		// Read it now: a typo in the path must be a boot-time complaint, not a
		// login-time one. A CA that cannot be loaded would otherwise fall back
		// to the system roots and reject every login with a TLS error that
		// looks like a directory problem.
		if _, err := d.tlsConfig(); err != nil {
			return fmt.Errorf("ldap: ca_file: %w", err)
		}
	}
	return nil
}

// Capabilities implements auth.Driver.
func (d *Driver) Capabilities() auth.Capabilities {
	return auth.Capabilities{
		SignIn:         true,
		Logout:         true,
		ChangePassword: false,
		Register:       false,
	}
}

// Authenticate is a no-op: this driver has no per-request credential to read.
// A directory account's requests carry the same session cookie as everyone
// else's, and the local driver resolves those.
//
// ⚠ This method being a flat refusal is exactly why the driver used to be
// unreachable: the auth middleware walks the enabled drivers calling
// Authenticate, so being "enabled" told it nothing. Login is reached through
// auth.LoginChain instead — see internal/auth/chain.go.
func (d *Driver) Authenticate(_ *http.Request) (*model.User, error) {
	return nil, auth.ErrUnauthorized
}

// tlsConfig returns the TLS settings for both ldaps:// and StartTLS.
//
// With no ca_file the result is nil, which is Go's default verification
// against the system trust store — unchanged behaviour. With one, the system
// pool is CLONED and the extra CA appended, so a private directory CA is
// trusted WITHOUT dropping every public root (the alternative, a pool holding
// only the private CA, breaks nothing here but is a footgun the moment the
// same file is pointed at a public directory).
func (d *Driver) tlsConfig() (*tls.Config, error) {
	pem := []byte(d.caPEM)
	where := "the pasted CA"
	if d.caFile != "" {
		var err error
		if pem, err = os.ReadFile(d.caFile); err != nil {
			return nil, err
		}
		where = d.caFile
	} else if strings.TrimSpace(d.caPEM) == "" {
		return nil, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no PEM certificate found in %s", where)
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
}

// startTLSConfig is the TLS configuration for StartTLS on an ldap://
// address: tlsConfig's trust roots plus the server name the certificate is
// checked against.
//
// ⚠ tlsConfig answers nil when no ca_file is set, and `tls.Client(conn, nil)`
// refuses to handshake ("either ServerName or InsecureSkipVerify must be
// specified"), so StartTLS without a private CA never worked. Found while
// writing the provider test (probe.go), which would otherwise have passed a
// configuration every login then failed.
func (d *Driver) startTLSConfig() (*tls.Config, error) {
	tc, err := d.tlsConfig()
	if err != nil {
		return nil, err
	}
	if tc == nil {
		tc = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		tc = tc.Clone()
	}
	if tc.ServerName == "" {
		if u, perr := url.Parse(d.url); perr == nil {
			tc.ServerName = u.Hostname()
		}
	}
	return tc, nil
}

// dialTimeout bounds how long a login (and a provider test) waits for the
// directory to answer at all: a firewalled port used to hang the sign-in
// form for as long as the operating system's own TCP timeout.
const dialTimeout = 10 * time.Second

// connect dials the directory and applies StartTLS plus the service bind.
func (d *Driver) connect(ctx context.Context) (conn, error) {
	if d.dial != nil {
		return d.dial(ctx)
	}
	tc, err := d.tlsConfig()
	if err != nil {
		return nil, fmt.Errorf("ldap: ca_file: %w", err)
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	if d.guarded {
		// Judged on the resolved address, right before the socket connects.
		dialer.Control = netguard.Control
	}
	opts := []ldap.DialOpt{ldap.DialWithDialer(dialer)}
	if tc != nil {
		opts = append(opts, ldap.DialWithTLSConfig(tc))
	}
	c, err := ldap.DialURL(d.url, opts...)
	if err != nil {
		return nil, fmt.Errorf("ldap: dial: %w", err)
	}
	return c, nil
}

// Login verifies the credentials against the directory and mints a browser
// session for the resulting account.
//
// The browser sign-in is also where the person's LDAP links are brought in
// step with the directory (syncLinkGroups); the file protocols, which present
// the password on every request, never move those memberships.
func (d *Driver) Login(ctx context.Context, identifier, password string) (*model.User, string, error) {
	return authlocal.LoginWith(ctx, d.store, func(ctx context.Context, identifier, password string) (*model.User, error) {
		return d.verifyWith(ctx, identifier, password, true)
	}, identifier, password)
}

// Logout revokes a session minted by Login.
//
// ⚠ Its real job is making *Driver satisfy auth.LoginDriver. Without it the
// driver could not be placed in the login chain AT ALL — the compiler said
// "missing method Logout" — which is half of why the directory path was
// unreachable.
func (d *Driver) Logout(ctx context.Context, token string) error {
	return authlocal.RevokeSession(ctx, d.store, token)
}

// VerifyPassword checks a password against the directory and returns the local
// account, WITHOUT minting a session. This is what the non-HTTP protocols use;
// see internal/protocolauth.
func (d *Driver) VerifyPassword(ctx context.Context, identifier, password string) (*model.User, error) {
	return d.verify(ctx, identifier, password)
}

// verify performs the search-then-bind and upserts the account.
func (d *Driver) verify(ctx context.Context, identifier, password string) (*model.User, error) {
	return d.verifyWith(ctx, identifier, password, false)
}

// verifyWith is verify; withLinks also brings the person's LDAP links in step
// with the directory (a browser sign-in).
func (d *Driver) verifyWith(ctx context.Context, identifier, password string, withLinks bool) (*model.User, error) {
	// An empty password is refused up front: many directories treat a bind
	// with an empty password as a successful ANONYMOUS bind, which would turn
	// "no password" into "authenticated as whoever was searched for".
	if password == "" || strings.TrimSpace(identifier) == "" {
		return nil, auth.ErrUnauthorized
	}
	c, err := d.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	if d.startTLS {
		tc, err := d.startTLSConfig()
		if err != nil {
			return nil, fmt.Errorf("ldap: ca_file: %w", err)
		}
		if err := c.StartTLS(tc); err != nil {
			return nil, fmt.Errorf("ldap: starttls: %w", err)
		}
	}
	if d.bindDN != "" {
		if err := c.Bind(d.bindDN, d.bindPass); err != nil {
			return nil, fmt.Errorf("ldap: service bind: %w", err)
		}
	}

	// The realm the account's address is made in (multi-tenant): the tenant
	// the sign-in named, else the one the pin homes it in — `alex@acme.local`,
	// never another tenant's `alex@local` (auth.TenantHoming.DirectoryRealm).
	// "" on a single-tenant install and for the platform's own tenant.
	realm, home, viaPin := d.homing.DirectoryRealm(ctx, d.store)
	entry, name, err := d.find(c, identifier, realm)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, auth.ErrUnauthorized
	}
	if err := c.Bind(entry.DN, password); err != nil {
		// The user bind failing IS the "wrong password" answer, and it is also
		// the "account locked/expired/disabled" answer. Neither is reported to
		// the caller (no enumeration oracle), so log it at debug with the DN so
		// an operator can tell them apart.
		slog.Debug("ldap: user bind refused",
			slog.String("dn", entry.DN), slog.Any("err", err))
		return nil, auth.ErrUnauthorized
	}

	// The account's address, ALWAYS `name@domain`: the entry's e-mail
	// attribute when it holds an address; else the name it was found by when
	// that is already an address (a UPN); else `<name>@<token>` — the
	// derivation every login-name provider uses (identity.DeriveEmail), so
	// `alex` is `alex@local`. A bare `alex` is never an account's e-mail any
	// more — not even from an email_attr pointed at `uid`.
	em := identity.Normalize(entry.GetAttributeValue(d.emailAttr))
	if !identity.LooksLikeEmail(em) {
		em = identity.DeriveEmail(name, "", realm, d.emailToken)
	}
	if !d.domainAllowed(em) {
		slog.Debug("ldap: the address is outside this directory's e-mail domains",
			slog.String("directory", d.Directory()), slog.String("email", em))
		return nil, auth.ErrUnauthorized
	}
	if why := switchedOff(entry); why != "" {
		// The bind normally fails first; a directory that lets a locked
		// account bind still gets no for an answer.
		slog.Debug("ldap: the directory has switched the person off",
			slog.String("dn", entry.DN), slog.String("why", why))
		return nil, auth.ErrUnauthorized
	}
	groups := d.groupsOf(entry)
	// The realm the pin homed this sign-in in, noted before account() looks
	// for the account: nothing may be written to one outside it (outOfReach).
	auth.NoteHome(ctx, home, viaPin)
	user, created, err := d.account(ctx, entry, name, em, groups, true, nil)
	if err != nil {
		var nm errNotMine
		if errors.As(err, &nm) {
			// Another directory's account, one made here that only the main
			// directory may sign in, or a previous owner's: this directory's
			// answer is no, and the login chain asks the next one.
			slog.Debug("ldap: the account is not this directory's",
				slog.String("directory", d.Directory()), slog.String("email", em), slog.String("why", nm.why))
			return nil, auth.ErrUnauthorized
		}
		return nil, err
	}
	// ⚠ Before anything is written to the account: it must be one the
	// sign-in's realm admits. A directory address (the mail attribute) can
	// belong to another tenant's account, and signing in to it — or recording
	// this sign-in's groups on it — would cross the tenant boundary.
	if !auth.LoginRealmAdmits(ctx, user) {
		slog.Warn("ldap: the directory's account is in another tenant than the sign-in's realm; refused",
			slog.Int64("user_id", user.ID), slog.String("dn", entry.DN))
		return nil, auth.ErrUnauthorized
	}
	if user.DisabledByDirectory() {
		// Switched off by directory sync, and the directory has since let
		// them back in.
		if err := d.store.SetUserEnabledByDirectory(ctx, user.ID, true); err != nil {
			return nil, err
		}
		user.Enabled, user.DisabledReason = true, ""
		slog.Info("ldap: the directory let a person back in; their account is on again",
			slog.String("email", user.Email), slog.String("directory", d.Directory()))
	}
	// An account from before migration 00084 with no password here is the
	// directory's (a no-op once labelled).
	auth.ClaimSource(ctx, d.store, user, model.AuthSourceLDAP)
	d.claimDirectory(ctx, user)
	if d.groupAttr != "" {
		// Recorded at every sign-in and REPLACED: the directory is the authority
		// on membership (as for OIDC's claim) — the starting role of a new
		// account and the filex groups linked to these (auth.RecordSignInGroups,
		// the one rule for every provider). A failure is loud, not fatal.
		user = auth.RecordSignInGroups(ctx, d.store, "ldap", user, groups, created)
	}
	if withLinks {
		user = d.syncLinkGroups(ctx, c, user, entry, name)
	}
	return user, nil
}

// find searches for the entry the identifier names and returns it with the
// name it was found by.
//
// ⚠ The second search is the file protocols' way in. They ask with the
// account's e-mail (protocolauth: SFTP's username field and the account share
// nothing else), and an account whose entry has no e-mail attribute is
// `alex@local` — which a login-name filter such as `(uid=%s)` matches nobody by.
// An address this driver made up (`<name>@<token>`, or `<name>@<realm>.<token>`
// in the sign-in's realm — identity.LoginNameOf) that matched nothing is asked
// again as the name it was made from. A real mailbox, another realm's address,
// or an address that matched, is never taken apart.
func (d *Driver) find(c conn, identifier, realm string) (*ldap.Entry, string, error) {
	entry, err := d.search(c, identifier)
	if err != nil || entry != nil {
		return entry, identifier, err
	}
	name, ok := identity.LoginNameOf(identifier, realm, d.emailToken)
	if !ok {
		return nil, identifier, nil
	}
	entry, err = d.search(c, name)
	return entry, name, err
}

// defaultGroupAttr is where Active Directory (and OpenLDAP with the memberOf
// overlay) list a person's groups.
const defaultGroupAttr = "memberOf"

// groupsOf returns the group names an entry carries: each value of the group
// attribute as it is, and — for a distinguished name such as
// `CN=Editors,OU=Groups,DC=corp,DC=example` — its leading value too (`Editors`),
// so an operator can write either in allowed_groups and in a role's SSO-group
// target. nil when groups are not being read.
func (d *Driver) groupsOf(e *ldap.Entry) []string {
	if d.groupAttr == "" || e == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(g string) {
		if g = strings.TrimSpace(g); g != "" && !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	for _, v := range e.GetAttributeValues(d.groupAttr) {
		add(v)
		if dn, err := ldap.ParseDN(v); err == nil && len(dn.RDNs) > 0 && len(dn.RDNs[0].Attributes) > 0 {
			add(dn.RDNs[0].Attributes[0].Value)
		}
	}
	return out
}

// search finds the single entry the identifier names.
//
// Two things here used to be silently wrong:
//
//  1. A search ERROR and a search MISS were folded into the same
//     ErrUnauthorized. An unreachable directory, an expired service account and
//     a typo in base_dn all looked identical to a wrong password, with nothing
//     in the log. They are separated now: a transport/protocol failure is
//     returned as an error (the login chain logs it and keeps it as the last
//     error), a genuine miss is a nil entry.
//
//  2. sizeLimit was 1. Active Directory answers a subtree search from the
//     domain root with continuation references (DomainDnsZones, ForestDnsZones,
//     Configuration) alongside the entry, and a server that counts those
//     against a limit of 1 can answer "size limit exceeded" instead of the
//     match. The limit is 2 now, and an ambiguous filter (more than one entry)
//     is refused loudly rather than resolving to whichever entry came first.
func (d *Driver) search(c conn, identifier string) (*ldap.Entry, error) {
	filter := d.filter(identifier)
	res, err := c.Search(ldap.NewSearchRequest(
		d.baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 0, false,
		filter, d.searchAttrs(), nil,
	))
	if err != nil {
		// A size-limit answer still carries the entries the server did return;
		// treat it as data rather than as a failure, which is what makes the
		// AD referral case survive.
		if !ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) || res == nil {
			return nil, fmt.Errorf("ldap: search: %w", err)
		}
	}
	switch {
	case res == nil || len(res.Entries) == 0:
		slog.Debug("ldap: no directory entry matched",
			slog.String("filter", filter), slog.String("base_dn", d.baseDN))
		return nil, nil
	case len(res.Entries) > 1:
		slog.Warn("ldap: user_filter matched more than one entry; refusing to guess",
			slog.String("filter", filter), slog.Int("matches", len(res.Entries)))
		return nil, nil
	}
	return res.Entries[0], nil
}

// filter substitutes the login identifier into user_filter.
//
// ⚠ It does NOT use fmt.Sprintf. Sprintf fills ONE %s per argument, so the
// natural AD filter that accepts either address form —
//
//	(&(objectClass=user)(|(mail=%s)(userPrincipalName=%s)))
//
// — silently became `...(userPrincipalName=%!s(MISSING))`, a filter that
// matches nobody and reports nothing. Every %s (and Go's indexed %[1]s, which
// operators reach for once they hit the Sprintf behaviour) is replaced with the
// same escaped value, so a filter with one placeholder and a filter with three
// behave the same way.
func (d *Driver) filter(identifier string) string {
	esc := ldap.EscapeFilter(strings.ToLower(strings.TrimSpace(identifier)))
	f := strings.ReplaceAll(d.userFilter, "%[1]s", "%s")
	return strings.ReplaceAll(f, "%s", esc)
}

// searchAttrs is what a search asks the directory for: the DN, the e-mail
// attribute, and the group attribute when groups are being read.
//
// Every search also asks for the person's permanent id and whether the
// directory has switched them off (personAttrs, people.go).
func (d *Driver) searchAttrs() []string {
	attrs := []string{"dn", d.emailAttr}
	if d.groupAttr != "" {
		attrs = append(attrs, d.groupAttr)
	}
	return append(attrs, personAttrs...)
}
