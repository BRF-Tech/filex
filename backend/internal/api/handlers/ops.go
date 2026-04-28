package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"gitlab.com/brftech/filemanager/backend/internal/ops"
)

// Ops handles async copy/move/delete tasks.
//
// State is persisted in the pending_ops table — restart-safe so a crash
// doesn't lose in-flight work. The actual execution happens in the worker
// goroutine launched in server.New (see ops.Service.Run).
type Ops struct {
	Service *ops.Service
}

// NewOps constructs an Ops handler.
func NewOps(svc *ops.Service) *Ops {
	return &Ops{Service: svc}
}

// opsRequest is the body of POST /api/files/ops.
type opsRequest struct {
	Kind      string   `json:"kind"`       // copy, move, delete
	StorageID int64    `json:"storage_id"`
	Sources   []string `json:"sources"`
	Dest      string   `json:"dest,omitempty"`
}

// Submit queues a new op and returns the opID.
func (o *Ops) Submit(w http.ResponseWriter, r *http.Request) {
	if o.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	var req opsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	op, err := o.Service.Submit(r.Context(), req.Kind, req.StorageID, req.Sources, req.Dest)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, op)
}

// Status returns the live or final state of a submitted op.
func (o *Ops) Status(w http.ResponseWriter, r *http.Request) {
	if o.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	op, err := o.Service.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown op"})
		return
	}
	writeJSON(w, http.StatusOK, op)
}
