package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/httpx"
)

// Logger emits one structured log per request.
//
// Beyond method, path, status, ip and duration it names the caller when one
// was established — `user_id`, `token_id` (the API token's row id, never its
// secret) and the multi-tenant `tenant` slug — and, on the file manager's one
// path, WHICH verb was asked (`action`). Before, a line read `method=POST
// path=/api/files/manager status=500`: every file operation shares that path,
// one account often backs several tokens (a desktop, a CLI, a proxy), and
// "which client failed doing what" had no answer.
//
// ⚠ The query string is never logged, and must not be. It carries thumbnail
// and OnlyOffice signatures (`sig`), WebSocket tickets, share PINs, OIDC codes,
// S3 presigned credentials on /s3, and people's search text. `action` is the one
// value taken from it, only on /api/files/manager, and only as one of the
// manager's own verbs (managerAction).
func Logger(next http.Handler) http.Handler { return LoggerAt("")(next) }

// LoggerAt is Logger for a router served under a base path
// (internal/basepath). It runs OUTSIDE the base middleware, so a request that
// never reaches a handler — one outside the base, the classic sign of a proxy
// that strips the prefix — is still logged, with the path exactly as it
// arrived; the manager's verb is read from the path with the base taken off.
func LoggerAt(base string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return loggerFor(base, next)
	}
}

func loggerFor(base string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: 200, req: r.Context()}
		// ⚠ Read BEFORE the handler runs. confine.Middleware rewrites the
		// query of this very request (it shares the url.URL), so afterwards
		// it is not the query the caller sent.
		action := ""
		if p, ok := basepath.Strip(base, r.URL.Path); ok {
			action = managerAction(p, r)
		}
		// A peer that sends X-Forwarded-For without being a trusted proxy is
		// remembered here, once per request, so the Sign-in security page can
		// name it (internal/clientip, forwarders.go).
		clientip.Observe(r)
		// Authentication happens further in, on contexts this function never
		// sees; auth.WithUser / auth.WithToken and the tenant resolver write
		// into this holder on their way past (httpx.RequestLog).
		ctx, who := httpx.WithRequestLog(r.Context())
		// ⚠ The "http" line below is written when the answer is FINISHED. A
		// request that reached the server and never finished left no line at
		// all, so "the browser never sent it" could not be told from "it
		// arrived and hung" (e2e task #81: a login page whose i18n chunk the
		// log never showed). At debug level the arrival is logged as well,
		// with the socket's own address (`peer`, port included): `ip` is the
		// client as resolved through trusted proxies and carries no port, and
		// the port is what tells which connection a request came on.
		if slog.Default().Enabled(r.Context(), slog.LevelDebug) {
			slog.LogAttrs(context.Background(), slog.LevelDebug, "http start",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("ip", clientIP(r)),
				slog.String("peer", r.RemoteAddr))
		}
		defer func() {
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.status),
				slog.String("ip", clientIP(r)),
				slog.Int64("dur_us", time.Since(start).Microseconds()),
			}
			if id := who.UserID(); id > 0 {
				attrs = append(attrs, slog.Int64("user_id", id))
			}
			if id := who.TokenID(); id > 0 {
				attrs = append(attrs, slog.Int64("token_id", id))
			}
			if t := who.Tenant(); t != "" {
				attrs = append(attrs, slog.String("tenant", t))
			}
			if action != "" {
				attrs = append(attrs, slog.String("action", action))
			}
			slog.LogAttrs(context.Background(), slog.LevelInfo, "http", attrs...)
		}()
		next.ServeHTTP(ww, r.WithContext(ctx))
	})
}

// managerPath is the one route whose verb is a query parameter.
const managerPath = "/api/files/manager"

// managerVerbs are the verbs handlers.Manager dispatches on — List (GET) and
// Mutate (POST). A value outside this set is logged as "other", never as
// itself: it is whatever the caller typed.
var managerVerbs = map[string]bool{
	"index": true, "subfolders": true, "changes": true, "search": true, "preview": true, "download": true,
	"newfolder": true, "newfile": true, "rename": true, "move": true, "delete": true, "upload": true, "allowed": true,
}

// managerAction returns the file-manager verb of r for the access log: "" on
// every other path and when no verb was sent, "other" for a verb the manager
// does not have.
//
// `q` is read as the verb ONLY here, because it is the manager's legacy
// spelling of `action` (handlers.Manager.List). On /api/files/search the same
// name is the search text.
func managerAction(path string, r *http.Request) string {
	if path != managerPath {
		return ""
	}
	q := r.URL.Query()
	verb := q.Get("action")
	if verb == "" {
		verb = q.Get("q")
	}
	switch {
	case verb == "":
		return ""
	case managerVerbs[verb]:
		return verb
	default:
		return "other"
	}
}

// Recoverer returns 500 JSON on panic and prints the stack to slog.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rv := recover(); rv != nil {
				slog.Error("panic",
					slog.Any("rec", rv),
					slog.String("stack", string(debug.Stack())))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "internal server error",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// JSONError is a small JSON 4xx/5xx helper used by handlers.
func JSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// JSONOk writes a 200 JSON body.
func JSONOk(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

type statusWriter struct {
	http.ResponseWriter
	status int
	// req is the request's own context, taken before any middleware wraps
	// it: net/http cancels it when the client is no longer waiting (it
	// disconnected, or cancelled the request).
	req context.Context
}

// StatusClientClosedRequest is the status an answer is given and logged with
// when the client left before it was written: nginx's 499. Not an HTTP status
// anybody receives - there is nobody left to receive it.
const StatusClientClosedRequest = 499

// WriteHeader records the status for the access log.
//
// ⚠ A 5xx written AFTER the client went away is a 499 (task #260). A handler
// whose store read was cancelled because the browser dropped the request (a
// page being left: WebKit cancels everything in flight) answered 500 with
// "context canceled", and the log said the server had failed - the 0.56.3
// gate's one 500, on the bell's unread count, read as a server fault while
// the explorer next to it was the thing to look at. Every handler gets this
// here, once, instead of each learning to tell a cancelled context apart.
// A 4xx is left alone: it is an answer about the request, not about the
// server, whatever became of the client.
func (sw *statusWriter) WriteHeader(code int) {
	if code >= 500 && sw.clientGone() {
		code = StatusClientClosedRequest
	}
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// clientGone reports whether the client has stopped waiting. ⚠ Cancelled,
// not past a deadline: a deadline is the server running out of time, which
// is the server's failure and stays a 5xx.
func (sw *statusWriter) clientGone() bool {
	return sw.req != nil && errors.Is(sw.req.Err(), context.Canceled)
}

// Unwrap exposes the wrapped ResponseWriter so http.ResponseController /
// coder/websocket can reach the underlying http.Hijacker for WebSocket
// upgrades (GET /api/ws). Without it the Logger middleware's statusWriter
// hides the Hijacker and the upgrade fails with 501.
func (sw *statusWriter) Unwrap() http.ResponseWriter { return sw.ResponseWriter }

// clientIP is the address the request came from (internal/clientip: the
// socket's peer, or the forwarded address when the peer is a trusted proxy).
func clientIP(r *http.Request) string { return clientip.FromRequest(r) }
