package handlers

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"gitlab.com/brftech/filemanager/backend/internal/auth"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/onlyoffice"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
)

// OnlyOffice exposes the editor config + fetch + callback endpoints.
type OnlyOffice struct {
	Service         *onlyoffice.Service
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
}

// NewOnlyOffice constructs the handler. Pass nil svc to disable the routes
// (handlers will return 503 — easier than gating in routes.go).
func NewOnlyOffice(svc *onlyoffice.Service, store db.Store, resolver func(int64) (storage.Driver, error)) *OnlyOffice {
	return &OnlyOffice{Service: svc, Store: store, StorageResolver: resolver}
}

// Config returns the editor descriptor for an iframe to render.
//
// GET /api/files/onlyoffice/config?id=<node-id>&lang=tr
func (h *OnlyOffice) Config(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "onlyoffice not configured"})
		return
	}
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	node, err := h.Store.GetNode(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	cfg, err := h.Service.BuildConfigForNode(node, user, r.URL.Query().Get("lang"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// Fetch streams document bytes back to the OnlyOffice document server.
//
// Public: no session required, but the URL must be HMAC-signed via the
// onlyoffice service.
//
// GET /api/files/onlyoffice/fetch?n=<id>&exp=<unix>&sig=<b64url>
func (h *OnlyOffice) Fetch(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "onlyoffice not configured"})
		return
	}
	q := r.URL.Query()
	id, err := strconv.ParseInt(q.Get("n"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad n"})
		return
	}
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad exp"})
		return
	}
	if err := h.Service.VerifyFetchSignature(id, exp, q.Get("sig")); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	node, err := h.Store.GetNode(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	drv, err := h.StorageResolver(node.StorageID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver"})
		return
	}
	rc, err := drv.Read(r.Context(), node.Path)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rc.Close()
	mime := node.Mime
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	if node.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(node.Size, 10))
	}
	_, _ = io.Copy(w, rc)
}

// Callback receives save events from the OnlyOffice document server.
//
// POST /api/files/onlyoffice/callback?node=<id>
//
// Public — relies on the JWT in the body or Authorization header for
// integrity.
func (h *OnlyOffice) Callback(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": 1, "message": "onlyoffice not configured"})
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("node"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": 1, "message": "bad node"})
		return
	}
	resp, err := h.Service.HandleCallback(r, id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": 1, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
