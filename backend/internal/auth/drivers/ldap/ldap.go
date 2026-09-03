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
	"net/http"
	"os"
	"strings"

	"github.com/go-ldap/ldap/v3"

	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
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
	Close() error
}

// Driver is the LDAP/AD auth driver.
type Driver struct {
	store      db.Store
	url        string // ldap:// or ldaps://
	bindDN     string // service account
	bindPass   string
	baseDN     string
	userFilter string // e.g. "(mail=%s)"
	emailAttr  string // e.g. "mail"
	startTLS   bool
	caFile     string // optional PEM bundle for a private CA

	// dial is swapped in tests. Nil means the real dialer.
	dial func(ctx context.Context) (conn, error)
}

// New constructs an empty driver — Init must be called.
func New(store db.Store) *Driver {
	return &Driver{store: store, emailAttr: "mail", userFilter: "(mail=%s)"}
}

// Name implements auth.Driver.
func (d *Driver) Name() string { return "ldap" }

// Init configures the driver.
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	if d.store == nil {
		return errors.New("ldap: nil store")
	}
	d.url, _ = cfg["url"].(string)
	d.bindDN, _ = cfg["bind_dn"].(string)
	d.bindPass, _ = cfg["bind_password"].(string)
	d.baseDN, _ = cfg["base_dn"].(string)
	if v, ok := cfg["user_filter"].(string); ok && v != "" {
		d.userFilter = v
	}
	if v, ok := cfg["email_attr"].(string); ok && v != "" {
		d.emailAttr = v
	}
	d.startTLS, _ = cfg["start_tls"].(bool)
	d.caFile, _ = cfg["ca_file"].(string)
	if d.url == "" || d.baseDN == "" {
		return errors.New("ldap: url and base_dn required")
	}
	if d.caFile != "" {
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
	if d.caFile == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(d.caFile)
	if err != nil {
		return nil, err
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no PEM certificate found in %s", d.caFile)
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
}

// connect dials the directory and applies StartTLS plus the service bind.
func (d *Driver) connect(ctx context.Context) (conn, error) {
	if d.dial != nil {
		return d.dial(ctx)
	}
	tc, err := d.tlsConfig()
	if err != nil {
		return nil, fmt.Errorf("ldap: ca_file: %w", err)
	}
	var opts []ldap.DialOpt
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
func (d *Driver) Login(ctx context.Context, identifier, password string) (*model.User, string, error) {
	user, err := d.verify(ctx, identifier, password)
	if err != nil {
		return nil, "", err
	}
	tok, err := authlocal.IssueSession(ctx, d.store, user.ID)
	if err != nil {
		return nil, "", err
	}
	_ = d.store.TouchLastLogin(ctx, user.ID)
	return user, tok, nil
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
		tc, err := d.tlsConfig()
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

	entry, err := d.search(c, identifier)
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

	em := entry.GetAttributeValue(d.emailAttr)
	if em == "" {
		em = identifier
	}
	em = strings.ToLower(strings.TrimSpace(em))
	user, err := d.store.GetUserByEmail(ctx, em)
	if err != nil {
		user, err = d.store.CreateUser(ctx, em, "", model.RoleUser, "en", "UTC")
		if err != nil {
			return nil, err
		}
		slog.Info("ldap: provisioned a directory account",
			slog.String("email", em), slog.String("dn", entry.DN))
	}
	return user, nil
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
		filter, []string{"dn", d.emailAttr}, nil,
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
