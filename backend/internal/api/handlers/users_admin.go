// Package handlers — users_admin.go
//
// Admin-only extra actions on users.
//
//	POST /api/admin/users/{id}/reset-password
package handlers

import (
	"crypto/rand"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
)

// UsersAdmin holds admin-only user actions.
type UsersAdmin struct {
	Store db.Store
}

// NewUsersAdmin constructs the handler.
func NewUsersAdmin(store db.Store) *UsersAdmin {
	return &UsersAdmin{Store: store}
}

// ResetPassword generates a fresh random password for the user, persists the
// hash, returns the cleartext password ONCE in the response (admin must save).
func (h *UsersAdmin) ResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	// Existence check — without this the handler happily generates a
	// password + updates 0 rows + returns 200, leaking the cleartext
	// password into the caller's response for a user that does not
	// exist. (Found by Cypress 41-users-crud sweep, 2026-05-18.)
	if _, gerr := h.Store.GetUser(r.Context(), id); gerr != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	pw, err := generateRandomPassword(16)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	hash, err := authlocal.HashPassword(pw)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := h.Store.UpdateUserPassword(r.Context(), id, hash); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Revoke ALL sessions for this user — they have to re-login.
	_ = h.Store.DeleteSessionsForUser(r.Context(), id, "")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"new_password": pw,
		"warning":      "this password is shown ONCE — copy it now",
	})
}

func generateRandomPassword(n int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789!@#$%&*"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(out), nil
}
