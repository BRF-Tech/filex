// Package auth — audit_middleware.go
//
// Chi middleware that watches mutating routes and records a row in
// audit_log when the response is a 2xx. Wraps the response writer to
// capture the final status code and reads the *model.User from the
// request context (so it must be installed AFTER auth.Middleware).
package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// AuditMiddleware returns a chi middleware that records audit_log entries
// for successful (2xx) mutating requests on a curated set of paths.
func AuditMiddleware(store db.Store) func(http.Handler) http.Handler {
	if store == nil {
		// Defensive: behave as a no-op when the store is missing rather
		// than crashing the server at boot.
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !shouldAudit(r) {
				next.ServeHTTP(w, r)
				return
			}
			rw := &auditRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			if rw.status < 200 || rw.status >= 300 {
				return
			}
			user := UserFrom(r.Context())
			action, targetType, targetID := actionFor(r)
			if action == "" {
				return
			}
			entry := &model.AuditEntry{
				Action:     action,
				TargetType: targetType,
				TargetID:   targetID,
				IP:         clientIP(r),
				CreatedAt:  time.Now(),
			}
			if user != nil && user.ID > 0 {
				uid := user.ID
				entry.UserID = &uid
			}
			// Token-authenticated calls stamp WHICH credential + identity acted:
			// one account often backs several tokens (work, fishapp, MCP…), and
			// user_id alone can't tell them apart.
			if tok := TokenFrom(r.Context()); tok != nil {
				entry.Metadata = map[string]interface{}{"token_id": tok.ID}
				if tu := TokenUserFrom(r.Context()); tu != "" {
					entry.Metadata["token_username"] = tu
				}
			}
			// Best-effort — don't block the response on a logging error.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := store.InsertAuditEntry(ctx, entry); err != nil {
				slog.Warn("audit middleware insert failed",
					slog.String("action", action),
					slog.String("err", err.Error()))
			}
		})
	}
}

// auditRecorder captures the response status code so we only log 2xx requests.
type auditRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader records the status and forwards the call.
func (a *auditRecorder) WriteHeader(code int) {
	if !a.wroteHeader {
		a.status = code
		a.wroteHeader = true
	}
	a.ResponseWriter.WriteHeader(code)
}

// Write captures an implicit 200 if WriteHeader was never called.
func (a *auditRecorder) Write(b []byte) (int, error) {
	if !a.wroteHeader {
		a.status = http.StatusOK
		a.wroteHeader = true
	}
	return a.ResponseWriter.Write(b)
}

// shouldAudit decides whether this request is interesting enough to log.
//
// We only audit mutating verbs on /api/admin/* plus a curated subset of
// /api/auth/* and /api/files/*. Read-only GETs are NEVER audited (way too
// noisy and not what the audit log is for).
func shouldAudit(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/admin/"):
		return true
	case strings.HasPrefix(p, "/api/ai/admin/"):
		// AI-surface admin REST mirror (/api/ai/admin/*). Same admin write
		// ops as the native panel, just behind a token instead of a session.
		return true
	case strings.HasPrefix(p, "/api/ai/"):
		// AI file surface: reads are GET (already filtered out by method), so
		// every mutating call here is a real write (upload/mkdir/move/delete/
		// share/zip…) made by an integration token — exactly what the audit
		// log should attribute per token username.
		return true
	case strings.HasPrefix(p, "/api/sharex/"):
		// ShareX uploads are token-driven writes too.
		return true
	case strings.HasPrefix(p, "/api/auth/"):
		// Skip noisy auth endpoints (login/logout get their own dedicated
		// trail; whoami is GET only).
		switch p {
		case "/api/auth/login", "/api/auth/logout":
			return false
		}
		return true
	case strings.HasPrefix(p, "/api/files/"):
		// Audit only structurally-significant file ops. Reads / listings /
		// stat / search are GET (already filtered by method) but ops like
		// /api/files/ops are noise — skip.
		switch {
		case strings.HasPrefix(p, "/api/files/share"),
			strings.HasPrefix(p, "/api/files/manager"),
			strings.HasPrefix(p, "/api/files/upload/finalize"),
			strings.HasPrefix(p, "/api/files/upload/abort"),
			strings.HasPrefix(p, "/api/files/versions"),
			strings.HasPrefix(p, "/api/files/archive/extract"),
			strings.HasPrefix(p, "/api/files/archive/add"):
			return true
		}
	}
	return false
}

// actionFor maps the request to (action, target_type, target_id), reading the
// chi URL params off the context. It dispatches to ActionForPath, normalizing
// + tagging the AI-surface admin mirror (/api/ai/admin/*) via AIAdminAction so
// those writes are distinguishable from native-panel ones in the audit log.
func actionFor(r *http.Request) (string, string, string) {
	id := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	if strings.HasPrefix(r.URL.Path, "/api/ai/admin") {
		return AIAdminAction(r.Method, r.URL.Path, id, name)
	}
	return ActionForPath(r.Method, r.URL.Path, id, name)
}

// AIAdminAction derives the audit (action, targetType, targetID) for a request
// on the AI admin surface (/api/ai/admin/*). It normalizes the path down to the
// native /api/admin/* form, reuses ActionForPath's mapping, and prefixes the
// resulting action with "ai." so AI-token-driven admin writes are clearly
// distinguishable from native-panel ones in the Audit page. id/name are the
// corresponding chi URL params ("" when absent). Returns an empty action when
// the call isn't an auditable mutating admin op.
//
// Used both by the HTTP AuditMiddleware (for /api/ai/admin REST calls) and by
// the MCP admin tools (which bypass HTTP middleware and audit in-process).
func AIAdminAction(method, path, id, name string) (string, string, string) {
	norm := path
	if strings.HasPrefix(norm, "/api/ai/admin") {
		norm = "/api/admin" + strings.TrimPrefix(norm, "/api/ai/admin")
	}
	action, targetType, targetID := ActionForPath(method, norm, id, name)
	if action != "" {
		action = "ai." + action
	}
	return action, targetType, targetID
}

// ActionForPath maps a (method, path) pair to (action, target_type, target_id).
//
// The path matching uses strings.HasPrefix on /api/admin/storages/ etc. so
// trailing slashes / IDs are handled uniformly. We intentionally don't try
// to read JSON bodies — only URL-derivable identifiers. id/name are the
// already-resolved chi URL params (passed in so this function is reusable
// outside an *http.Request context, e.g. from the in-process MCP invoker).
func ActionForPath(method, p, id, name string) (string, string, string) {
	switch {
	// ── storages ──
	case method == http.MethodPost && p == "/api/admin/storages/":
		return "storage.create", "storage", ""
	case method == http.MethodPost && p == "/api/admin/storages":
		return "storage.create", "storage", ""
	case method == http.MethodPatch && strings.HasPrefix(p, "/api/admin/storages/") && id != "":
		return "storage.update", "storage", id
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/admin/storages/") && id != "":
		return "storage.delete", "storage", id
	case method == http.MethodPost && strings.HasSuffix(p, "/sync") && strings.HasPrefix(p, "/api/admin/storages/"):
		return "storage.sync_trigger", "storage", id
	case method == http.MethodPost && p == "/api/admin/storages/test":
		return "storage.test", "storage", ""

	// ── users ──
	case method == http.MethodPost && (p == "/api/admin/users/" || p == "/api/admin/users"):
		return "user.create", "user", ""
	case method == http.MethodPatch && strings.HasPrefix(p, "/api/admin/users/") && id != "" && !strings.Contains(p, "/quota"):
		return "user.update", "user", id
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/admin/users/") && id != "":
		return "user.delete", "user", id
	case method == http.MethodPost && strings.HasSuffix(p, "/reset-password") && strings.HasPrefix(p, "/api/admin/users/"):
		return "user.password_reset", "user", id
	case method == http.MethodPatch && strings.HasSuffix(p, "/quota") && strings.HasPrefix(p, "/api/admin/users/"):
		return "user.quota_set", "user", id
	case method == http.MethodPost && strings.HasSuffix(p, "/quota/recompute") && strings.HasPrefix(p, "/api/admin/users/"):
		return "user.quota_recompute", "user", id

	// ── self-service ──
	case method == http.MethodPatch && p == "/api/auth/profile":
		return "profile.update", "profile", ""
	case method == http.MethodPost && p == "/api/auth/password":
		return "profile.password_change", "profile", ""
	case method == http.MethodPost && p == "/api/auth/totp/enroll":
		return "totp.enroll", "profile", ""
	case method == http.MethodPost && p == "/api/auth/totp/verify":
		return "totp.verify", "profile", ""
	case method == http.MethodPost && p == "/api/auth/totp/disable":
		return "totp.disable", "profile", ""

	// ── shares ──
	case method == http.MethodPost && p == "/api/files/share":
		return "share.create", "share", ""
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/files/share/") && id != "":
		return "share.delete", "share", id
	case method == http.MethodPost && strings.HasSuffix(p, "/revoke") && strings.HasPrefix(p, "/api/admin/shares/"):
		return "share.revoke", "share", id
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/admin/shares/") && id != "":
		return "share.delete", "share", id

	// ── files ──
	case method == http.MethodDelete && p == "/api/files/manager":
		return "file.delete", "node", ""
	case method == http.MethodPost && p == "/api/files/manager/restore":
		return "file.restore", "node", ""
	case method == http.MethodPost && p == "/api/files/upload/finalize":
		return "file.upload", "node", ""
	case method == http.MethodPost && p == "/api/files/upload/abort":
		return "file.upload_abort", "upload", ""
	case method == http.MethodPost && p == "/api/files/archive/extract":
		return "file.archive_extract", "node", ""
	case method == http.MethodPost && p == "/api/files/archive/add":
		return "file.archive_add", "node", ""
	case method == http.MethodPost && strings.HasPrefix(p, "/api/files/manager/tags"):
		return "file.tags_set", "node", ""
	case method == http.MethodPost && strings.HasPrefix(p, "/api/files/manager/star"):
		return "file.star", "node", ""

	// ── versions ──
	case method == http.MethodPost && p == "/api/files/versions/restore":
		return "version.restore", "version", ""
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/files/versions/") && id != "":
		return "version.delete", "version", id

	// ── settings / external / auth providers ──
	case method == http.MethodPatch && p == "/api/admin/settings":
		return "settings.update", "setting", ""
	case method == http.MethodPut && strings.HasPrefix(p, "/api/admin/settings/"):
		return "settings.update", "setting", ""
	case method == http.MethodPatch && strings.HasPrefix(p, "/api/admin/external/") && name != "":
		return "external.update", "external", name
	case method == http.MethodPost && strings.HasSuffix(p, "/test") && strings.HasPrefix(p, "/api/admin/external/"):
		return "external.test", "external", name
	case method == http.MethodPatch && strings.HasPrefix(p, "/api/admin/auth-providers/") && name != "":
		return "auth_provider.update", "auth_provider", name
	case method == http.MethodPost && strings.HasSuffix(p, "/test") && strings.HasPrefix(p, "/api/admin/auth-providers/"):
		return "auth_provider.test", "auth_provider", name

	// ── trash ──
	case method == http.MethodPost && p == "/api/admin/trash/empty":
		return "trash.empty", "trash", ""

	// ── search ──
	case method == http.MethodPost && p == "/api/admin/search/rebuild":
		return "search.rebuild", "search", ""

	// ── sync ──
	case method == http.MethodPost && strings.HasPrefix(p, "/api/admin/sync-runs/"):
		return "sync.action", "sync_run", id
	}

	// AI file surface (/api/ai/<verb>, POST-only writes). Named after the verb
	// segment so upload/mkdir/move/delete/share/zip all map without a case
	// each — and a future endpoint lands as ai.file.<verb> instead of
	// vanishing.
	if strings.HasPrefix(p, "/api/ai/") && !strings.HasPrefix(p, "/api/ai/admin") {
		seg := strings.TrimPrefix(p, "/api/ai/")
		if i := strings.IndexByte(seg, '/'); i >= 0 {
			seg = seg[:i]
		}
		switch seg {
		case "share":
			return "ai.share.create", "share", ""
		case "unshare":
			return "ai.share.delete", "share", ""
		case "":
		default:
			return "ai.file." + seg, "node", ""
		}
	}
	if method == http.MethodPost && strings.HasPrefix(p, "/api/sharex/") {
		return "sharex.upload", "node", ""
	}

	// Generic fallback: any other mutating /api/admin/* request still gets a
	// recognizable audit entry instead of silently vanishing (replica,
	// replication-targets, ai-tokens, notifications, …).
	if strings.HasPrefix(p, "/api/admin/") {
		seg := strings.TrimPrefix(p, "/api/admin/")
		if i := strings.IndexByte(seg, '/'); i >= 0 {
			seg = seg[:i]
		}
		if seg != "" {
			verb := "action"
			switch method {
			case http.MethodPost:
				verb = "create"
			case http.MethodPatch, http.MethodPut:
				verb = "update"
			case http.MethodDelete:
				verb = "delete"
			}
			return seg + "." + verb, seg, id
		}
	}
	return "", "", ""
}

// clientIP mirrors the helper in api/middleware.go so we don't pull a dep cycle.
func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		// take just the first hop
		if idx := strings.IndexByte(v, ','); idx >= 0 {
			return strings.TrimSpace(v[:idx])
		}
		return v
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	return r.RemoteAddr
}
