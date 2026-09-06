// Package handlers — trash.go
//
// Endpoints:
//
//	GET  /api/files/manager/trash                          (auth)  list trashed
//	POST /api/files/manager/restore                        (auth)  body {node_id}
//	DELETE /api/admin/trash/{id}                           (admin) immediate single purge
//	POST /api/admin/trash/empty?older_than_days=N          (admin) immediate batch purge
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// Trash wires trash retention HTTP routes.
type Trash struct {
	Service *trash.Service
	Store   db.Store
	ACL     *acl.Resolver
	// Index puts a restored file back into search. Optional; nil skips it.
	Index *search.Index
}

// AttachSearchIndex wires the search index. ⚠ Deleting a file removes its
// document from the index (correctly). Restoring it never put the document
// back, so a restored file was in the listing, on the storage, and
// unfindable — and looked findable anyway, because a name search falls back
// to a SQL LIKE over node rows.
func (h *Trash) AttachSearchIndex(i *search.Index) { h.Index = i }

// NewTrash constructs the handler.
func NewTrash(svc *trash.Service, store db.Store) *Trash { return &Trash{Service: svc, Store: store} }

// AttachACL wires the RBAC resolver so the trash list is filtered to nodes the
// caller may see and restore requires ≥editor on the node's original path.
func (h *Trash) AttachACL(r *acl.Resolver) { h.ACL = r }

// storageName resolves a storage id → its adapter name (for confinement checks).
func (h *Trash) storageName(ctx context.Context, id int64) string {
	if h.Store == nil {
		return ""
	}
	if all, err := h.Store.ListStorages(ctx); err == nil {
		for _, st := range all {
			if st.ID == id {
				return st.Name
			}
		}
	}
	return ""
}

type restoreNodeReq struct {
	NodeID int64 `json:"node_id"`
}

// Restore lifts the deleted_at flag on a soft-deleted node.
func (h *Trash) Restore(w http.ResponseWriter, r *http.Request) {
	var req restoreNodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing node_id"})
		return
	}
	// Confinement: a root-locked caller may only restore nodes whose original
	// path lives inside its root (else it could resurrect another tenant's file).
	if root, ok := confine.RootFrom(r.Context()); ok {
		node, err := h.Store.GetNode(r.Context(), req.NodeID)
		if err != nil || node == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trash entry not found"})
			return
		}
		orig := node.StorageKey
		if orig == "" {
			orig = node.Path
		}
		if !root.Within(h.storageName(r.Context(), node.StorageID), orig) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "path outside confined root"})
			return
		}
	}
	// RBAC: restoring writes the file back → require ≥editor on its original path.
	if h.ACL != nil {
		node, err := h.Store.GetNode(r.Context(), req.NodeID)
		if err != nil || node == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trash entry not found"})
			return
		}
		orig := node.StorageKey
		if orig == "" {
			orig = node.Path
		}
		if !aclAllowID(r.Context(), h.ACL, h.Store, node.StorageID, orig, acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
			return
		}
	}
	if err := h.Service.Restore(r.Context(), req.NodeID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.announceRestore(r.Context(), req.NodeID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// announceRestore re-indexes the restored node — and, for a folder, every
// cached descendant, since deleting it dropped the whole subtree from the
// index — enqueues a virus scan for every file coming back, then tells the
// folder it landed back in.
//
// Read AFTER the restore on purpose: the row's path is rewritten from
// `.filex-trash/…` back to the original by Service.Restore, and indexing the
// pre-restore value would file the document under a path that no longer
// exists. The scan needs the post-restore row for the same reason, and for
// one more: queue's Eligible() refuses anything still soft-deleted or still
// sitting under `.filex-trash/`, which is every row before Restore ran.
//
// ⚠⚠ Why restore scans at all. The trash is where quarantine puts an infected
// file — the antivirus job and a user deletion produce the identical row — so
// "restore from trash" includes "release a file ClamAV condemned". It is also
// the one way bytes go live in filex without passing an upload surface: they
// were scanned when they arrived, but the signature database has moved on, and
// a file that was clean in March is not necessarily clean today. Same
// asynchronous contract as an upload: live first, verdict shortly after; an
// infected verdict quarantines it straight back.
func (h *Trash) announceRestore(ctx context.Context, nodeID int64) {
	node, err := h.Store.GetNode(ctx, nodeID)
	if err != nil || node == nil {
		return
	}
	sy := protocolsync.New(h.Store, h.Index, nil, "")
	for _, n := range sy.CollectSubtree(ctx, node.StorageID, node) {
		sy.IndexNode(ctx, n)
		// A restored FOLDER brings its whole subtree back, and each file in it
		// is as live and as unverified as the folder row itself. Scanning only
		// the row the user clicked would protect the one case and miss the one
		// that carries more files.
		enqueueAntivirusScan(ctx, n)
	}
	emitFolderChange(node.StorageID, path.Dir(node.Path), realtime.ChangeEvent{
		Action: "create", Name: node.Name,
	})
}

// AdminEmpty triggers an immediate purge.
//
// `older_than_days` may also arrive in the JSON body (the admin SPA's
// trashApi.empty posts {storage_id, older_than_days}). 0/missing wipes
// everything currently soft-deleted.
func (h *Trash) AdminEmpty(w http.ResponseWriter, r *http.Request) {
	older := 0
	if v := r.URL.Query().Get("older_than_days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			older = n
		}
	}
	// Accept the same field as a JSON body too (frontend uses POST body).
	if r.Body != nil && r.ContentLength > 0 {
		var body struct {
			OlderThanDays *int   `json:"older_than_days"`
			StorageID     *int64 `json:"storage_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if body.OlderThanDays != nil && *body.OlderThanDays >= 0 {
				older = *body.OlderThanDays
			}
		}
	}
	res, err := h.Service.EmptyOlderThan(r.Context(), older)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Frontend reads `purged` count.
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"purged":  res.Deleted,
		"failed":  res.Failed,
		"scanned": res.Scanned,
		"bytes":   res.Bytes,
	})
}

// List returns soft-deleted nodes for the admin trash view.
//
// Query: ?storage_id=…&limit=…&offset=…
func (h *Trash) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var storagePtr *int64
	if v := q.Get("storage_id"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			storagePtr = &n
		}
	}
	limit := 50
	offset := 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	entries, total, err := h.Service.List(r.Context(), storagePtr, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Confinement: only surface trashed nodes whose original path is inside
	// the caller's root, so a tenant never sees another tenant's deleted files.
	if root, ok := confine.RootFrom(r.Context()); ok {
		kept := entries[:0]
		for _, e := range entries {
			if root.Within(e.StorageName, e.Path) {
				kept = append(kept, e)
			}
		}
		entries = kept
		total = len(kept)
	}
	// RBAC: only surface trashed nodes the caller may see.
	if h.ACL != nil {
		kept := entries[:0]
		for _, e := range entries {
			if aclAllowName(r.Context(), h.ACL, h.Store, e.StorageName, e.Path, acl.LevelViewer) {
				kept = append(kept, e)
			}
		}
		entries = kept
		total = len(kept)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// Purge hard-deletes a single trashed node by id.
//
// DELETE /api/admin/trash/{id}
func (h *Trash) Purge(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.Service.PurgeOne(r.Context(), id); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no rows in result set") || strings.Contains(msg, "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trash entry not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
