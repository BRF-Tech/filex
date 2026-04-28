// Package handlers — users_admin.go
//
// Admin-only extra actions on users.
//
//   POST /api/admin/users/{id}/reset-password
package handlers

import (
	"crypto/rand"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	authlocal "gitlab.com/brftech/filemanager/backend/internal/auth/drivers/local"
	"gitlab.com/brftech/filemanager/backend/internal/db"
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
