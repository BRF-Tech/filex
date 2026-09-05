package handlers

// Staged ingest for the whole-body surfaces.
//
// The protocol in upload_staged.go is for clients that can chunk and resume.
// The public drop link, ShareX and the AI/REST write cannot: each is a single
// request carrying the whole file. What they still want is the second half of
// the staged contract — the bytes land in filex's own staging area, the
// response comes back as soon as they are safe, and the transfer to a slow or
// distant driver happens afterwards in the ops worker.
//
// This is deliberately ONE helper rather than a staged branch inside each
// handler. The standing rule is that behaviour belongs in shared code: the
// drop link, a ShareX capture and an agent write must all end up with the same
// node, the same hooks and the same failure semantics, or we have three
// products again.
//
// ⚠ It performs NO path or name handling. Callers arrive with a storage id and
// a storage-relative key that has already been through sanitizeUploadName /
// resolveStorage — chunk 4 fixed a traversal where a name of `..` escaped the
// destination, and the fix holds only while there is exactly one guard.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// ErrStagingUnavailable means this instance cannot stage: no staging
// directory, no ops queue, or no storage resolver. Callers treat it as "write
// synchronously instead", never as a failure — an operator who has not
// configured staging must still be able to upload.
var ErrStagingUnavailable = errors.New("staged ingest is not available")

// StagedThreshold is the size above which a whole-body upload is staged rather
// than written straight to the driver. It is the same chunk size the chunked
// protocol hands out, so "large" means the same thing on every surface.
func (h *StagedUpload) StagedThreshold() int64 {
	if h == nil || h.ChunkSize <= 0 {
		return 8 << 20
	}
	return h.ChunkSize
}

// StagedReady reports whether IngestStream can run at all.
func (h *StagedUpload) StagedReady() bool {
	return h != nil &&
		h.Store != nil &&
		h.Area != nil && h.Area.Enabled() &&
		h.Ops != nil &&
		h.Manager != nil && h.Manager.StorageResolver != nil
}

// ShouldStage is the single decision every whole-body surface asks.
func (h *StagedUpload) ShouldStage(size int64) bool {
	return h.StagedReady() && size > h.StagedThreshold()
}

// IngestStream stages src (exactly size bytes) for storageKey on storageID,
// publishes the node immediately with transfer_state="staged" and hands the
// driver write to the ops worker. The returned node is listable before a
// single byte has reached the backend — that is the point.
//
// A short or over-long body is refused and the staging directory removed: the
// same rule as a chunk that arrives short, for the same reason.
//
// declaredMime is advisory. As on every other write path the bytes decide,
// sniffed from staging once they are all there.
func (h *StagedUpload) IngestStream(
	ctx context.Context,
	storageID int64,
	storageKey string,
	src io.Reader,
	size int64,
	userID int64,
	declaredMime string,
) (*model.Node, error) {
	if !h.StagedReady() {
		return nil, ErrStagingUnavailable
	}
	if size <= 0 {
		// A zero-byte file has no parts; the synchronous path writes it in one
		// call and there is nothing to gain from a background op.
		return nil, ErrStagingUnavailable
	}
	drv, err := h.Manager.StorageResolver(storageID)
	if err != nil {
		return nil, fmt.Errorf("no driver: %w", err)
	}
	if _, isWriter := drv.(storage.Writer); !isWriter {
		return nil, storage.ErrUnsupported
	}
	if err := storage.EnsureFileTarget(ctx, drv, storageKey); err != nil {
		return nil, err
	}
	// The last moment at which the bytes we are about to replace still exist --
	// see writehook/overwrite.go. This is the ONE guard call for the whole
	// ingest-then-transfer sequence: the commit below publishes the node as
	// transfer_state="staged", after which a snapshot would read the incoming
	// bytes instead of the ones being replaced (same note as transfer() in
	// upload_staged.go).
	if err := writehook.BeforeOverwrite(ctx, storageID, storageKey); err != nil {
		return nil, err
	}
	if err := h.Area.EnsureFree(size); err != nil {
		return nil, err
	}
	chunk, err := h.effectiveChunk(0, size)
	if err != nil {
		return nil, err
	}

	id := uuid.NewString()
	m, err := h.Area.Create(id, size, chunk, "")
	if err != nil {
		return nil, err
	}
	// Everything below this point owns the staging directory: any error path
	// removes it, so a rejected body leaves no debris behind. That is the same
	// discipline the multipart handlers owe /tmp (lesson #16, 29 GB in two
	// hours) applied to our own temp space.
	fail := func(err error) (*model.Node, error) {
		if rerr := h.Area.Remove(id); rerr != nil {
			slog.Warn("staged ingest: cleanup", slog.String("id", id), slog.String("err", rerr.Error()))
		}
		return nil, err
	}
	for n := 1; n <= m.PartCount(); n++ {
		want := m.ExpectedPartSize(n)
		// io.LimitReader, never io.MultiReader: the body is consumed straight
		// into the part file, so nothing downstream loses a Seeker.
		if _, werr := h.Area.WritePart(id, n, io.LimitReader(src, want), want); werr != nil {
			return fail(werr)
		}
	}
	m, err = h.Area.Manifest(id)
	if err != nil {
		return fail(err)
	}
	if !m.Complete() {
		return fail(fmt.Errorf("staged ingest: %d of %d bytes received", m.Offset(), m.TotalSize))
	}

	row := &model.StagedUpload{
		ID:         id,
		StorageID:  storageID,
		StorageKey: storageKey,
		UserID:     userID,
		TotalSize:  size,
		ChunkSize:  chunk,
		Mime:       declaredMime,
		State:      model.StagedUploadStaging,
		ExpiresAt:  time.Now().Add(h.TTL),
	}
	if err := h.Store.CreateStagedUpload(ctx, row); err != nil {
		return fail(fmt.Errorf("persist staged upload: %w", err))
	}
	remove := func(err error) (*model.Node, error) {
		if derr := h.Store.DeleteStagedUpload(ctx, id); derr != nil {
			slog.Warn("staged ingest: row cleanup", slog.String("id", id), slog.String("err", derr.Error()))
		}
		return fail(err)
	}

	mime := declaredMime
	if sniffed := h.sniffMime(row); sniffed != "" {
		mime = sniffed
	}
	node, err := h.publishStagedNode(ctx, row, mime, m)
	if err != nil {
		return remove(fmt.Errorf("node: %w", err))
	}

	// The caller's request may be finished (or cancelled) the instant we
	// answer; the transfer must not be.
	bg := context.WithoutCancel(ctx)
	if err := h.Store.AttachStagedUploadTarget(bg, id, node.ID, 0); err != nil {
		slog.Warn("staged ingest: attach node", slog.String("id", id), slog.String("err", err.Error()))
	}
	if err := h.Store.UpdateStagedUploadState(bg, id, model.StagedUploadCommitting, ""); err != nil {
		return remove(fmt.Errorf("state: %w", err))
	}
	op, err := h.Ops.Submit(bg, ops.OpUploadCommit, storageID, []string{id}, "")
	if err != nil {
		return remove(fmt.Errorf("submit: %w", err))
	}
	if err := h.Store.AttachStagedUploadTarget(bg, id, node.ID, op.ID); err != nil {
		slog.Warn("staged ingest: attach op", slog.String("id", id), slog.String("err", err.Error()))
	}
	emitFolderChange(storageID, storageRelDir(node.Path), realtime.ChangeEvent{Action: "upload"})

	slog.Info("staged ingest",
		slog.String("id", id),
		slog.Int64("node", node.ID),
		slog.Int64("op", op.ID),
		slog.String("path", storageKey),
		slog.Int64("size", size))
	return node, nil
}
