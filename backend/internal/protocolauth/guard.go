package protocolauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/loginguard"
)

// ThrottledError is what Password and Any answer when a sign-in attempt is
// refused because a lock is in force (loginguard). It reads as ErrUnauthorized
// to any code that only checks for that — FTP and SFTP have no way to say more
// than "no" — while WebDAV, which can, answers 429 with Retry-After.
type ThrottledError struct {
	Verdict loginguard.Verdict
}

func (e *ThrottledError) Error() string {
	return fmt.Sprintf("protocolauth: too many failed attempts (%s lock, %s left)", e.Verdict.Scope, e.Verdict.RetryAfter.Round(1e9))
}

// Unwrap makes errors.Is(err, ErrUnauthorized) true.
func (e *ThrottledError) Unwrap() error { return ErrUnauthorized }

// AsThrottled reports whether err is a lock refusal, and returns it.
func AsThrottled(err error) (*ThrottledError, bool) {
	var t *ThrottledError
	if errors.As(err, &t) {
		return t, true
	}
	return nil, false
}

type doorKey struct{}

// WithSource stamps a context with where a protocol login came from: the door
// (loginguard.ProtoDAV, ProtoFTP, ProtoSFTP) and the client's address. The
// protocol calls it once per authentication, from what only it can see — the
// socket peer, or for HTTP-based ones the request. An address left out is
// counted under a single "unknown" subject, which is the wrong way to fail
// (every unknown caller shares one counter), so a new protocol must supply it.
func WithSource(ctx context.Context, door, ip string) context.Context {
	return context.WithValue(clientip.WithIP(ctx, ip), doorKey{}, door)
}

func doorOf(ctx context.Context) string {
	d, _ := ctx.Value(doorKey{}).(string)
	return d
}

// attempt is one sign-in for the limit. The realm (multi-tenant) is part of the
// account counter: `acme/alex`, `alex` at acme's address and the web form's
// alex in realm acme are one counter; beta's alex and the platform's are
// others (auth.LoginRealm.CounterRealm).
func (r *Resolver) attempt(ctx context.Context, identifier string, lr *auth.LoginRealm) loginguard.Attempt {
	return loginguard.Attempt{Identifier: identifier, Realm: lr.CounterRealm(), IP: clientip.FromContext(ctx), Protocol: doorOf(ctx)}
}

// gate asks the limit whether this attempt may be judged at all.
func (r *Resolver) gate(ctx context.Context, identifier string, lr *auth.LoginRealm) (loginguard.Attempt, error) {
	att := r.attempt(ctx, identifier, lr)
	if r.Guard == nil {
		return att, nil
	}
	if v := r.Guard.Check(ctx, att); v.Blocked {
		return att, &ThrottledError{Verdict: v}
	}
	return att, nil
}

// failed records a wrong attempt. It answers the error to give: the plain
// ErrUnauthorized, or a ThrottledError when this very attempt tripped a lock —
// the caller is told the door has just shut, not merely "no".
func (r *Resolver) failed(ctx context.Context, att loginguard.Attempt) error {
	if r.Guard == nil {
		return ErrUnauthorized
	}
	o := r.Guard.Failed(ctx, att, loginguard.ReasonCredentials)
	if o.Locked {
		return &ThrottledError{Verdict: loginguard.Verdict{Blocked: true, Scope: o.Scope, RetryAfter: o.RetryAfter}}
	}
	return ErrUnauthorized
}

func (r *Resolver) succeeded(ctx context.Context, att loginguard.Attempt) {
	if r.Guard != nil {
		r.Guard.Succeeded(ctx, att)
	}
}
