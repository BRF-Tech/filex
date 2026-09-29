package auth

import (
	"context"
	"net/http"
)

// The file verbs an API token can carry. They mirror drivers/apitoken's
// ScopeRead / ScopeWrite / ScopeDelete, declared here too because the driver
// imports this package and not the reverse.
const (
	VerbRead   = "read"
	VerbWrite  = "write"
	VerbDelete = "delete"
)

// # A token does what its verbs name, on every door
//
// `read` lists, downloads and searches; `write` creates, changes, moves and
// shares; `delete` removes. Every door a token can reach asks the same
// question: /api/ai (RequireScope), the web explorer's routes and their
// handlers (RequireVerb, AllowVerb), and the protocols (the principal's
// HasScope). A verb asked on one door and not on its twin is a token that is
// narrower in one client than in another.
//
// These helpers are that question for the HTTP doors. A request that
// carries no token — a browser session, a password over a protocol — is judged
// by the account alone, exactly as before; a token is judged by its list, and
// an empty list grants nothing (model.APIToken.HasScope).

// TokenAllows reports whether the request may use verb: always for a request
// with no API token on its context, and for a token when its list names verb.
func TokenAllows(ctx context.Context, verb string) bool {
	tok := TokenFrom(ctx)
	return tok == nil || tok.HasScope(verb)
}

// RefuseVerb writes the refusal a token without verb gets — the same body
// RequireScope has always answered on /api/ai, so a client reads one message
// whichever door it knocked on.
func RefuseVerb(w http.ResponseWriter, verb string) {
	writeAuthErr(w, http.StatusForbidden, "token missing scope: "+verb)
}

// RequireVerb is RequireScope for the routes that accept BOTH a session and a
// token (MiddlewareWithToken): a session passes, a token must hold verb.
//
// ⚠ RequireScope refuses a request with no token at all, which is right on the
// token-only /api/ai surface and wrong here — it would lock the web explorer
// out of its own routes.
func RequireVerb(verb string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !TokenAllows(r.Context(), verb) {
				RefuseVerb(w, verb)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AllowVerb is the in-handler form, for a route whose verb depends on the
// request (POST /api/files/manager?action=…, POST /api/files/ops {kind}). It
// answers the refusal itself and reports whether the handler may go on.
func AllowVerb(w http.ResponseWriter, r *http.Request, verb string) bool {
	if TokenAllows(r.Context(), verb) {
		return true
	}
	RefuseVerb(w, verb)
	return false
}
