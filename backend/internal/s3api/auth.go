package s3api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// Authenticator turns a signed S3 request into a filex caller.
//
// It exists so the two halves cannot drift apart: looking a key up
// (protocolauth.AccessKey) and proving the request was signed by its holder
// (Verify) are useless separately, and a handler that did the first and forgot
// the second would authenticate anybody who knows an access key id — which is
// public by design. Nothing in this package hands out a Principal without a
// verified signature.
type Authenticator struct {
	Res *protocolauth.Resolver
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

// NewAuthenticator builds one.
func NewAuthenticator(res *protocolauth.Resolver) *Authenticator {
	return &Authenticator{Res: res}
}

func (a *Authenticator) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Authenticate verifies the signature and returns the caller.
//
// The returned Principal already carries the tenant scope, the confinement and
// the ACL resolver, so a handler cannot forget to attach one of them — see
// internal/protocolauth for why that matters more here than anywhere else.
func (a *Authenticator) Authenticate(r *http.Request) (*protocolauth.Principal, error) {
	if a == nil || a.Res == nil {
		return nil, ErrNoCredentials
	}
	sr, err := Parse(r)
	if err != nil {
		return nil, err
	}

	// The principal is resolved BEFORE the signature is checked, because the
	// secret is what the check needs. It is held here and returned only if the
	// signature proves the caller holds that secret — an access key id is not
	// a secret, it travels in the clear in every request.
	var (
		principal *protocolauth.Principal
		ctx       = r.Context()
	)
	lookup := func(keyID string) (string, error) {
		p, secret, lerr := a.Res.AccessKey(ctx, keyID)
		if lerr != nil {
			return "", lerr
		}
		principal = p
		return secret, nil
	}

	if err := Verify(r, sr, lookup, a.now()); err != nil {
		return nil, err
	}
	if principal == nil {
		// Defensive: Verify only reaches the comparison when lookup succeeded,
		// so this cannot happen — and if a future edit makes it possible, the
		// safe answer is to refuse rather than to fall through with no caller.
		return nil, ErrSignatureMismatch
	}
	return principal, nil
}

// ErrorCode maps a verification failure onto the S3 error code a client
// expects. Clients parse the code, not the HTTP status, so getting this wrong
// makes a correct refusal look like a broken server.
func ErrorCode(err error) (code string, status int) {
	switch {
	case errors.Is(err, ErrNoCredentials):
		return "AccessDenied", http.StatusForbidden
	case errors.Is(err, ErrMalformed):
		return "InvalidRequest", http.StatusBadRequest
	case errors.Is(err, ErrExpired):
		// S3's own code for a stale timestamp; clients key their clock-skew
		// retry off exactly this string.
		return "RequestTimeTooSkewed", http.StatusForbidden
	case errors.Is(err, ErrSignatureMismatch), errors.Is(err, protocolauth.ErrUnauthorized):
		return "SignatureDoesNotMatch", http.StatusForbidden
	default:
		return "InternalError", http.StatusInternalServerError
	}
}

// ctxKeyPrincipal is unexported so nothing outside this package can plant a
// caller on a context.
type ctxKeyPrincipal struct{}

// WithPrincipal stashes the verified caller for the handlers downstream.
func WithPrincipal(ctx context.Context, p *protocolauth.Principal) context.Context {
	return context.WithValue(ctx, ctxKeyPrincipal{}, p)
}

// PrincipalFrom returns the verified caller, if any.
func PrincipalFrom(ctx context.Context) (*protocolauth.Principal, bool) {
	p, ok := ctx.Value(ctxKeyPrincipal{}).(*protocolauth.Principal)
	return p, ok
}
