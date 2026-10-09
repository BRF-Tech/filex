package handlers

import (
	"net/http"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
)

// PluginRequestsPath is where somebody who may not install a plugin leaves a
// request for it (plugin_requests.go). The refusals below name it.
const PluginRequestsPath = "/api/admin/plugin-requests"

// requireSession refuses a request authenticated by an API key, answering 403
// itself: installing, upgrading, removing, switching or re-permissioning a
// plugin needs an administrator signed in to the admin panel.
//
// # Why a session and not "an admin-scoped key" (owner, 2026-09-28)
//
// An app's security boundary is the administrator's permission review ("the
// sha256 protects the administrator, the sandbox protects the user"). The
// install endpoint asks for the manifest's permissions to be repeated back in
// `permissions` as that approval — and an agent holding an admin-scoped key
// could simply copy them, so the "a person approves" step belonged to the
// agent. A storage plugin is worse: its process runs with filex's rights and
// receives every storage's credentials. So a key may READ plugins, run the
// install review (a dry run installs nothing) and LEAVE A REQUEST; the
// decision is a person's, on the Plugins page.
//
// ⚠ A session is "no API key on the context" — the web panel's own sign-in
// (cookie or the session bearer it keeps), an SSO session, a trusted
// proxy's header. The session cookie is SameSite=Lax, which is what keeps a
// cross-site form from riding it.
//
// ⚠ Called inside the handler, not as a route middleware: /api/ai/admin and
// the admin MCP tools reach handlers in-process with the key on the context
// (ai_admin.go invoke), and a route-level check would guard only one door —
// the reasoning of supertenant.go.
func requireSession(w http.ResponseWriter, r *http.Request, what string) bool {
	if auth.TokenFrom(r.Context()) == nil {
		return true
	}
	// The reader's sentence (server.error.plugin_session_required), naming
	// where to leave a request; what was refused stays as English detail.
	writeErrorSaid(w, r, http.StatusForbidden, "session_required", "plugin_session_required",
		apierr.Params{"endpoint": PluginRequestsPath}, "request_endpoint", PluginRequestsPath, "detail", what)
	return false
}

// isDryRun reports whether an install or upgrade asks only for its review.
func isDryRun(r *http.Request) bool {
	v := r.URL.Query().Get("dry_run")
	return v == "1" || v == "true"
}
