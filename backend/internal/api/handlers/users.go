package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"gitlab.com/brftech/filemanager/backend/internal/auth/drivers/local"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/model"
)

// Users handles /api/admin/users.
type Users struct {
	Store db.Store
}

// NewUsers constructs a Users handler.
func NewUsers(store db.Store) *Users { return &Users{Store: store} }

// List returns all users.
func (h *Users) List(w http.ResponseWriter, r *http.Request) {
	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// Get returns a single user by id. The admin UI's UserEdit page
// hits this when the row is clicked; without it chi returned 405
// (only PATCH/DELETE were wired).
func (h *Users) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	u, err := h.Store.GetUser(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type userCreateReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

// Create makes a new user.
func (h *Users) Create(w http.ResponseWriter, r *http.Request) {
	var req userCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and password required"})
		return
	}
	if req.Role == "" {
		req.Role = model.RoleUser
	}
	if req.Locale == "" {
		req.Locale = "en"
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	hash, err := local.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	u, err := h.Store.CreateUser(r.Context(), req.Email, hash, req.Role, req.Locale, req.Timezone)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type userUpdateReq struct {
	Password *string `json:"password,omitempty"`
	Role     *string `json:"role,omitempty"`
	Locale   *string `json:"locale,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
}

// Update modifies a user. Only fields present in the body are touched.
func (h *Users) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var req userUpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Password != nil {
		hash, err := local.HashPassword(*req.Password)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		_ = h.Store.UpdateUserPassword(r.Context(), id, hash)
	}
	if req.Role != nil {
		_ = h.Store.UpdateUserRole(r.Context(), id, *req.Role)
	}
	if req.Locale != nil || req.Timezone != nil {
		// Fetch current to fill in the missing field.
		cur, err := h.Store.GetUser(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		l := cur.Locale
		tz := cur.Timezone
		if req.Locale != nil {
			l = *req.Locale
		}
		if req.Timezone != nil {
			tz = *req.Timezone
		}
		_ = h.Store.UpdateUserLocale(r.Context(), id, l, tz)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete removes a user. Cannot delete the last admin (V2 enforcement).
func (h *Users) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.Store.DeleteUser(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
