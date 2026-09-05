// Package local implements username + password authentication backed by
// the users table. Password hashes use bcrypt. Sessions are issued via
// the sessions table and conveyed by Cookie or Bearer header.
package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
)

func init() {
	// Bare registration so auth.Names() reflects driver availability;
	// the actual instance with a Store is built by server.New.
	auth.Register("local", func() auth.Driver { return &Driver{} })
}

const (
	// SessionCookieName is the HTTP cookie used by the local driver.
	SessionCookieName = "filex_session"
	// SessionTTL is how long a freshly-issued session is valid for.
	SessionTTL = 12 * time.Hour
	// BearerPrefix detects API token usage.
	BearerPrefix = "Bearer "
)

// Driver is the local-DB password auth driver.
type Driver struct {
	store db.Store
}

// New constructs an empty driver — must be Init'd before use.
func New(store db.Store) *Driver {
	return &Driver{store: store}
}

// Name implements auth.Driver.
func (d *Driver) Name() string { return "local" }

// Init currently has no config of its own.
func (d *Driver) Init(_ context.Context, _ map[string]any) error {
	if d.store == nil {
		return errors.New("local: nil store")
	}
	return nil
}

// Capabilities implements auth.Driver.
func (d *Driver) Capabilities() auth.Capabilities {
	return auth.Capabilities{
		SignIn:         true,
		Logout:         true,
		ChangePassword: true,
		Register:       false,
	}
}

// Authenticate looks for either a session cookie or an `Authorization:
// Bearer …` token, validates it against sessions table, and resolves the
// user. Returns auth.ErrUnauthorized when no credentials present.
func (d *Driver) Authenticate(r *http.Request) (*model.User, error) {
	tok := extractToken(r)
	if tok == "" {
		return nil, auth.ErrUnauthorized
	}
	ctx := r.Context()
	sess, err := d.store.GetSessionByToken(ctx, tok)
	if err != nil {
		return nil, auth.ErrUnauthorized
	}
	user, err := d.store.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, auth.ErrUnauthorized
	}
	return user, nil
}

// Login validates an identifier + password and returns a freshly created
// session token.
//
// The identifier is an e-mail OR a username (migration 00025): identity.Resolve
// owns that decision so this surface cannot drift from WebDAV, SFTP or FTPS.
// The parameter keeps its old name because every caller passes what the user
// typed into a field labelled "e-mail or username", and renaming it would touch
// the whole auth.LoginDriver interface for no behaviour change.
// ⚠ The four ways this can fail all answer the caller with the SAME
// auth.ErrUnauthorized, which the handler turns into one unhelpful
// `401 invalid credentials`. That is deliberate and must stay: telling an
// anonymous caller "no such account" apart from "wrong password" is account
// enumeration, and it is the whole reason identity.Resolve reports a malformed
// username as ErrNotFound too.
//
// What was NOT deliberate is that the SERVER could not tell either. A Cypress
// run once saw every login answer 401 with nothing in the log to say why, and
// it could not be reproduced — because a store failure (a locked sqlite file, a
// dropped postgres connection) came back through exactly the same return
// statement as a typo in a password. So each case now names itself in the log,
// at a level that matches who is at fault:
//
//	debug  the caller got it wrong — a typo is not an operator's problem, and
//	       an unauthenticated endpoint must not let a stranger fill the log
//	info   a real account exists but has no local password (directory/OIDC
//	       account) — worth seeing while an operator debugs a rollout
//	error  the server could not judge at all — a store failure or an unusable
//	       stored hash, which is nobody's login attempt going wrong
//
// The store failure is ALSO returned as itself rather than as ErrUnauthorized:
// auth.LoginChain treats "not ErrUnauthorized" as "this driver could not judge"
// and reports it, so an operator reading the log can tell a wrong password from
// a database that is down. The client still gets its single 401 either way —
// handlers.Auth.Login answers 401 for any error.
func (d *Driver) Login(ctx context.Context, email, password string) (*model.User, string, error) {
	user, err := identity.Resolve(ctx, d.store, email)
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			slog.Debug("local: login refused",
				slog.String("reason", "no such account"),
				slog.String("identifier", email))
			return nil, "", auth.ErrUnauthorized
		}
		slog.Error("local: could not judge the credentials",
			slog.String("reason", "user lookup failed"),
			slog.String("identifier", email),
			slog.String("err", err.Error()))
		return nil, "", fmt.Errorf("local: user lookup: %w", err)
	}
	if user.PasswordHash == "" {
		slog.Info("local: login refused",
			slog.String("reason", "account has no local password"),
			slog.String("identifier", email))
		return nil, "", auth.ErrUnauthorized
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			slog.Debug("local: login refused",
				slog.String("reason", "password mismatch"),
				slog.String("identifier", email))
		} else {
			// Not a wrong password: the stored hash itself is unusable
			// (truncated column, a hash written by something that is not
			// bcrypt). The account can never log in until it is reset, and
			// silence here is what makes that look like a forgotten password.
			slog.Error("local: stored password hash is unusable",
				slog.String("reason", "bad password hash"),
				slog.String("identifier", email),
				slog.String("err", err.Error()))
		}
		return nil, "", auth.ErrUnauthorized
	}
	tok, err := IssueSession(ctx, d.store, user.ID)
	if err != nil {
		slog.Error("local: could not issue a session",
			slog.String("reason", "session insert failed"),
			slog.String("identifier", email),
			slog.String("err", err.Error()))
		return nil, "", err
	}
	_ = d.store.TouchLastLogin(ctx, user.ID)
	return user, tok, nil
}

// IssueSession mints a `filex_session` token for uid and records it.
//
// ⚠ Exported because it is the ONE place a browser session comes into
// existence, and more than one login driver has to be able to mint one. The
// LDAP driver used to end its Login with `return user, "", nil` and the comment
// "caller mints session token via the local driver" — no caller ever did, so a
// directory login that had already succeeded handed back an EMPTY token: the
// cookie was set to "", the very next request had no session, and the failure
// looked like a login failure. A second implementation would have been worse
// still: the cookie name and the 12h TTL are a contract the middleware and the
// SPA both depend on, and two copies of a TTL drift.
func IssueSession(ctx context.Context, store db.Store, userID int64) (string, error) {
	tok, err := generateToken()
	if err != nil {
		return "", err
	}
	if _, err := store.CreateSession(ctx, userID, tok, time.Now().Add(SessionTTL), "", ""); err != nil {
		return "", err
	}
	return tok, nil
}

// RevokeSession deletes a session token. The counterpart to IssueSession, for
// the same reason: a driver that can mint must be able to revoke.
func RevokeSession(ctx context.Context, store db.Store, token string) error {
	if token == "" {
		return nil
	}
	return store.DeleteSession(ctx, token)
}

// Logout revokes the given session.
func (d *Driver) Logout(ctx context.Context, token string) error {
	return RevokeSession(ctx, d.store, token)
}

// HashPassword returns a bcrypt hash suitable for users.password_hash.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// generateToken returns a random 64-char hex string.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, BearerPrefix) {
		return strings.TrimSpace(h[len(BearerPrefix):])
	}
	if c, err := r.Cookie(SessionCookieName); err == nil {
		return c.Value
	}
	return ""
}
