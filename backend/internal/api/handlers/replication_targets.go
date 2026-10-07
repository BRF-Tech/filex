// Package handlers — replication_targets.go
//
// /api/admin/replication-targets — CRUD for the new backup-only
// entity. Replication targets are NOT regular storages: operators
// never write to them, they don't show up in the Depolar list, and
// the file explorer never lists them as a virtual root. Each entry
// is a backup sink that a primary storage can fan its writes out to
// via `storages.replica_target_id`.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// ReplicaLinks is what a storage or replication-target edit tells the running
// process (internal/server storage_cache.go): the drivers of the storages
// concerned are rebuilt around the new pairing, and their initial copies
// start, restart or stop. Without it a link saved on the Replication page
// changed a row and nothing else (#186).
type ReplicaLinks interface {
	// StorageLinkChanged: the storage was linked, relinked, unlinked,
	// created linked or deleted.
	StorageLinkChanged(ctx context.Context, storageID int64)
	// TargetChanged: the target was edited or deleted. storageIDs are the
	// storages linked to it (read before a delete clears the links);
	// restartCopies begins their initial copies again - the target's driver
	// or configuration changed, or it was switched back on.
	TargetChanged(ctx context.Context, targetID int64, storageIDs []int64, restartCopies bool)
	// FolderChanged: the storage's folder on its target was changed; its
	// driver is rebuilt and its initial copy begins again.
	FolderChanged(ctx context.Context, storageID int64)
}

// targetShown is a target as an admin surface may see it: every credential in its
// configuration masked (storage.MaskSecrets - the driver's descriptor says
// which fields are credentials).
//
// ⚠ Every read of a target went out with its S3 secret key, its SMB or SFTP
// password or its private key in clear: the Replication page, an admin API
// key on /api/ai/admin and the admin_replication_targets_* MCP tools all read
// these handlers. A save that sends the mask back keeps the stored value
// (Update, storage.KeepSecrets).
func targetShown(rt *model.ReplicationTarget) *model.ReplicationTarget {
	if rt == nil {
		return nil
	}
	cp := *rt
	cp.ConfigJSON = storage.MaskSecrets(rt.Driver, rt.ConfigJSON)
	return &cp
}

type ReplicationTargets struct {
	Store db.Store
	// Links applies an edit to the running process; nil: nothing replicates
	// (tests, embedders).
	Links ReplicaLinks
}

func NewReplicationTargets(store db.Store) *ReplicationTargets {
	return &ReplicationTargets{Store: store}
}

func (h *ReplicationTargets) List(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication targets are instance-wide backup sinks") {
		return
	}
	out, err := h.Store.ListReplicationTargets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	shownAll := make([]*model.ReplicationTarget, 0, len(out))
	for _, rt := range out {
		shownAll = append(shownAll, targetShown(rt))
	}
	writeJSON(w, http.StatusOK, shownAll)
}

func (h *ReplicationTargets) Get(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication targets are instance-wide backup sinks") {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	rt, err := h.Store.GetReplicationTarget(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, targetShown(rt))
}

func (h *ReplicationTargets) Create(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication targets are instance-wide backup sinks") {
		return
	}
	var rt model.ReplicationTarget
	if err := json.NewDecoder(r.Body).Decode(&rt); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if rt.Name == "" || rt.Driver == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and driver required"})
		return
	}
	if rt.Mode == "" {
		rt.Mode = "async"
	}
	if !rt.Enabled {
		// Default enabled=true unless explicitly turned off so the
		// fan-out engine picks the row up immediately. JSON
		// `enabled:false` still wins.
		rt.Enabled = true
	}
	// A mask sent with nothing behind it is not a password.
	rt.ConfigJSON, _ = storage.KeepSecrets(rt.Driver, rt.ConfigJSON, "", nil)
	created, err := h.Store.CreateReplicationTarget(r.Context(), &rt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, targetShown(created))
}

// Update changes a target. The body is merged onto the stored row - a field
// the body leaves out keeps its value, so `{"enabled": false}` switches a
// target off without wiping its name, driver and configuration (it used to
// replace the whole row with the body).
//
// The storages linked to it get their drivers rebuilt, so the change reaches
// the fan-out at once; a new driver or configuration, or switching the target
// back on, also begins their initial copies again: the new place is empty, or
// the changes made while it was off went nowhere.
func (h *ReplicationTargets) Update(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication targets are instance-wide backup sinks") {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	cur, err := h.Store.GetReplicationTarget(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	before := *cur
	before.ConfigJSON = append(json.RawMessage(nil), cur.ConfigJSON...)
	next := *cur
	next.ConfigJSON = nil
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if next.ConfigJSON == nil || string(next.ConfigJSON) == "null" {
		next.ConfigJSON = before.ConfigJSON
	}
	// The page and the API show credentials masked; sent back as they were
	// shown, they are the stored ones - while they still go where they were
	// saved (a new driver or address needs them typed again).
	kept, err := storage.KeepSecrets(next.Driver, next.ConfigJSON, before.Driver, before.ConfigJSON)
	if err != nil {
		refuseStorageConfig(w, r, err)
		return
	}
	next.ConfigJSON = kept
	next.ID = id
	if next.Name == "" || next.Driver == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and driver required"})
		return
	}
	if err := h.Store.UpdateReplicationTarget(r.Context(), &next); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.Links != nil {
		restart := before.Driver != next.Driver ||
			!sameJSON(before.ConfigJSON, next.ConfigJSON) ||
			(!before.Enabled && next.Enabled)
		h.Links.TargetChanged(r.Context(), id, h.linkedTo(r.Context(), id), restart)
	}
	fresh, _ := h.Store.GetReplicationTarget(r.Context(), id)
	writeJSON(w, http.StatusOK, targetShown(fresh))
}

// Delete removes a target; the store unlinks every storage that pointed at
// it, and those storages go back to their bare drivers at once (their writes
// stop fanning out to a sink that no longer exists).
func (h *ReplicationTargets) Delete(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication targets are instance-wide backup sinks") {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	linked := h.linkedTo(r.Context(), id)
	if err := h.Store.DeleteReplicationTarget(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.Links != nil {
		h.Links.TargetChanged(r.Context(), id, linked, false)
	}
	writeJSON(w, http.StatusNoContent, nil)
}

// linkedTo lists the storages linked to the target now.
func (h *ReplicationTargets) linkedTo(ctx context.Context, targetID int64) []int64 {
	list, err := h.Store.ListStorages(ctx)
	if err != nil {
		return nil
	}
	var out []int64
	for _, st := range list {
		if st.ReplicaTargetID != nil && *st.ReplicaTargetID == targetID {
			out = append(out, st.ID)
		}
	}
	return out
}
