package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/model"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
)

// Upload handles browser → S3 multipart uploads.
type Upload struct {
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
}

// NewUpload constructs an Upload handler.
func NewUpload(store db.Store, resolver func(int64) (storage.Driver, error)) *Upload {
	return &Upload{Store: store, StorageResolver: resolver}
}

// initRequest is the body of POST /api/files/upload/init.
type initRequest struct {
	StorageID int64  `json:"storage_id"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	PartSize  int64  `json:"part_size"`
}

// initResponse is returned to the browser.
type initResponse struct {
	UploadID  string                  `json:"upload_id"`
	PartURLs  []string                `json:"part_urls"`
	PartSize  int64                   `json:"part_size"`
	ExpiresAt time.Time               `json:"expires_at"`
}

// Init kicks off a multipart upload — returns presigned PUT URLs for each part.
func (u *Upload) Init(w http.ResponseWriter, r *http.Request) {
	var req initRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.StorageID == 0 || req.Path == "" || req.Size <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing fields"})
		return
	}
	if req.PartSize <= 0 {
		req.PartSize = 8 * 1024 * 1024
	}
	drv, err := u.StorageResolver(req.StorageID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage"})
		return
	}
	mp, ok := drv.(storage.MultipartUploader)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "driver not multipart-capable"})
		return
	}
	partCount := int((req.Size + req.PartSize - 1) / req.PartSize)
	uploadID, urls, err := mp.InitMultipart(r.Context(), req.Path, req.Size, partCount)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	id := uuid.NewString()
	expires := time.Now().Add(24 * time.Hour)
	cu := &model.ChunkedUpload{
		ID:         id,
		StorageID:  req.StorageID,
		StorageKey: req.Path,
		UploadID:   uploadID,
		TotalSize:  req.Size,
		Parts:      []model.UploadPart{},
		ExpiresAt:  expires,
	}
	if err := u.Store.CreateChunkedUpload(r.Context(), cu); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, initResponse{
		UploadID:  id,
		PartURLs:  urls,
		PartSize:  req.PartSize,
		ExpiresAt: expires,
	})
}

// finalizeRequest is the body of POST /api/files/upload/finalize.
type finalizeRequest struct {
	UploadID string             `json:"upload_id"`
	Parts    []model.UploadPart `json:"parts"`
}

// Finalize tells S3 to assemble the parts.
func (u *Upload) Finalize(w http.ResponseWriter, r *http.Request) {
	var req finalizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	cu, err := u.Store.GetChunkedUpload(r.Context(), req.UploadID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "upload not found"})
		return
	}
	drv, err := u.StorageResolver(cu.StorageID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage"})
		return
	}
	mp, ok := drv.(storage.MultipartUploader)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "driver not multipart-capable"})
		return
	}
	completions := make([]storage.PartCompletion, len(req.Parts))
	for i, p := range req.Parts {
		completions[i] = storage.PartCompletion{PartNumber: p.PartNumber, Etag: p.Etag}
	}
	if err := mp.CompleteMultipart(r.Context(), cu.StorageKey, cu.UploadID, completions); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_ = u.Store.DeleteChunkedUpload(r.Context(), cu.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"path":  cu.StorageKey,
	})
}

// Abort cancels an in-progress upload.
func (u *Upload) Abort(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UploadID string `json:"upload_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	cu, err := u.Store.GetChunkedUpload(r.Context(), req.UploadID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if drv, err := u.StorageResolver(cu.StorageID); err == nil {
		if mp, ok := drv.(storage.MultipartUploader); ok {
			_ = mp.AbortMultipart(r.Context(), cu.StorageKey, cu.UploadID)
		}
	}
	_ = u.Store.DeleteChunkedUpload(r.Context(), cu.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
