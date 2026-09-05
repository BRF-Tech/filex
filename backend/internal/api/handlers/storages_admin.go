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
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// ProbeTimeout bounds one "Test connection" attempt.
//
// ⚠ This is NOT a driver setting, and lowering the drivers' own retry budgets
// would be the wrong fix. The S3 driver retries six times with a backoff capped
// at ten seconds (see s3.newRetryer) precisely so a *sync run* rides out an
// object store's transient 503 instead of dying on one listing page — measured
// against Hetzner Object Storage, twelve times in a month. That budget is right
// for a background worker and wrong for a person waiting on a button: with an
// endpoint that refuses the connection outright, the six attempts ran to
// completion and this endpoint answered in 23.6s and 29.7s on two consecutive
// measured calls. Cypress spec 43-storages-crud spends its whole duration here.
//
// So the retry budget stays and the PROBE gets a deadline. Every driver honours
// the context — the AWS SDK abandons its remaining attempts, and a hung SFTP or
// WebDAV dial is bounded by the same line — and a local-path probe, which
// answered in 6ms, never comes near it. Ten seconds is far more than a healthy
// remote store needs to list one prefix and short enough that the answer arrives
// while the operator is still looking at the form.
const ProbeTimeout = 10 * time.Second

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
	ctx, cancel := context.WithTimeout(r.Context(), ProbeTimeout)
	defer cancel()
	if err := drv.Init(ctx, req.Config); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": probeError(ctx, err),
		})
		return
	}
	objects, err := drv.List(ctx, "")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": probeError(ctx, err),
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

// probeError names a probe that ran out of time as such.
//
// Without it the caller is shown the driver's own last error, which for the S3
// SDK is a paragraph about attempt counts and a dial failure — technically the
// truth, and no help at all in deciding whether the endpoint is wrong or merely
// slow. The driver's message is kept either way; the deadline just gets said out
// loud in front of it.
func probeError(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("timed out after %s — the endpoint did not answer: %v", ProbeTimeout, err)
	}
	return err.Error()
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
