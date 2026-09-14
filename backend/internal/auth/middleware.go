package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Middleware tries each enabled auth Driver in order. The first one that
// returns a non-nil user wins; subsequent drivers are not consulted.
//
// On failure with required=true, a 401 JSON error is written and the
// downstream handler is NOT called. With required=false, the request
// proceeds with no user attached (useful for public viewer endpoints).
func Middleware(required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			drivers := Enabled()

			var user *model.User
			var token *model.APIToken
			for _, d := range drivers {
				u, tok, err := authenticate(d, r)
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

			// A disabled account (migration 00022) is refused even when a driver
			// happily resolved it. A session minted before the switch would
			// otherwise keep working until it expired, which makes "disabled"
			// mean "disabled tomorrow".
			if user != nil && !user.Enabled {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "this account is disabled",
				})
				return
			}

			if user == nil && required {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "unauthorized",
				})
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
				}
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
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
