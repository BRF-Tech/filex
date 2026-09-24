package httpx

import (
	"context"
	"sync/atomic"
)

// RequestLog carries who a request turned out to be, from the middleware that
// authenticated it back out to the access log.
//
// ⚠ Why a holder and not a context value read afterwards: the access log
// (api.Logger) is the OUTERMOST middleware, and every authenticating
// middleware sits further in, inside its own route group. Each one hands the
// rest of the chain a NEW context (auth.WithUser, auth.WithToken,
// tenant.WithScope); when the handler returns, the logger still holds the
// request it started with, whose context never saw any of it. So the logger
// puts this holder on the context FIRST, and the functions that attach a caller
// write into it on their way past. One place records, whatever door the
// request came through — session, API token, /dav, /s3, a WebSocket ticket.
//
// The fields are atomic because a WebSocket handler keeps the request's
// context alive in goroutines that can still be running when the logger reads.
type RequestLog struct {
	userID  atomic.Int64
	tokenID atomic.Int64
	tenant  atomic.Pointer[string]
}

type requestLogKey struct{}

// WithRequestLog returns ctx carrying a fresh holder, and the holder itself.
func WithRequestLog(ctx context.Context) (context.Context, *RequestLog) {
	l := &RequestLog{}
	return context.WithValue(ctx, requestLogKey{}, l), l
}

// RequestLogFrom returns the holder on ctx, or nil when there is none — a
// background job, a test, a request that did not come through the router. Every
// method below is safe on nil, so a caller never has to check.
func RequestLogFrom(ctx context.Context) *RequestLog {
	if ctx == nil {
		return nil
	}
	l, _ := ctx.Value(requestLogKey{}).(*RequestLog)
	return l
}

// NoteUser records the authenticated account. Zero is not an account (a
// synthesized principal carries it) and is ignored rather than logged as one.
func (l *RequestLog) NoteUser(id int64) {
	if l != nil && id > 0 {
		l.userID.Store(id)
	}
}

// NoteToken records the API token the request was authenticated by — its row
// id, never the secret.
func (l *RequestLog) NoteToken(id int64) {
	if l != nil && id > 0 {
		l.tokenID.Store(id)
	}
}

// NoteTenant records the tenant (provider) slug the request is scoped to.
func (l *RequestLog) NoteTenant(slug string) {
	if l != nil && slug != "" {
		l.tenant.Store(&slug)
	}
}

// UserID is the recorded account id, 0 when none was.
func (l *RequestLog) UserID() int64 {
	if l == nil {
		return 0
	}
	return l.userID.Load()
}

// TokenID is the recorded token id, 0 when the request carried no API token.
func (l *RequestLog) TokenID() int64 {
	if l == nil {
		return 0
	}
	return l.tokenID.Load()
}

// Tenant is the recorded tenant slug, "" when the request was not scoped.
func (l *RequestLog) Tenant() string {
	if l == nil {
		return ""
	}
	if s := l.tenant.Load(); s != nil {
		return *s
	}
	return ""
}
