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
			for _, d := range drivers {
				u, err := d.Authenticate(r)
				if err != nil && !errors.Is(err, ErrUnauthorized) {
					slog.Warn("auth driver error",
						slog.String("driver", d.Name()),
						slog.String("err", err.Error()))
					continue
				}
				if u != nil {
					user = u
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
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
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

// RequireAdmin denies non-admin users with 403.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFrom(r.Context())
		if u == nil || !u.IsAdmin() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "forbidden",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}
