package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
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
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Locale      string `json:"locale"`
	Timezone    string `json:"timezone"`
	// ProviderID homes the new user in a tenant. Optional; when absent the
	// caller's own tenant is used.
	ProviderID *int64 `json:"provider_id,omitempty"`
}

// resolveProvider decides which provider a created/updated user belongs to
// and whether the caller may put them there. It returns the provider id to
// write (0 = leave the store's own default alone), or an HTTP status + message.
//
// Store.CreateUser defaults provider_id to 1, and provider 1 (`default`) is
// the SUPERTENANT — so "unspecified" used to mean "confine-exempt, sees every
// tenant's storages". A tenant admin's new users therefore default to that
// admin's own tenant, and only a supertenant caller may name an arbitrary one
// (olivov G1, 2026-08-05).
func (h *Users) resolveProvider(ctx context.Context, requested *int64) (int64, int, string) {
	scope, scoped := tenant.FromContext(ctx)
	confined := scoped && !scope.IsSupertenant

	var target int64
	if confined {
		target = scope.ProviderID
	}
	if requested != nil {
		if confined && *requested != scope.ProviderID {
			return 0, http.StatusForbidden, "cannot assign a user to another tenant"
		}
		target = *requested
	}
	if target == 0 {
		return 0, 0, "" // single-tenant / unscoped and nothing asked for
	}
	// provider_id carries no foreign key, so an unchecked value would strand
	// the user in a tenant that does not exist.
	if p, err := h.Store.GetProvider(ctx, target); err != nil || p == nil {
		return 0, http.StatusBadRequest, "unknown provider_id"
	}
	return target, 0, ""
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
	if !model.ValidRole(req.Role) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid role: " + req.Role})
		return
	}
	if req.Locale == "" {
		req.Locale = "en"
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	// Resolve the tenant BEFORE creating anything, so a rejected provider_id
	// doesn't leave a half-provisioned user behind.
	providerID, status, msg := h.resolveProvider(r.Context(), req.ProviderID)
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
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
	if providerID != 0 {
		if err := h.Store.SetUserProvider(r.Context(), u.ID, providerID, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		u.ProviderID = &providerID
	}
	if name := strings.TrimSpace(req.DisplayName); name != "" {
		if err := h.Store.UpdateUserDisplayName(r.Context(), u.ID, name); err == nil {
			u.DisplayName = name
		}
	}
	writeJSON(w, http.StatusOK, u)
}

type userUpdateReq struct {
	Password    *string `json:"password,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Role        *string `json:"role,omitempty"`
	Locale      *string `json:"locale,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
	// ProviderID re-homes the user into another tenant. Supertenant only —
	// see Update.
	ProviderID *int64 `json:"provider_id,omitempty"`
	// Enabled gates whether the account may start a session.
	Enabled *bool `json:"enabled,omitempty"`
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
	// Reject an unknown role up-front, and refuse to demote the last admin
	// out of the admin role (which would otherwise lock everyone out of the
	// admin surface just as effectively as deleting them).
	if req.Role != nil {
		if !model.ValidRole(*req.Role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid role: " + *req.Role})
			return
		}
		if *req.Role != model.RoleAdmin {
			if last, err := h.isLastAdmin(r.Context(), id); err == nil && last {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot demote the last admin"})
				return
			}
		}
	}
	// Disabling the final admin locks everyone out of the admin surface just
	// as surely as deleting or demoting them, both of which are already
	// refused above.
	if req.Enabled != nil && !*req.Enabled {
		if last, err := h.isLastAdmin(r.Context(), id); err == nil && last {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot disable the last admin"})
			return
		}
	}
	// Re-homing a user between tenants is a platform-operator action, so it is
	// restricted to an unscoped or supertenant caller. Letting a tenant admin
	// do it would be an escalation in the other direction: Update has no
	// ownership check on its target, so they could pull another tenant's user
	// into their own tenant. It exists to repair accounts stranded in
	// provider 1 by the create-side bug (G1).
	if req.ProviderID != nil {
		scope, scoped := tenant.FromContext(r.Context())
		if scoped && !scope.IsSupertenant {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the supertenant may move a user between tenants"})
			return
		}
		if p, err := h.Store.GetProvider(r.Context(), *req.ProviderID); err != nil || p == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown provider_id"})
			return
		}
		if err := h.Store.SetUserProvider(r.Context(), id, *req.ProviderID, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if req.Password != nil {
		hash, err := local.HashPassword(*req.Password)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		_ = h.Store.UpdateUserPassword(r.Context(), id, hash)
	}
	if req.DisplayName != nil {
		_ = h.Store.UpdateUserDisplayName(r.Context(), id, strings.TrimSpace(*req.DisplayName))
	}
	if req.Role != nil {
		_ = h.Store.UpdateUserRole(r.Context(), id, *req.Role)
	}
	if req.Enabled != nil {
		if err := h.Store.SetUserEnabled(r.Context(), id, *req.Enabled); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
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

// Delete removes a user. The last remaining admin can never be deleted —
// not even by itself — so the instance can't be locked out of its own admin
// surface. A non-existent id is reported as 404 rather than a silent 200.
func (h *Users) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	target, err := h.Store.GetUser(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if target.IsAdmin() {
		if last, err := h.isLastAdmin(r.Context(), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		} else if last {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot delete the last admin"})
			return
		}
	}
	if err := h.Store.DeleteUser(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// isLastAdmin reports whether userID is an admin and the only admin left.
// Used to block deleting/demoting the final admin. The user table is tiny
// (operator accounts), so a full ListUsers scan is cheaper than threading a
// dedicated COUNT through every Store implementation.
func (h *Users) isLastAdmin(ctx context.Context, userID int64) (bool, error) {
	users, err := h.Store.ListUsers(ctx)
	if err != nil {
		return false, err
	}
	admins := 0
	targetIsAdmin := false
	for _, u := range users {
		if u.IsAdmin() {
			admins++
			if u.ID == userID {
				targetIsAdmin = true
			}
		}
	}
	return targetIsAdmin && admins <= 1, nil
}
