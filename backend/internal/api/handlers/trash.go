// Package handlers — trash.go
//
// Endpoints:
//
//	GET  /api/files/manager/trash                          (auth)  list trashed
//	POST /api/files/manager/restore                        (auth)  body {node_id}
//	DELETE /api/admin/trash/{id}                           (admin) immediate single purge
//	POST /api/admin/trash/empty?older_than_days=N          (admin) start a batch purge
//	GET  /api/admin/trash/empty                            (admin) that purge's progress
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

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
	// EmptyWait is how long AdminEmpty waits for the purge it started before
	// answering 202 with the run's progress instead. Zero is defaultEmptyWait.
	EmptyWait time.Duration
}

// defaultEmptyWait is short enough to sit well inside every timeout in front
// of the endpoint — the admin page's HTTP client gives up at 30s, nginx at
// 60s, Cloudflare at 100s — and long enough that an ordinary trash is gone
// before the answer, which is then the final count, as it always was.
const defaultEmptyWait = 2 * time.Second

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
		// A row that records no original path (trash.OriginalPath, known=false)
		// cannot be placed inside anybody's root, so it is outside every one.
		orig, known := trash.OriginalPath(node)
		if !known || !root.Within(h.storageName(r.Context(), node.StorageID), orig) {
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
		// ⚠ Judged on where the file came from, never on `.filex-trash/…`: a
		// grant on the bin says nothing about somebody else's deleted file.
		orig, known := trash.OriginalPath(node)
		if !known || !aclAllowID(r.Context(), h.ACL, h.Store, node.StorageID, orig, acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
			return
		}
	}
	if err := h.Service.Restore(r.Context(), req.NodeID); err != nil {
		var conflict *trash.ConflictError
		if errors.As(err, &conflict) {
			// Nothing moved and the entry is still in the trash: the name was
			// taken, and a restore does not destroy what holds it.
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "something already exists at this path: " + path.Base(conflict.Path),
				"code":  "EXISTS",
				"name":  path.Base(conflict.Path),
				"path":  conflict.Path,
			})
			return
		}
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

// AdminEmpty starts "empty the trash now" and answers when it is over or when
// EmptyWait has passed, whichever comes first: 200 with the final counts, or
// 202 with the run's progress so far — which GET on the same path
// (EmptyStatus) keeps reporting until the run ends. 409 BUSY while another
// purge holds the trash; 400 for a request it cannot read.
//
// `older_than_days` and `storage_id` arrive in the query or the JSON body
// (the admin SPA's trashApi.empty posts {storage_id, older_than_days}).
// 0/missing days wipes everything currently soft-deleted; 0/missing storage
// is every storage the caller can reach.
//
// ⚠⚠ The purge used to run inside this request, and a large trash cannot be
// purged inside any request. 61,844 files needed the better part of an hour;
// nginx answered 504 at sixty seconds, the request's context was cancelled,
// and the purge died mid-batch — while the admin page, whose HTTP client had
// given up at thirty, showed nothing at all. Now the run belongs to the
// server (trash.Service.StartEmpty) and this request only watches it for a
// moment.
func (h *Trash) AdminEmpty(w http.ResponseWriter, r *http.Request) {
	older, storageID, err := emptyRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	run, err := h.Service.StartEmpty(r.Context(), older, storageID)
	if errors.Is(err, trash.ErrBusy) {
		// The second press of a button that seemed to do nothing. The caller's
		// own run comes back with the refusal, so the page can show it; a run
		// another tenant started is not described at all.
		body := map[string]any{"error": "the trash is already being emptied", "code": "BUSY"}
		if cur, ok := h.Service.LastEmpty(r.Context()); ok {
			if st := cur.Status(); st.Running {
				body["job"] = st
			}
		}
		writeJSON(w, http.StatusConflict, body)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	wait := h.EmptyWait
	if wait <= 0 {
		wait = defaultEmptyWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-run.Done():
	case <-timer.C:
	case <-r.Context().Done():
		return // the caller has gone; the run has not
	}
	st := run.Status()
	if !st.Running && st.Error != "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": st.Error})
		return
	}
	code := http.StatusOK
	if st.Running {
		code = http.StatusAccepted
	}
	writeJSON(w, code, emptyAnswer(st))
}

// EmptyStatus reports the caller's latest "empty the trash now": still
// running, or how it ended. `{"running": false}` alone when the caller's
// tenant has not started one since the server did.
func (h *Trash) EmptyStatus(w http.ResponseWriter, r *http.Request) {
	run, ok := h.Service.LastEmpty(r.Context())
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"running": false})
		return
	}
	writeJSON(w, http.StatusOK, emptyAnswer(run.Status()))
}

// emptyAnswer is a run's status, flattened beside the `ok` this endpoint has
// always answered with. The counters keep their old names (purged, failed,
// scanned, bytes), so a caller written for the synchronous endpoint still
// reads a finished run correctly.
func emptyAnswer(st trash.EmptyStatus) any {
	return struct {
		OK bool `json:"ok"`
		trash.EmptyStatus
	}{true, st}
}

// emptyRequest reads what AdminEmpty was asked to purge.
//
// ⚠ storage_id is read, not merely decoded: it was once parsed into a struct
// and dropped, while the admin UI's confirmation promised it narrowed the
// purge. Emptying "one storage" emptied all of them, permanently.
//
// ⚠⚠ And anything it cannot read is an error, never a default. The body used
// to be decoded into one struct and, on any decode error, ignored whole — so
// {"storage_id":2,"older_than_days":""}, what the admin page sent once its
// days box had been typed in and cleared, lost the storage_id along with the
// bad field and emptied every storage the caller could reach. A negative day
// count, or a storage_id that was not a number, was dropped the same way and
// meant "everything"; so did a misspelt field. A narrowing that cannot be
// read stops this request instead of widening it.
func emptyRequest(r *http.Request) (olderThanDays int, storageID int64, err error) {
	const badStorage = "storage_id must be a storage id"
	const badDays = "older_than_days must be a whole number of days, 0 or more"
	q := r.URL.Query()
	if v := q.Get("storage_id"); v != "" {
		if storageID, err = strconv.ParseInt(v, 10, 64); err != nil || storageID < 0 {
			return 0, 0, errors.New(badStorage)
		}
	}
	if v := q.Get("older_than_days"); v != "" {
		if olderThanDays, err = strconv.Atoi(v); err != nil || olderThanDays < 0 {
			return 0, 0, errors.New(badDays)
		}
	}
	if r.Body == nil || r.ContentLength == 0 {
		return olderThanDays, storageID, nil
	}
	var body struct {
		OlderThanDays *int   `json:"older_than_days"`
		StorageID     *int64 `json:"storage_id"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			return olderThanDays, storageID, nil // an empty body of unknown length
		}
		return 0, 0, errors.New("bad json: " + err.Error())
	}
	if body.StorageID != nil {
		if *body.StorageID < 0 {
			return 0, 0, errors.New(badStorage)
		}
		storageID = *body.StorageID
	}
	if body.OlderThanDays != nil {
		if *body.OlderThanDays < 0 {
			return 0, 0, errors.New(badDays)
		}
		olderThanDays = *body.OlderThanDays
	}
	return olderThanDays, storageID, nil
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
	// Tenancy: only surface trashed nodes from storages the caller's tenant can
	// reach.
	//
	// ⚠ This is a DIFFERENT filter from the confine one below, and conflating
	// the two is what left the listing instance-wide: the comment on that block
	// used to claim "so a tenant never sees another tenant's deleted files",
	// but `confine.RootFrom` answers about the EMBEDDED root (an X-Filex-Root
	// header or a `root:`-scoped token), and an ordinary browser login has
	// none — so the block was skipped and nothing filtered. Tenancy and root
	// confinement are two independent boundaries; the code claimed one while
	// implementing the other.
	//
	// `total` is recomputed the same way the two filters below do it, so the
	// count never describes rows that were not returned.
	if scope, confined := confinedScope(r.Context()); confined {
		kept := entries[:0]
		for _, e := range entries {
			if scope.CanAccessStorage(e.StorageID) {
				kept = append(kept, e)
			}
		}
		entries = kept
		total = len(kept)
	}
	// Confinement: only surface trashed nodes whose original path is inside
	// the caller's root — the embedded client's boundary, orthogonal to the
	// tenant one above.
	//
	// ⚠ Both filters below read `e.Path`, which Service.List resolves through
	// trash.OriginalPath. An entry whose path is still a trash key records no
	// original path, so neither filter has a subject to judge and it is not
	// shown: a grant on `.filex-trash/` would otherwise surface another
	// account's deleted file, and the restore refuses the row anyway.
	if root, ok := confine.RootFrom(r.Context()); ok {
		kept := entries[:0]
		for _, e := range entries {
			if !trash.IsTrashPath(e.Path) && root.Within(e.StorageName, e.Path) {
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
			if !trash.IsTrashPath(e.Path) && aclAllowName(r.Context(), h.ACL, h.Store, e.StorageName, e.Path, acl.LevelViewer) {
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
	if !ownsNode(w, r, h.Store, id, "trash entry") {
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
