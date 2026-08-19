// Package handlers — plugins.go
//
// Admin surface for storage plugins (internal/plugin, docs/PLUGINS.md):
//
//	GET    /api/admin/plugins              — every plugin with its live state
//	POST   /api/admin/plugins              — install: multipart {name, file} | JSON {name,url,sha256} | JSON {name,kind:"remote",address,token}
//	GET    /api/admin/plugins/{id}
//	PATCH  /api/admin/plugins/{id}         — {"enabled": bool}
//	POST   /api/admin/plugins/{id}/restart
//	DELETE /api/admin/plugins/{id}
//
// ⚠ Instance-wide, never tenant-scoped: a plugin is a PROCESS filex runs (or a
// service it trusts with storage credentials), so in multi-tenant mode only
// the supertenant may touch this surface. A tenant admin gets 403, not an
// empty list, so the boundary is visible.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// Plugins is the handler set. Manager may be nil when the subsystem is
// disabled by configuration; every route then answers 503 with the reason.
type Plugins struct {
	Manager     *plugin.Manager
	MultiTenant bool
}

// NewPlugins constructs the handler.
func NewPlugins(m *plugin.Manager, multiTenant bool) *Plugins {
	return &Plugins{Manager: m, MultiTenant: multiTenant}
}

func (h *Plugins) gate(w http.ResponseWriter, r *http.Request) bool {
	if h.Manager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":   "plugins_disabled",
			"message": "storage plugins are disabled on this instance (FILEX_PLUGINS_DISABLED)",
		})
		return false
	}
	if h.MultiTenant {
		if sc, ok := tenant.FromContext(r.Context()); !ok || sc == nil || !sc.IsSupertenant {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error":   "supertenant_only",
				"message": "storage plugins are managed by the platform operator",
			})
			return false
		}
	}
	return true
}

func (h *Plugins) List(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	list, err := h.Manager.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": list, "dir": h.Manager.Dir()})
}

func (h *Plugins) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	st, err := h.Manager.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type pluginInstallReq struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Address string `json:"address"`
	Token   string `json:"token"`
}

// Install accepts three shapes; the Content-Type decides which.
func (h *Plugins) Install(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		// The binary itself. 32 MiB in memory, the rest spills to disk; the
		// manager caps the total.
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad multipart: " + err.Error()})
			return
		}
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()
		name := strings.TrimSpace(r.FormValue("name"))
		f, hdr, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file is required"})
			return
		}
		defer f.Close()
		st, err := h.Manager.InstallBinary(r.Context(), name, hdr.Filename, f)
		if err != nil {
			writeJSON(w, installStatus(err), map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, st)
		return
	}
	var req pluginInstallReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	var (
		st  *plugin.Status
		err error
	)
	switch {
	case req.Kind == "remote" || (req.Address != "" && req.URL == ""):
		st, err = h.Manager.InstallRemote(r.Context(), req.Name, strings.TrimSpace(req.Address), req.Token)
	case req.URL != "":
		st, err = h.Manager.InstallFromURL(r.Context(), req.Name, strings.TrimSpace(req.URL), req.SHA256)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send a multipart file, or {url, sha256}, or {kind:\"remote\", address, token}"})
		return
	}
	if err != nil {
		writeJSON(w, installStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, st)
}

func installStatus(err error) int {
	if errors.Is(err, plugin.ErrBadName) {
		return http.StatusBadRequest
	}
	s := err.Error()
	if strings.Contains(s, "already exists") {
		return http.StatusConflict
	}
	if strings.Contains(s, "required") || strings.Contains(s, "must be") || strings.Contains(s, "sha256") ||
		strings.Contains(s, "FILEX_SECRET_KEY") || strings.Contains(s, "address") || strings.Contains(s, "download") ||
		strings.Contains(s, "empty file") || strings.Contains(s, "larger than") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func (h *Plugins) Patch(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be {\"enabled\": true|false}"})
		return
	}
	st, err := h.Manager.SetEnabled(r.Context(), id, *req.Enabled)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *Plugins) Restart(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	st, err := h.Manager.Restart(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *Plugins) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.Manager.Remove(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
