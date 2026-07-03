package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
)

// validateStorageRootPath decodes ConfigJSON and rejects storages that
// would mount at the backend root (empty or "/" prefix/path). A non-root
// sub-folder is required so that filemanager never shadows pre-existing
// files at the bucket / FS root. See storage.ValidateNonRootPath.
func validateStorageRootPath(st *model.Storage) error {
	cfg := map[string]any{}
	if len(st.ConfigJSON) > 0 {
		if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
			return err
		}
	}
	return storage.ValidateNonRootPath(st.Driver, cfg)
}

// Storages handles /api/admin/storages.
type Storages struct {
	Store  db.Store
	Worker *syncpkg.Worker
}

// NewStorages constructs a Storages handler.
func NewStorages(store db.Store, worker *syncpkg.Worker) *Storages {
	return &Storages{Store: store, Worker: worker}
}

// List returns all configured storages. Each entry carries a `stats`
// blob with the file count + total byte sum so the admin Storages list
// page can render real "12 files, 4.2 MB" labels instead of static
// placeholders.
//
// Accepts `?role=primary` / `?role=replica` so the Depolar page can
// hide replica targets (operators never write to them directly) and
// the Replikasyon page can list replica candidates separately.
func (h *Storages) List(w http.ResponseWriter, r *http.Request) {
	roleFilter := r.URL.Query().Get("role")
	out, err := h.Store.ListStorages(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	type storageWithStats struct {
		*model.Storage
		Stats struct {
			FileCount int64 `json:"file_count"`
			TotalSize int64 `json:"total_size_bytes"`
		} `json:"stats"`
	}
	enriched := make([]storageWithStats, 0, len(out))
	for _, st := range out {
		role := st.Role
		if role == "" {
			role = "primary"
		}
		if roleFilter != "" && role != roleFilter {
			continue
		}
		row := storageWithStats{Storage: st}
		if c, sz, err := h.Store.StorageStats(r.Context(), st.ID); err == nil {
			row.Stats.FileCount = c
			row.Stats.TotalSize = sz
		}
		enriched = append(enriched, row)
	}
	writeJSON(w, http.StatusOK, enriched)
}

// Get returns a single storage by id. The admin Storages list page
// fetches /api/admin/storages/{id} when the user clicks a row to
// open the edit view; without this route chi returned 405.
func (h *Storages) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	st, err := h.Store.GetStorage(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	// Mirror List's stats blob so the detail page header can show the
	// same "N files, M bytes" label without a second roundtrip.
	type storageWithStats struct {
		*model.Storage
		Stats struct {
			FileCount int64 `json:"file_count"`
			TotalSize int64 `json:"total_size_bytes"`
		} `json:"stats"`
	}
	out := storageWithStats{Storage: st}
	if c, sz, err := h.Store.StorageStats(r.Context(), st.ID); err == nil {
		out.Stats.FileCount = c
		out.Stats.TotalSize = sz
	}
	writeJSON(w, http.StatusOK, out)
}

// Create adds a new storage.
func (h *Storages) Create(w http.ResponseWriter, r *http.Request) {
	var st model.Storage
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if st.Name == "" || st.Driver == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and driver required"})
		return
	}
	if err := validateStorageRootPath(&st); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if st.SyncMode == "" {
		st.SyncMode = model.SyncModePoll
	}
	if st.SyncIntervalS == 0 {
		st.SyncIntervalS = 900
	}
	created, err := h.Store.CreateStorage(r.Context(), &st)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.Worker != nil && created.Enabled {
		// Use a detached context — the initial sync run kicks off
		// asynchronously and outlives this request. r.Context() is
		// cancelled the moment the HTTP response is flushed, which
		// would otherwise abort the in-flight worker.
		_ = h.Worker.AddStorage(context.Background(), created)
	}
	writeJSON(w, http.StatusOK, created)
}

// Update modifies a storage row.
func (h *Storages) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	cur, err := h.Store.GetStorage(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err := json.NewDecoder(r.Body).Decode(cur); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	cur.ID = id
	if err := validateStorageRootPath(cur); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := h.Store.UpdateStorage(r.Context(), cur); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, cur)
}

// Delete removes a storage and its descendant nodes (cascade).
func (h *Storages) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	// Existence check before destructive work (DeleteStorage swallows
	// "no rows" for some drivers + RemoveStorage silently no-ops on
	// unknown ids). Without this, DELETE on a bogus id returns
	// {ok:true} which is misleading.
	if _, gerr := h.Store.GetStorage(r.Context(), id); gerr != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "storage not found"})
		return
	}
	if h.Worker != nil {
		h.Worker.RemoveStorage(id)
	}
	if err := h.Store.DeleteStorage(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// TriggerSync forces an immediate sync run for a storage.
func (h *Storages) TriggerSync(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if h.Worker == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "worker offline"})
		return
	}
	if err := h.Worker.Trigger(r.Context(), id); err != nil {
		// Worker.Trigger errors with "no syncer for storage" when the
		// id doesn't match any registered driver — that's a 404, not
		// an internal error.
		msg := err.Error()
		if strings.Contains(msg, "no syncer") || strings.Contains(msg, "not found") ||
			strings.Contains(msg, "no rows in result set") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "storage not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
