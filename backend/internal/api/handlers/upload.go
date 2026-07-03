package handlers

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// Upload handles browser to S3 multipart uploads.
//
// Flow:
//
//  1. Init   — caller posts {storage_id, path, size}. Backend computes
//     part count, calls MultipartUploader.InitMultipart, persists a
//     chunked_uploads row and returns the per-part presigned URLs.
//  2. Browser uploads each part directly to S3 via the presigned URLs and
//     captures each part's ETag from the response header.
//  3. Finalize — caller posts {upload_id, parts:[{part_number, etag}]}.
//     Backend asks S3 to assemble the parts via CompleteMultipartUpload,
//     then upserts a node row and dispatches a thumbnail job.
//  4. Abort — caller cancels mid-flight; AbortMultipartUpload is called and
//     the chunked_uploads row deleted.
type Upload struct {
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
	Thumbs          *thumb.Pipeline
	ACL             *acl.Resolver
}

// AttachACL wires the RBAC resolver so chunked uploads require ≥editor on the
// destination at init time (finalize/abort continue an already-authorized id).
func (u *Upload) AttachACL(r *acl.Resolver) { u.ACL = r }

// NewUpload constructs an Upload handler.
//
// pipeline may be nil; in that case node insertion still happens but no
// thumbnail job is dispatched.
func NewUpload(store db.Store, resolver func(int64) (storage.Driver, error), pipeline *thumb.Pipeline) *Upload {
	return &Upload{Store: store, StorageResolver: resolver, Thumbs: pipeline}
}

// minPartSize / maxPartCount mirror the S3 multipart spec.
//
// AWS allows a minimum of 5 MiB per part except for the final, and at most
// 10 000 parts per upload — anything above that is rejected by S3 itself.
const (
	minPartSize  = 5 * 1024 * 1024
	maxPartCount = 10000
)

// initRequest is the body of POST /api/files/upload/init.
type initRequest struct {
	StorageID  int64  `json:"storage_id"`
	Path       string `json:"path"`
	Filename   string `json:"filename,omitempty"`
	Size       int64  `json:"size"`
	Mime       string `json:"mime,omitempty"`
	ChunkBytes int64  `json:"chunk_bytes,omitempty"`
}

// initResponse is returned to the browser.
type initResponse struct {
	UploadID  string    `json:"upload_id"`
	PartURLs  []string  `json:"part_urls"`
	PartSize  int64     `json:"part_size"`
	PartCount int       `json:"part_count"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Init kicks off a multipart upload — returns presigned PUT URLs for each part.
func (u *Upload) Init(w http.ResponseWriter, r *http.Request) {
	var req initRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	// Resolve the storage from the path's adapter prefix when the caller sent
	// no numeric storage_id (embedders / the SFC speak adapter names), and
	// strip the prefix so the target is storage-relative. Previously Init hard-
	// required storage_id and used the qualified path verbatim, so every SFC
	// init 400'd "missing fields" and always fell back to the legacy upload.
	adapter, rel := splitAdapterPath(req.Path)
	storageID := req.StorageID
	if storageID == 0 && adapter != "" {
		if st, err := u.Store.GetStorageByName(r.Context(), adapter); err == nil && st != nil {
			storageID = st.ID
		}
	}
	// Fold the filename into the path BEFORE validating: an upload to a storage
	// root arrives as path="adapter://" (rel empty) + a separate filename, which
	// is perfectly valid — the old `rel == ""` guard wrongly 400'd it as "missing
	// fields", so every root-dir (and confined-root embed) upload fell back to the
	// legacy path. The real requirement is a non-empty *target*.
	target := rel
	if req.Filename != "" {
		// Treat the path as parent dir when both are supplied.
		target = path.Join(rel, req.Filename)
	}
	target = strings.TrimLeft(path.Clean("/"+target), "/")
	if storageID == 0 || target == "" || req.Size <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing fields"})
		return
	}
	if strings.Contains(target, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path"})
		return
	}
	// RBAC: uploading writes a new file → require ≥editor on the target path.
	if !aclAllowID(r.Context(), u.ACL, u.Store, storageID, target, acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}

	chunk := req.ChunkBytes
	if chunk <= 0 {
		chunk = 8 * 1024 * 1024
	}
	if chunk < minPartSize {
		chunk = minPartSize
	}
	// Re-balance chunk so we never exceed 10 000 parts.
	partCount := int((req.Size + chunk - 1) / chunk)
	if partCount > maxPartCount {
		chunk = (req.Size + maxPartCount - 1) / maxPartCount
		if chunk < minPartSize {
			chunk = minPartSize
		}
		partCount = int((req.Size + chunk - 1) / chunk)
	}
	if partCount < 1 {
		partCount = 1
	}

	drv, err := u.StorageResolver(storageID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage: " + err.Error()})
		return
	}
	mp, ok := drv.(storage.MultipartUploader)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "storage does not support multipart upload"})
		return
	}

	uploadID, urls, err := mp.InitMultipart(r.Context(), target, req.Size, partCount)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "init multipart: " + err.Error()})
		return
	}

	id := uuid.NewString()
	expires := time.Now().Add(24 * time.Hour)
	cu := &model.ChunkedUpload{
		ID:         id,
		StorageID:  storageID,
		StorageKey: target,
		UploadID:   uploadID,
		TotalSize:  req.Size,
		Parts:      []model.UploadPart{},
		ExpiresAt:  expires,
	}
	if err := u.Store.CreateChunkedUpload(r.Context(), cu); err != nil {
		// Best effort: cancel the multipart upload so we don't leak parts.
		_ = mp.AbortMultipart(r.Context(), target, uploadID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "persist chunked upload: " + err.Error()})
		return
	}
	slog.Info("upload init",
		slog.String("upload_id", id),
		slog.Int64("storage", req.StorageID),
		slog.String("path", target),
		slog.Int64("size", req.Size),
		slog.Int("parts", partCount))

	writeJSON(w, http.StatusOK, initResponse{
		UploadID:  id,
		PartURLs:  urls,
		PartSize:  chunk,
		PartCount: partCount,
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
	if req.UploadID == "" || len(req.Parts) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing fields"})
		return
	}
	cu, err := u.Store.GetChunkedUpload(r.Context(), req.UploadID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "upload not found"})
		return
	}
	if time.Now().After(cu.ExpiresAt) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "upload expired"})
		return
	}
	drv, err := u.StorageResolver(cu.StorageID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage: " + err.Error()})
		return
	}
	mp, ok := drv.(storage.MultipartUploader)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "storage does not support multipart upload"})
		return
	}

	completions := make([]storage.PartCompletion, len(req.Parts))
	for i, p := range req.Parts {
		completions[i] = storage.PartCompletion{
			PartNumber: p.PartNumber,
			Etag:       strings.Trim(p.Etag, `"`),
		}
	}
	if err := mp.CompleteMultipart(r.Context(), cu.StorageKey, cu.UploadID, completions); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "complete multipart: " + err.Error()})
		return
	}

	// Best-effort metadata: prefer driver Stat (most accurate ETag/Mime/Size).
	var (
		mime  string
		etag  string
		size  = cu.TotalSize
		mtime = time.Now()
	)
	if obj, err := drv.Stat(r.Context(), cu.StorageKey); err == nil {
		size = obj.Size
		etag = obj.Etag
		mime = obj.Mime
		if !obj.Mtime.IsZero() {
			mtime = obj.Mtime
		}
	}

	node, err := u.upsertNode(r.Context(), cu.StorageID, cu.StorageKey, size, mime, etag, mtime)
	if err != nil {
		slog.Warn("upload: node upsert failed",
			slog.String("path", cu.StorageKey),
			slog.String("err", err.Error()))
	}

	_ = u.Store.DeleteChunkedUpload(r.Context(), cu.ID)

	if u.Thumbs != nil && node != nil {
		// Dispatch async — thumbnail generation is not part of the upload SLA.
		go func(n *model.Node) {
			defer func() {
				if rec := recover(); rec != nil {
					slog.Warn("upload: thumbnail panic", slog.Any("recover", rec))
				}
			}()
			tctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := u.Thumbs.GenerateThumb(tctx, n); err != nil && err != thumb.ErrSkipped {
				slog.Warn("upload: thumbnail dispatch",
					slog.Int64("node", n.ID),
					slog.String("err", err.Error()))
			}
		}(node)
	}

	resp := map[string]any{
		"ok":   true,
		"path": cu.StorageKey,
	}
	if node != nil {
		resp["node_id"] = node.ID
	}
	writeJSON(w, http.StatusOK, resp)
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
			if err := mp.AbortMultipart(r.Context(), cu.StorageKey, cu.UploadID); err != nil {
				slog.Warn("upload: abort multipart", slog.String("err", err.Error()))
			}
		}
	}
	_ = u.Store.DeleteChunkedUpload(r.Context(), cu.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// upsertNode either creates a new node row or updates an existing one for
// the (storage, path) pair. Returns the resulting node so callers can
// dispatch follow-up work (thumbnails, indexing).
func (u *Upload) upsertNode(ctx context.Context, storageID int64, p string, size int64, mime, etag string, mtime time.Time) (*model.Node, error) {
	clean := "/" + strings.TrimLeft(path.Clean("/"+p), "/")
	hash := uploadPathHash(storageID, clean)
	if existing, err := u.Store.GetNodeByPath(ctx, storageID, hash); err == nil && existing != nil {
		_ = u.Store.UpdateNodeMeta(ctx, existing.ID, size, mime, etag, mtime)
		existing.Size = size
		existing.Mime = mime
		existing.Etag = etag
		return existing, nil
	}
	n := &model.Node{
		StorageID:  storageID,
		Name:       path.Base(clean),
		Path:       clean,
		PathHash:   hash,
		StorageKey: clean,
		Type:       model.NodeTypeFile,
		Size:       size,
		Mime:       mime,
		Etag:       etag,
		SyncState:  model.SyncStateSynced,
	}
	if !mtime.IsZero() {
		n.BackendMtime = &mtime
	}
	created, err := u.Store.CreateNode(ctx, n)
	if err != nil {
		return nil, fmt.Errorf("create node: %w", err)
	}
	return created, nil
}

// uploadPathHash mirrors sync.pathHash but is duplicated here to avoid
// pulling the sync package into every handler.
func uploadPathHash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
}
