package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Middleware tries each enabled auth Driver in order. The first one that
// returns a non-nil user wins; subsequent drivers are not consulted.
//
// On failure with required=true, a 401 JSON error is written and the
// downstream handler is NOT called. With required=false, the request
// proceeds with no user attached (useful for public viewer endpoints).
//
// ⚠ Without a store it cannot read a tenant, so a suspended tenant's session
// is held only by the chain MiddlewareWithToken builds, which every route
// group in the router uses; a disabled account is refused either way.
func Middleware(required bool) func(http.Handler) http.Handler {
	return middlewareWith(required, nil)
}

// middlewareWith is Middleware with the store AccountBlock reads the
// account's tenant from.
func middlewareWith(required bool, store db.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			drivers := Enabled()

			var user *model.User
			var token *model.APIToken
			// told is the reason a driver refused THIS person and may say so
			// (the header proxy's first-login rule, auth.RefusalToTell): the
			// 401 below carries it as a code, for the sign-in page.
			told := ""
			for _, d := range drivers {
				u, tok, err := authenticate(d, r)
				if err != nil && told == "" && errors.Is(err, ErrUnauthorized) {
					told = SSOReason(err)
				}
				if err != nil && !errors.Is(err, ErrUnauthorized) {
					slog.Warn("auth driver error",
						slog.String("driver", d.Name()),
						slog.String("err", err.Error()))
					continue
				}
				if u != nil {
					user, token = u, tok
					break
				}
			}

			// A disabled account (migration 00022), or one whose tenant is
			// suspended, is refused even when a driver happily resolved it. A
			// session minted before the switch would otherwise keep working until
			// it expired, which makes "disabled" mean "disabled tomorrow". The
			// api-token driver's owner is refused here too.
			if RefuseBlockedAccount(w, r, store, user) {
				return
			}

			if user == nil && required {
				body := map[string]string{"error": "unauthorized"}
				if told != "" {
					// Only a code, and only when a driver vouched for who the
					// caller is: any other 401 is byte for byte what it was.
					body["reason"] = told
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(body)
				return
			}

			if user != nil {
				ctx := WithUser(r.Context(), user)
				if token != nil {
					// The token travels with the user it authenticated, so every
					// gate downstream judges the request by what the token grants
					// rather than by the account alone. Same username rule as
					// MiddlewareWithToken: a name outside the allow-list is a hard
					// error, not a silent fall back to the default identity.
					username, ok := token.ResolveUsername(r.Header.Get(tokenUserHeaderName))
					if !ok {
						writeAuthErr(w, http.StatusForbidden, "unknown token username")
						return
					}
					ctx = WithTokenUser(WithToken(ctx, token), username)
				} else if refuseTwoFactorPending(w, r.WithContext(ctx), user) {
					// A session whose account a rule requires to enrol 2FA
					// first (token_perm.go).
					return
				}
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RefuseBlockedAccount answers an account that may not act with the
// credential it holds (AccountBlock) and reports whether it did:
//
//   - switched off (users.enabled = false, migration 00022): 403
//     {"error":"this account is disabled","reason":"account_disabled"};
//   - its tenant suspended: the sign-in's own answer to a suspended tenant
//     (RefuseTenantLocked), reason `tenant_suspended`.
//
// ⭐ The one answer at every door a held credential opens: the session chain
// (Middleware), both API-token middlewares (admitTokenOwner,
// apitoken_middleware.go) and the sockets a ticket opens (handlers.WS.Handle,
// handlers.CoEdit.Socket), so the status, the words and the code the sign-in
// page translates are the same wherever the account is turned away. A door
// that resolves an account from a credential its request carries asks this,
// not its own copy. A nil store reads no tenant (a disabled account is still
// refused).
func RefuseBlockedAccount(w http.ResponseWriter, r *http.Request, store db.Store, u *model.User) bool {
	switch AccountBlock(r.Context(), store, u) {
	case SSOReasonAccountDisabled:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":  "this account is disabled",
			"reason": SSOReasonAccountDisabled,
		})
		return true
	case SSOReasonTenantSuspended:
		RefuseTenantLocked(w, SSOReasonTenantSuspended)
		return true
	}
	return false
}

// RefuseTenantLocked writes the answer to an account its tenant holds off - a
// suspended tenant (`tenant_suspended`), maintenance mode at sign-in
// (`maintenance`): 403 {"error":"sign-in is temporarily limited to the
// platform operator","maintenance":true,"reason":…}. The sign-in form
// (handlers.Auth.Login) and RefuseBlockedAccount write it, so a session, a key
// and a sign-in of a suspended tenant read the same thing.
func RefuseTenantLocked(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":       "sign-in is temporarily limited to the platform operator",
		"maintenance": true,
		"reason":      reason,
	})
}

// TokenAuthenticator is implemented by a Driver whose credential IS an API
// token (drivers/apitoken). The chain calls it instead of Authenticate so the
// matched token reaches the request context together with the user.
//
// ⚠ Why this exists: the api-token driver is always enabled, so a token
// authenticates on any route group built on Middleware — and the driver used
// to hand back the user and drop the token. Every scope check downstream then
// saw a request with no token at all and judged it by the account's role,
// which made a read-only token minted on an administrator's account a full
// administrator credential on the admin routes.
type TokenAuthenticator interface {
	AuthenticateToken(r *http.Request) (*model.User, *model.APIToken, error)
}

// authenticate asks one driver, preferring the token-reporting form.
func authenticate(d Driver, r *http.Request) (*model.User, *model.APIToken, error) {
	if ta, ok := d.(TokenAuthenticator); ok {
		return ta.AuthenticateToken(r)
	}
	u, err := d.Authenticate(r)
	return u, nil, err
}

// AnnotateUser attaches the caller's user to the context when a credential
// resolves to one, and NOTHING else. It never rejects.
//
// Middleware(false) is nearly this, but it still answers 403 for a disabled
// account — which is why /api/capabilities cannot use it: that route is
// fetched by the login screen and the public share pages, and a disabled
// user's browser still holds a cookie. Here a credential that does not work
// is simply an anonymous caller.
//
// The one thing the annotation buys is the ability to answer a PUBLIC
// endpoint differently for a stranger: /api/capabilities publishes the
// operator's external service hosts, and a caller with no credential has no
// business learning them (see handlers.Capabilities).
//
// ⚠ Cheap on the anonymous path: every driver returns ErrUnauthorized
// immediately when the request carries no credential of its kind — no
// database round trip, no LDAP bind.
func AnnotateUser() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, d := range Enabled() {
				u, err := d.Authenticate(r)
				if err != nil && !errors.Is(err, ErrUnauthorized) {
					slog.Warn("auth driver error",
						slog.String("driver", d.Name()),
						slog.String("err", err.Error()))
					continue
				}
				if u != nil && u.Enabled {
					r = r.WithContext(WithUser(r.Context(), u))
					break
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdmin denies with 403 a caller that may not administer: a non-admin
// account, or — when the request was authenticated by an API token — a token
// that does not grant the admin scope or is confined to a folder (see
// CallerMayAdminister). A signed-in administrator's session is judged by the
// account alone, as it always was.
//
// ⚠ It can only judge a token it can see, so the route group must attach the
// token to the context: MiddlewareWithToken does, and so does Middleware since
// the api-token driver reports its token to the chain (TokenAuthenticator).
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !CallerMayAdminister(r.Context()) {
			msg := "forbidden"
			if u := UserFrom(r.Context()); u != nil && u.IsAdmin() {
				msg = adminRefusal(TokenFrom(r.Context()))
			}
			writeAuthErr(w, http.StatusForbidden, msg)
			return
		}
		next.ServeHTTP(w, r)
	})
}
