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
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/clientip"
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
			// ⚠⚠ ONE row per request. When an outer AuditMiddleware already
			// records this request, this one steps aside: the handler's detail
			// lands in the outer recorder and the outer one writes the row.
			// /api/ai/admin/* sat under two of them (the /api/ai group's and the
			// /admin route's own) and every admin write through an API key was
			// written twice - the second row empty (measured 2026-10-01).
			if d, _ := r.Context().Value(auditDetailKey{}).(*AuditDetail); d != nil {
				next.ServeHTTP(w, r)
				return
			}
			rw := &auditRecorder{ResponseWriter: w, status: http.StatusOK}
			ctx, detail := WithAuditDetail(r.Context())
			r = r.WithContext(ctx)
			next.ServeHTTP(rw, r)
			if rw.status < 200 || rw.status >= 300 {
				return
			}
			if detail.Skipped() {
				return
			}
			user := UserFrom(r.Context())
			action, targetType, targetID := actionFor(r)
			// A handler that knows its write belongs to another family than its
			// route says (a sign-in setting written through the generic settings
			// API) renames the row - through the same door rule as the route.
			if a, tt := detail.Action(); a != "" {
				action, targetType = DoorAction(a, strings.HasPrefix(r.URL.Path, "/api/ai/admin")), tt
			}
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
			entry.Metadata = detail.Into(entry.Metadata)
			entry.TargetID, entry.Metadata = detail.ApplyTarget(entry.TargetID, entry.Metadata)
			// Token-authenticated calls stamp WHICH credential + identity acted:
			// one account often backs several tokens (work, fishapp, MCP…), and
			// user_id alone can't tell them apart.
			entry.Metadata = StampTokenDoor(r.Context(), entry.Metadata, ViaAPI)
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
	case p == "/api/ai/mcp" || strings.HasPrefix(p, "/api/ai/mcp/"):
		// ⚠ The MCP transport is one POST per JSON-RPC message - a read, a
		// tools/list, a write alike. Every tool that CHANGES something writes
		// its own row, the one its REST twin writes (handlers/ai_mcp.go
		// mcpAuditWrite, AIAdmin.auditInvoke); a row per transport message
		// logged every read as "ai.file.mcp" and no write by what it did.
		return false
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
		case "/api/auth/account/check":
			// Asked while a form is typed: it writes nothing, and a row per
			// keystroke would bury the log.
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
			strings.HasPrefix(p, "/api/files/versions"),
			strings.HasPrefix(p, "/api/files/archive/extract"),
			strings.HasPrefix(p, "/api/files/archive/create"),
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
	return DoorAction(action, true), targetType, targetID
}

// doorAgnosticFamilies are the action families whose rows keep ONE name
// whatever door the administrator came in by - the panel, an admin API key,
// an MCP tool. The door is in the row's metadata instead (`token_id`, `via`).
//
// ⚠⚠ The sign-in limit's story is one filter (`login.`, plus
// `login_security.` for its settings): the limiter writes login.failed /
// locked / unlocked / allowlist_pass itself with no door prefix, and the
// Sign-in security page's trail reads that family. An unlock through an API
// key filed as `ai.login.unlocked` fell outside it, so the trail never showed
// it (measured 2026-10-01). plugin_request.* follows the same rule
// (internal/pluginreq writes its own rows, one name for every door).
var doorAgnosticFamilies = []string{"login.", "login_security."}

// DoorAction is the action an admin write is filed under: through the AI
// surface (aiDoor - /api/ai/admin or an admin_* MCP tool) it is prefixed
// "ai.", except for the door-agnostic families above. "" stays "".
func DoorAction(action string, aiDoor bool) string {
	if action == "" || !aiDoor {
		return action
	}
	for _, f := range doorAgnosticFamilies {
		if strings.HasPrefix(action, f) {
			return action
		}
	}
	return "ai." + action
}

// The doors an audited write through a token came in by, as metadata `via`.
// A session's write (the panel) carries none.
const (
	ViaAPI = "api"
	ViaMCP = "mcp"
)

// StampTokenDoor adds to meta (allocating it when needed) which token acted -
// `token_id`, `token_username` - and the door (`via`), when the request was
// authenticated by a token. A session's row is returned unchanged.
func StampTokenDoor(ctx context.Context, meta map[string]interface{}, via string) map[string]interface{} {
	tok := TokenFrom(ctx)
	if tok == nil {
		return meta
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta["token_id"] = tok.ID
	if tu := TokenUserFrom(ctx); tu != "" {
		meta["token_username"] = tu
	}
	if via != "" {
		meta["via"] = via
	}
	return meta
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
	// ── per-user permissions (internal/perm) ──
	// Ahead of the users block: /users/{id}/exceptions is a users path, and
	// "who changed what this account may do" deserves its own action rather
	// than the generic update the fallback would name it.
	case method == http.MethodPut && strings.HasPrefix(p, "/api/admin/users/") && strings.HasSuffix(p, "/exceptions"):
		return "user.permissions_set", "user", id
	case method == http.MethodPut && strings.HasPrefix(p, "/api/admin/users/") && strings.HasSuffix(p, "/roles"):
		return "user.roles_set", "user", id
	// Action names keep their first spelling: they are stored in the audit
	// log, and renaming them would orphan the rows already written.
	// /roles/builtin is matched before the /roles/{id} prefix below.
	case method == http.MethodPut && p == "/api/admin/roles/builtin":
		return "permissions.defaults_set", "permissions", ""
	// A role given back the permission a save on an older version took
	// away, or marked as lacking it on purpose (handlers/permission_gaps.go).
	// The target is the gap: "builtin:user:files.encrypt", "role:12:…".
	case method == http.MethodPost && p == "/api/admin/roles/gaps/restore":
		return "permission_gap.restore", "permission_gap", ""
	case method == http.MethodPost && p == "/api/admin/roles/gaps/dismiss":
		return "permission_gap.dismiss", "permission_gap", ""
	case method == http.MethodPost && (p == "/api/admin/roles" || p == "/api/admin/roles/"):
		return "permission_rule.create", "permission_rule", ""
	case method == http.MethodPut && strings.HasPrefix(p, "/api/admin/roles/") && id != "":
		return "permission_rule.update", "permission_rule", id
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/admin/roles/") && id != "":
		return "permission_rule.delete", "permission_rule", id
	// ── plugin install requests ──
	// internal/pluginreq writes its own rows — plugin_request.create /
	// approve / reject / expire / supersede — with what was frozen and who
	// decided, whichever door the call came in by (the panel, /api/ai/admin,
	// an MCP tool). A second, generic row here would count every event twice
	// and call an approval a "create".
	case strings.HasPrefix(p, "/api/admin/plugin-requests"):
		return "", "", ""
	// ── who may encrypt ──
	// internal/e2epolicy writes its own rows — e2e_policy.update,
	// e2e_tenant.update, e2e_request.* — naming the tenant and the value it
	// had and got (e2epolicy.Audit). A generic `e2e.update` here would be a
	// second row with neither.
	case p == "/api/admin/e2e" || strings.HasPrefix(p, "/api/admin/e2e/"):
		return "", "", ""
	// ── the panel's search (task #168) ──
	// A person's own recent searches: their bookkeeping, not a change to the
	// instance - and the words they searched for are theirs, not the audit
	// log's readers'.
	case p == "/api/admin/panel-search" || strings.HasPrefix(p, "/api/admin/panel-search/"):
		return "", "", ""

	// ── groups (internal/group) ──
	// Members first: /groups/{id}/members/… is under the /groups/{id} prefix.
	case method == http.MethodPost && strings.HasPrefix(p, "/api/admin/groups/") && strings.HasSuffix(p, "/members"):
		return "group.members_add", "group", id
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/admin/groups/") && strings.Contains(p, "/members/"):
		return "group.member_remove", "group", id
	case method == http.MethodPost && strings.HasPrefix(p, "/api/admin/groups/") && strings.HasSuffix(p, "/detach"):
		return "group.detach", "group", id
	case method == http.MethodPost && (p == "/api/admin/groups" || p == "/api/admin/groups/"):
		return "group.create", "group", ""
	case method == http.MethodPut && strings.HasPrefix(p, "/api/admin/groups/") && id != "":
		return "group.update", "group", id
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/admin/groups/") && id != "":
		return "group.delete", "group", id
	// A group's folder grant is numbered apart from a person's: the generic
	// "grants.delete" would file both under the same resource and id, and the
	// log could not say which one was revoked.
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/admin/grants/groups/") && id != "":
		return "group_grant.delete", "group_grant", id

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

	// ── tools ──
	// The target (storage id and the qualified path, or "*" for every
	// storage) and the mode are set by the handler (handlers.ThumbRepair).
	case method == http.MethodPost && p == "/api/admin/tools/thumbnails/repair":
		return "thumbnail.repair", "storage", ""
	case method == http.MethodPatch && p == "/api/admin/tools/thumbnails/settings":
		return "thumbnail.settings_update", "settings", ""

	// ── default apps (handlers.FileTypesAdmin; the kind and the before and
	// after are set by the handler) ──
	case method == http.MethodPut && strings.HasPrefix(p, "/api/admin/file-types/"):
		return "file_association.update", "file_type", ""
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/admin/file-types/"):
		return "file_association.reset", "file_type", ""
	case method == http.MethodPut && strings.HasPrefix(p, "/api/admin/app-plugins/") && strings.HasSuffix(p, "/thumbnails"):
		return "app_plugin.thumbnail_limits", "app_plugin", id

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
	case method == http.MethodPost && p == "/api/files/archive/extract":
		return "file.archive_extract", "node", ""
	case method == http.MethodPost && p == "/api/files/archive/create":
		return "file.archive_create", "node", ""
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

	// ── sign-in security ──
	// The unlock is filed under the same `login.` family the limiter writes its
	// own rows in (failed / locked / unlocked / allowlist_pass), so one filter
	// reads the whole story; the handler adds scope, subject and reason. Both
	// families keep their name through every door (DoorAction): the page's
	// trail reads `login.` and `login_security.`.
	case method == http.MethodPost && p == "/api/admin/login-security/unlock":
		return "login.unlocked", "login", ""
	case (method == http.MethodPatch || method == http.MethodPut) && strings.TrimSuffix(p, "/") == "/api/admin/login-security":
		return "login_security.update", "login_security", ""

	// ── settings / external / auth providers ──
	case method == http.MethodPatch && p == "/api/admin/settings":
		return "settings.update", "setting", ""
	case method == http.MethodPut && strings.HasPrefix(p, "/api/admin/settings/"):
		return "settings.update", "setting", ""
	case method == http.MethodPatch && strings.HasPrefix(p, "/api/admin/external/") && name != "":
		return "external.update", "external", name
	case method == http.MethodPost && strings.HasSuffix(p, "/test") && strings.HasPrefix(p, "/api/admin/external/"):
		return "external.test", "external", name
	case method == http.MethodPost && (p == "/api/admin/auth-providers" || p == "/api/admin/auth-providers/"):
		return "auth_provider.create", "auth_provider", ""
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/admin/auth-providers/") && name != "" && !strings.Contains(strings.TrimPrefix(p, "/api/admin/auth-providers/"), "/"):
		return "auth_provider.delete", "auth_provider", name
	case method == http.MethodPatch && strings.HasPrefix(p, "/api/admin/auth-providers/") && name != "":
		return "auth_provider.update", "auth_provider", name
	case method == http.MethodPost && strings.TrimSuffix(p, "/") == "/api/admin/auth-providers":
		return "auth_provider.create", "auth_provider", ""
	case method == http.MethodPut && strings.HasPrefix(p, "/api/admin/auth-providers/") && strings.HasSuffix(p, "/tenants"):
		return "auth_provider.tenants_set", "auth_provider", name
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/admin/auth-providers/") && name != "":
		return "auth_provider.delete", "auth_provider", name
	case method == http.MethodPost && strings.HasSuffix(p, "/test") && strings.HasPrefix(p, "/api/admin/auth-providers/"):
		return "auth_provider.test", "auth_provider", name
	case method == http.MethodPost && strings.HasSuffix(p, "/sync") && strings.HasPrefix(p, "/api/admin/auth-providers/"):
		return "auth_provider.sync", "auth_provider", name

	// ── a tenant running itself (handlers/tenant_self.go) ──
	// Its own providers are filed with the platform's (auth_provider.*), the
	// tenant in the row's metadata; its domains under tenant_domain.*.
	case strings.HasPrefix(p, "/api/admin/tenant/auth-providers"):
		switch {
		case method == http.MethodPost && strings.HasSuffix(p, "/test"):
			return "auth_provider.test", "auth_provider", name
		case method == http.MethodPost:
			return "auth_provider.create", "auth_provider", ""
		case method == http.MethodPatch:
			return "auth_provider.update", "auth_provider", name
		case method == http.MethodDelete:
			return "auth_provider.delete", "auth_provider", name
		}
	case strings.HasPrefix(p, "/api/admin/tenant/domains"):
		switch {
		case method == http.MethodPost && strings.HasSuffix(p, "/check"):
			return "tenant_domain.check", "tenant_domain", id
		case method == http.MethodPut && strings.HasSuffix(p, "/certificate"):
			return "tenant_domain.certificate_set", "tenant_domain", id
		case method == http.MethodDelete && strings.HasSuffix(p, "/certificate"):
			return "tenant_domain.certificate_delete", "tenant_domain", id
		case method == http.MethodPost:
			return "tenant_domain.create", "tenant_domain", ""
		case method == http.MethodDelete:
			return "tenant_domain.delete", "tenant_domain", id
		}
	case method == http.MethodPut && p == "/api/admin/tenant/insecure":
		return "tenant.insecure_auth_set", "providers", ""

	// ── tenants (providers) ──
	// A storage link names the tenant in its path, not the storage: the
	// generic fallback below filed a link as "providers.create" and an unlink
	// as "providers.delete", which reads as a tenant made or removed. The
	// storage id is in the row's metadata (handlers.Providers).
	case method == http.MethodPost && strings.HasPrefix(p, "/api/admin/providers/") && strings.HasSuffix(p, "/storages"):
		return "providers.storage_link", "providers", id
	case method == http.MethodDelete && strings.HasPrefix(p, "/api/admin/providers/") && strings.Contains(p, "/storages/"):
		return "providers.storage_unlink", "providers", id

	// ── the explorer's operations behind the AI surface ──
	// (handlers/ai_doors.go: /api/ai/<route> and the MCP tool that shares it,
	// whose row is this route's - mcpWriteTwin). Named here because the first
	// path segment alone - the generic ai.file.<segment> below - cannot tell a
	// version restore from a snapshot, or an archive made from one opened.
	case method == http.MethodPost && strings.HasPrefix(p, "/api/ai/ops/") && strings.HasSuffix(p, "/cancel"):
		return "ai.file.op_cancel", "op", id
	case method == http.MethodPost && p == "/api/ai/trash/restore":
		return "ai.file.restore", "node", ""
	case method == http.MethodPost && p == "/api/ai/versions/restore":
		return "ai.file.version_restore", "node", ""
	case method == http.MethodPost && p == "/api/ai/versions/snapshot":
		return "ai.file.version_snapshot", "node", ""
	case method == http.MethodPost && p == "/api/ai/archive/create":
		return "ai.file.archive_create", "node", ""
	case method == http.MethodPost && p == "/api/ai/archive/extract":
		return "ai.file.archive_extract", "node", ""
	// An app's action: the run names its row app_plugin.action_run once it is
	// queued (AppPlugins.enqueue); an action that only opened its form keeps
	// this one.
	case method == http.MethodPost && (p == "/api/ai/apps/run" || p == "/api/ai/convert"):
		return "ai.file.action_run", "node", ""
	// Marking one's own notices read is bookkeeping, unaudited as on
	// /api/notifications.
	case strings.HasPrefix(p, "/api/ai/notifications"):
		return "", "", ""
	case method == http.MethodPost && p == "/api/ai/comments":
		return "ai.file.comment_add", "node", ""
	case method == http.MethodPost && strings.HasPrefix(p, "/api/ai/comments/") && strings.HasSuffix(p, "/delete"):
		return "ai.file.comment_delete", "comment", id
	case method == http.MethodPost && p == "/api/ai/permissions":
		return "ai.file.grant_set", "node", ""
	case method == http.MethodPost && strings.HasPrefix(p, "/api/ai/permissions/") && strings.HasSuffix(p, "/revoke"):
		return "ai.file.grant_revoke", "grant", id

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

// clientIP is the address the request came from, without the port — the one
// resolver every surface shares (internal/clientip).
//
// ⚠ The port is the client's ephemeral source port — a different number on
// every connection, meaningless to anybody reading the log, and it made the
// Audit page's IP column read "127.0.0.1:54452" (release-candidate sweep,
// 2026-09-21). The shared resolver never returns one.
func clientIP(r *http.Request) string { return clientip.FromRequest(r) }

// AuditDetail is what a handler adds to the audit row the middleware writes
// for its request: the facts only the handler knows (which fields changed,
// before and after) — so a change is ONE row that says what it did, not a
// bare "auth_provider.update".
//
// ⚠ Names and flags only. Nothing a handler puts here may be a secret: the
// audit log is read by every administrator and exported.
type AuditDetail struct {
	mu sync.Mutex
	m  map[string]interface{}
	// targetID / targetName: the thing the request acted on, when the URL
	// does not say (a create has no id in its path; a delete's row is gone by
	// the time anybody reads the log). See SetAuditTarget.
	targetID, targetName string
	// skip: the handler recorded the request itself. See SkipAuditRow.
	skip bool
	// act / actType rename the row (SetAuditAction); "" = the route's name.
	act, actType string
}

// SetAuditAction files the request's audit row under another action (and
// target type) than its route maps to - for a generic route whose write, this
// time, belongs to a family of its own: a sign-in security setting written
// through the settings API is a `login_security.update` like the Sign-in
// security page's. The door rule still applies (DoorAction). A no-op when the
// request is not being audited.
func SetAuditAction(ctx context.Context, action, targetType string) {
	d, _ := ctx.Value(auditDetailKey{}).(*AuditDetail)
	if d == nil {
		return
	}
	d.mu.Lock()
	d.act, d.actType = action, targetType
	d.mu.Unlock()
}

// Action answers SetAuditAction's rename - action and target type; "" when
// there is none. Nil-safe.
func (d *AuditDetail) Action() (string, string) {
	if d == nil {
		return "", ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.act, d.actType
}

type auditDetailKey struct{}

// Audited reports whether a door is recording this request's audit row - the
// middleware above, or an in-process AI door (an MCP tool) that writes the row
// its REST twin writes. A handler that also writes a row of its own asks it
// first: when a door records the request, the handler names THAT row
// (SetAuditAction, SetAuditTarget, AddAuditDetail) instead of writing a
// second one beside it.
func Audited(ctx context.Context) bool {
	d, _ := ctx.Value(auditDetailKey{}).(*AuditDetail)
	return d != nil
}

// WithAuditDetail returns a context carrying an empty detail holder.
func WithAuditDetail(ctx context.Context) (context.Context, *AuditDetail) {
	d := &AuditDetail{}
	return context.WithValue(ctx, auditDetailKey{}, d), d
}

// AddAuditDetail records one fact for the request's audit row; a no-op when
// the request is not being audited.
func AddAuditDetail(ctx context.Context, key string, v interface{}) {
	d, _ := ctx.Value(auditDetailKey{}).(*AuditDetail)
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.m == nil {
		d.m = map[string]interface{}{}
	}
	d.m[key] = v
	d.mu.Unlock()
}

// SetAuditTarget names the thing the request acted on, for the audit row the
// middleware writes: id fills target_id when the route did not carry one,
// name is stored as metadata["target_name"].
//
// ⚠⚠ Why: the Panel and the Audit page printed the KIND of thing and nothing
// else — "Kullanıcı: oluşturuldu — Kullanıcı", "Depo: oluşturuldu — Depo"
// (release-candidate sweep, 2026-09-21) — because a create's URL has no id and
// the middleware never reads bodies. The handler knows what it made; this is
// how it says so. The name is kept because it is what a reader wants AFTER the
// thing is gone ("which user was deleted?"). A name here is a label — an
// e-mail, a storage name, a path — never a secret.
func SetAuditTarget(ctx context.Context, id, name string) {
	d, _ := ctx.Value(auditDetailKey{}).(*AuditDetail)
	if d == nil {
		return
	}
	d.mu.Lock()
	if id != "" {
		d.targetID = id
	}
	if name != "" {
		d.targetName = name
	}
	d.mu.Unlock()
}

// SkipAuditRow says the handler has recorded this request itself, so the
// AuditMiddleware that records the request, or the admin MCP tools'
// in-process copy of it (handlers.AIAdmin.auditInvoke), writes no generic row
// beside its own. A no-op when the request is not being audited.
//
// It is ActionForPath's skip for a door the URL cannot tell apart: PATCH
// /api/admin/settings carries e2e.policy only sometimes. When the policy is
// all a request changed, internal/e2epolicy's e2e_policy.update row, with the
// value before and after, is the record, and a bare settings.update beside it
// would be a second row that says less (handlers/settings.go).
//
// One request has one recorder: an AuditMiddleware nested inside another
// steps aside and leaves the row to the outer one (AuditMiddleware). On
// /api/ai/admin that is the /api/ai group's, the only one it passes
// (api/routes.go), so the mark reaches the recorder that writes the request's
// only row, and the handler's own row is the one left.
func SkipAuditRow(ctx context.Context) {
	d, _ := ctx.Value(auditDetailKey{}).(*AuditDetail)
	if d == nil {
		return
	}
	d.mu.Lock()
	d.skip = true
	d.mu.Unlock()
}

// Skipped reports whether the handler recorded its request itself
// (SkipAuditRow). Nil-safe.
func (d *AuditDetail) Skipped() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.skip
}

// ApplyTarget folds SetAuditTarget's answer into a row: the id only where the
// route gave none, the name into meta. Nil-safe.
func (d *AuditDetail) ApplyTarget(targetID string, meta map[string]interface{}) (string, map[string]interface{}) {
	if d == nil {
		return targetID, meta
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if targetID == "" {
		targetID = d.targetID
	}
	if d.targetName != "" {
		if meta == nil {
			meta = map[string]interface{}{}
		}
		meta["target_name"] = d.targetName
	}
	return targetID, meta
}

// Into merges the recorded facts into meta (allocating it when needed).
func (d *AuditDetail) Into(meta map[string]interface{}) map[string]interface{} {
	if d == nil {
		return meta
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.m) == 0 {
		return meta
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	for k, v := range d.m {
		meta[k] = v
	}
	return meta
}
