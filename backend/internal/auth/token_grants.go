package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The two token scopes this file reasons about. They mirror
// drivers/apitoken.ScopeAdmin and drivers/apitoken.ScopeRootPrefix, declared
// here too because the driver imports this package and not the reverse.
const (
	scopeAdmin      = "admin"
	scopeRootPrefix = "root:"
)

// # One rule, every door
//
// A request authenticated by an API token is limited to what THAT TOKEN
// grants, whatever the account behind it could do. For the account-wide
// operator surfaces the grant is: the `admin` scope (an empty scope list grants
// every scope, so it counts), and no `root:` confinement. A token confined to
// one folder is a folder credential; it is never an operator credential, even
// when it also names `admin`.
//
// Every surface that asks "may this caller administer" asks it here: the panel
// routes (RequireAdmin), the token-only admin surface (RequireAdminToken), the
// MCP admin tools, and the handler branches that act on other people's rows.
// A second copy of this rule is the copy that drifts.

// TokenIsConfined reports whether tok carries a `root:` confinement scope.
//
// ⚠ Syntactic on purpose: ANY `root:` entry counts, well-formed or not. A scope
// the confinement parser cannot read (`root:projects`, no storage) would
// otherwise make the token look unconfined here — the one direction a mistake
// in an authorization check must not fall.
func TokenIsConfined(tok *model.APIToken) bool {
	if tok == nil {
		return false
	}
	for _, s := range strings.Split(tok.Scopes, ",") {
		if strings.HasPrefix(strings.TrimSpace(s), scopeRootPrefix) {
			return true
		}
	}
	return false
}

// TokenMayAdminister reports whether a token is an operator credential: it
// grants the admin scope and is not confined. It says nothing about the
// account the token is bound to — see CallerMayAdminister for that.
func TokenMayAdminister(tok *model.APIToken) bool {
	return tok != nil && tok.HasScope(scopeAdmin) && !TokenIsConfined(tok)
}

// CallerMayAdminister reports whether the request's caller may use an
// account-wide operator power: the account is an administrator AND, when the
// request was authenticated by a token, that token may administer.
//
// A session (no token on the context) is judged by the account alone, exactly
// as before.
func CallerMayAdminister(ctx context.Context) bool {
	u := UserFrom(ctx)
	if u == nil || !u.IsAdmin() {
		return false
	}
	tok := TokenFrom(ctx)
	return tok == nil || TokenMayAdminister(tok)
}

// adminRefusal explains a refused admin request. The caller already holds the
// credential, so naming what it lacks discloses nothing — and whoever reads it
// is looking at a scrape job's or an integration's log, not at this file.
func adminRefusal(tok *model.APIToken) string {
	switch {
	case tok == nil:
		return "forbidden"
	case TokenIsConfined(tok):
		return "a token confined to a folder (root: scope) cannot use account-wide admin routes"
	case !tok.HasScope(scopeAdmin):
		return "token missing scope: " + scopeAdmin
	}
	return "forbidden"
}

// RequireAdminToken is the gate for the token-only admin surface (/api/ai/admin),
// where the token's scope — not the account's role — is what authorizes (the
// handlers there elevate the bound principal). It replaces RequireScope("admin")
// so a confined token is refused too. Must run after APITokenMiddleware.
func RequireAdminToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := TokenFrom(r.Context())
		if !TokenMayAdminister(tok) {
			msg := adminRefusal(tok)
			if tok == nil {
				msg = "token missing scope: " + scopeAdmin
			}
			writeAuthErr(w, http.StatusForbidden, msg)
			return
		}
		next.ServeHTTP(w, r)
	})
}
