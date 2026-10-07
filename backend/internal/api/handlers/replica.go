package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/replica"
)

// Replica wires the admin endpoints over the replica subsystem.
type Replica struct {
	Store    db.Store
	Service  *replica.Service
	Cron     *replica.CronScheduler
	Reloader *replica.RulesReloader
	// Links applies a folder change to the running process (the storage's
	// driver rebuilt, its initial copy begun again). Nil: nothing replicates.
	Links ReplicaLinks
}

// NewReplica constructs a handler. Components may be nil when
// replica is disabled — endpoints return 503 in that case.
func NewReplica(store db.Store, svc *replica.Service, cron *replica.CronScheduler, reloader *replica.RulesReloader) *Replica {
	return &Replica{Store: store, Service: svc, Cron: cron, Reloader: reloader}
}

// ListRules paginates rules.
//
//	GET /admin/api/replica/rules
func (h *Replica) ListRules(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	rules, err := h.Store.ListReplicaRules(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rules})
}

// CreateRule inserts a new rule.
//
//	POST /admin/api/replica/rules
func (h *Replica) CreateRule(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	var in model.ReplicaRuleInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	rule, err := h.Store.CreateReplicaRule(r.Context(), &in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.bumpRules(r.Context())
	writeJSON(w, http.StatusOK, rule)
}

// UpdateRule replaces a rule by id.
//
//	PATCH /admin/api/replica/rules/{id}
func (h *Replica) UpdateRule(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var in model.ReplicaRuleInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	rule, err := h.Store.UpdateReplicaRule(r.Context(), id, &in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.bumpRules(r.Context())
	writeJSON(w, http.StatusOK, rule)
}

// DeleteRule removes a rule.
//
//	DELETE /admin/api/replica/rules/{id}
func (h *Replica) DeleteRule(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.Store.DeleteReplicaRule(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.bumpRules(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// ListFailures paginates the failures table.
//
//	GET /admin/api/replica/failures?unresolved=true&limit=50&offset=0
func (h *Replica) ListFailures(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	onlyUnresolved := r.URL.Query().Get("unresolved") == "true"
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	rows, total, err := h.Store.ListReplicaFailures(r.Context(), onlyUnresolved, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  rows,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// CountFailures returns the unresolved failure count.
//
//	GET /admin/api/replica/failures/count
func (h *Replica) CountFailures(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	n, err := h.Store.CountUnresolvedReplicaFailures(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

// FixAll enqueues retry ops for every unresolved failure.
//
//	POST /admin/api/replica/fix
func (h *Replica) FixAll(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica reconcile offline"})
		return
	}
	if !h.someStorageReplicates(w, r) {
		return
	}
	// `already_queued` counts failures whose retry was still waiting in the
	// queue: pressing again adds nothing for them (replica.RetryDedupKey).
	got, err := h.Service.ReconcileAll(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// someStorageReplicates answers 503 "no replica configured" when no storage is
// linked to an enabled replication target - the only case where there is
// nothing a repair could go through - and says whether to go on.
func (h *Replica) someStorageReplicates(w http.ResponseWriter, r *http.Request) bool {
	linked, err := h.Service.HasLinked(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return false
	}
	if !linked {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": replica.ErrNoReplica.Error()})
		return false
	}
	return true
}

// FixOne enqueues a single retry.
//
//	POST /admin/api/replica/fix-one
//	body: {storage_id: 7, path: "...", op: "write|delete|move|copy"}
//
// storage_id may be left out (a caller from before 0.53) when exactly one
// storage has an unresolved failure at (path, op).
func (h *Replica) FixOne(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica reconcile offline"})
		return
	}
	var body struct {
		StorageID int64  `json:"storage_id"`
		Path      string `json:"path"`
		Op        string `json:"op"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" || body.Op == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path and op required"})
		return
	}
	if body.StorageID == 0 {
		id, err := h.failureStorage(r.Context(), body.Path, body.Op)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		body.StorageID = id
	}
	if !h.someStorageReplicates(w, r) {
		return
	}
	queued, err := h.Service.FixOne(r.Context(), body.StorageID, body.Path, body.Op)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// `queued: false` — a retry of this failure was already waiting.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": queued})
}

// failureStorage finds the storage of the one unresolved failure at
// (path, op), for a fix-one request that did not name it.
func (h *Replica) failureStorage(ctx context.Context, path, op string) (int64, error) {
	var found []int64
	for offset := 0; offset < 100000; offset += 1000 {
		rows, _, err := h.Store.ListReplicaFailures(ctx, true, 1000, offset)
		if err != nil {
			return 0, err
		}
		for _, f := range rows {
			if f.Path == path && f.Op == op {
				found = append(found, f.StorageID)
			}
		}
		if len(rows) < 1000 {
			break
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return 0, errors.New("no unresolved failure at that path and op")
	default:
		return 0, errors.New("storage_id required: more than one storage has a failure at that path")
	}
}

// InitialCopies lists every replicating storage's initial copy - the copy of
// what it held when it was linked - with its progress.
//
//	GET /admin/api/replica/initial-copies → {items: [...]}
func (h *Replica) InitialCopies(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	items, err := h.Service.InitialCopies(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// RestartInitialCopy begins a storage's initial copy again: every file is
// looked at anew, the ones the target already holds are left alone.
//
//	POST /admin/api/replica/initial-copies/{storage_id}/restart
func (h *Replica) RestartInitialCopy(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "storage_id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage id"})
		return
	}
	link, err := h.Service.Linked(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var target int64
	for _, st := range link {
		if st.ID == id {
			target = *st.ReplicaTargetID
		}
	}
	if target == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this storage is not linked to an enabled replication target"})
		return
	}
	c, err := h.Service.StartInitialCopy(r.Context(), id, target, true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// ListLinks lists the folder each linked storage writes into on its target.
//
//	GET /admin/api/replica/links → {items: [{storage_id, storage_name, target_id, target_name, folder, created_unix}]}
func (h *Replica) ListLinks(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	items, err := h.Service.Links(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// SetLinkFolder changes the folder a storage writes into on its target. The
// name is made safe the way a new folder's is; the storage's initial copy
// begins again in the new folder, and nothing is moved or deleted in the old
// one.
//
//	PUT /admin/api/replica/links/{storage_id}  {folder: "..."} → the link
//	409 when another storage on the target uses that folder, when the storage
//	is not linked
func (h *Replica) SetLinkFolder(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "storage_id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage id"})
		return
	}
	var body struct {
		Folder string `json:"folder"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Folder == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "folder required"})
		return
	}
	link, err := h.Service.SetFolder(r.Context(), id, body.Folder)
	switch {
	case errors.Is(err, replica.ErrFolderTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, replica.ErrNotLinked):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this storage is not linked to a replication target"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.Links != nil {
		h.Links.FolderChanged(r.Context(), id)
	}
	writeJSON(w, http.StatusOK, link)
}

// GetReport returns the latest singleton status report. nil → 204.
//
//	GET /admin/api/replica/report
func (h *Replica) GetReport(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	rep, err := h.Store.GetReplicaStatusReport(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if rep == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// RunReportNow triggers GenerateReport synchronously.
//
//	POST /admin/api/replica/report/run-now
func (h *Replica) RunReportNow(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	if err := h.Service.GenerateReport(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GetSettings returns the singleton replica_settings row.
//
//	GET /admin/api/replica/settings
func (h *Replica) GetSettings(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	st, err := h.Store.GetReplicaSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// UpdateSettings rewrites the singleton replica_settings row and
// reloads the cron schedule + rules engine.
//
//	PATCH /admin/api/replica/settings
//	body: {report_cron, report_enabled, default_mode}
func (h *Replica) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "replication fans every tenant's writes at a shared sink") {
		return
	}
	if h.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica offline"})
		return
	}
	var st model.ReplicaSettings
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if err := h.Store.UpsertReplicaSettings(r.Context(), &st); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.Cron != nil {
		_ = h.Cron.Reload(r.Context())
	}
	h.bumpRules(r.Context())
	writeJSON(w, http.StatusOK, st)
}

func (h *Replica) bumpRules(ctx context.Context) {
	if h.Reloader != nil {
		_ = h.Reloader.Reload(ctx)
	}
}
