package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// Providers handles /api/admin/providers — the tenant lifecycle API
// (docs/MULTI-TENANCY.md §8, §11): create/suspend/delete tenants, link
// storages, transfer the supertenant flag.
//
// Guards enforced here (not scattered):
//   - only a supertenant admin may manage providers in multi-tenant mode;
//   - at most ONE supertenant — setting the flag on another provider is a
//     TRANSFER (the old supertenant becomes a regular tenant atomically);
//   - the supertenant cannot be deleted, disabled, or directly un-flagged;
//   - deleting a tenant with users requires ?force=1 and then cascades its
//     users and storage LINKS. Storage rows and file data are never touched —
//     unlinking is reversible, deleting files is not.
type Providers struct {
	Store       db.Store
	MultiTenant bool
}

// NewProviders constructs the handler.
func NewProviders(store db.Store, multiTenant bool) *Providers {
	return &Providers{Store: store, MultiTenant: multiTenant}
}

// requireSupertenant gates management: in multi-tenant mode only the
// supertenant's admins pass (the route group already requires admin); in
// single-tenant mode every admin passes (no scope is set).
func (h *Providers) requireSupertenant(w http.ResponseWriter, r *http.Request) bool {
	if scope, ok := tenant.FromContext(r.Context()); ok && !scope.IsSupertenant {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the platform operator may manage tenants"})
		return false
	}
	return true
}

type providerReq struct {
	Slug             *string `json:"slug"`
	Name             *string `json:"name"`
	Host             *string `json:"host"`
	AuthType         *string `json:"auth_type"`
	OIDCIssuer       *string `json:"oidc_issuer"`
	OIDCClientID     *string `json:"oidc_client_id"`
	OIDCClientSecret *string `json:"oidc_client_secret"`
	OIDCRedirectURL  *string `json:"oidc_redirect_url"`
	RoleClaim        *string `json:"role_claim"`
	AdminGroup       *string `json:"admin_group"`
	IsSupertenant    *bool   `json:"is_supertenant"`
	Enabled          *bool   `json:"enabled"`
}

func (req *providerReq) apply(p *model.Provider) {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = strings.TrimSpace(*src)
		}
	}
	set(&p.Slug, req.Slug)
	set(&p.Name, req.Name)
	set(&p.Host, req.Host)
	set(&p.AuthType, req.AuthType)
	set(&p.OIDCIssuer, req.OIDCIssuer)
	set(&p.OIDCClientID, req.OIDCClientID)
	set(&p.OIDCRedirectURL, req.OIDCRedirectURL)
	set(&p.RoleClaim, req.RoleClaim)
	set(&p.AdminGroup, req.AdminGroup)
	if req.OIDCClientSecret != nil && *req.OIDCClientSecret != "" {
		// Empty secret in the payload means "keep the stored one" so the UI can
		// round-trip the (never-serialized) secret without re-entering it.
		p.OIDCClientSecret = *req.OIDCClientSecret
	}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
}

type providerOut struct {
	*model.Provider
	StorageIDs []int64 `json:"storage_ids"`
	UserCount  int     `json:"user_count"`
}

func (h *Providers) out(r *http.Request, p *model.Provider) providerOut {
	o := providerOut{Provider: p}
	o.StorageIDs, _ = h.Store.ListProviderStorageIDs(r.Context(), p.ID)
	if users, err := h.Store.ListUsersByProvider(r.Context(), p.ID); err == nil {
		o.UserCount = len(users)
	}
	return o
}

// List returns every provider (tenant) with its storage links + user count.
func (h *Providers) List(w http.ResponseWriter, r *http.Request) {
	if !h.requireSupertenant(w, r) {
		return
	}
	list, err := h.Store.ListProviders(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]providerOut, 0, len(list))
	for _, p := range list {
		out = append(out, h.out(r, p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out, "multi_tenant": h.MultiTenant})
}

// Create provisions a tenant.
func (h *Providers) Create(w http.ResponseWriter, r *http.Request) {
	if !h.requireSupertenant(w, r) {
		return
	}
	var req providerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	p := &model.Provider{AuthType: model.AuthTypeOIDC, Enabled: true}
	req.apply(p)
	if p.Slug == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "slug required"})
		return
	}
	created, err := h.Store.CreateProvider(r.Context(), p)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if req.IsSupertenant != nil && *req.IsSupertenant {
		if err := h.transferSupertenant(r, created); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	fresh, _ := h.Store.GetProvider(r.Context(), created.ID)
	if fresh == nil {
		fresh = created
	}
	writeJSON(w, http.StatusCreated, h.out(r, fresh))
}

// Update edits a tenant; flag changes go through the transfer guard.
func (h *Providers) Update(w http.ResponseWriter, r *http.Request) {
	if !h.requireSupertenant(w, r) {
		return
	}
	p := h.load(w, r)
	if p == nil {
		return
	}
	var req providerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if p.IsSupertenant {
		if req.IsSupertenant != nil && !*req.IsSupertenant {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot un-flag the supertenant — transfer it by setting is_supertenant on another provider"})
			return
		}
		if req.Enabled != nil && !*req.Enabled {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot disable the supertenant"})
			return
		}
	}
	req.apply(p)
	if err := h.Store.UpdateProvider(r.Context(), p); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if req.IsSupertenant != nil && *req.IsSupertenant && !p.IsSupertenant {
		if err := h.transferSupertenant(r, p); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	fresh, _ := h.Store.GetProvider(r.Context(), p.ID)
	if fresh == nil {
		fresh = p
	}
	writeJSON(w, http.StatusOK, h.out(r, fresh))
}

// transferSupertenant moves the platform flag to p, un-flagging the previous
// holder — the only way the flag moves, preserving the at-most-one invariant.
func (h *Providers) transferSupertenant(r *http.Request, p *model.Provider) error {
	prev, err := h.Store.GetSupertenant(r.Context())
	if err != nil {
		return err
	}
	if prev != nil && prev.ID != p.ID {
		prev.IsSupertenant = false
		if err := h.Store.UpdateProvider(r.Context(), prev); err != nil {
			return err
		}
	}
	p.IsSupertenant = true
	return h.Store.UpdateProvider(r.Context(), p)
}

// Delete removes a tenant. Users require ?force=1 (then cascade); storage
// LINKS are removed but storage rows + file data are never touched.
func (h *Providers) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.requireSupertenant(w, r) {
		return
	}
	p := h.load(w, r)
	if p == nil {
		return
	}
	if p.IsSupertenant {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot delete the supertenant"})
		return
	}
	users, err := h.Store.ListUsersByProvider(r.Context(), p.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(users) > 0 && r.URL.Query().Get("force") != "1" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "tenant still has users — pass ?force=1 to delete them too",
			"user_count": len(users),
		})
		return
	}
	for _, u := range users {
		if err := h.Store.DeleteUser(r.Context(), u.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	ids, _ := h.Store.ListProviderStorageIDs(r.Context(), p.ID)
	for _, sid := range ids {
		_ = h.Store.UnlinkProviderStorage(r.Context(), p.ID, sid)
	}
	if err := h.Store.DeleteProvider(r.Context(), p.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_users": len(users)})
}

// LinkStorage links a storage to the tenant (POST {storage_id}).
func (h *Providers) LinkStorage(w http.ResponseWriter, r *http.Request) {
	if !h.requireSupertenant(w, r) {
		return
	}
	p := h.load(w, r)
	if p == nil {
		return
	}
	var req struct {
		StorageID int64 `json:"storage_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.StorageID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "storage_id required"})
		return
	}
	if _, err := h.Store.GetStorage(r.Context(), req.StorageID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown storage"})
		return
	}
	if err := h.Store.LinkProviderStorage(r.Context(), p.ID, req.StorageID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, h.out(r, p))
}

// UnlinkStorage removes a storage link (never the storage itself).
func (h *Providers) UnlinkStorage(w http.ResponseWriter, r *http.Request) {
	if !h.requireSupertenant(w, r) {
		return
	}
	p := h.load(w, r)
	if p == nil {
		return
	}
	sid, err := strconv.ParseInt(chi.URLParam(r, "storageID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage id"})
		return
	}
	if err := h.Store.UnlinkProviderStorage(r.Context(), p.ID, sid); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, h.out(r, p))
}

// load parses {id} and fetches the provider (writing the error response).
func (h *Providers) load(w http.ResponseWriter, r *http.Request) *model.Provider {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return nil
	}
	p, err := h.Store.GetProvider(r.Context(), id)
	if err != nil || p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "provider not found"})
		return nil
	}
	return p
}
