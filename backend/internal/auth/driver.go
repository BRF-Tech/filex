// Package auth defines the AuthDriver interface, registry, and middleware.
//
// Drivers live under auth/drivers/ and self-register via init() blocks.
// The middleware reads request headers/cookies, attempts each enabled
// driver in turn, and stores the resulting *model.User on the request
// context for downstream handlers.
package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ErrUnauthorized is returned when no driver could authenticate a request.
var ErrUnauthorized = errors.New("auth: unauthorized")

// Capabilities advertises what an auth driver supports — used by the UI to
// decide whether to render Login / Register / ChangePassword buttons.
type Capabilities struct {
	SignIn         bool `json:"sign_in"`
	Logout         bool `json:"logout"`
	ChangePassword bool `json:"change_password"`
	Register       bool `json:"register"`
}

// Driver is the abstract authentication backend.
//
// Authenticate is called by the auth middleware for every protected request
// and must be cheap. Drivers should NOT block on network IO during
// Authenticate — heavy work belongs in Init or in callbacks.
type Driver interface {
	Name() string
	Init(ctx context.Context, cfg map[string]any) error
	Authenticate(r *http.Request) (*model.User, error)
	Capabilities() Capabilities
}

// LoginDriver is an optional capability — drivers that accept username +
// password directly (i.e. local) implement it.
type LoginDriver interface {
	Login(ctx context.Context, email, password string) (*model.User, string, error) // user, sessionToken, err
	Logout(ctx context.Context, token string) error
}

// OIDCDriver is an optional capability — drivers that perform browser
// redirects to an external IdP implement it.
type OIDCDriver interface {
	StartFlow(w http.ResponseWriter, r *http.Request) error
	HandleCallback(w http.ResponseWriter, r *http.Request) (*model.User, string, error)
}

// userCtxKey is unexported to prevent collision.
type userCtxKey struct{}

// WithUser stores u on ctx.
func WithUser(ctx context.Context, u *model.User) context.Context {
	return context.WithValue(ctx, userCtxKey{}, u)
}

// UserFrom returns the user from ctx, nil if absent.
func UserFrom(ctx context.Context) *model.User {
	v, _ := ctx.Value(userCtxKey{}).(*model.User)
	return v
}
