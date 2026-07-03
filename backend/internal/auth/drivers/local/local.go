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

// Login validates email + password and returns a freshly created session token.
func (d *Driver) Login(ctx context.Context, email, password string) (*model.User, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	user, err := d.store.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, "", auth.ErrUnauthorized
	}
	if user.PasswordHash == "" {
		return nil, "", auth.ErrUnauthorized
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, "", auth.ErrUnauthorized
	}
	tok, err := generateToken()
	if err != nil {
		return nil, "", err
	}
	expires := time.Now().Add(SessionTTL)
	if _, err := d.store.CreateSession(ctx, user.ID, tok, expires, "", ""); err != nil {
		return nil, "", err
	}
	_ = d.store.TouchLastLogin(ctx, user.ID)
	return user, tok, nil
}

// Logout revokes the given session.
func (d *Driver) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return d.store.DeleteSession(ctx, token)
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
