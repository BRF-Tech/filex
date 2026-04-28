package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"gitlab.com/brftech/filemanager/backend/internal/auth"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/share"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
)

// Share handles share creation and the public viewer endpoints.
type Share struct {
	Service         *share.Service
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
}

// NewShare constructs a Share handler.
func NewShare(svc *share.Service, store db.Store, resolver func(int64) (storage.Driver, error)) *Share {
	return &Share{Service: svc, Store: store, StorageResolver: resolver}
}

type shareCreateReq struct {
	NodeID       int64  `json:"node_id"`
	PIN          string `json:"pin,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"` // seconds from now
	MaxDownloads int    `json:"max_downloads,omitempty"`
}

// HandleCreate mints a new share token.
func (h *Share) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req shareCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	user := auth.UserFrom(r.Context())
	var userID *int64
	if user != nil {
		uid := user.ID
		userID = &uid
	}
	opts := share.CreateOpts{
		NodeID:    req.NodeID,
		PIN:       req.PIN,
		CreatedBy: userID,
	}
	if req.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresIn) * time.Second)
		opts.ExpiresAt = &t
	}
	if req.MaxDownloads > 0 {
		opts.MaxDownloads = &req.MaxDownloads
	}
	sh, err := h.Service.Create(r.Context(), opts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sh)
}

// HandleDelete revokes a share.
func (h *Share) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.Service.Delete(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// HandleMetadata returns metadata for a share token (no PIN check).
func (h *Share) HandleMetadata(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	sh, err := h.Store.GetShareByToken(r.Context(), tok)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"has_pin":        sh.PinHash != "",
		"expires_at":     sh.ExpiresAt,
		"download_count": sh.DownloadCount,
		"max_downloads":  sh.MaxDownloads,
	})
}

// HandleDownload streams the shared file (after PIN check).
func (h *Share) HandleDownload(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		pin = r.Header.Get("X-Filex-Pin")
	}
	sh, err := h.Service.Resolve(r.Context(), tok, pin)
	switch {
	case errors.Is(err, share.ErrExpired):
		writeJSON(w, http.StatusGone, map[string]string{"error": "expired"})
		return
	case errors.Is(err, share.ErrBadPIN):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad pin"})
		return
	case err != nil:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	node, err := h.Store.GetNode(r.Context(), sh.NodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "node missing"})
		return
	}
	drv, err := h.StorageResolver(node.StorageID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver"})
		return
	}
	rc, err := drv.Read(r.Context(), node.Path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read"})
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Disposition", `attachment; filename="`+node.Name+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	if node.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(node.Size, 10))
	}
	_, _ = io.Copy(w, rc)
	_ = h.Service.IncrementDownload(r.Context(), sh.ID)
}
