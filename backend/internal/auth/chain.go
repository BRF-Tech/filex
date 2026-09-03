package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
)

// LoginChain tries a password against several login drivers in turn.
//
// # Why this type exists
//
// Before it, the login handler held ONE LoginDriver and the server bootstrap
// only ever assigned the `local` driver to it. A directory driver could be
// configured, initialised, and listed in the boot banner while its Login method
// was unreachable from any code path — `FILEX_AUTH_DRIVERS=local,ldap` produced
// a server that said "Auth: ldap, local" and answered every directory account
// with 401 in ~350us, which is less than one LDAPS round trip. The docs
// meanwhile described exactly the behaviour this type implements ("filex tries
// each enabled driver in order"), so the gap read as a directory
// misconfiguration to everyone who hit it.
//
// # Order is the contract
//
// Drivers are tried in the order the operator listed them in
// `auth.drivers` / FILEX_AUTH_DRIVERS. Keeping `local` first matters: it is a
// bcrypt compare against a row that is already local, so the built-in
// admin@local account and every break-glass password stay answerable when the
// directory is unreachable. A directory driver placed first would put a network
// round trip in front of every local login and, on an unreachable directory,
// in front of the one login that is supposed to still work.
type LoginChain struct {
	drivers []LoginDriver
	names   []string
}

// NewLoginChain returns a chain over drivers, in order. Nil drivers are
// dropped so a caller can pass an optional driver without a nil check.
func NewLoginChain(drivers ...LoginDriver) *LoginChain {
	c := &LoginChain{}
	for _, d := range drivers {
		if d == nil {
			continue
		}
		c.drivers = append(c.drivers, d)
		c.names = append(c.names, driverName(d))
	}
	return c
}

// Len reports how many drivers the chain will try. Used by the bootstrap to
// avoid wrapping a single driver in a chain for nothing.
func (c *LoginChain) Len() int { return len(c.drivers) }

// Name reports the chain and its members, so a boot line or a log entry names
// the drivers a password will actually be tried against — the thing that was
// impossible to see from the outside before.
func (c *LoginChain) Name() string { return "login-chain(" + strings.Join(c.names, ",") + ")" }

// Login tries each driver in order and returns the first success.
//
// A driver that answers ErrUnauthorized has judged the credentials and said no;
// the chain moves on, because "not my user" and "wrong password" are the same
// answer by design (no account enumeration). Any OTHER error means the driver
// could not judge at all — an unreachable directory, a TLS failure, a bad
// service-account bind — and that is NOT swallowed: it is logged with the
// driver name and returned when no later driver succeeds, so an operator
// reading the log can tell "wrong password" from "the directory is down".
func (c *LoginChain) Login(ctx context.Context, identifier, password string) (*model.User, string, error) {
	var lastErr error = ErrUnauthorized
	for _, d := range c.drivers {
		u, tok, err := d.Login(ctx, identifier, password)
		if err == nil && u != nil {
			return u, tok, nil
		}
		if err != nil && !errors.Is(err, ErrUnauthorized) {
			slog.Warn("auth: login driver could not judge the credentials",
				slog.String("driver", driverName(d)), slog.Any("err", err))
			lastErr = err
		}
	}
	return nil, "", lastErr
}

// Logout revokes the session on every member.
//
// Every login driver mints its session in the same sessions table (that is what
// makes one cookie work regardless of which driver authenticated), so revoking
// on all of them is idempotent rather than wasteful — and it stays correct if a
// future driver keeps state of its own. The first error is reported; the
// remaining drivers still run, because a half-revoked session is the one
// outcome worth avoiding.
func (c *LoginChain) Logout(ctx context.Context, token string) error {
	var firstErr error
	for _, d := range c.drivers {
		if err := d.Logout(ctx, token); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// driverName reports a driver's registered name when it has one.
func driverName(d LoginDriver) string {
	if n, ok := d.(interface{ Name() string }); ok {
		return n.Name()
	}
	return "unknown"
}
