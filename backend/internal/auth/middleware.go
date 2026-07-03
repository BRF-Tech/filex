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
