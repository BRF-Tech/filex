package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// apiTokenHeaderName is the dedicated header AI/MCP callers send. Kept in
// sync with drivers/apitoken.HeaderName (declared here too so this file has
// no dependency on the driver package — the driver imports auth, not the
// reverse).
const apiTokenHeaderName = "X-Filex-Token"

// tokenCtxKey carries the matched *model.APIToken on the request context.
type tokenCtxKey struct{}

// WithToken stores the matched API token on ctx.
func WithToken(ctx context.Context, t *model.APIToken) context.Context {
	return context.WithValue(ctx, tokenCtxKey{}, t)
}

// TokenFrom returns the API token from ctx, nil if the request was not
// authenticated by an API token (e.g. a cookie session).
func TokenFrom(ctx context.Context) *model.APIToken {
	v, _ := ctx.Value(tokenCtxKey{}).(*model.APIToken)
	return v
}

// APITokenMiddleware authenticates a request strictly via an API token
// (X-Filex-Token or Authorization: Bearer), attaches both the bound user
// and the matched token to the context, and rejects anything else with 401.
//
// Unlike the generic Middleware chain it does NOT accept cookie sessions —
// the AI/MCP namespace is token-only by design. Use RequireScope after it
// to gate individual verbs.
func APITokenMiddleware(store db.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := extractAPIToken(r)
			if raw == "" {
				writeAuthErr(w, http.StatusUnauthorized, "missing api token")
				return
			}
			ctx := r.Context()
			tok, err := store.GetAPITokenByHash(ctx, hashAPIToken(raw))
			if err != nil {
				writeAuthErr(w, http.StatusUnauthorized, "invalid api token")
				return
			}
			if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
				writeAuthErr(w, http.StatusUnauthorized, "token expired")
				return
			}
			user, err := store.GetUser(ctx, tok.UserID)
			if err != nil {
				writeAuthErr(w, http.StatusUnauthorized, "token user not found")
				return
			}
			_ = store.TouchAPIToken(ctx, tok.ID)

			ctx = WithUser(ctx, user)
			ctx = WithToken(ctx, tok)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// MiddlewareWithToken authenticates EITHER via an API token (X-Filex-Token /
// Bearer) OR, failing that, the regular user-session driver chain. It powers
// the /api/files surface so a host app can proxy with a root-confined API
// token (see package confine) while the native admin panel keeps using its
// cookie/JWT session. An API-token bearer and a session JWT never collide:
// the token's sha256 simply won't match a JWT, so we fall through cleanly.
func MiddlewareWithToken(store db.Store, required bool) func(http.Handler) http.Handler {
	userChain := Middleware(required)
	return func(next http.Handler) http.Handler {
		fallthroughHandler := userChain(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if raw := extractAPIToken(r); raw != "" {
				ctx := r.Context()
				if tok, err := store.GetAPITokenByHash(ctx, hashAPIToken(raw)); err == nil && tok != nil {
					if tok.ExpiresAt == nil || tok.ExpiresAt.After(time.Now()) {
						if user, err := store.GetUser(ctx, tok.UserID); err == nil && user != nil {
							_ = store.TouchAPIToken(ctx, tok.ID)
							ctx = WithUser(ctx, user)
							ctx = WithToken(ctx, tok)
							next.ServeHTTP(w, r.WithContext(ctx))
							return
						}
					}
				}
			}
			// Not a valid API token — defer to cookie/JWT/proxy-header auth.
			fallthroughHandler.ServeHTTP(w, r)
		})
	}
}

// RequireScope rejects requests whose token does not grant `scope`. A token
// with an empty Scopes field grants everything. Must run after
// APITokenMiddleware.
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := TokenFrom(r.Context())
			if tok == nil || !tok.HasScope(scope) {
				writeAuthErr(w, http.StatusForbidden, "token missing scope: "+scope)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractAPIToken(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(apiTokenHeaderName)); v != "" {
		return v
	}
	const bearer = "Bearer "
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, bearer) {
		return strings.TrimSpace(h[len(bearer):])
	}
	return ""
}

func hashAPIToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func writeAuthErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
