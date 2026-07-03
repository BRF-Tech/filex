// Package handlers — external_admin.go
//
// Admin CRUD for external services (OnlyOffice, Drawio, Mermaid, …).
//
//	GET    /api/admin/external
//	PATCH  /api/admin/external/{name}
//	POST   /api/admin/external/{name}/test
package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/db"
)

// ExternalAdmin handles /api/admin/external.
type ExternalAdmin struct {
	Store db.Store
	Caps  *capability.Service
}

// NewExternalAdmin constructs the handler.
func NewExternalAdmin(store db.Store, caps *capability.Service) *ExternalAdmin {
	return &ExternalAdmin{Store: store, Caps: caps}
}

// List returns every configured external service.
func (h *ExternalAdmin) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Store.ListExternalServices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Redact secrets before returning.
	for _, row := range rows {
		if row.SecretEnc != "" {
			row.SecretEnc = "***"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": rows})
}

type extPatchReq struct {
	Enabled     *bool   `json:"enabled,omitempty"`
	URL         *string `json:"url,omitempty"`
	Secret      *string `json:"secret,omitempty"`       // plaintext from UI; will be encrypted server-side
	OptionsJSON *string `json:"options_json,omitempty"` // raw JSON blob
}

// Update upserts a row and re-runs the health probe.
func (h *ExternalAdmin) Update(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	var req extPatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	cur, _ := h.Store.GetExternalService(r.Context(), name)
	enabled := true
	url := ""
	secret := ""
	options := "{}"
	if cur != nil {
		enabled = cur.Enabled
		url = cur.URL
		secret = cur.SecretEnc
		options = cur.OptionsJSON
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.URL != nil {
		url = *req.URL
	}
	if req.Secret != nil {
		secret = *req.Secret // TODO: encrypt with master key
	}
	if req.OptionsJSON != nil {
		options = *req.OptionsJSON
	}
	if err := h.Store.UpsertExternalService(r.Context(), name, enabled, url, secret, options, nowOrZero(), "unknown"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Invalidate the capability cache so the new URL is probed on next call.
	if h.Caps != nil {
		h.Caps.Invalidate()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Test runs an immediate health probe and returns the state.
func (h *ExternalAdmin) Test(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	if h.Caps == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "capability service unavailable"})
		return
	}
	state, err := h.Caps.ProbeExternal(r.Context(), name)
	if err != nil {
		// "no rows in result set" → unknown service, not a probe
		// failure. Surface that as 404 so callers (and Cypress) can
		// distinguish "service down" from "you misspelled the name".
		if strings.Contains(err.Error(), "no rows in result set") || strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "unknown external service: " + name,
				"name":  name,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        false,
			"reachable": false,
			"error":     err.Error(),
			"name":      name,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"name":      name,
		"reachable": state.State == "ok",
		"url":       state.URL,
		"state":     state.State,
	})
}

func nowOrZero() time.Time { return time.Now() }

// _ keeps db pkg import alive even if linter complains (we use db.Store).
var _ db.Store = nil
