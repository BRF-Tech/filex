package handlers

// Staged uploads — the driver-agnostic, resumable write path.
//
//	POST   /api/files/upload/begin      {path,name,size,mime,hash?,chunk_size?}
//	                                    → {id, chunk_size, offset}
//	PUT    /api/files/upload/{id}       Content-Range: bytes A-B/total → {offset}
//	GET    /api/files/upload/{id}       → {offset, state, error?}
//	POST   /api/files/upload/{id}/commit→ {op_id, node_id}
//	DELETE /api/files/upload/{id}       → abort + delete staging
//
// filex takes the bytes into its own staging area first (internal/staging),
// acknowledges them, and transfers them to the storage driver afterwards
// through internal/ops. That is what makes it work on EVERY driver: the old
// chunked path (upload.go) needs storage.MultipartUploader, which only the S3
// driver implements, and on that path the browser PUTs its parts straight to S3
// so filex never sees the bytes at all.
//
// The legacy `?action=upload` multipart path (manager_mutate.go vfUpload) is
// untouched and stays the small-file fast path.
//
// ⚠ Nothing here calls r.ParseMultipartForm — the chunk body IS the request
// body. That keeps this surface out of the /tmp/multipart-* leak that filled
// 29 GB on fm.example.com in two hours (v0.13.4). Our own temp files get the
// equivalent discipline in internal/staging plus the sweeper below.

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/metrics"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/staging"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/throughput"
	"github.com/brf-tech/filex/backend/internal/writegate"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// minStagingChunk is the smallest part size a client may ask for. The real
// protection against a pathological manifest is staging.MaxParts; this floor
// just keeps the per-part bookkeeping from dwarfing the data.
const minStagingChunk = 4096

// maxStagingChunk caps a single PUT body, so one request cannot be asked to
// buffer an unbounded amount of work before the offset can advance.
const maxStagingChunk = 256 << 20

// StagedUpload is the HTTP surface of the staged upload protocol plus the
// transfer worker that ops calls back into.
type StagedUpload struct {
	Store   db.Store
	Manager *Manager
	Area    *staging.Area
	Ops     *ops.Service
	Quota   *quota.Service
	ACL     *acl.Resolver

	// ChunkSize is the default part size handed to clients that do not ask.
	ChunkSize int64
	// TTL is how long an idle staging directory survives the sweeper.
	TTL time.Duration
}

// NewStagedUpload constructs the handler. mgr carries the post-write hooks
// (search index, thumbnails, parent lookup, realtime) so the committed bytes go
// through exactly the same door as a plain upload rather than a parallel one.
func NewStagedUpload(store db.Store, mgr *Manager, area *staging.Area, opsSvc *ops.Service, q *quota.Service, chunkSize int64, ttl time.Duration) *StagedUpload {
	if chunkSize <= 0 {
		chunkSize = 8 << 20
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &StagedUpload{Store: store, Manager: mgr, Area: area, Ops: opsSvc, Quota: q, ChunkSize: chunkSize, TTL: ttl}
}

// AttachACL wires the RBAC resolver. Begin gates on the destination directory
// through the manager's resolveAdapterDir; every later call re-checks the
// stored key, because an upload id is not a capability.
func (h *StagedUpload) AttachACL(r *acl.Resolver) { h.ACL = r }

// ── begin ───────────────────────────────────────────────────────────────────

type beginRequest struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Filename  string `json:"filename,omitempty"` // alias, matches upload.go
	Size      int64  `json:"size"`
	Mime      string `json:"mime,omitempty"`
	Hash      string `json:"hash,omitempty"` // "sha256:<hex>" or "md5:<hex>"
	ChunkSize int64  `json:"chunk_size,omitempty"`
}

// Begin reserves a staging directory for a new upload.
func (h *StagedUpload) Begin(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	var req beginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Name == "" {
		req.Name = req.Filename
	}
	// resolveAdapterDir is the shared first half of every mutation: adapter
	// split, `..` rejection, tenant-scoped storage lookup and the ≥editor ACL
	// check on the destination directory. Reused rather than re-implemented on
	// purpose — a second copy of path sanitisation is a second place to get it
	// wrong.
	st, destRel, _, ok := h.Manager.resolveAdapterDir(w, r, req.Path)
	if !ok {
		return
	}
	if st.ReadOnly {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "storage is read-only"})
		return
	}
	name, nameOK := sanitizeUploadName(req.Name)
	if !nameOK {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad upload filename"})
		return
	}
	if req.Size < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad size"})
		return
	}
	if req.Hash != "" {
		if _, _, err := parseDeclaredHash(req.Hash); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	fullRel := path.Join(destRel, name)
	if gate(w, r, h.ACL, st.ID, writegate.Writes(fullRel)) {
		return
	}

	chunk, err := h.effectiveChunk(req.ChunkSize, req.Size)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	drv, err := h.Manager.StorageResolver(st.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	if _, isWriter := drv.(storage.Writer); !isWriter {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support write"})
		return
	}
	// Early, cheap conflict feedback: refuse now rather than after the user has
	// pushed 4 GB into a name that is already a folder. Re-checked at commit,
	// which is the authoritative moment.
	if err := storage.EnsureFileTarget(r.Context(), drv, fullRel); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": err.Error()})
		return
	}

	userID := currentUserID(r.Context())
	// Quota is RESERVED here, not at commit: a staged upload that never commits
	// would otherwise be invisible to the ceiling and a user could stage past
	// it. The reservation is derived from the open rows themselves, so it is
	// released by the row leaving the open set (commit, abort or sweep) and can
	// never drift from what it describes.
	if h.Quota != nil && userID > 0 && req.Size > 0 {
		pending, perr := h.Store.SumOpenStagedUploadBytes(r.Context(), userID)
		if perr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "quota: " + perr.Error()})
			return
		}
		if qerr := h.Quota.CheckCanWrite(r.Context(), userID, req.Size+pending); qerr != nil {
			if errors.Is(qerr, quota.ErrQuotaExceeded) {
				metrics.GuardRefusals.WithLabelValues(metrics.GuardQuota).Inc()
				slog.Info("staged upload refused: quota",
					slog.Int64("user", userID),
					slog.Int64("size", req.Size),
					slog.Int64("pending", pending))
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
					"error": "quota exceeded",
					"code":  "QUOTA_EXCEEDED",
				})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": qerr.Error()})
			return
		}
	}
	// Disk guard: the whole object passes through staging, so accepting an
	// upload the staging filesystem cannot hold just moves the failure to the
	// worst possible moment (and takes the rest of the instance with it).
	//
	// The message is deliberately specific — "need N bytes free for an M byte
	// upload, have K" — because the operator reading it has to decide whether
	// to free space or to move FILEX_UPLOAD_STAGING_DIR, and "insufficient
	// storage" answers neither question.
	if err := h.Area.EnsureFree(req.Size); err != nil {
		metrics.GuardRefusals.WithLabelValues(metrics.GuardDisk).Inc()
		slog.Warn("staged upload refused: staging disk",
			slog.String("path", fullRel),
			slog.Int64("size", req.Size),
			slog.String("err", err.Error()))
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{
			"error": err.Error(),
			"code":  "NO_DISK_SPACE",
		})
		return
	}

	id := uuid.NewString()
	if _, err := h.Area.Create(id, req.Size, chunk, req.Hash); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	row := &model.StagedUpload{
		ID:         id,
		StorageID:  st.ID,
		StorageKey: fullRel,
		UserID:     userID,
		TotalSize:  req.Size,
		ChunkSize:  chunk,
		Mime:       req.Mime,
		Hash:       req.Hash,
		State:      model.StagedUploadStaging,
		ExpiresAt:  time.Now().Add(h.TTL),
	}
	if err := h.Store.CreateStagedUpload(r.Context(), row); err != nil {
		_ = h.Area.Remove(id)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "persist staged upload: " + err.Error()})
		return
	}
	metrics.StagedBegun.Inc()
	metrics.StagedInFlight.Inc()
	slog.Info("staged upload begin",
		slog.String("id", id),
		slog.Int64("storage", st.ID),
		slog.String("path", fullRel),
		slog.Int64("size", req.Size),
		slog.Int64("chunk", chunk))

	writeJSON(w, http.StatusOK, map[string]any{
		"id":         id,
		"chunk_size": chunk,
		"chunkSize":  chunk, // camelCase alias — see docs/UPLOADS.md
		"offset":     int64(0),
		"total_size": req.Size,
		"state":      row.State,
		"expires_at": row.ExpiresAt,
	})
}

// ── put (one chunk) ─────────────────────────────────────────────────────────

// Put stores one chunk. The chunk's position comes from Content-Range, and the
// part number is derived from it against the grid `begin` handed out — so parts
// may arrive out of order and a retried chunk simply overwrites itself.
func (h *StagedUpload) Put(w http.ResponseWriter, r *http.Request) {
	row, ok := h.authorize(w, r)
	if !ok {
		return
	}
	if row.State != model.StagedUploadStaging {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "upload is " + row.State + "; no more chunks accepted",
		})
		return
	}
	m, err := h.Area.Manifest(row.ID)
	if err != nil {
		h.writeStagingErr(w, err)
		return
	}
	if m.TotalSize == 0 {
		// A zero-byte file has no parts; it is complete the moment it begins.
		// A client that sends the empty body anyway gets an honest "nothing to
		// do" rather than "there is no part 1".
		writeJSON(w, http.StatusOK, map[string]any{
			"id": row.ID, "offset": int64(0), "received": int64(0),
			"total_size": int64(0), "state": row.State,
		})
		return
	}

	start, length, err := parseChunkRange(r.Header.Get("Content-Range"), m)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if length > maxStagingChunk {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "chunk too large"})
		return
	}
	if start%m.ChunkSize != 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("chunk must start on a %d byte boundary, got %d", m.ChunkSize, start),
		})
		return
	}
	partNo := int(start/m.ChunkSize) + 1
	// A part we already hold means the client is re-sending — a resumed
	// upload, or a chunk whose response never arrived. Counted separately
	// from failures: a rising retry line with a flat failure line is a flaky
	// link, not a broken server, and the two get very different responses.
	_, resent := m.Part(partNo)

	m, err = h.Area.WritePart(row.ID, partNo, r.Body, length)
	if err != nil {
		h.writeStagingErr(w, err)
		return
	}
	if resent {
		metrics.StagedChunkRetries.Inc()
	} else {
		metrics.StagedBytes.Add(float64(length))
	}
	metrics.StagedBytesStaged.Add(float64(length))
	// Persist progress with a context the client cannot cancel: it has just
	// finished sending and may well hang up on the response, and losing the
	// offset write would make the next resume re-send a chunk we hold.
	bg := context.WithoutCancel(r.Context())
	if err := h.Store.UpdateStagedUploadProgress(bg, row.ID, m.Offset()); err != nil {
		slog.Warn("staged upload: progress write", slog.String("id", row.ID), slog.String("err", err.Error()))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":         row.ID,
		"offset":     m.Offset(),
		"received":   m.Received(),
		"total_size": m.TotalSize,
		"state":      row.State,
	})
}

// ── status ──────────────────────────────────────────────────────────────────

// Status is the resume oracle: a client that lost its state asks here and
// continues from `offset`.
func (h *StagedUpload) Status(w http.ResponseWriter, r *http.Request) {
	row, ok := h.authorize(w, r)
	if !ok {
		return
	}
	resp := map[string]any{
		"id":         row.ID,
		"offset":     row.ReceivedBytes,
		"received":   row.ReceivedBytes,
		"total_size": row.TotalSize,
		"chunk_size": row.ChunkSize,
		"chunkSize":  row.ChunkSize,
		"state":      row.State,
		"path":       row.StorageKey,
	}
	if row.Error != "" {
		resp["error"] = row.Error
	}
	if row.NodeID != nil {
		resp["node_id"] = *row.NodeID
		resp["nodeId"] = *row.NodeID
	}
	if row.OpID != nil {
		resp["op_id"] = *row.OpID
		resp["opId"] = *row.OpID
	}
	// The manifest is the authority for the offset — the DB column is a mirror
	// kept for cheap listing. Prefer it when it is readable.
	if m, err := h.Area.Manifest(row.ID); err == nil {
		resp["offset"] = m.Offset()
		resp["received"] = m.Received()
		resp["chunk_size"] = m.ChunkSize
		resp["chunkSize"] = m.ChunkSize
		parts := make([]map[string]any, 0, len(m.Parts))
		for _, p := range m.Parts {
			parts = append(parts, map[string]any{"n": p.N, "size": p.Size, "md5": p.MD5})
		}
		resp["parts"] = parts
		resp["complete"] = m.Complete()
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── commit ──────────────────────────────────────────────────────────────────

// Commit verifies the staged bytes, publishes the node immediately as `staged`
// and hands the actual transfer to a background op.
func (h *StagedUpload) Commit(w http.ResponseWriter, r *http.Request) {
	row, ok := h.authorize(w, r)
	if !ok {
		return
	}
	switch row.State {
	case model.StagedUploadStaging, model.StagedUploadFailed:
		// `failed` is committable on purpose: the staging directory is kept on
		// a failed transfer precisely so it can be retried without re-sending.
	default:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "upload is already " + row.State})
		return
	}
	m, err := h.Area.Manifest(row.ID)
	if err != nil {
		h.writeStagingErr(w, err)
		return
	}
	// Size is verified before anything is accepted: Complete() means every part
	// of the declared grid is present AND the sizes add up to the declared
	// total, so a short or over-long upload cannot reach the driver.
	if !m.Complete() {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "upload incomplete",
			"offset":     m.Offset(),
			"total_size": m.TotalSize,
		})
		return
	}
	if m.TotalSize != row.TotalSize {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "declared size changed"})
		return
	}
	if row.Hash != "" {
		if err := h.verifyHash(row); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
	}

	if h.Ops == nil {
		// Checked before anything is mutated: half-committing an upload that
		// can never be transferred would leave the row stuck in `committing`,
		// where neither a retry nor an abort can reach it.
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	st, err := h.Store.GetStorage(r.Context(), row.StorageID)
	if err != nil || st == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "storage not found"})
		return
	}
	if st.ReadOnly {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "storage is read-only"})
		return
	}
	drv, err := h.Manager.StorageResolver(row.StorageID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	if err := storage.EnsureFileTarget(r.Context(), drv, row.StorageKey); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": err.Error()})
		return
	}
	// The same overwrite precondition as the multipart path (upload_expect.go),
	// taken at COMMIT: that is the moment the file is replaced, and on a large
	// upload it can be minutes after the client last looked. A failed row stays
	// committable, so a refusal here costs no bytes.
	if !uploadExpectHolds(r.Context(), h.Store, row.StorageID, row.StorageKey, r.URL.Query().Get("expect")) {
		writePreconditionFailed(w)
		return
	}

	// The last moment at which the bytes we are about to replace still exist,
	// and the ONLY place on this path that gets to say so.
	//
	// ⚠⚠ It has to be HERE and not in transfer(), where the driver write
	// actually happens, for two independent reasons:
	//
	//  1. publishStagedNode below flips this node to transfer_state="staged",
	//     and from then on filebody.Resolver answers with the STAGED (incoming)
	//     bytes rather than the live (about-to-be-replaced) ones -- so a
	//     snapshot taken later records the wrong content, corrupting the very
	//     history it exists to keep.
	//  2. transfer() picks between two write mechanisms, and only one of them
	//     is storage.Writer.Write: on any driver implementing PartUploader
	//     (i.e. S3, which is what real deployments run) it calls
	//     streamMultipart instead, which never touches Writer.Write at all. A
	//     guard hung off the driver write would therefore protect small
	//     uploads and silently skip every large one on exactly the storage
	//     backend where it matters most.
	if err := writehook.BeforeOverwrite(r.Context(), row.StorageID, row.StorageKey); err != nil {
		slog.Warn("staged commit refused: snapshot",
			slog.String("id", row.ID),
			slog.Int64("storage", row.StorageID),
			slog.String("path", row.StorageKey),
			slog.String("err", err.Error()))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "could not preserve the existing file: " + err.Error(),
			"code":  "SNAPSHOT_FAILED",
		})
		return
	}

	mime := row.Mime
	if sniffed := h.sniffMime(row); sniffed != "" {
		// Same rule as vfUpload: the bytes decide, not the client's claim.
		mime = sniffed
	}

	node, err := h.publishStagedNode(r.Context(), row, mime, m)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "node: " + err.Error()})
		return
	}

	bg := context.WithoutCancel(r.Context())
	if err := h.Store.AttachStagedUploadTarget(bg, row.ID, node.ID, 0); err != nil {
		slog.Warn("staged upload: attach node", slog.String("id", row.ID), slog.String("err", err.Error()))
	}
	if err := h.Store.UpdateStagedUploadState(bg, row.ID, model.StagedUploadCommitting, ""); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "state: " + err.Error()})
		return
	}
	op, err := h.Ops.Submit(bg, ops.OpUploadCommit, row.StorageID, []string{row.ID}, "")
	if err != nil {
		_ = h.Store.UpdateStagedUploadState(bg, row.ID, model.StagedUploadFailed, err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "submit: " + err.Error()})
		return
	}
	if err := h.Store.AttachStagedUploadTarget(bg, row.ID, node.ID, op.ID); err != nil {
		slog.Warn("staged upload: attach op", slog.String("id", row.ID), slog.String("err", err.Error()))
	}
	// The row is listed now, before its bytes have moved — that is the point.
	emitFolderChange(row.StorageID, storageRelDir(node.Path), realtime.ChangeEvent{Action: "upload"})

	slog.Info("staged upload commit",
		slog.String("id", row.ID),
		slog.Int64("node", node.ID),
		slog.Int64("op", op.ID),
		slog.String("path", row.StorageKey),
		slog.Int64("size", row.TotalSize))

	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":             row.ID,
		"op_id":          op.ID,
		"opId":           op.ID,
		"node_id":        node.ID,
		"nodeId":         node.ID,
		"path":           row.StorageKey,
		"transfer_state": model.TransferStateStaged,
	})
}

// ── abort ───────────────────────────────────────────────────────────────────

// Abort drops the staging directory and the row.
func (h *StagedUpload) Abort(w http.ResponseWriter, r *http.Request) {
	row, ok := h.authorize(w, r)
	if !ok {
		return
	}
	if row.State == model.StagedUploadCommitting {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "transfer in progress; cannot abort",
		})
		return
	}
	if err := h.Area.Remove(row.ID); err != nil {
		slog.Warn("staged upload: remove staging", slog.String("id", row.ID), slog.String("err", err.Error()))
	}
	if err := h.Store.DeleteStagedUpload(r.Context(), row.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	metrics.StagedAborted.Inc()
	metrics.StagedInFlight.Dec()
	metrics.StagedBytes.Sub(float64(row.ReceivedBytes))
	slog.Info("staged upload abort", slog.String("id", row.ID), slog.String("path", row.StorageKey))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── the transfer (ops.UploadCommitter) ──────────────────────────────────────

// CommitUpload streams one staged upload into the driver and fires every
// post-write hook the synchronous path fires. Called by the ops worker with a
// server-lifetime context — never a request context.
//
// On success: transfer_state → "stored", staging deleted, row deleted.
// On failure: the row goes to "failed" with the message, the staging directory
// is KEPT (so a retry costs no bytes) and the node stays "staged".
func (h *StagedUpload) CommitUpload(ctx context.Context, uploadID string) error {
	row, err := h.Store.GetStagedUpload(ctx, uploadID)
	if err != nil || row == nil {
		// Already finished (or aborted) — nothing to do, and failing here would
		// mark a completed op as broken.
		slog.Info("staged upload: commit for unknown id", slog.String("id", uploadID))
		return nil
	}
	if err := h.transfer(ctx, row); err != nil {
		metrics.StagedFailed.Inc()
		_ = h.Store.UpdateStagedUploadState(ctx, row.ID, model.StagedUploadFailed, err.Error())
		slog.Warn("staged upload: transfer failed",
			slog.String("id", row.ID),
			slog.String("path", row.StorageKey),
			slog.String("err", err.Error()))
		// ⚠ Everything below is what makes the failure VISIBLE, and it is the
		// point of issue #16: the client was answered 202 the moment the last
		// chunk landed, so by now it has already drawn a finished upload. A
		// transfer that dies here used to leave exactly one WARN line in the
		// server log — the node stayed at "staged" (identical to still in
		// flight), no notification fired, and the user's only clue was that the
		// file would not download. Mark the node, then tell the user.
		if row.NodeID != nil {
			if serr := h.Store.SetNodeTransferState(ctx, *row.NodeID, model.TransferStateFailed); serr != nil {
				slog.Warn("staged upload: transfer_state failed-mark",
					slog.Int64("node", *row.NodeID), slog.String("err", serr.Error()))
			}
		}
		writehook.OnUploadFailed(context.WithoutCancel(ctx), row.StorageID, row.UserID,
			row.StorageKey, path.Base(row.StorageKey), writehook.OriginManager, err.Error(),
			map[string]any{"staged": true, "upload_id": row.ID})
		return err
	}
	metrics.StagedCommitted.Inc()
	metrics.StagedInFlight.Dec()
	return nil
}

// logStagedWriteFailure emits one named, grep-able line -- "upload failed",
// the same message and field shape vfUpload's write-error branch uses -- no
// matter which of transfer's two driver write mechanisms failed. Without it a
// failed staged transfer showed up server-side only as "staged upload:
// transfer failed" with no size and no basename, so "which file failed
// yesterday afternoon" had no answer from the logs alone.
func logStagedWriteFailure(row *model.StagedUpload, err error) {
	slog.Warn("upload failed",
		slog.Int64("storage", row.StorageID),
		slog.String("path", row.StorageKey),
		slog.String("name", path.Base(row.StorageKey)),
		slog.Int64("size", row.TotalSize),
		slog.String("reason", err.Error()))
}

func (h *StagedUpload) transfer(ctx context.Context, row *model.StagedUpload) error {
	drv, err := h.Manager.StorageResolver(row.StorageID)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	rd, err := h.Area.Open(row.ID)
	if err != nil {
		return err
	}
	defer rd.Close()

	if err := storage.EnsureFileTarget(ctx, drv, row.StorageKey); err != nil {
		return err
	}

	// Created vs replaced, asked of the DRIVER and asked HERE.
	//
	// ⚠ Every other surface answers this from its node-row lookup, and this
	// one cannot: Commit already ran publishStagedNode, so by the time the
	// bytes move there is a row at this path either way -- the DB has
	// forgotten whether it minted it. The storage itself has not. An object
	// still sitting at the target key one statement before we overwrite it is
	// exactly "a file was already there", and unlike a flag carried from
	// commit it survives a restart between commit and transfer.
	//
	// Inconclusive Stat (driver unwell, unsupported) degrades to Created --
	// the value the event carried before file.updated existed. A RETRY after a
	// transfer that failed part-way can find its own leftovers and report
	// Replaced; on the drivers that matters for (a plain Writer, not S3 -- a
	// multipart upload publishes no object until Complete) it is also not
	// wrong: there really are bytes at that key being overwritten.
	writeKind := writehook.Created
	if obj, serr := drv.Stat(ctx, row.StorageKey); serr == nil && obj.Kind != storage.KindDirectory {
		writeKind = writehook.Replaced
	}

	// ⚠ Deliberately NOT writehook.BeforeOverwrite here, even though this is
	// the function that actually writes the bytes. Both routes into this
	// transfer -- Commit (this file) and IngestStream (upload_staged_ingest.go)
	// -- already ran the guard BEFORE publishing this row's node, which is the
	// moment that matters: publishing flips the node to
	// transfer_state="staged", and from then on a snapshot taken here reads
	// the staged (incoming) bytes instead of the live ones via
	// internal/filebody. A second call here would not add protection; it would
	// record a version holding the wrong content.
	// TestStagedCommit_OverExistingFile_KeepsAVersion pins this.

	// Time the DRIVER WRITE only — not the staging open, the DB mirror, the
	// index or the hooks. This number is what tells an operator "the NAS is
	// slow", so anything that is not the backend has to stay out of it. The
	// same observation feeds internal/throughput, which is where
	// internal/filecache reads its per-storage rate from: one signal, two
	// consumers, so the cache and the dashboard can never disagree.
	started := time.Now()

	// An S3-style backend gets a real multipart upload — no 5 GB single-PUT
	// ceiling, and the parts are re-chunked to the DRIVER's minimum rather than
	// the client's chunk size.
	if pu, isPart := drv.(storage.PartUploader); isPart && row.TotalSize > 0 {
		if err := streamMultipart(ctx, pu, row.StorageKey, rd, row.TotalSize); err != nil {
			logStagedWriteFailure(row, err)
			return err
		}
	} else {
		wr, isWriter := drv.(storage.Writer)
		if !isWriter {
			return storage.ErrUnsupported
		}
		if err := wr.Write(ctx, row.StorageKey, rd, row.TotalSize); err != nil {
			logStagedWriteFailure(row, err)
			return err
		}
	}
	// One call: internal/metrics subscribes to this, so the histogram, the
	// bytes counter and the rolling rate all come from the same observation.
	throughput.Observe(row.StorageID, throughput.Write, row.TotalSize, time.Since(started))
	metrics.StagedBytes.Sub(float64(row.TotalSize))

	// ⚠⚠ From here on the bytes ARE on the storage, and two catalogue writes
	// stand between them and a readable file: the backend's metadata and
	// transfer_state "stored". Both used to be fire-and-forget — the metadata
	// error discarded, the flip's only logged — and staging and the session
	// were deleted right after, whatever had happened. One failed write (a
	// database busy under a large scan) left a row that said "staged" with
	// nothing staged: listed, every read 503 STAGING_GONE, every overwrite
	// refused, never scanned, and the op had told the client "ok".
	//
	// So: the bookkeeping runs detached from the op's context (a shutdown
	// landing between the driver write and the flip must not cut it in half),
	// each write is retried through a transient failure, and staging and the
	// session are released only once the row says "stored". If the flip still
	// cannot be written, the transfer FAILS: the session and its staging stay,
	// reads keep coming out of staging, and the commit can be retried without
	// re-sending a byte. Once the session is gone for good, the storage sync
	// and the boot pass (RecoverUnstored) settle the row from the evidence.
	bctx := context.WithoutCancel(ctx)

	// Post-write, in the same order and through the same helpers as vfUpload:
	// node meta → search index → thumbnail → writehook (antivirus + webhook +
	// notify) → realtime. A commit path that skips one of these is a silent
	// regression across five features.
	size := row.TotalSize
	etag := ""
	mtime := time.Now()
	statCtx, cancelStat := context.WithTimeout(bctx, 30*time.Second)
	if obj, serr := drv.Stat(statCtx, row.StorageKey); serr == nil {
		size = obj.Size
		etag = obj.Etag
		if !obj.Mtime.IsZero() {
			mtime = obj.Mtime
		}
	}
	cancelStat()
	var node *model.Node
	if row.NodeID != nil {
		nodeID := *row.NodeID
		// Keep the mime the commit step resolved. It came from sniffing the
		// staged bytes (what vfUpload does); row.Mime is only what the client
		// claimed, and writing that back here would quietly undo the sniff.
		mime := row.Mime
		if existing, _ := h.Store.GetNode(bctx, nodeID); existing != nil && existing.Mime != "" {
			mime = existing.Mime
		}
		if err := retryBookkeeping(bctx, func(c context.Context) error {
			return h.Store.UpdateNodeMeta(c, nodeID, size, mime, etag, mtime)
		}); err != nil {
			return fmt.Errorf("the bytes are on the storage, but the file's row could not be updated (the upload stays retryable): %w", err)
		}
		if err := retryBookkeeping(bctx, func(c context.Context) error {
			return h.Store.SetNodeTransferState(c, nodeID, model.TransferStateStored)
		}); err != nil {
			return fmt.Errorf("the bytes are on the storage, but the file's row could not be marked stored (the upload stays retryable): %w", err)
		}
		node, _ = h.Store.GetNode(bctx, nodeID)
	}
	if node != nil {
		h.Manager.indexNode(bctx, node)
		h.Manager.dispatchThumb(node)
		/* bag:b3 event + koru:k2 av — single post-write gate */
		writehook.OnFileWritten(bctx, row.StorageID, node, writehook.OriginManager, writeKind,
			map[string]any{"staged": true})
		emitFolderChange(row.StorageID, storageRelDir(node.Path), realtime.ChangeEvent{Action: "upload"})
	}

	// Only now, with the row saying "stored", may the session and the staging
	// go. The row first: a session row left behind with its staging gone is a
	// `committing` row the sweeper never touches (and a quota reservation that
	// never ends); a staging directory left behind is swept as an orphan.
	if err := retryBookkeeping(bctx, func(c context.Context) error {
		return h.Store.DeleteStagedUpload(c, row.ID)
	}); err != nil {
		slog.Warn("staged upload: row cleanup", slog.String("id", row.ID), slog.String("err", err.Error()))
		// ⚠ A row the delete could not remove must not stay `committing`:
		// the sweeper skips that state on purpose (its bytes are being read
		// right now) and SumOpenStagedUploadBytes counts it, so the user
		// would carry the reservation of a finished upload for ever. The
		// file is stored; it is the SESSION that failed to close, which is
		// what the message says.
		if err := retryBookkeeping(bctx, func(c context.Context) error {
			return h.Store.UpdateStagedUploadState(c, row.ID, model.StagedUploadFailed,
				"the file was stored; only this upload session could not be closed")
		}); err != nil {
			slog.Warn("staged upload: row demote", slog.String("id", row.ID), slog.String("err", err.Error()))
		}
	}
	if err := h.Area.Remove(row.ID); err != nil {
		slog.Warn("staged upload: staging cleanup", slog.String("id", row.ID), slog.String("err", err.Error()))
	}
	slog.Info("staged upload stored",
		slog.String("id", row.ID),
		slog.String("path", row.StorageKey),
		slog.Int64("size", size))
	return nil
}

// bookkeepingRetry is the pause before each retry of a catalogue write that
// follows a successful driver write.
var bookkeepingRetry = []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}

// retryBookkeeping runs one catalogue write, retrying it through a transient
// failure such as a busy database. The bytes are already on the storage when
// this runs, so giving up on the first error is what turned a hiccup into a
// file nobody could read.
func retryBookkeeping(ctx context.Context, write func(context.Context) error) error {
	err := write(ctx)
	for _, pause := range bookkeepingRetry {
		if err == nil {
			return nil
		}
		time.Sleep(pause)
		err = write(ctx)
	}
	return err
}

// streamMultipart pushes an assembled staging reader into a driver multipart
// upload, RE-CHUNKING to the driver's grid.
//
// ⚠ This is the boundary rule: staging part sizes belong to the client, backend
// part sizes belong to the driver. A client that sent 1 MiB chunks must not
// break an S3 backend, which rejects any non-final part below 5 MiB — and it
// rejects it at CompleteMultipartUpload, i.e. after every byte has been sent.
//
// ⚠⚠ The second boundary rule is that a part body has to be REWINDABLE.
// AWS SigV4 signs the SHA256 of the payload, so the S3 SDK reads the part
// once to hash it and then rewinds to send it; when the body is a plain
// io.Reader it cannot rewind and fails the request before a byte leaves the
// process, with "failed to compute payload hash: failed to seek body to
// start, request stream is not seekable" (issue #16 — every staged upload
// above the client's 8 MiB chunk threshold died this way on every S3
// backend, while smaller uploads took the single-shot path and worked).
// io.LimitReader would drop the Seek method the staging reader already has,
// so parts are cut with sectionOf, which keeps it.
func streamMultipart(ctx context.Context, pu storage.PartUploader, key string, rd io.Reader, total int64) error {
	partSize := int64(storage.MinBackendPartSize)
	// Keep the part count inside the protocol's ceiling for very large objects.
	if n := (total + partSize - 1) / partSize; n > staging.MaxParts {
		partSize = (total + staging.MaxParts - 1) / staging.MaxParts
	}
	count := int((total + partSize - 1) / partSize)
	if count < 1 {
		count = 1
	}
	uploadID, _, err := pu.InitMultipart(ctx, key, total, count)
	if err != nil {
		return fmt.Errorf("init multipart: %w", err)
	}
	completions := make([]storage.PartCompletion, 0, count)
	remaining := total
	var offset int64
	for i := 1; i <= count; i++ {
		n := partSize
		if remaining < n {
			n = remaining
		}
		etag, perr := pu.UploadPart(ctx, key, uploadID, i, sectionOf(rd, offset, n), n)
		if perr != nil {
			_ = pu.AbortMultipart(ctx, key, uploadID)
			return fmt.Errorf("upload part %d: %w", i, perr)
		}
		completions = append(completions, storage.PartCompletion{PartNumber: i, Etag: etag})
		remaining -= n
		offset += n
	}
	if err := pu.CompleteMultipart(ctx, key, uploadID, completions); err != nil {
		_ = pu.AbortMultipart(ctx, key, uploadID)
		return fmt.Errorf("complete multipart: %w", err)
	}
	return nil
}

// sectionOf cuts [offset, offset+size) out of rd for one multipart part.
//
// When rd can seek — which the staging reader can, and which is the only case
// that reaches production — the result can seek too, so a driver is free to
// read the part twice (hash, then send). When it cannot, this degrades to
// io.LimitReader: sequential, single-pass, exactly the old behaviour, so a
// driver that never rewinds keeps working with any reader.
func sectionOf(rd io.Reader, offset, size int64) io.Reader {
	rs, ok := rd.(io.ReadSeeker)
	if !ok {
		return io.LimitReader(rd, size)
	}
	return &partSection{rs: rs, start: offset, size: size}
}

// partSection is a rewindable, length-bounded window over a seekable source.
//
// io.SectionReader would be the stdlib answer, but it needs io.ReaderAt, and
// the staging reader is a cursor over a chain of part files rather than a
// single addressable file. Seeking the shared cursor is enough here because
// parts are uploaded one at a time: every Read and Seek positions the source
// explicitly, so no caller can be surprised by where the cursor was left.
type partSection struct {
	rs    io.ReadSeeker
	start int64
	size  int64
	pos   int64
}

func (p *partSection) Read(b []byte) (int, error) {
	if p.pos >= p.size {
		return 0, io.EOF
	}
	if int64(len(b)) > p.size-p.pos {
		b = b[:p.size-p.pos]
	}
	if _, err := p.rs.Seek(p.start+p.pos, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := p.rs.Read(b)
	p.pos += int64(n)
	// A short source is a truncated staging area, not an empty part: report it
	// rather than completing the upload with fewer bytes than promised.
	if err == io.EOF && p.pos < p.size {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

func (p *partSection) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = p.pos + offset
	case io.SeekEnd:
		abs = p.size + offset
	default:
		return 0, fmt.Errorf("partSection: invalid whence %d", whence)
	}
	if abs < 0 {
		return 0, fmt.Errorf("partSection: negative position %d", abs)
	}
	p.pos = abs
	if _, err := p.rs.Seek(p.start+abs, io.SeekStart); err != nil {
		return 0, err
	}
	return abs, nil
}

// publishStagedNode creates or updates the destination node with
// transfer_state="staged" so the file is listed the moment it is committed,
// while the bytes are still in staging.
//
// ⚠ The ETag comes from the STAGED PARTS (md5(concat(part md5s))-N, computed
// from the manifest without re-reading a byte), not from the driver and not
// from whatever the row carried before. On an overwrite, keeping the previous
// ETag would let a client that has the old file revalidate, get a 304 and keep
// the version it was just told had been replaced. The transfer overwrites it
// with the backend's own ETag when the bytes land.
func (h *StagedUpload) publishStagedNode(ctx context.Context, row *model.StagedUpload, mime string, m *staging.Manifest) (*model.Node, error) {
	clean := normalizeDBPath(row.StorageKey)
	hash := pathkey.Hash(row.StorageID, clean)
	etag := ""
	if m != nil {
		etag = m.CompositeETag()
	}
	if existing, _ := h.Store.GetNodeByPath(ctx, row.StorageID, hash); existing != nil {
		if err := h.Store.UpdateNodeMeta(ctx, existing.ID, row.TotalSize, mime, etag, time.Now()); err != nil {
			return nil, err
		}
		if err := h.Store.SetNodeTransferState(ctx, existing.ID, model.TransferStateStaged); err != nil {
			return nil, err
		}
		fresh, err := h.Store.GetNode(ctx, existing.ID)
		if err != nil {
			return existing, nil
		}
		h.Manager.indexNode(ctx, fresh)
		return fresh, nil
	}
	parentID, _ := h.Manager.lookupDirID(ctx, row.StorageID, path.Dir(clean))
	n := &model.Node{
		StorageID:     row.StorageID,
		ParentID:      parentID,
		Name:          path.Base(clean),
		Path:          clean,
		PathHash:      hash,
		StorageKey:    clean,
		Type:          model.NodeTypeFile,
		Size:          row.TotalSize,
		Mime:          mime,
		Etag:          etag,
		SyncState:     model.SyncStateSynced,
		TransferState: model.TransferStateStaged,
	}
	created, err := h.Store.CreateNode(ctx, n)
	if err != nil {
		return nil, err
	}
	h.Manager.indexNode(ctx, created)
	return created, nil
}

// ── sweeper ─────────────────────────────────────────────────────────────────

// Sweep removes staging that has seen no activity for longer than TTL, and the
// rows that describe it. Returns (rows, orphan directories) removed.
//
// `committing` rows are never swept: their bytes are being read right now.
// `failed` rows are, once they too have been idle for a full TTL — otherwise a
// permanently failing upload keeps its bytes forever.
func (h *StagedUpload) Sweep(ctx context.Context) (int, int) {
	if h == nil || h.Area == nil || !h.Area.Enabled() {
		return 0, 0
	}
	now := time.Now()
	cutoff := now.Add(-h.TTL)
	rows, err := h.Store.ListIdleStagedUploads(ctx, cutoff, 500)
	if err != nil {
		slog.Warn("staged upload sweep: list", slog.String("err", err.Error()))
		return 0, 0
	}
	live := map[string]bool{}
	removed := 0
	for _, row := range rows {
		if row.State == model.StagedUploadCommitting {
			live[row.ID] = true
			continue
		}
		if err := h.Area.Remove(row.ID); err != nil {
			slog.Warn("staged upload sweep: remove", slog.String("id", row.ID), slog.String("err", err.Error()))
			continue
		}
		if err := h.Store.DeleteStagedUpload(ctx, row.ID); err != nil {
			slog.Warn("staged upload sweep: delete row", slog.String("id", row.ID), slog.String("err", err.Error()))
			continue
		}
		removed++
		slog.Info("staged upload swept",
			slog.String("id", row.ID),
			slog.String("path", row.StorageKey),
			slog.Int64("staged_bytes", row.ReceivedBytes),
			slog.String("state", row.State))
	}
	// Directories with no row at all: debris from a crash between mkdir and the
	// INSERT. Anything the DB still knows about is protected.
	orphans, err := h.Area.Sweep(h.TTL, now, func(id string) bool {
		if live[id] {
			return true
		}
		row, err := h.Store.GetStagedUpload(ctx, id)
		return err == nil && row != nil
	})
	if err != nil {
		slog.Warn("staged upload sweep: orphans", slog.String("err", err.Error()))
	}
	for _, id := range orphans {
		slog.Info("staged upload swept orphan directory", slog.String("id", id))
	}

	// Reconcile the gauges against what is actually on disk. The counters are
	// moved by events (begin / chunk / commit / abort) so they are fresh
	// between sweeps; this is what makes them TRUE again after a restart, or
	// after a crash that left parts behind with no event to account for them.
	uploads, bytes := h.Area.Usage()
	metrics.StagedInFlight.Set(float64(uploads))
	metrics.StagedBytes.Set(float64(bytes))
	metrics.SweepRuns.Inc()
	metrics.Swept.WithLabelValues("row").Add(float64(removed))
	metrics.Swept.WithLabelValues("orphan").Add(float64(len(orphans)))

	// One line per pass, always — including the quiet ones. A sweeper that
	// only speaks when it deletes something is indistinguishable from a
	// sweeper that has stopped running, and 29 GB of temp files went
	// unnoticed here once already (v0.13.4).
	slog.Info("staged upload sweep",
		slog.Int("rows_removed", removed),
		slog.Int("orphans_removed", len(orphans)),
		slog.Int("remaining_uploads", uploads),
		slog.Int64("remaining_bytes", bytes),
		slog.String("ttl", h.TTL.String()))
	return removed, len(orphans)
}

// RecoverUnstored settles the rows a staged upload left "staged" or "failed"
// although their bytes are on the storage, and reports how many it settled.
//
// transfer() now makes the "stored" flip a condition of releasing staging, but
// a process that died between the driver write and the flip, or ran a version
// that released staging regardless, can leave such a row with no session
// behind it: listed, and unreadable. Nothing else settles it on a storage
// nobody scans (sync_mode `ondemand`), so this runs once at boot, from
// RunSweeper, after the first sweep.
//
// A row is settled only on the evidence the storage sync uses: no session
// references it (a session owns its bytes — in flight, or retryable), and the
// object at its key has the committed size and is not older than the commit
// (model.TransferLanded). Anything short of that is left exactly as it is and
// logged: an error a person can read beats a wrong file served as the right
// one.
func (h *StagedUpload) RecoverUnstored(ctx context.Context) int {
	if h == nil || h.Store == nil || h.Manager == nil || h.Manager.StorageResolver == nil {
		return 0
	}
	const page = 200
	settled, left := 0, 0
	var after int64
	for {
		rows, err := h.Store.ListUnstoredNodes(ctx, after, page)
		if err != nil {
			slog.Warn("staged upload recovery: list", slog.String("err", err.Error()))
			break
		}
		for _, n := range rows {
			after = n.ID
			if h.settleUnstored(ctx, n) {
				settled++
			} else {
				left++
			}
		}
		if len(rows) < page {
			break
		}
	}
	if settled > 0 || left > 0 {
		slog.Info("staged upload recovery",
			slog.Int("settled", settled),
			slog.Int("left_unstored", left))
	}
	return settled
}

// settleUnstored is RecoverUnstored for one row.
func (h *StagedUpload) settleUnstored(ctx context.Context, n *model.Node) bool {
	if n.Type != model.NodeTypeFile {
		return false
	}
	sess, err := h.Store.GetStagedUploadByNode(ctx, n.ID)
	switch {
	case err == nil && sess != nil:
		return false // its session owns the bytes: the commit or the sweeper settles it
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return false // could not check: never guess
	}
	drv, err := h.Manager.StorageResolver(n.StorageID)
	if err != nil {
		return false
	}
	key := n.StorageKey
	if key == "" {
		key = n.Path
	}
	statCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	obj, err := drv.Stat(statCtx, key)
	cancel()
	if err != nil || obj.Kind == storage.KindDirectory || !model.TransferLanded(n, obj.Size, obj.Mtime) {
		reason := "the object at its key is not the committed upload"
		if err != nil {
			reason = err.Error()
		}
		slog.Warn("staged upload recovery: left unstored — its bytes are not provably on the storage",
			slog.Int64("node", n.ID),
			slog.String("path", n.Path),
			slog.String("transfer_state", n.TransferState),
			slog.String("reason", reason))
		return false
	}
	// The write the transfer never made, under the identity it would have
	// carried, so the uploader keeps ownership and last-actor attribution.
	actx := ctx
	if n.OwnerID != nil {
		actx = quotastore.WithOwner(actx, *n.OwnerID)
	}
	if n.LastActorID != nil {
		actx = quotastore.WithActor(actx, *n.LastActorID)
	}
	mime := n.Mime
	if mime == "" {
		mime = obj.Mime
	}
	if err := h.Store.UpdateNodeMeta(actx, n.ID, obj.Size, mime, obj.Etag, obj.Mtime); err != nil {
		slog.Warn("staged upload recovery: node meta", slog.Int64("node", n.ID), slog.String("err", err.Error()))
		return false
	}
	if err := h.Store.SetNodeTransferState(actx, n.ID, model.TransferStateStored); err != nil {
		slog.Warn("staged upload recovery: transfer_state", slog.Int64("node", n.ID), slog.String("err", err.Error()))
		return false
	}
	if fresh, _ := h.Store.GetNode(ctx, n.ID); fresh != nil {
		// The hooks the transfer would have run. The upload event is not
		// re-sent: whoever uploaded was already answered.
		h.Manager.indexNode(ctx, fresh)
		h.Manager.dispatchThumb(fresh)
		enqueueAntivirusScan(ctx, fresh)
	}
	slog.Info("staged upload recovery: the bytes were already on the storage; the row now says stored",
		slog.Int64("node", n.ID),
		slog.String("path", n.Path),
		slog.String("was", n.TransferState))
	return true
}

// RunSweeper sweeps once at boot and then on every tick until ctx is done.
func (h *StagedUpload) RunSweeper(ctx context.Context, interval time.Duration) {
	if h == nil || h.Area == nil || !h.Area.Enabled() {
		return
	}
	if interval <= 0 {
		interval = time.Hour
	}
	h.Sweep(ctx)
	// Once per process, after the first sweep: the sweep may just have removed
	// the session that was the only thing standing between a row and the
	// recovery.
	h.RecoverUnstored(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Sweep(ctx)
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (h *StagedUpload) enabled(w http.ResponseWriter) bool {
	if h.Area == nil || !h.Area.Enabled() {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "staged uploads are not configured"})
		return false
	}
	if h.Manager == nil || h.Manager.StorageResolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no storage resolver"})
		return false
	}
	return true
}

// storageRelDir turns a DB node path ("/a/b.txt") into the storage-relative
// directory the realtime change event is keyed by ("a"), matching what vfUpload
// passes. A root-level file yields "" — never ".".
func storageRelDir(nodePath string) string {
	return strings.TrimPrefix(path.Dir(nodePath), "/")
}

// authorize loads the row named by the URL and re-checks, on EVERY call, that
// the caller owns it and may still write to it. An upload id is not a
// capability: it is a handle whose owner is verified each time.
//
// A caller who is not the owner gets 404 rather than 403 — the id space is
// random and there is nothing to gain from confirming that someone else's
// upload exists.
func (h *StagedUpload) authorize(w http.ResponseWriter, r *http.Request) (*model.StagedUpload, bool) {
	if !h.enabled(w) {
		return nil, false
	}
	id := chi.URLParam(r, "id")
	if !staging.ValidID(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "upload not found"})
		return nil, false
	}
	row, err := h.Store.GetStagedUpload(r.Context(), id)
	if err != nil || row == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "upload not found"})
		return nil, false
	}
	if row.UserID != currentUserID(r.Context()) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "upload not found"})
		return nil, false
	}
	// Re-asked on every chunk and at commit: a freeze taken while the bytes
	// were still arriving stops them landing.
	if gate(w, r, h.ACL, row.StorageID, writegate.Writes(row.StorageKey)) {
		return nil, false
	}
	if !aclAllowID(r.Context(), h.ACL, h.Store, row.StorageID, row.StorageKey, acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return nil, false
	}
	// A root-confined credential must not be able to drive an upload that was
	// begun outside its subtree, even when it belongs to the same account.
	if root, ok := confine.RootFrom(r.Context()); ok {
		st, err := h.Store.GetStorage(r.Context(), row.StorageID)
		if err != nil || st == nil || !root.Within(st.Name, strings.TrimPrefix(row.StorageKey, "/")) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "outside confinement root"})
			return nil, false
		}
	}
	return row, true
}

// currentUserID is 0 for an unauthenticated/system caller. Rows created by such
// a caller also carry 0, so ownership stays an equality check everywhere.
func currentUserID(ctx context.Context) int64 {
	if u := auth.UserFrom(ctx); u != nil {
		return u.ID
	}
	return 0
}

// effectiveChunk resolves the part size: the client's request when sane, the
// configured default otherwise, grown when the object would need more parts
// than the protocol allows.
func (h *StagedUpload) effectiveChunk(requested, total int64) (int64, error) {
	chunk := requested
	if chunk <= 0 {
		chunk = h.ChunkSize
	}
	if chunk < minStagingChunk {
		chunk = minStagingChunk
	}
	if chunk > maxStagingChunk {
		chunk = maxStagingChunk
	}
	if total > 0 {
		if n := (total + chunk - 1) / chunk; n > staging.MaxParts {
			chunk = (total + staging.MaxParts - 1) / staging.MaxParts
		}
	}
	if total > 0 && (total+chunk-1)/chunk > staging.MaxParts {
		return 0, fmt.Errorf("upload too large for %d parts", staging.MaxParts)
	}
	return chunk, nil
}

// parseChunkRange reads `Content-Range: bytes A-B/total` and returns the start
// offset and the length. B is inclusive, per RFC 9110.
//
// A missing header is accepted only when the whole object fits in one part —
// the convenience case for a curl user with a small file.
func parseChunkRange(header string, m *staging.Manifest) (int64, int64, error) {
	if header == "" {
		if m.TotalSize > m.ChunkSize {
			return 0, 0, errors.New("Content-Range required")
		}
		return 0, m.TotalSize, nil
	}
	v := strings.TrimSpace(header)
	if !strings.HasPrefix(v, "bytes ") {
		return 0, 0, errors.New("Content-Range must use the `bytes` unit")
	}
	v = strings.TrimSpace(strings.TrimPrefix(v, "bytes "))
	spec, totalStr, found := strings.Cut(v, "/")
	if !found {
		return 0, 0, errors.New("Content-Range must be `bytes A-B/total`")
	}
	startStr, endStr, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, errors.New("Content-Range must be `bytes A-B/total`")
	}
	start, err := strconv.ParseInt(strings.TrimSpace(startStr), 10, 64)
	if err != nil || start < 0 {
		return 0, 0, errors.New("bad Content-Range start")
	}
	end, err := strconv.ParseInt(strings.TrimSpace(endStr), 10, 64)
	if err != nil || end < start {
		return 0, 0, errors.New("bad Content-Range end")
	}
	if t := strings.TrimSpace(totalStr); t != "*" {
		declared, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			return 0, 0, errors.New("bad Content-Range total")
		}
		if declared != m.TotalSize {
			return 0, 0, fmt.Errorf("Content-Range total %d does not match the declared size %d", declared, m.TotalSize)
		}
	}
	if end >= m.TotalSize {
		return 0, 0, fmt.Errorf("Content-Range end %d is past the declared size %d", end, m.TotalSize)
	}
	return start, end - start + 1, nil
}

// parseDeclaredHash splits "<algo>:<hex>" into a fresh hasher and the expected
// digest. sha256 and md5 only — the two every client already has.
func parseDeclaredHash(declared string) (hash.Hash, string, error) {
	algo, want, found := strings.Cut(strings.TrimSpace(declared), ":")
	if !found {
		// Bare hex: treat as sha256, the only sensible default in 2026.
		algo, want = "sha256", declared
	}
	want = strings.ToLower(strings.TrimSpace(want))
	if _, err := hex.DecodeString(want); err != nil || want == "" {
		return nil, "", errors.New("hash must be hex")
	}
	switch strings.ToLower(algo) {
	case "sha256":
		return sha256.New(), want, nil
	case "md5":
		return md5.New(), want, nil
	default:
		return nil, "", fmt.Errorf("unsupported hash algorithm %q (sha256, md5)", algo)
	}
}

// verifyHash reads the assembled staging bytes once and compares the digest the
// client declared. Local disk, so this costs IO but no network.
func (h *StagedUpload) verifyHash(row *model.StagedUpload) error {
	hasher, want, err := parseDeclaredHash(row.Hash)
	if err != nil {
		return err
	}
	rd, err := h.Area.Open(row.ID)
	if err != nil {
		return err
	}
	defer rd.Close()
	if _, err := io.Copy(hasher, rd); err != nil {
		return fmt.Errorf("hash staging: %w", err)
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if got != want {
		return fmt.Errorf("hash mismatch: declared %s, staged %s", want, got)
	}
	return nil
}

// sniffMime detects the content type from the first bytes of the staged data,
// then refines ZIP-based office formats — identical to what vfUpload does with
// the multipart body, so a file uploaded either way gets the same mime.
func (h *StagedUpload) sniffMime(row *model.StagedUpload) string {
	rd, err := h.Area.Open(row.ID)
	if err != nil {
		return ""
	}
	defer rd.Close()
	var sniff [512]byte
	n, _ := io.ReadFull(rd, sniff[:])
	if n <= 0 {
		return ""
	}
	return storage.RefineOfficeMime(http.DetectContentType(sniff[:n]), path.Base(row.StorageKey))
}

// writeStagingErr maps a staging error onto a status code.
func (h *StagedUpload) writeStagingErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, staging.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, staging.ErrBadID), errors.Is(err, staging.ErrBadPart):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, staging.ErrShortPart):
		// The chunk did not arrive whole. The offset has NOT moved; the client
		// re-sends this chunk from the offset it can read back.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "SHORT_CHUNK"})
	case errors.Is(err, staging.ErrIncomplete):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, staging.ErrNoDiskSpace):
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}
