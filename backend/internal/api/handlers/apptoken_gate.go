package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

// The self-service CREDENTIAL surfaces — /api/tokens, /api/auth/s3-keys,
// /api/auth/ssh-keys, /api/auth/nfs-exports — all answer the same question:
// "show me, and let me mint, the credentials of the person who is calling."
// An app token has no such person, so all four refuse it, from ONE
// implementation.
//
// Why one and not four: an API token authenticates AS its owner
// (auth/apitoken_middleware.go sets WithUser(GetUser(tok.UserID))), and the
// embeds we run authenticate every visitor with ONE shared token injected by
// the host's proxy. The first round of this change gated only /api/tokens and
// measured the other three still answering 200 — an embed visitor could not
// list the proxy's API tokens any more, but could still mint an S3 access key
// bound to the token's owner. Same hole, different button. Three copies of a
// refusal is three chances for one of them to drift, which is exactly the bug
// class that produced the stray `.shared` tab strip in this release.
//
// ⚠ Confinement is a DIFFERENT axis. A `root:`-scoped token may perfectly well
// be a person's; it is not caught here, and must not be.
//
// ⚠ Role is a different axis too. A viewer is still a person, and the
// per-surface ceilings (SelfTokens.cappedScopes, protocolauth.Issue) keep
// doing their own job unchanged.

// RequirePersonalCaller refuses a request that arrived on an app token.
// Mounted on the self-service credential routes in routes.go — one place, so a
// new credential surface joins the rule by being registered inside the group
// rather than by remembering to copy a check.
func RequirePersonalCaller(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if deniedToApp(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// deniedToApp reports whether this request must be refused because it arrived
// on an APP token, and writes the 403 when it must.
//
// Status: 403, not 404. The route exists and the credential is valid — what is
// refused is this KIND of credential using it, and 404 would send an operator
// hunting for a missing route instead of reading the message. The message
// names the token and both ways out, because whoever reads it is looking at a
// host app's proxy logs, not at this file.
func deniedToApp(w http.ResponseWriter, r *http.Request) bool {
	// No token at all is a cookie/OIDC session — a person, always allowed.
	tok := auth.TokenFrom(r.Context())
	if !tok.IsApp() {
		return false
	}
	label := strings.TrimSpace(tok.Label)
	if label == "" {
		label = "unnamed"
	}
	id := strconv.FormatInt(tok.ID, 10)
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error": "this request is authenticated by an app token (" + label +
			", id " + id + "), which cannot manage its owner's credentials " +
			"(API tokens, S3 access keys, SSH keys, NFS exports). " +
			"Sign in as a person to mint your own, or — if this token really belongs to one person — " +
			"change its kind with PATCH /api/admin/ai-tokens/" + id + ` {"kind":"user"}.`,
		// Machine-readable so a client can tell "wrong kind of credential"
		// apart from "your role is too low", which is also a 403 here.
		"reason":     "app_token",
		"token_kind": model.TokenKindApp,
	})
	return true
}
