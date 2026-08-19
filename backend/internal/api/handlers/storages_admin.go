// Package handlers — storages_admin.go
//
// Extra admin actions on storages beyond plain CRUD:
//
//	POST /api/admin/storages/test            — try a connection without saving
//	GET  /api/admin/storages/{id}/sync-runs  — recent runs for one storage
//	GET  /api/admin/storages/{id}/drift      — recent sync conflicts
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// StoragesAdmin holds extra admin actions on storages.
type StoragesAdmin struct {
	Store db.Store
	// Plugins is set when the plugin subsystem is on; Test then probes a
	// plugin driver's real capabilities instead of only listing its root.
	Plugins *plugin.Manager
}

// NewStoragesAdmin constructs the handler.
func NewStoragesAdmin(store db.Store) *StoragesAdmin {
	return &StoragesAdmin{Store: store}
}

type storageTestReq struct {
	Driver string                 `json:"driver"`
	Config map[string]interface{} `json:"config"`
}

// Test connects to the given driver+config without saving and lists the root.
func (h *StoragesAdmin) Test(w http.ResponseWriter, r *http.Request) {
	var req storageTestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	drv, err := storage.Get(req.Driver)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown driver"})
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if err := drv.Init(ctx, req.Config); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	objects, err := drv.List(ctx, "")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	preview := objects
	if len(preview) > 10 {
		preview = preview[:10]
	}
	resp := map[string]any{
		"ok":             true,
		"sample_listing": preview,
		"object_count":   len(objects),
	}

	// ⚠ For a PLUGIN driver, "it listed the root" is a weak test. The whole
	// point of a plugin is that somebody else wrote the write path, the
	// ranged read and the delete — so Test runs the same conformance probes
	// the save gate does, and reports which capability failed. A listing that
	// succeeds while write silently drops bytes is exactly the case this
	// button exists to catch.
	if h.Plugins != nil && strings.HasPrefix(req.Driver, plugin.DriverPrefix) {
		if caps, known := h.Plugins.CapabilitiesFor(req.Driver); known {
			rep := plugin.VerifyStorage(ctx, drv, caps)
			resp["conformance"] = rep
			if !rep.Verified {
				resp["ok"] = false
				resp["error"] = rep.FailureError().Error()
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// SyncRuns returns the recent sync_runs for a single storage.
func (h *StoragesAdmin) SyncRuns(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	runs, total, err := h.Store.ListSyncRunsAcrossAll(r.Context(), id, "", limit, 0)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": runs,
		"total":   total,
	})
}

// Drift returns recent unresolved sync conflicts for a storage.
func (h *StoragesAdmin) Drift(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	conflicts, err := h.Store.ListSyncConflictsByStorage(r.Context(), id, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": conflicts})
}
