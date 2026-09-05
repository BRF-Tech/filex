package handlers

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/throughput"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// cryptoRead is a tiny indirection so randHex6 can be tested deterministically.
var cryptoRead = crand.Read

// Mutate handles the POST verbs the FileExplorer SFC fires from its
// toolbar: newfolder, rename, move, delete, upload.
//
// All bodies use the @brftech/filex-core wire format (adapter://path).
// On success each verb re-renders the parent dir via vfIndex so the
// SFC's reactive store updates without a follow-up GET.
//
// Routes that hit this dispatcher live behind the auth middleware in
// routes.go (POST /api/files/manager?action=…). Reads stay on the GET
// dispatcher in manager.go to preserve cache semantics.
func (h *Manager) Mutate(w http.ResponseWriter, r *http.Request) {
	if h.StorageResolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no storage resolver"})
		return
	}

	q := r.URL.Query()
	action := q.Get("action")
	if action == "" {
		action = q.Get("q")
	}
	if action == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing action"})
		return
	}

	switch action {
	case "newfolder":
		h.vfNewFolder(w, r)
	case "rename":
		h.vfRename(w, r)
	case "move":
		h.vfMove(w, r)
	case "delete":
		h.vfDelete(w, r)
	case "upload":
		h.vfUpload(w, r)
	default:
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "action not implemented: " + action})
	}
}

// vfNewFolderBody is POST /api/files/manager?action=newfolder.
type vfNewFolderBody struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// vfNewFolder creates `name` under `path`'s adapter+dir on the backing
// driver, mirrors the create into the DB cache, and re-renders the
// parent listing.
func (h *Manager) vfNewFolder(w http.ResponseWriter, r *http.Request) {
	var body vfNewFolderBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || strings.ContainsAny(body.Name, "/\\") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad folder name"})
		return
	}

	current, parentRel, storageNames, ok := h.resolveAdapterDir(w, r, body.Path)
	if !ok {
		return
	}
	if current.ReadOnly {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "storage is read-only"})
		return
	}

	drv, err := h.StorageResolver(current.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	mk, ok := drv.(storage.Mkdirer)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support mkdir"})
		return
	}

	fullRel := path.Join(parentRel, body.Name)
	// A folder opened on top of an existing file name is the same collision
	// from the other side (storage.ErrKindConflict).
	if err := storage.EnsureDirTarget(r.Context(), drv, fullRel); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": err.Error()})
		return
	}
	if err := mk.Mkdir(r.Context(), fullRel); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "mkdir: " + err.Error()})
		return
	}

	// Mirror into DB cache so the very next index call shows the dir.
	parentID, err := h.lookupDirID(r.Context(), current.ID, parentRel)
	if err != nil {
		slog.Warn("manager: newfolder parent lookup",
			slog.String("path", parentRel),
			slog.String("err", err.Error()))
	} else {
		clean := normalizeDBPath(fullRel)
		hash := pathkey.Hash(current.ID, clean)
		if existing, _ := h.Store.GetNodeByPath(r.Context(), current.ID, hash); existing == nil {
			n := &model.Node{
				StorageID:  current.ID,
				ParentID:   parentID,
				Name:       body.Name,
				Path:       clean,
				PathHash:   hash,
				StorageKey: clean,
				Type:       model.NodeTypeDirectory,
				SyncState:  model.SyncStateSynced,
			}
			if created, err := h.Store.CreateNode(r.Context(), n); err != nil {
				slog.Warn("manager: newfolder db create",
					slog.String("path", clean),
					slog.String("err", err.Error()))
			} else {
				// Push to Bleve so the search box finds the new dir
				// without waiting for the next sync run.
				h.indexNode(r.Context(), created)
			}
		}
	}

	// Live: a folder appeared in this directory — refresh everyone viewing it.
	emitFolderChange(current.ID, parentRel, realtime.ChangeEvent{Action: "create", Name: body.Name})
	h.vfIndex(w, r, current, parentRel, storageNames, false)
}

// vfRenameBody is POST /api/files/manager?action=rename.
type vfRenameBody struct {
	Path string `json:"path"`
	Item string `json:"item"`
	Name string `json:"name"`
}

// vfRename renames the single item to a new sibling name in the same
// dir. (Cross-dir moves go through vfMove.)
func (h *Manager) vfRename(w http.ResponseWriter, r *http.Request) {
	var body vfRenameBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || strings.ContainsAny(body.Name, "/\\") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad new name"})
		return
	}

	current, parentRel, storageNames, ok := h.resolveAdapterDir(w, r, body.Path)
	if !ok {
		return
	}
	if current.ReadOnly {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "storage is read-only"})
		return
	}

	srcAdapter, srcRel := splitAdapterPath(body.Item)
	if srcAdapter != "" && srcAdapter != current.Name {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rename across adapters not supported"})
		return
	}
	if srcRel == "" || pathHasDotDot(srcRel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item path"})
		return
	}
	if !h.allowed(r.Context(), current, srcRel, acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}

	drv, err := h.StorageResolver(current.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	mv, ok := drv.(storage.Mover)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support move"})
		return
	}

	dstRel := path.Join(path.Dir(srcRel), body.Name)
	if dstRel == srcRel {
		// No-op rename — just re-render.
		h.vfIndex(w, r, current, parentRel, storageNames, false)
		return
	}
	if err := mv.Move(r.Context(), srcRel, dstRel); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "rename: " + err.Error()})
		return
	}

	h.applyDBMove(r.Context(), current.ID, srcRel, dstRel)
	/* bag:b3 event */
	writehook.OnFileMoved(r.Context(), current.ID, normalizeDBPath(srcRel), normalizeDBPath(dstRel), body.Name,
		writehook.OriginManager, map[string]any{"rename": true})
	// Live: an item was renamed in this directory.
	emitFolderChange(current.ID, parentRel, realtime.ChangeEvent{Action: "rename", Name: path.Base(srcRel), NewName: body.Name})
	h.vfIndex(w, r, current, parentRel, storageNames, false)
}

// vfMoveBody is POST /api/files/manager?action=move.
type vfMoveBody struct {
	Path  string         `json:"path"`
	Item  string         `json:"item,omitempty"`
	Items []vfPathHolder `json:"items"`
}

// vfPathHolder matches the {"path":"..."} shape the SFC sends per item.
type vfPathHolder struct {
	Path string `json:"path"`
}

// vfMove moves each item into the destination dir, preserving the
// item's basename. Cross-adapter moves are rejected — the SFC never
// generates them, but a stale paste could.
func (h *Manager) vfMove(w http.ResponseWriter, r *http.Request) {
	var body vfMoveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	current, destRel, storageNames, ok := h.resolveAdapterDir(w, r, body.Path)
	if !ok {
		return
	}
	if current.ReadOnly {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "storage is read-only"})
		return
	}
	if len(body.Items) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no items"})
		return
	}

	drv, err := h.StorageResolver(current.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	mv, ok := drv.(storage.Mover)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support move"})
		return
	}

	/* wiring:e2 — the synchronous move goes through the same boundary rule
	 * as the queued one. Checked up front over the WHOLE batch: this loop
	 * moves files one at a time, so a mid-loop refusal would leave the first
	 * half moved and the second half not. */
	if lk, ok := h.Store.(e2e.NodeByPathLookup); ok {
		rels := make([]string, 0, len(body.Items))
		for _, it := range body.Items {
			_, srcRel := splitAdapterPath(it.Path)
			rels = append(rels, srcRel)
		}
		if err := e2e.GuardTransfer(r.Context(), lk, current.ID, rels, current.ID, destRel); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	}

	srcDirs := make(map[string]struct{})
	for _, it := range body.Items {
		srcAdapter, srcRel := splitAdapterPath(it.Path)
		if srcAdapter != "" && srcAdapter != current.Name {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "move across adapters not supported: " + it.Path})
			return
		}
		if srcRel == "" || pathHasDotDot(srcRel) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item path: " + it.Path})
			return
		}
		if !h.allowed(r.Context(), current, srcRel, acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission: " + it.Path})
			return
		}
		dstRel := path.Join(destRel, path.Base(srcRel))
		if dstRel == srcRel {
			continue
		}
		if err := mv.Move(r.Context(), srcRel, dstRel); err != nil {
			writeJSON(w, mapDriverErr(err), map[string]string{"error": "move: " + err.Error()})
			return
		}
		h.applyDBMove(r.Context(), current.ID, srcRel, dstRel)
		/* bag:b3 event */
		writehook.OnFileMoved(r.Context(), current.ID, normalizeDBPath(srcRel), normalizeDBPath(dstRel), path.Base(dstRel),
			writehook.OriginManager)
		srcDirs[path.Dir(srcRel)] = struct{}{}
	}

	// Live: items landed in the destination — and left their source folders.
	emitFolderChange(current.ID, destRel, realtime.ChangeEvent{Action: "move"})
	destKey := normalizeDBPath(destRel)
	for d := range srcDirs {
		if normalizeDBPath(d) == destKey {
			continue // same room as dest, already emitted
		}
		emitFolderChange(current.ID, d, realtime.ChangeEvent{Action: "move"})
	}
	h.vfIndex(w, r, current, destRel, storageNames, false)
}

// vfDeleteBody is POST /api/files/manager?action=delete.
type vfDeleteBody struct {
	Path  string         `json:"path"`
	Items []vfPathHolder `json:"items"`
}

// vfDelete soft-deletes each item by RENAMING the underlying file to
// `.filex-trash/<unix>-<rand>__<basename>` on the storage and flipping
// the DB row's `deleted_at`. The original path is preserved in
// `storage_key` so trash.Service.Restore can move the file back.
//
// Background: an earlier implementation called Driver.Delete here,
// which made restore impossible (the file was already gone). Now
// purge is the only thing that hard-deletes — see trash.purgeOne.
func (h *Manager) vfDelete(w http.ResponseWriter, r *http.Request) {
	var body vfDeleteBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	current, parentRel, storageNames, ok := h.resolveAdapterDir(w, r, body.Path)
	if !ok {
		return
	}
	if current.ReadOnly {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "storage is read-only"})
		return
	}
	if len(body.Items) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no items"})
		return
	}

	drv, err := h.StorageResolver(current.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	for _, it := range body.Items {
		srcAdapter, srcRel := splitAdapterPath(it.Path)
		if srcAdapter != "" && srcAdapter != current.Name {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "delete across adapters not supported: " + it.Path})
			return
		}
		if srcRel == "" || pathHasDotDot(srcRel) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item path: " + it.Path})
			return
		}
		if !h.allowed(r.Context(), current, srcRel, acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission: " + it.Path})
			return
		}

		// Soft-delete inside `.filex-trash/` already? Hard delete this
		// time (mirrors brf-mono's "delete from trash = permanent").
		if strings.HasPrefix(srcRel, trashPrefix) {
			if del, ok := drv.(storage.Deleter); ok {
				if err := del.Delete(r.Context(), srcRel); err != nil && !errors.Is(err, storage.ErrNotFound) {
					writeJSON(w, mapDriverErr(err), map[string]string{"error": "delete: " + err.Error()})
					return
				}
			}
			origClean := normalizeDBPath(srcRel)
			hash := pathkey.Hash(current.ID, origClean)
			if existing, err := h.Store.GetNodeByPathIncludingDeleted(r.Context(), current.ID, hash); err == nil && existing != nil {
				_ = h.Store.HardDeleteNode(r.Context(), existing.ID)
				h.removeFromIndex(r.Context(), existing.ID)
			}
			/* bag:b3 event */
			writehook.OnFileDeleted(r.Context(), current.ID, origClean, path.Base(srcRel),
				writehook.OriginManager, map[string]any{"purged": true})
			continue
		}

		// Soft delete: rename to `.filex-trash/<unix>-<rand>__<basename>`.
		base := path.Base(srcRel)
		if base == "" || base == "." || base == "/" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item base: " + it.Path})
			return
		}
		// trash.Put is the shared implementation every delete surface uses
		// (WebDAV, AI/REST, the async ops worker, and this one), so the same
		// item deleted from any of them lands in the trash the same way.
		out, terr := trash.Put(r.Context(), drv, srcRel)
		trashed := terr == nil && out.Trashed
		switch {
		case trashed:
			/* bag:b3 event */
			writehook.OnFileTrashed(r.Context(), current.ID, normalizeDBPath(srcRel), base,
				normalizeDBPath(out.Key), writehook.OriginManager)

		case terr == nil && out.Missing:
			// Source object already gone (stale index / out-of-band delete):
			// drop the cache row and continue so one missing item doesn't
			// fail the whole delete batch.
			origClean := normalizeDBPath(srcRel)
			origHash := pathkey.Hash(current.ID, origClean)
			if existing, err := h.Store.GetNodeByPath(r.Context(), current.ID, origHash); err == nil && existing != nil {
				_ = h.Store.HardDeleteNode(r.Context(), existing.ID)
				h.removeFromIndex(r.Context(), existing.ID)
			}
			continue

		case errors.Is(terr, trash.ErrUnsupported):
			// Driver can neither move nor copy — fall back to hard delete.
			if del, ok := drv.(storage.Deleter); ok {
				if err := del.Delete(r.Context(), srcRel); err != nil && !errors.Is(err, storage.ErrNotFound) {
					writeJSON(w, mapDriverErr(err), map[string]string{"error": "delete: " + err.Error()})
					return
				}
			}
			/* bag:b3 event */
			writehook.OnFileDeleted(r.Context(), current.ID, normalizeDBPath(srcRel), base, writehook.OriginManager)

		default:
			writeJSON(w, mapDriverErr(terr), map[string]string{"error": "trash: " + terr.Error()})
			return
		}

		// Update DB: store the original path in storage_key so Restore
		// can find it; flip deleted_at; rewrite path/path_hash to the
		// trash location so a fresh upload at the original path works.
		// Directory rows drag their cached subtree into the trash inside
		// SoftDeleteAndRetag (issue #5) — collect the subtree ids UP
		// FRONT (children are still live) so the search index forgets
		// them too.
		origClean := normalizeDBPath(srcRel)
		origHash := pathkey.Hash(current.ID, origClean)
		if existing, err := h.Store.GetNodeByPath(r.Context(), current.ID, origHash); err == nil && existing != nil {
			var subtreeIDs []int64
			if existing.Type == model.NodeTypeDirectory {
				subtreeIDs = h.collectSubtreeIDs(r.Context(), current.ID, existing.ID)
			}
			if trashed {
				newClean := normalizeDBPath(out.Key)
				newHash := pathkey.Hash(current.ID, newClean)
				_ = h.Store.SoftDeleteAndRetag(r.Context(), existing.ID, newClean, newHash, origClean)
			} else {
				// Bytes are gone for good: drop the row instead of parking a
				// trash entry whose Restore could never find anything.
				_ = h.Store.HardDeleteNode(r.Context(), existing.ID)
			}
			h.removeFromIndex(r.Context(), existing.ID)
			for _, cid := range subtreeIDs {
				h.removeFromIndex(r.Context(), cid)
			}
		}
	}

	// Live: items were removed from this directory.
	emitFolderChange(current.ID, parentRel, realtime.ChangeEvent{Action: "delete"})
	h.vfIndex(w, r, current, parentRel, storageNames, false)
}

// trashPrefix is the in-storage directory where soft-deleted files are
// renamed to. Listings filter it out; trash.Service.Restore renames out.
const trashPrefix = trash.Prefix

// collectSubtreeIDs walks the live cached descendants of a directory node
// (DFS via ListNodesByParent) and returns their ids — used to purge the
// search index when a folder is trashed (the DB rows themselves are
// retagged inside Store.SoftDeleteAndRetag).
func (h *Manager) collectSubtreeIDs(ctx context.Context, storageID, rootID int64) []int64 {
	var out []int64
	var walk func(parentID int64, depth int)
	walk = func(parentID int64, depth int) {
		if depth > 64 {
			return
		}
		children, err := h.Store.ListNodesByParent(ctx, storageID, &parentID)
		if err != nil {
			return
		}
		for _, c := range children {
			if c.DeletedAt != nil {
				continue
			}
			out = append(out, c.ID)
			if c.Type == model.NodeTypeDirectory {
				walk(c.ID, depth+1)
			}
		}
	}
	walk(rootID, 0)
	return out
}

// randHex6 returns a 6-char lowercase hex string for trash key uniqueness.
func randHex6() string {
	var b [3]byte
	_, _ = cryptoRead(b[:])
	return hex.EncodeToString(b[:])
}

// vfUpload accepts multipart/form-data with one or more file[] parts
// and writes each into the destination dir on the backing driver.
//
// Limits: 32 MiB in-memory body (per ParseMultipartForm), the rest spilled to
// a temp file. This is the SMALL-FILE fast path and stays that way — every
// client now sends anything above the chunk size over the staged protocol
// (/api/files/upload/*, docs/UPLOADS.md), which is resumable and works on
// every driver. The old presigned `/upload/init` flow is S3-only and no client
// speaks it any more.
func (h *Manager) vfUpload(w http.ResponseWriter, r *http.Request) {
	// Spilled multipart temp files outlive the response unless dropped here —
	// see the note in AI.Upload. This is the browser upload path, so it is the
	// highest-volume producer of them.
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad multipart: " + err.Error()})
		return
	}
	pathStr := r.FormValue("path")
	current, destRel, storageNames, ok := h.resolveAdapterDir(w, r, pathStr)
	if !ok {
		return
	}
	if current.ReadOnly {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "storage is read-only"})
		return
	}

	files := r.MultipartForm.File["file[]"]
	if len(files) == 0 {
		// Some clients send `file` instead of `file[]`.
		files = r.MultipartForm.File["file"]
	}
	if len(files) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no files in upload"})
		return
	}

	// The ceiling applies here too. Large uploads reach the staged path, which
	// checks at `begin`; this is the small-file path, and without a check a
	// user could sail past their quota a few megabytes at a time. Checked once
	// for the whole batch — refusing halfway through would leave some files
	// written and some not.
	var batch int64
	for _, fh := range files {
		batch += fh.Size
	}
	if err := h.checkQuota(r.Context(), batch); err != nil {
		if errors.Is(err, quota.ErrQuotaExceeded) {
			slog.Info("upload refused: quota",
				slog.Int64("user", quotastore.OwnerFrom(r.Context())),
				slog.Int64("size", batch))
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
				"error": "quota exceeded",
				"code":  "QUOTA_EXCEEDED",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	drv, err := h.StorageResolver(current.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	wr, ok := drv.(storage.Writer)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support write"})
		return
	}

	// ⚠ ENSURE rather than look up: a destination whose directories have no
	// node rows yet is the normal case for a first upload into a new folder,
	// and giving up here used to mean the file never entered the catalogue.
	parentID, parentLookupErr := h.ensureDirChain(r.Context(), current.ID, destRel)

	for _, fh := range files {
		name, nameOK := sanitizeUploadName(fh.Filename)
		if !nameOK {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad upload filename"})
			return
		}
		fullRel := path.Join(destRel, name)

		src, err := fh.Open()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "open part: " + err.Error()})
			return
		}

		// Sniff the first 512 bytes for mime detection, then rewind. ZIP-based
		// office formats get refined via storage.RefineOfficeMime so
		// pptx/docx/odt don't end up tagged "application/zip" — see
		// internal/storage/mime.go for the OnlyOffice mismatch story.
		//
		// Rewind rather than io.MultiReader(sniff, src): multipart.File is an
		// io.Seeker, and handing the driver a seekable body keeps the S3 SDK
		// able to measure and to replay it on retry. Wrapping it cost us both
		// and put every upload on the chunked path (olivov H1, 2026-08-05).
		var sniff [512]byte
		n, _ := io.ReadFull(src, sniff[:])
		mime := ""
		if n > 0 {
			mime = storage.RefineOfficeMime(http.DetectContentType(sniff[:n]), name)
		}
		if _, err := src.Seek(0, io.SeekStart); err != nil {
			_ = src.Close()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "rewind part: " + err.Error()})
			return
		}
		// A file named exactly like an existing subfolder would leave `X` and
		// `X/…` side by side on an object store (storage.ErrKindConflict).
		if err := storage.EnsureFileTarget(r.Context(), drv, fullRel); err != nil {
			_ = src.Close()
			writeJSON(w, mapDriverErr(err), map[string]string{"error": err.Error()})
			return
		}

		started := time.Now()
		if err := wr.Write(r.Context(), fullRel, src, fh.Size); err != nil {
			_ = src.Close()
			writeJSON(w, mapDriverErr(err), map[string]string{"error": "write: " + err.Error()})
			return
		}
		// The driver call, timed. Without this the write side of
		// filex_transfer_bytes_total would count only staged uploads and
		// silently under-report every small one.
		throughput.Observe(current.ID, throughput.Write, fh.Size, time.Since(started))
		_ = src.Close()

		if parentLookupErr != nil {
			/* bag:b3 event — DB mirror unavailable; the bytes ARE on
			   storage, so still announce the write. Transient (unsaved)
			   node → the writehook skips the AV enqueue. */
			writehook.OnFileWritten(r.Context(), current.ID, &model.Node{
				StorageID: current.ID,
				Name:      name,
				Path:      normalizeDBPath(fullRel),
				Type:      model.NodeTypeFile,
				Size:      fh.Size,
				Mime:      mime,
			}, writehook.OriginManager)
			continue
		}
		clean := normalizeDBPath(fullRel)
		hash := pathkey.Hash(current.ID, clean)
		if existing, _ := h.Store.GetNodeByPath(r.Context(), current.ID, hash); existing != nil {
			_ = h.Store.UpdateNodeMeta(r.Context(), existing.ID, fh.Size, mime, existing.Etag, time.Now())
			// Refresh the row pointer so the index entry carries the
			// new size/mime — IndexNode keys off node fields.
			if fresh, _ := h.Store.GetNode(r.Context(), existing.ID); fresh != nil {
				h.indexNode(r.Context(), fresh)
				// Re-upload of an existing node — the bytes changed so
				// the stored thumb is stale. Mark it pending and let
				// the pipeline regenerate.
				h.dispatchThumb(fresh)
				/* bag:b3 event + koru:k2 av — single post-write gate */
				writehook.OnFileWritten(r.Context(), current.ID, fresh, writehook.OriginManager)
			}
			continue
		}
		n2 := &model.Node{
			StorageID:  current.ID,
			ParentID:   parentID,
			Name:       name,
			Path:       clean,
			PathHash:   hash,
			StorageKey: clean,
			Type:       model.NodeTypeFile,
			Size:       fh.Size,
			Mime:       mime,
			SyncState:  model.SyncStateSynced,
		}
		if created, err := h.Store.CreateNode(r.Context(), n2); err != nil {
			slog.Warn("manager: upload db create",
				slog.String("path", clean),
				slog.String("err", err.Error()))
		} else {
			h.indexNode(r.Context(), created)
			h.dispatchThumb(created)
			/* bag:b3 event + koru:k2 av — single post-write gate */
			writehook.OnFileWritten(r.Context(), current.ID, created, writehook.OriginManager)
		}
	}

	// Live: new/updated files in this directory.
	emitFolderChange(current.ID, destRel, realtime.ChangeEvent{Action: "upload"})
	h.vfIndex(w, r, current, destRel, storageNames, false)
}

// resolveAdapterDir is the shared first half of every mutation: split
// the adapter prefix off `pathStr`, look up the storage row, and
// validate the relative path. On error it writes the response and
// returns ok=false so the caller can early-exit.
func (h *Manager) resolveAdapterDir(w http.ResponseWriter, r *http.Request, pathStr string) (*model.Storage, string, []string, bool) {
	storages, err := h.Store.ListEnabledStorages(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return nil, "", nil, false
	}
	storageNames := make([]string, 0, len(storages))
	for _, s := range storages {
		storageNames = append(storageNames, s.Name)
	}

	adapter, rel := splitAdapterPath(pathStr)
	if adapter == "" {
		if len(storages) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no storages configured"})
			return nil, "", nil, false
		}
		adapter = storages[0].Name
	}

	var current *model.Storage
	for _, s := range storages {
		if s.Name == adapter {
			current = s
			break
		}
	}
	if current == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown adapter: " + adapter})
		return nil, "", nil, false
	}
	if pathHasDotDot(rel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path"})
		return nil, "", nil, false
	}
	// RBAC: every mutation writes into this base dir (create/upload/move-dest
	// /rename-parent/delete-parent) → require ≥editor on it. Viewer accounts
	// (ceiling=viewer) are thus read-only even on RBAC-off storages.
	if !h.allowed(r.Context(), current, rel, acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return nil, "", nil, false
	}
	return current, rel, storageNames, true
}

// lookupDirID resolves a relative dir to a *int64 parent ID inside the
// DB cache. Returns nil for the storage root. The error path is
// surfaced separately so callers can keep the driver mutation when DB
// cache is missing — the next sync will refill it.
func (h *Manager) lookupDirID(ctx context.Context, storageID int64, rel string) (*int64, error) {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return nil, nil
	}
	hash := pathkey.Hash(storageID, normalizeDBPath(rel))
	n, err := h.Store.GetNodeByPath(ctx, storageID, hash)
	if err != nil || n == nil {
		// Walk the parent chain — the DB might lag the driver.
		return h.walkDirID(ctx, storageID, rel)
	}
	id := n.ID
	return &id, nil
}

// ensureDirChain makes sure every directory on the way to rel has a node row,
// creating the ones that are missing, and returns the id of the deepest.
//
// ⚠⚠ This exists because looking a parent up and GIVING UP when it is missing
// loses the file from the catalogue entirely. Measured 2026-08-16: uploading to
// `main://newdir/a.txt` when `newdir` had no node row wrote the bytes to the
// driver and created NO rows at all — the file was on disk, the subfolder
// listing found it through the driver fallback, and the level above was empty.
// In the explorer that is a folder somebody just uploaded into that does not
// exist until the next sync run.
//
// The directories are real on the driver by the time this runs (the write went
// through them), so creating the rows is recording what is there rather than
// inventing anything. Idempotent: an existing row is reused, never duplicated.
func (h *Manager) ensureDirChain(ctx context.Context, storageID int64, rel string) (*int64, error) {
	rel = strings.Trim(normalizeDBPath(rel), "/")
	if rel == "" {
		return nil, nil
	}
	var parent *int64
	built := ""
	for _, segment := range strings.Split(rel, "/") {
		if segment == "" {
			continue
		}
		built = path.Join(built, segment)
		clean := normalizeDBPath(built)
		hash := pathkey.Hash(storageID, clean)
		if existing, _ := h.Store.GetNodeByPath(ctx, storageID, hash); existing != nil {
			id := existing.ID
			parent = &id
			continue
		}
		created, err := h.Store.CreateNode(ctx, &model.Node{
			StorageID:  storageID,
			ParentID:   parent,
			Name:       segment,
			Path:       clean,
			PathHash:   hash,
			StorageKey: clean,
			Type:       model.NodeTypeDirectory,
			SyncState:  model.SyncStateSynced,
		})
		if err != nil {
			// A concurrent writer may have created it between the read and the
			// write; re-reading is cheaper than a transaction and is the same
			// answer.
			if again, _ := h.Store.GetNodeByPath(ctx, storageID, hash); again != nil {
				id := again.ID
				parent = &id
				continue
			}
			return nil, err
		}
		id := created.ID
		parent = &id
		h.indexNode(ctx, created)
	}
	return parent, nil
}

// walkDirID is the slow fallback path that uses ListNodesByParent step
// by step (used when GetNodeByPath misses, e.g. directory created
// outside the cache).
func (h *Manager) walkDirID(ctx context.Context, storageID int64, rel string) (*int64, error) {
	parts := strings.Split(strings.Trim(rel, "/"), "/")
	var parentPtr *int64
	for _, segment := range parts {
		if segment == "" {
			continue
		}
		nodes, err := h.Store.ListNodesByParent(ctx, storageID, parentPtr)
		if err != nil {
			return nil, err
		}
		matched := false
		for _, n := range nodes {
			if n.Name == segment && n.Type == model.NodeTypeDirectory {
				id := n.ID
				parentPtr = &id
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("directory not found: %s", segment)
		}
	}
	return parentPtr, nil
}

// applyDBMove updates the cache row for srcRel to point at dstRel. If
// the destination changes parent dir, ParentID is updated too — store
// helpers don't have a single-call cross-parent move, but MoveNode
// already accepts a parent_id arg.
func (h *Manager) applyDBMove(ctx context.Context, storageID int64, srcRel, dstRel string) {
	srcClean := normalizeDBPath(srcRel)
	dstClean := normalizeDBPath(dstRel)
	srcHash := pathkey.Hash(storageID, srcClean)
	dstHash := pathkey.Hash(storageID, dstClean)

	existing, err := h.Store.GetNodeByPath(ctx, storageID, srcHash)
	if err != nil || existing == nil {
		return
	}

	parentID, err := h.lookupDirID(ctx, storageID, path.Dir(strings.TrimPrefix(dstClean, "/")))
	if err != nil {
		// Soft-delete the stale row so a future index lists the new
		// path under whichever parent the sync finds.
		_ = h.Store.SoftDeleteNode(ctx, existing.ID)
		return
	}

	name := path.Base(dstClean)
	if err := h.Store.MoveNode(ctx, existing.ID, parentID, name, dstClean, dstHash); err != nil {
		slog.Warn("manager: db move",
			slog.String("from", srcClean),
			slog.String("to", dstClean),
			slog.String("err", err.Error()))
		_ = h.Store.SoftDeleteNode(ctx, existing.ID)
		h.removeFromIndex(ctx, existing.ID)
		return
	}
	// Refresh + re-index the moved row so search hits the new path.
	if fresh, _ := h.Store.GetNode(ctx, existing.ID); fresh != nil {
		h.indexNode(ctx, fresh)
	}
}

// mapDriverErr normalizes driver errors into HTTP statuses for the
// FileExplorer toast.
func mapDriverErr(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, storage.ErrNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, storage.ErrReadOnly) {
		return http.StatusForbidden
	}
	if errors.Is(err, storage.ErrUnsupported) {
		return http.StatusNotImplemented
	}
	if errors.Is(err, os.ErrExist) {
		return http.StatusConflict
	}
	// The target exists as the other kind (file vs folder). A conflict, not a
	// server fault — and not a 4xx the client can fix by retrying.
	if errors.Is(err, storage.ErrKindConflict) {
		return http.StatusConflict
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "exists") || strings.Contains(msg, "already") {
		return http.StatusConflict
	}
	if strings.Contains(msg, "not found") || strings.Contains(msg, "no such") {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// normalizeDBPath canonicalises a relative path to the form sync.poll
// stores in the nodes table: leading slash, no trailing slash, no `.`.
// sanitizeUploadName is the ONE filename guard every upload surface uses:
// vfUpload (browser multipart), IngestFile (public file-drop) and the staged
// upload path. Keeping it in one place is the point — three copies of a path
// guard is three chances to fix a hole in two of them.
//
// It reduces the client's filename to a basename and refuses the values that
// would escape the destination directory once path.Join'ed. ".." is refused
// explicitly: path.Base("..") is ".." and joining it walks OUT of the target
// folder — the earlier inline copies of this check did not cover it.
func sanitizeUploadName(raw string) (string, bool) {
	name := path.Base(raw)
	switch name {
	case "", ".", "..", "/":
		return "", false
	}
	if strings.ContainsAny(name, "\\") {
		return "", false
	}
	return name, true
}

func normalizeDBPath(rel string) string {
	rel = strings.Trim(rel, "/")
	clean := path.Clean("/" + rel)
	return strings.TrimRight(clean, "/")
}

// IngestFile writes one uploaded file into destRel/filename on the given
// storage and upserts + indexes + thumbnails its node. It is the shared
// ingest path behind the authenticated multipart upload (vfUpload's loop) and
// the public file-drop handler, so both surface identical mime sniffing, node
// caching and thumbnail dispatch. Parent dir nodes are looked up lazily — call
// EnsureDir first when writing into a freshly-created folder so the new file's
// node links to the right parent.
func (h *Manager) IngestFile(ctx context.Context, st *model.Storage, destRel, filename string, src io.Reader, size int64) (*model.Node, error) {
	name, ok := sanitizeUploadName(filename)
	if !ok {
		return nil, fmt.Errorf("bad filename: %q", filename)
	}
	drv, err := h.StorageResolver(st.ID)
	if err != nil {
		return nil, err
	}
	wr, ok := drv.(storage.Writer)
	if !ok {
		return nil, storage.ErrUnsupported
	}
	fullRel := path.Join(destRel, name)
	// The ceiling, before a byte is written. For the public drop link the
	// account measured is the LINK CREATOR (quotastore.OwnerFrom), because
	// theirs is the disk being filled — the uploader has no account at all.
	if err := h.checkQuota(ctx, size); err != nil {
		return nil, err
	}
	// A file named exactly like an existing subfolder would leave `X` and `X/…`
	// side by side on an object store (storage.ErrKindConflict).
	if err := storage.EnsureFileTarget(ctx, drv, fullRel); err != nil {
		return nil, err
	}

	// Large body → filex's own staging, then a background transfer. The caller
	// gets a listed node as soon as the bytes are safe here instead of waiting
	// out a slow or distant driver. ErrStagingUnavailable (no staging dir, no
	// ops queue) falls through to the synchronous write below, so an instance
	// without staging behaves exactly as it did before.
	if h.Staged.ShouldStage(size) {
		node, serr := h.Staged.IngestStream(ctx, st.ID, fullRel, src, size, currentUserID(ctx), "")
		if serr == nil {
			return node, nil
		}
		if !errors.Is(serr, ErrStagingUnavailable) {
			return nil, serr
		}
		// ⚠ Staging consumed nothing on the unavailable path (it refuses
		// before touching src), so falling through here is safe. Any other
		// error has already eaten part of the body and must NOT be retried
		// synchronously — that would write a truncated file.
	}

	// Sniff the first 512 bytes for mime, then REWIND — see vfUpload for the
	// OnlyOffice office-format refinement rationale.
	//
	// Rewinding rather than io.MultiReader(sniff, src) is not a style choice:
	// wrapping the body destroys its Seeker, the S3 SDK can then neither
	// measure nor replay it, and the request goes out chunked with no
	// Content-Length. Providers that require one answer 411 — which is exactly
	// how browser uploads broke for ten days (H1). vfUpload was fixed then;
	// this path, which serves the PUBLIC file-drop link, was missed and kept
	// failing. A caller whose reader is not seekable still gets the old
	// behaviour rather than an error.
	var sniff [512]byte
	n, _ := io.ReadFull(src, sniff[:])
	mime := ""
	if n > 0 {
		mime = storage.RefineOfficeMime(http.DetectContentType(sniff[:n]), name)
	}
	body := io.Reader(io.MultiReader(bytes.NewReader(sniff[:n]), src))
	if s, ok := src.(io.Seeker); ok && n > 0 {
		if _, err := s.Seek(0, io.SeekStart); err == nil {
			body = src
		}
	}
	started := time.Now()
	if err := wr.Write(ctx, fullRel, body, size); err != nil {
		return nil, err
	}
	throughput.Observe(st.ID, throughput.Write, size, time.Since(started))

	clean := normalizeDBPath(fullRel)
	hash := pathkey.Hash(st.ID, clean)
	if existing, _ := h.Store.GetNodeByPath(ctx, st.ID, hash); existing != nil {
		_ = h.Store.UpdateNodeMeta(ctx, existing.ID, size, mime, existing.Etag, time.Now())
		if fresh, _ := h.Store.GetNode(ctx, existing.ID); fresh != nil {
			h.indexNode(ctx, fresh)
			h.dispatchThumb(fresh)
			return fresh, nil
		}
		return existing, nil
	}
	parentID, _ := h.ensureDirChain(ctx, st.ID, path.Dir(clean))
	node := &model.Node{
		StorageID:  st.ID,
		ParentID:   parentID,
		Name:       name,
		Path:       clean,
		PathHash:   hash,
		StorageKey: clean,
		Type:       model.NodeTypeFile,
		Size:       size,
		Mime:       mime,
		SyncState:  model.SyncStateSynced,
	}
	created, err := h.Store.CreateNode(ctx, node)
	if err != nil {
		return nil, err
	}
	h.indexNode(ctx, created)
	h.dispatchThumb(created)
	return created, nil
}

// EnsureDir makes sure a directory exists on the driver AND has a node row,
// returning its node id. The file-drop handler calls it to materialise a
// per-submission subfolder before ingesting files into it, so the owner sees
// the folder (and its parent link) immediately without waiting for a sync.
// Idempotent: returns the existing node id when the dir is already known.
func (h *Manager) EnsureDir(ctx context.Context, st *model.Storage, rel string) (*int64, error) {
	clean := normalizeDBPath(rel)
	if clean == "" || clean == "/" {
		return nil, fmt.Errorf("EnsureDir: empty path")
	}
	drv, err := h.StorageResolver(st.ID)
	if err != nil {
		return nil, err
	}
	if mk, ok := drv.(storage.Mkdirer); ok {
		// Best-effort — object stores have no real dirs; a placeholder or a
		// no-op is fine, the files written under the prefix stand on their own.
		_ = mk.Mkdir(ctx, strings.TrimPrefix(clean, "/"))
	}
	hash := pathkey.Hash(st.ID, clean)
	if existing, _ := h.Store.GetNodeByPath(ctx, st.ID, hash); existing != nil {
		id := existing.ID
		return &id, nil
	}
	parentID, _ := h.ensureDirChain(ctx, st.ID, path.Dir(clean))
	node := &model.Node{
		StorageID:  st.ID,
		ParentID:   parentID,
		Name:       path.Base(clean),
		Path:       clean,
		PathHash:   hash,
		StorageKey: clean,
		Type:       model.NodeTypeDirectory,
		SyncState:  model.SyncStateSynced,
	}
	created, err := h.Store.CreateNode(ctx, node)
	if err != nil {
		return nil, err
	}
	h.indexNode(ctx, created)
	id := created.ID
	return &id, nil
}
