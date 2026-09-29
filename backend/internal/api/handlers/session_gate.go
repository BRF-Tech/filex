package handlers

import (
	"net/http"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// sessionOnly is the session-only gate: it lets a request through when no API
// key is on its context — the admin panel's own sign-in (cookie or the session
// bearer it keeps), an SSO session, a trusted proxy's header — and answers an
// API key 403 {"error":"session_required", "message": …} itself, merging extra
// into the body. It reports whether the handler may go on.
//
// ⚠ One gate for every "a person must do this" door: each answers with this
// shape, and one that has more to say (the plugin install doors name where to
// leave a request instead) passes it in extra rather than writing a check of
// its own.
//
// ⚠ Called inside the handler, never as a route middleware: /api/ai/admin and
// the admin_* MCP tools reach the same handlers in-process with the key on the
// context (AIAdmin.invoke), and a route-level check would guard one door of
// three.
func sessionOnly(w http.ResponseWriter, r *http.Request, message string, extra map[string]any) bool {
	if auth.TokenFrom(r.Context()) == nil {
		return true
	}
	body := map[string]any{"error": "session_required", "message": message}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, http.StatusForbidden, body)
	return false
}

// adminCredentialBySession refuses an API key an act that hands out an
// ADMINISTRATOR's credential: creating an administrator, promoting an account
// to administrator, changing an administrator's role, and setting or resetting
// an administrator's password. what names the act for the message.
//
// # Why these, and only these (owner's decision, 2026-09-28)
//
// What an admin-scoped key may not do itself — install a plugin, approve an
// install request — is decided by a person signed in to the panel. A key that
// could create an administrator with a password it chose, or reset one and read
// the new password back, could sign that account in and BE that person. So the
// acts that end in an administrator's password need a session; managing
// ordinary accounts stays open to a key.
func adminCredentialBySession(w http.ResponseWriter, r *http.Request, what string) bool {
	return sessionOnly(w, r,
		what+" needs an administrator signed in to the admin panel; an API key cannot do it. "+
			"An API key may still create and manage accounts that are not administrators.",
		nil)
}

// allowsAdministration reports whether a permission list or an effects map
// ALLOWS any admin-area permission (perm.GroupAdmin: admin.users, .grants,
// .shares, .audit, .monitor). Handing one out makes an account a delegated
// administrator — an administrator's credential in all but name — so the
// doors that do it (a person's exceptions, a custom role's list and its folder
// part, the built-in roles' defaults) ask adminCredentialBySession exactly as
// creating an administrator does (#120). Taking one away is not a grant and
// stays open to a key.
func allowsAdministration(keys []string, effects map[string]string) bool {
	isAdmin := func(k string) bool {
		d, ok := perm.Lookup(perm.Perm(k))
		return ok && d.Group == perm.GroupAdmin
	}
	for _, k := range keys {
		if isAdmin(k) {
			return true
		}
	}
	for k, eff := range effects {
		if eff == model.PermAllow && isAdmin(k) {
			return true
		}
	}
	return false
}
