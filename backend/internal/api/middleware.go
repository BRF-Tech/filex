package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

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
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: 200}
		// ⚠ Read BEFORE the handler runs. confine.Middleware rewrites the
		// query of this very request (it shares the url.URL), so afterwards
		// it is not the query the caller sent.
		action := managerAction(r)
		// Authentication happens further in, on contexts this function never
		// sees; auth.WithUser / auth.WithToken and the tenant resolver write
		// into this holder on their way past (httpx.RequestLog).
		ctx, who := httpx.WithRequestLog(r.Context())
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
	"index": true, "subfolders": true, "search": true, "preview": true, "download": true,
	"newfolder": true, "newfile": true, "rename": true, "move": true, "delete": true, "upload": true,
}

// managerAction returns the file-manager verb of r for the access log: "" on
// every other path and when no verb was sent, "other" for a verb the manager
// does not have.
//
// `q` is read as the verb ONLY here, because it is the manager's legacy
// spelling of `action` (handlers.Manager.List). On /api/files/search the same
// name is the search text.
func managerAction(r *http.Request) string {
	if r.URL.Path != managerPath {
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
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the wrapped ResponseWriter so http.ResponseController /
// coder/websocket can reach the underlying http.Hijacker for WebSocket
// upgrades (GET /api/ws). Without it the Logger middleware's statusWriter
// hides the Hijacker and the upgrade fails with 501.
func (sw *statusWriter) Unwrap() http.ResponseWriter { return sw.ResponseWriter }

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	return r.RemoteAddr
}
