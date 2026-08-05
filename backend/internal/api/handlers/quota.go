// Package handlers — quota.go
//
// Endpoints:
//
//	GET   /api/auth/me/quota                             (auth)
//	GET   /api/admin/users/{id}/quota                    (admin)
//	POST  /api/admin/users/{id}/quota                    (admin)
//	PATCH /api/admin/users/{id}/quota                    (admin)
//	POST  /api/admin/users/{id}/quota/recompute          (admin)
//
// The flat forms — /api/admin/quota/{user_id} and
// /api/admin/quota/{user_id}/recompute — are the original spelling and are
// still served. Note both take a USER id: there is no per-provider quota
// (see docs/MULTI-TENANCY.md, "Not yet").
package handlers

import (
	"encoding/json"
	"errors"
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

// quotaUserID reads the target user id from whichever route mounted the
// handler: /api/admin/quota/{user_id} or the nested, more discoverable
// /api/admin/users/{id}/quota.
func quotaUserID(r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "user_id")
	if raw == "" {
		raw = chi.URLParam(r, "id")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// quotaErrStatus keeps "no such user" out of the 500 bucket. Reading a quota
// for an id that isn't a user is a client mistake, not a server fault, and
// answering 500 `sql: no rows in result set` told olivov nothing about the
// real problem — they were passing provider ids to a user endpoint (H5).
func quotaErrStatus(err error) int {
	if errors.Is(err, quota.ErrUserNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// Me returns the caller's quota snapshot.
func (h *Quota) Me(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	snap, err := h.Service.Get(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, quotaErrStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

type setQuotaReq struct {
	QuotaBytes int64 `json:"quota_bytes"`
}

// AdminGet returns the target user's current quota + usage snapshot.
// (The admin routes bind {user_id}; reading "id" here was a long-standing
// mismatch that made these endpoints always 400 — fixed alongside.)
func (h *Quota) AdminGet(w http.ResponseWriter, r *http.Request) {
	id, ok := quotaUserID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	snap, err := h.Service.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, quotaErrStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// AdminSet writes a new quota_bytes for the target user.
func (h *Quota) AdminSet(w http.ResponseWriter, r *http.Request) {
	id, ok := quotaUserID(r)
	if !ok {
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
		writeJSON(w, quotaErrStatus(err), map[string]string{"error": err.Error()})
		return
	}
	snap, _ := h.Service.Get(r.Context(), id)
	writeJSON(w, http.StatusOK, snap)
}

// AdminRecompute rescans nodes owned by id and rewrites usage_bytes.
func (h *Quota) AdminRecompute(w http.ResponseWriter, r *http.Request) {
	id, ok := quotaUserID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	used, err := h.Service.Recompute(r.Context(), id)
	if err != nil {
		writeJSON(w, quotaErrStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"used_bytes": used,
	})
}
