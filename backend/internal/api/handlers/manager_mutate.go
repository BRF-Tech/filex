package handlers

import (
	"bytes"
	"context"
	"crypto/md5"
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

	"gitlab.com/brftech/filemanager/backend/internal/model"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
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
		hash := managerPathHash(current.ID, clean)
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
			if _, err := h.Store.CreateNode(r.Context(), n); err != nil {
				slog.Warn("manager: newfolder db create",
					slog.String("path", clean),
					slog.String("err", err.Error()))
			}
		}
	}

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
	if srcRel == "" || strings.Contains(srcRel, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item path"})
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

	for _, it := range body.Items {
		srcAdapter, srcRel := splitAdapterPath(it.Path)
		if srcAdapter != "" && srcAdapter != current.Name {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "move across adapters not supported: " + it.Path})
			return
		}
		if srcRel == "" || strings.Contains(srcRel, "..") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item path: " + it.Path})
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
	mover, _ := drv.(storage.Mover)

	for _, it := range body.Items {
		srcAdapter, srcRel := splitAdapterPath(it.Path)
		if srcAdapter != "" && srcAdapter != current.Name {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "delete across adapters not supported: " + it.Path})
			return
		}
		if srcRel == "" || strings.Contains(srcRel, "..") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item path: " + it.Path})
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
			hash := managerPathHash(current.ID, origClean)
			if existing, err := h.Store.GetNodeByPathIncludingDeleted(r.Context(), current.ID, hash); err == nil && existing != nil {
				_ = h.Store.HardDeleteNode(r.Context(), existing.ID)
			}
			continue
		}

		// Soft delete: rename to `.filex-trash/<unix>-<rand>__<basename>`.
		base := path.Base(srcRel)
		if base == "" || base == "." || base == "/" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item base: " + it.Path})
			return
		}
		trashRel := fmt.Sprintf("%s/%d-%s__%s", trashPrefix, time.Now().Unix(), randHex6(), base)

		if mover == nil {
			// Driver can't move — fall back to hard delete (legacy).
			if del, ok := drv.(storage.Deleter); ok {
				if err := del.Delete(r.Context(), srcRel); err != nil && !errors.Is(err, storage.ErrNotFound) {
					writeJSON(w, mapDriverErr(err), map[string]string{"error": "delete: " + err.Error()})
					return
				}
			}
		} else {
			if err := mover.Move(r.Context(), srcRel, trashRel); err != nil {
				writeJSON(w, mapDriverErr(err), map[string]string{"error": "trash: " + err.Error()})
				return
			}
		}

		// Update DB: store the original path in storage_key so Restore
		// can find it; flip deleted_at; rewrite path/path_hash to the
		// trash location so a fresh upload at the original path works.
		origClean := normalizeDBPath(srcRel)
		origHash := managerPathHash(current.ID, origClean)
		if existing, err := h.Store.GetNodeByPath(r.Context(), current.ID, origHash); err == nil && existing != nil {
			if mover != nil {
				newClean := normalizeDBPath(trashRel)
				newHash := managerPathHash(current.ID, newClean)
				_ = h.Store.SoftDeleteAndRetag(r.Context(), existing.ID, newClean, newHash, origClean)
			} else {
				_ = h.Store.SoftDeleteNode(r.Context(), existing.ID)
			}
		}
	}

	h.vfIndex(w, r, current, parentRel, storageNames, false)
}

// trashPrefix is the in-storage directory where soft-deleted files are
// renamed to. Listings filter it out; trash.Service.Restore renames out.
const trashPrefix = ".filex-trash"

// randHex6 returns a 6-char lowercase hex string for trash key uniqueness.
func randHex6() string {
	var b [3]byte
	_, _ = cryptoRead(b[:])
	return hex.EncodeToString(b[:])
}

// vfUpload accepts multipart/form-data with one or more file[] parts
// and writes each into the destination dir on the backing driver.
//
// Limits: 32 MiB in-memory body (per ParseMultipartForm); larger
// uploads should use the chunked /api/files/upload/init flow which
// hands out S3 presigned URLs directly to the browser.
func (h *Manager) vfUpload(w http.ResponseWriter, r *http.Request) {
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

	parentID, parentLookupErr := h.lookupDirID(r.Context(), current.ID, destRel)

	for _, fh := range files {
		name := path.Base(fh.Filename)
		if name == "" || name == "." || name == "/" || strings.ContainsAny(name, "\\") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad upload filename"})
			return
		}
		fullRel := path.Join(destRel, name)

		src, err := fh.Open()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "open part: " + err.Error()})
			return
		}

		// Sniff the first 512 bytes for mime detection, then prepend
		// them back so the driver receives the full payload.
		var sniff [512]byte
		n, _ := io.ReadFull(src, sniff[:])
		mime := ""
		if n > 0 {
			mime = http.DetectContentType(sniff[:n])
		}
		merged := io.MultiReader(bytes.NewReader(sniff[:n]), src)

		if err := wr.Write(r.Context(), fullRel, merged, fh.Size); err != nil {
			_ = src.Close()
			writeJSON(w, mapDriverErr(err), map[string]string{"error": "write: " + err.Error()})
			return
		}
		_ = src.Close()

		if parentLookupErr != nil {
			continue
		}
		clean := normalizeDBPath(fullRel)
		hash := managerPathHash(current.ID, clean)
		if existing, _ := h.Store.GetNodeByPath(r.Context(), current.ID, hash); existing != nil {
			_ = h.Store.UpdateNodeMeta(r.Context(), existing.ID, fh.Size, mime, existing.Etag, time.Now())
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
		if _, err := h.Store.CreateNode(r.Context(), n2); err != nil {
			slog.Warn("manager: upload db create",
				slog.String("path", clean),
				slog.String("err", err.Error()))
		}
	}

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
	if strings.Contains(rel, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path"})
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
	hash := managerPathHash(storageID, normalizeDBPath(rel))
	n, err := h.Store.GetNodeByPath(ctx, storageID, hash)
	if err != nil || n == nil {
		// Walk the parent chain — the DB might lag the driver.
		return h.walkDirID(ctx, storageID, rel)
	}
	id := n.ID
	return &id, nil
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
	srcHash := managerPathHash(storageID, srcClean)
	dstHash := managerPathHash(storageID, dstClean)

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
func normalizeDBPath(rel string) string {
	rel = strings.Trim(rel, "/")
	clean := path.Clean("/" + rel)
	return strings.TrimRight(clean, "/")
}

// managerPathHash mirrors sync.pathHash so the manager handler reads
// the same cache rows the sync worker writes.
func managerPathHash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
}
