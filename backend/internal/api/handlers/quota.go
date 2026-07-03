// Package handlers — quota.go
//
// Endpoints:
//
//	GET  /api/auth/me/quota                              (auth)
//	PATCH /api/admin/users/{id}/quota                    (admin)
//	POST /api/admin/users/{id}/quota/recompute           (admin)
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/quota"
)

// Quota wires quota HTTP routes.
type Quota struct {
	Service *quota.Service
}

// NewQuota constructs the handler.
func NewQuota(svc *quota.Service) *Quota { return &Quota{Service: svc} }

// Me returns the caller's quota snapshot.
func (h *Quota) Me(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	snap, err := h.Service.Get(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

type setQuotaReq struct {
	QuotaBytes int64 `json:"quota_bytes"`
}

// AdminSet writes a new quota_bytes for the target user.
func (h *Quota) AdminSet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var req setQuotaReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.QuotaBytes < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "quota_bytes must be >= 0"})
		return
	}
	if err := h.Service.SetQuota(r.Context(), id, req.QuotaBytes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	snap, _ := h.Service.Get(r.Context(), id)
	writeJSON(w, http.StatusOK, snap)
}

// AdminRecompute rescans nodes owned by id and rewrites usage_bytes.
func (h *Quota) AdminRecompute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	used, err := h.Service.Recompute(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"used_bytes": used,
	})
}
