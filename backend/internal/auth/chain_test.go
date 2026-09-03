package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

// stubDriver is a LoginDriver whose answer is dictated by the test.
type stubDriver struct {
	name string
	// accepts is the password this driver knows. Anything else is refused.
	accepts string
	user    *model.User
	token   string
	// failWith, when set, is returned instead of a verdict — the "I could not
	// judge" case (directory down, TLS failure, bad service bind).
	failWith error

	loginCalls  int
	logoutCalls int
	logoutErr   error
}

func (s *stubDriver) Name() string { return s.name }

func (s *stubDriver) Login(_ context.Context, _, password string) (*model.User, string, error) {
	s.loginCalls++
	if s.failWith != nil {
		return nil, "", s.failWith
	}
	if password != s.accepts {
		return nil, "", auth.ErrUnauthorized
	}
	return s.user, s.token, nil
}

func (s *stubDriver) Logout(_ context.Context, _ string) error {
	s.logoutCalls++
	return s.logoutErr
}

func newStub(name, accepts string) *stubDriver {
	return &stubDriver{
		name:    name,
		accepts: accepts,
		user:    &model.User{ID: 7, Email: name + "@example.com", Enabled: true},
		token:   "session-" + name,
	}
}

// TestChainStopsAtTheFirstSuccess — and does not consult later drivers, so a
// local password never costs a directory round trip.
func TestChainStopsAtTheFirstSuccess(t *testing.T) {
	local := newStub("local", "local-pw")
	dir := newStub("ldap", "dir-pw")
	c := auth.NewLoginChain(local, dir)

	u, tok, err := c.Login(context.Background(), "someone@example.com", "local-pw")
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, "session-local", tok)
	assert.Equal(t, 1, local.loginCalls)
	assert.Zero(t, dir.loginCalls, "the directory must not be asked once local said yes")
}

// TestChainFallsThroughToTheDirectory is the whole point of the type: this is
// the case that answered 401 in under a millisecond before it existed.
func TestChainFallsThroughToTheDirectory(t *testing.T) {
	local := newStub("local", "local-pw")
	dir := newStub("ldap", "dir-pw")
	c := auth.NewLoginChain(local, dir)

	u, tok, err := c.Login(context.Background(), "ayse@example.com", "dir-pw")
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, "session-ldap", tok)
	assert.Equal(t, 1, local.loginCalls, "local is still tried first")
	assert.Equal(t, 1, dir.loginCalls)
}

// TestChainReturnsUnauthorizedWhenEveryDriverRefuses — a wrong password must
// stay a wrong password, not become a server error.
func TestChainReturnsUnauthorizedWhenEveryDriverRefuses(t *testing.T) {
	c := auth.NewLoginChain(newStub("local", "a"), newStub("ldap", "b"))

	_, _, err := c.Login(context.Background(), "ayse@example.com", "neither")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
}

// TestChainKeepsANonVerdictError is the diagnosability contract.
//
// ⚠ An unreachable directory must not be swallowed into ErrUnauthorized. The
// handler still answers 401 either way, so this error is the ONLY way an
// operator can tell "wrong password" from "the directory is down" — which is
// exactly the distinction the original bug destroyed.
func TestChainKeepsANonVerdictError(t *testing.T) {
	boom := errors.New("ldap: dial: connection refused")
	local := newStub("local", "local-pw")
	dir := &stubDriver{name: "ldap", failWith: boom}
	c := auth.NewLoginChain(local, dir)

	_, _, err := c.Login(context.Background(), "ayse@example.com", "wrong")
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, auth.ErrUnauthorized)
}

// TestChainKeepsTryingAfterADriverFails — a broken directory must not lock out
// the local break-glass account, whichever order they were listed in.
func TestChainKeepsTryingAfterADriverFails(t *testing.T) {
	dir := &stubDriver{name: "ldap", failWith: errors.New("ldap: dial: connection refused")}
	local := newStub("local", "local-pw")
	c := auth.NewLoginChain(dir, local)

	u, tok, err := c.Login(context.Background(), "admin@local", "local-pw")
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, "session-local", tok)
}

// TestChainLogoutHitsEveryDriver — one cookie, so the revocation must reach
// whichever driver minted it.
func TestChainLogoutHitsEveryDriver(t *testing.T) {
	local := newStub("local", "a")
	dir := newStub("ldap", "b")
	c := auth.NewLoginChain(local, dir)

	require.NoError(t, c.Logout(context.Background(), "some-token"))
	assert.Equal(t, 1, local.logoutCalls)
	assert.Equal(t, 1, dir.logoutCalls)
}

// TestChainLogoutReportsTheFirstErrorButStillRuns — a half-revoked session is
// the one outcome worth avoiding.
func TestChainLogoutReportsTheFirstErrorButStillRuns(t *testing.T) {
	boom := errors.New("db is down")
	local := newStub("local", "a")
	local.logoutErr = boom
	dir := newStub("ldap", "b")
	c := auth.NewLoginChain(local, dir)

	err := c.Logout(context.Background(), "some-token")
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, 1, dir.logoutCalls, "the later driver must still be asked to revoke")
}

// TestChainDropsNilDrivers lets the bootstrap pass an optional driver without a
// nil check — and, more importantly, keeps a nil out of the loop where it would
// panic on the first login.
func TestChainDropsNilDrivers(t *testing.T) {
	local := newStub("local", "pw")
	c := auth.NewLoginChain(nil, local, nil)
	assert.Equal(t, 1, c.Len())

	u, _, err := c.Login(context.Background(), "x@example.com", "pw")
	require.NoError(t, err)
	require.NotNil(t, u)
}

// TestChainNameListsItsMembers — the boot line has to say which drivers a
// password will actually be tried against. Being unable to see that from the
// outside is how the hole survived for months.
func TestChainNameListsItsMembers(t *testing.T) {
	c := auth.NewLoginChain(newStub("local", "a"), newStub("ldap", "b"))
	assert.Equal(t, "login-chain(local,ldap)", c.Name())
}

// TestChainSatisfiesLoginDriver — it has to be assignable to the handler's
// single LoginDriver field, which is the whole delivery mechanism.
func TestChainSatisfiesLoginDriver(t *testing.T) {
	var _ auth.LoginDriver = auth.NewLoginChain()
}
