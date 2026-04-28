package handlers

import (
	"net/http"

	"gitlab.com/brftech/filemanager/backend/internal/capability"
)

// Capabilities exposes /api/capabilities.
type Capabilities struct {
	Service *capability.Service
}

// NewCapabilities constructs a Capabilities handler.
func NewCapabilities(svc *capability.Service) *Capabilities {
	return &Capabilities{Service: svc}
}

// Get returns the runtime feature snapshot.
func (h *Capabilities) Get(w http.ResponseWriter, r *http.Request) {
	c, err := h.Service.Get(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, c)
}
