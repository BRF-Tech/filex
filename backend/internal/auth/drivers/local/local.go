// Package local implements username + password authentication backed by
// the users table. Password hashes use bcrypt. Sessions are issued via
// the sessions table and conveyed by Cookie or Bearer header.
package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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
func (d *Driver) Login(ctx context.Context, email, password string) (*model.User, string, error) {
	user, err := identity.Resolve(ctx, d.store, email)
	if err != nil {
		return nil, "", auth.ErrUnauthorized
	}
	if user.PasswordHash == "" {
		return nil, "", auth.ErrUnauthorized
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, "", auth.ErrUnauthorized
	}
	tok, err := IssueSession(ctx, d.store, user.ID)
	if err != nil {
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
