package handlers

import (
	"context"
	"encoding/json"
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
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica reconcile offline"})
		return
	}
	queued, err := h.Service.ReconcileAll(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"queued": queued})
}

// FixOne enqueues a single retry.
//
//	POST /admin/api/replica/fix-one
//	body: {path: "...", op: "write|delete|move|copy"}
func (h *Replica) FixOne(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "replica reconcile offline"})
		return
	}
	var body struct {
		Path string `json:"path"`
		Op   string `json:"op"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" || body.Op == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path and op required"})
		return
	}
	if err := h.Service.FixOne(r.Context(), body.Path, body.Op); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GetReport returns the latest singleton status report. nil → 204.
//
//	GET /admin/api/replica/report
func (h *Replica) GetReport(w http.ResponseWriter, r *http.Request) {
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
