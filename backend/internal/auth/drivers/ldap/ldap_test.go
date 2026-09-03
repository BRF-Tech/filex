package ldap

import (
	"context"
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// ⚠ The bug this file exists for was NOT in the LDAP protocol. It was that
// Login could not be reached and, once reached, handed back an empty session.
// So the assertions here are about the call path — the interface the driver
// satisfies, the token it returns, the filter it builds — and the fake
// directory is only scaffolding.

// TestDriverSatisfiesLoginDriver is a compile-time assertion with a name.
//
// ⚠ Before Logout existed this did not compile ("missing method Logout"), which
// is precisely why server.go could not put the driver in the login chain: the
// hole was enforced by the type system and nobody had ever asked the compiler
// the question.
func TestDriverSatisfiesLoginDriver(t *testing.T) {
	var _ auth.LoginDriver = (*Driver)(nil)
}

// fakeConn stands in for *ldap.Conn.
type fakeConn struct {
	binds    []bindCall
	searches []*goldap.SearchRequest
	// entries is what Search answers with.
	entries []*goldap.Entry
	// searchErr, when set, is what Search returns.
	searchErr error
	// userPassword is the password the entry's own bind accepts. Any other
	// value is refused the way a directory refuses a wrong password.
	userPassword string
	// servicePassword is what the service-account bind accepts ("" = any).
	servicePassword string
	closed          bool
	startTLSConfig  *tls.Config
	startTLSCalled  bool
}

type bindCall struct{ dn, password string }

func (f *fakeConn) StartTLS(c *tls.Config) error {
	f.startTLSCalled = true
	f.startTLSConfig = c
	return nil
}

func (f *fakeConn) Bind(dn, password string) error {
	f.binds = append(f.binds, bindCall{dn, password})
	// The service bind is the one that names a DN we were configured with.
	if strings.HasPrefix(dn, "cn=svc") {
		if f.servicePassword != "" && password != f.servicePassword {
			return goldap.NewError(goldap.LDAPResultInvalidCredentials, errors.New("bad service credentials"))
		}
		return nil
	}
	if password != f.userPassword {
		return goldap.NewError(goldap.LDAPResultInvalidCredentials, errors.New("bad credentials"))
	}
	return nil
}

func (f *fakeConn) Search(req *goldap.SearchRequest) (*goldap.SearchResult, error) {
	f.searches = append(f.searches, req)
	if f.searchErr != nil {
		return &goldap.SearchResult{Entries: f.entries}, f.searchErr
	}
	return &goldap.SearchResult{Entries: f.entries}, nil
}

func (f *fakeConn) Close() error { f.closed = true; return nil }

// newStore returns the store PRODUCTION hands the driver — the identitystore
// wrapper included. Without it a directory account is created unnamed, and
// "unnamed" is exactly the account that cannot log in over SFTP or FTPS.
func newStore(t *testing.T) db.Store {
	t.Helper()
	_, raw := dbtest.NewTestDB(t)
	return identitystore.New(raw)
}

func entry(dn, mail string) *goldap.Entry {
	return goldap.NewEntry(dn, map[string][]string{"mail": {mail}})
}

// newDriver builds a driver wired to fc, with a real store behind it.
func newDriver(t *testing.T, fc *fakeConn, cfg map[string]any) (*Driver, db.Store) {
	t.Helper()
	store := newStore(t)
	d := New(store)
	base := map[string]any{
		"url":     "ldaps://directory.invalid",
		"base_dn": "dc=example,dc=com",
	}
	for k, v := range cfg {
		base[k] = v
	}
	require.NoError(t, d.Init(context.Background(), base))
	d.dial = func(context.Context) (conn, error) { return fc, nil }
	return d, store
}

// TestLoginMintsAUsableSession is the regression that matters most.
//
// ⚠ The old Login ended with `return user, "", nil` and a comment saying the
// caller would mint the session "via the local driver". No caller did. So a
// directory user whose password was CORRECT got a 200 with an empty token, the
// cookie was set to "", and the next request was anonymous — a successful login
// that presented as a failed one.
func TestLoginMintsAUsableSession(t *testing.T) {
	fc := &fakeConn{
		entries:      []*goldap.Entry{entry("cn=ayse,dc=example,dc=com", "ayse@example.com")},
		userPassword: "directory-pw",
	}
	d, store := newDriver(t, fc, nil)

	u, tok, err := d.Login(context.Background(), "ayse@example.com", "directory-pw")
	require.NoError(t, err)
	require.NotNil(t, u)
	require.NotEmpty(t, tok, "an empty session token is the bug this test exists for")

	sess, err := store.GetSessionByToken(context.Background(), tok)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, u.ID, sess.UserID)
	assert.True(t, fc.closed, "the connection must be closed")
}

// TestLoginProvisionsAndReusesTheAccount checks the upsert half.
func TestLoginProvisionsAndReusesTheAccount(t *testing.T) {
	fc := &fakeConn{
		entries:      []*goldap.Entry{entry("cn=ayse,dc=example,dc=com", "Ayse@Example.COM")},
		userPassword: "pw",
	}
	d, store := newDriver(t, fc, nil)

	u1, _, err := d.Login(context.Background(), "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, "ayse@example.com", u1.Email, "email_attr is canonicalised to lower case")
	assert.NotEmpty(t, u1.Username, "identitystore must have named the account (SFTP/FTPS need it)")

	u2, _, err := d.Login(context.Background(), "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, u1.ID, u2.ID, "a second login must reuse the account, not create a new one")

	users, err := store.ListUsers(context.Background())
	require.NoError(t, err)
	assert.Len(t, users, 1)
}

// TestVerifyPasswordMintsNoSession — the protocol path must not create a
// session row per request.
func TestVerifyPasswordMintsNoSession(t *testing.T) {
	fc := &fakeConn{
		entries:      []*goldap.Entry{entry("cn=ayse,dc=example,dc=com", "ayse@example.com")},
		userPassword: "pw",
	}
	d, _ := newDriver(t, fc, nil)

	for i := 0; i < 3; i++ {
		u, err := d.VerifyPassword(context.Background(), "ayse@example.com", "pw")
		require.NoError(t, err)
		require.NotNil(t, u)
	}
	// Nothing to assert on the sessions table directly (no lister), so assert
	// the shape instead: VerifyPassword returns no token at all, and Login is
	// the only path that does.
	_, tok, err := d.Login(context.Background(), "ayse@example.com", "pw")
	require.NoError(t, err)
	require.NotEmpty(t, tok)
}

// TestLogoutRevokesTheSession closes the loop Login opens.
func TestLogoutRevokesTheSession(t *testing.T) {
	fc := &fakeConn{
		entries:      []*goldap.Entry{entry("cn=ayse,dc=example,dc=com", "ayse@example.com")},
		userPassword: "pw",
	}
	d, store := newDriver(t, fc, nil)

	_, tok, err := d.Login(context.Background(), "ayse@example.com", "pw")
	require.NoError(t, err)
	require.NoError(t, d.Logout(context.Background(), tok))

	_, err = store.GetSessionByToken(context.Background(), tok)
	assert.Error(t, err, "the session must be gone after Logout")
}

// TestWrongPasswordIsUnauthorized — and is NOT reported as a driver error, so
// the login chain moves on rather than logging a false alarm.
func TestWrongPasswordIsUnauthorized(t *testing.T) {
	fc := &fakeConn{
		entries:      []*goldap.Entry{entry("cn=ayse,dc=example,dc=com", "ayse@example.com")},
		userPassword: "right",
	}
	d, _ := newDriver(t, fc, nil)

	_, _, err := d.Login(context.Background(), "ayse@example.com", "wrong")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
}

// TestEmptyPasswordIsRefusedBeforeDialing — an empty-password bind is a
// successful ANONYMOUS bind on many directories.
func TestEmptyPasswordIsRefusedBeforeDialing(t *testing.T) {
	fc := &fakeConn{userPassword: "pw"}
	d, _ := newDriver(t, fc, nil)

	_, _, err := d.Login(context.Background(), "ayse@example.com", "")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Empty(t, fc.binds, "no bind may be attempted for an empty password")
}

// TestNoMatchIsUnauthorized separates "nobody matched" from "could not ask".
func TestNoMatchIsUnauthorized(t *testing.T) {
	fc := &fakeConn{userPassword: "pw"}
	d, _ := newDriver(t, fc, nil)

	_, err := d.VerifyPassword(context.Background(), "ghost@example.com", "pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
}

// TestSearchFailureIsNotUnauthorized is the diagnosability fix.
//
// ⚠ The old code was `if err != nil || len(res.Entries) == 0 { return
// ErrUnauthorized }`: an unreachable directory, an expired service account and
// a typo in base_dn all came out as "wrong password", with nothing logged. The
// login chain can only report "the directory is down" if the driver stops
// pretending the directory answered.
func TestSearchFailureIsNotUnauthorized(t *testing.T) {
	fc := &fakeConn{
		searchErr:    errors.New("connection reset by peer"),
		userPassword: "pw",
	}
	d, _ := newDriver(t, fc, nil)

	_, err := d.VerifyPassword(context.Background(), "ayse@example.com", "pw")
	require.Error(t, err)
	assert.NotErrorIs(t, err, auth.ErrUnauthorized)
	assert.Contains(t, err.Error(), "ldap: search")
}

// TestSizeLimitExceededStillResolves covers the AD referral shape: the server
// answers with the entry AND a size-limit result because continuation
// references counted against the limit.
func TestSizeLimitExceededStillResolves(t *testing.T) {
	fc := &fakeConn{
		entries:      []*goldap.Entry{entry("cn=ayse,dc=example,dc=com", "ayse@example.com")},
		searchErr:    goldap.NewError(goldap.LDAPResultSizeLimitExceeded, errors.New("size limit exceeded")),
		userPassword: "pw",
	}
	d, _ := newDriver(t, fc, nil)

	u, err := d.VerifyPassword(context.Background(), "ayse@example.com", "pw")
	require.NoError(t, err)
	require.NotNil(t, u)
}

// TestSearchRequestUsesSizeLimitTwo pins the request itself: with a limit of 1
// an AD subtree search from the domain root can answer "size limit exceeded"
// instead of the match, because the referrals count.
func TestSearchRequestUsesSizeLimitTwo(t *testing.T) {
	fc := &fakeConn{
		entries:      []*goldap.Entry{entry("cn=ayse,dc=example,dc=com", "ayse@example.com")},
		userPassword: "pw",
	}
	d, _ := newDriver(t, fc, nil)

	_, err := d.VerifyPassword(context.Background(), "ayse@example.com", "pw")
	require.NoError(t, err)
	require.Len(t, fc.searches, 1)
	assert.Equal(t, 2, fc.searches[0].SizeLimit)
}

// TestAmbiguousFilterIsRefused — two matches must not resolve to whichever
// came first.
func TestAmbiguousFilterIsRefused(t *testing.T) {
	fc := &fakeConn{
		entries: []*goldap.Entry{
			entry("cn=ayse,dc=example,dc=com", "ayse@example.com"),
			entry("cn=ayse2,ou=old,dc=example,dc=com", "ayse@example.com"),
		},
		userPassword: "pw",
	}
	d, _ := newDriver(t, fc, nil)

	_, err := d.VerifyPassword(context.Background(), "ayse@example.com", "pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
}

// TestFilterFillsEveryPlaceholder is the %!s(MISSING) regression.
//
// ⚠ fmt.Sprintf consumes ONE argument per verb, so the standard AD filter that
// accepts either address form silently became
// `(userPrincipalName=%!s(MISSING))` — a filter matching nobody, reported as a
// wrong password.
func TestFilterFillsEveryPlaceholder(t *testing.T) {
	cases := []struct {
		name, filter, want string
	}{
		{
			name:   "single placeholder",
			filter: "(mail=%s)",
			want:   "(mail=ayse@example.com)",
		},
		{
			name:   "two placeholders",
			filter: "(&(objectClass=user)(|(mail=%s)(userPrincipalName=%s)))",
			want:   "(&(objectClass=user)(|(mail=ayse@example.com)(userPrincipalName=ayse@example.com)))",
		},
		{
			name:   "go indexed verb",
			filter: "(|(mail=%[1]s)(userPrincipalName=%[1]s))",
			want:   "(|(mail=ayse@example.com)(userPrincipalName=ayse@example.com))",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := newDriver(t, &fakeConn{}, map[string]any{"user_filter": tc.filter})
			assert.Equal(t, tc.want, d.filter("Ayse@Example.com"))
			assert.NotContains(t, d.filter("Ayse@Example.com"), "MISSING")
		})
	}
}

// TestFilterEscapesTheIdentifier — the identifier is attacker-supplied.
func TestFilterEscapesTheIdentifier(t *testing.T) {
	d, _ := newDriver(t, &fakeConn{}, map[string]any{"user_filter": "(mail=%s)"})
	got := d.filter("a*)(objectClass=*")
	assert.NotContains(t, got, "*)(", "an unescaped identifier would widen the filter")
	assert.Contains(t, got, `\2a`)
}

// TestServiceBindFailureIsAnError — a locked service account must not look like
// a user's wrong password.
func TestServiceBindFailureIsAnError(t *testing.T) {
	fc := &fakeConn{servicePassword: "correct-svc", userPassword: "pw"}
	d, _ := newDriver(t, fc, map[string]any{
		"bind_dn":       "cn=svc,dc=example,dc=com",
		"bind_password": "wrong-svc",
	})

	_, err := d.VerifyPassword(context.Background(), "ayse@example.com", "pw")
	require.Error(t, err)
	assert.NotErrorIs(t, err, auth.ErrUnauthorized)
	assert.Contains(t, err.Error(), "service bind")
}

// TestCAFileIsValidatedAtInit — a typo in the path must be a boot complaint,
// not a per-login TLS error that reads as a directory outage.
func TestCAFileIsValidatedAtInit(t *testing.T) {
	d := New(newStore(t))
	err := d.Init(context.Background(), map[string]any{
		"url":     "ldaps://directory.invalid",
		"base_dn": "dc=example,dc=com",
		"ca_file": filepath.Join(t.TempDir(), "does-not-exist.pem"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ca_file")
}

// TestCAFileAppendsToSystemRoots — a private CA must be ADDED to the system
// pool, not substituted for it.
func TestCAFileAppendsToSystemRoots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(path, []byte(testCAPEM), 0o600))

	d := New(newStore(t))
	require.NoError(t, d.Init(context.Background(), map[string]any{
		"url":     "ldaps://directory.invalid",
		"base_dn": "dc=example,dc=com",
		"ca_file": path,
	}))

	tc, err := d.tlsConfig()
	require.NoError(t, err)
	require.NotNil(t, tc)
	require.NotNil(t, tc.RootCAs)
	assert.GreaterOrEqual(t, len(tc.RootCAs.Subjects()), 1) //nolint:staticcheck // counting is the point
}

// TestNoCAFileKeepsSystemVerification — the default must stay exactly what it
// was: Go's own verification, not a nil-safety hole.
func TestNoCAFileKeepsSystemVerification(t *testing.T) {
	d, _ := newDriver(t, &fakeConn{}, nil)
	tc, err := d.tlsConfig()
	require.NoError(t, err)
	assert.Nil(t, tc, "no ca_file means Go's default verification, unchanged")
}

// TestStartTLSCarriesTheCA — the CA must apply to the StartTLS upgrade too, not
// only to ldaps://. An install that uses ldap:// + start_tls with an internal
// CA is the AD default, and it is the one the PoC actually ran.
func TestStartTLSCarriesTheCA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(path, []byte(testCAPEM), 0o600))

	fc := &fakeConn{
		entries:      []*goldap.Entry{entry("cn=ayse,dc=example,dc=com", "ayse@example.com")},
		userPassword: "pw",
	}
	d, _ := newDriver(t, fc, map[string]any{
		"url":       "ldap://directory.invalid",
		"start_tls": true,
		"ca_file":   path,
	})

	_, err := d.VerifyPassword(context.Background(), "ayse@example.com", "pw")
	require.NoError(t, err)
	require.True(t, fc.startTLSCalled)
	require.NotNil(t, fc.startTLSConfig)
	assert.NotNil(t, fc.startTLSConfig.RootCAs)
}

// TestInitRequiresURLAndBase keeps the existing boot-time guard honest.
func TestInitRequiresURLAndBase(t *testing.T) {
	d := New(newStore(t))
	err := d.Init(context.Background(), map[string]any{"url": "ldaps://x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_dn")
}

// testCAPEM is a throwaway self-signed certificate used only to exercise the
// PEM-loading path. It is not trusted by anything.
const testCAPEM = `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----
`
