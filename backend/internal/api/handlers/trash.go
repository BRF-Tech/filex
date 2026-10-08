// Package handlers — trash.go
//
// Endpoints:
//
//	GET  /api/files/manager/trash                          (auth)  list trashed (+ totals)
//	POST /api/files/manager/restore                        (auth)  body {node_id} or {node_ids}
//	DELETE /api/admin/trash/{id}                           (admin) immediate single purge
//	POST /api/admin/trash/purge                            (admin) body {node_ids}: purge a batch
//	POST /api/admin/trash/empty?older_than_days=N          (admin) start a batch purge
//	GET  /api/admin/trash/empty                            (admin) that purge's progress
//	GET  /api/admin/trash/empty/preview                    (admin) what that purge would delete
package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// Trash wires trash retention HTTP routes.
type Trash struct {
	Service *trash.Service
	Store   db.Store
	ACL     *acl.Resolver
	// Index puts a restored file back into search. Optional; nil skips it.
	Index *search.Index
	// Ops is the queue "empty the trash now" runs on (ops.SubmitTrashEmpty).
	// Without it AdminEmpty answers 503, like every queued operation.
	Ops *ops.Service
	// EmptyWait is how long AdminEmpty waits for the purge it started before
	// answering 202 with the run's progress instead. Zero is defaultEmptyWait.
	EmptyWait time.Duration
}

// AttachOps wires the queue "empty the trash now" runs on.
func (h *Trash) AttachOps(o *ops.Service) { h.Ops = o }

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
	// NodeIDs is the batch a restore asked with `queued=1` carries.
	NodeIDs []int64 `json:"node_ids,omitempty"`
}

// Restore lifts the deleted_at flag on a soft-deleted node.
//
// Asked with `queued=1` it takes a batch (`node_ids`), judges every entry the
// way it judges one, and queues them as a job of the operations queue instead:
// 202 `{ops}`, one job per storage (restoreQueued).
func (h *Trash) Restore(w http.ResponseWriter, r *http.Request) {
	var req restoreNodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if h.Ops != nil && r.URL.Query().Get("queued") == "1" {
		h.restoreQueued(w, r, req)
		return
	}
	if len(req.NodeIDs) > 0 {
		h.restoreBatch(w, r, req.NodeIDs)
		return
	}
	if req.NodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing node_id"})
		return
	}
	if _, ok := h.mayRestore(w, r, req.NodeID); !ok {
		return
	}
	// A folder on an object store comes back one object at a time: finished
	// even if the client leaves (detachedMutation).
	ctx, cancel := detachedMutation(r.Context())
	defer cancel()
	if err := h.Service.Restore(ctx, req.NodeID); err != nil {
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
		if errors.Is(err, trash.ErrNotInTrash) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trash entry not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.announceRestore(ctx, req.NodeID)
	if n, err := h.Store.GetNode(ctx, req.NodeID); err == nil && n != nil {
		auth.SetAuditTarget(ctx, strconv.FormatInt(n.ID, 10), n.Path)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// mayRestore judges one trash entry for the caller: the checks Restore asks,
// which a queued batch asks of every entry before it queues any. node is nil
// when the row could not be read and no check needed it; Restore then answers
// what the service says. A refusal is written, and ok is false.
func (h *Trash) mayRestore(w http.ResponseWriter, r *http.Request, id int64) (node *model.Node, ok bool) {
	// Tenancy first, as Purge asks it: an entry of another tenant's storage is
	// a trash entry that does not exist. Nothing below asked — the ACL is
	// tenant-blind, and on a storage without access control it answers editor
	// for anybody — so a member of one customer could bring another's deleted
	// file back by id, and a queued batch brought back a whole list of them.
	if !ownsNode(w, r, h.Store, id, "trash entry") {
		return nil, false
	}
	if n, err := h.Store.GetNode(r.Context(), id); err == nil {
		node = n
	}
	// A row deleted where it stood holds nothing in the trash (issue #74):
	// the list does not offer it, and restoring it by id is the same "not
	// found" — not a permission question.
	if trash.Vanished(node) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "trash entry not found"})
		return nil, false
	}
	// A swept open-with working copy (or anything else whose original path
	// is filex's own) is not offered by the trash list, and restoring it by
	// id would put it back where nobody can see it — the same "not found"
	// the list implies.
	//
	// The one exception is a discarded draft of the caller's OWN (issue #71):
	// restoring it puts it back in their drafts folder, and it is a draft
	// again (syspath.OwnDraft). Anybody else's reads as not found.
	if node != nil {
		uid := currentUserID(r.Context())
		if orig, known := trash.OriginalPath(node); known && syspath.Hidden(orig) && !syspath.IsDraftOf(orig, uid) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trash entry not found"})
			return nil, false
		} else if known && gate(w, r, h.ACL, node.StorageID, writegate.Writes(orig).As(syspath.OwnDraft).By(uid)) {
			// Restoring writes the file back: not onto a path an app has
			// frozen. Asked before the permission check below, which would
			// read the lock's viewer cap as a plain 403.
			return nil, false
		}
	}
	// Confinement: a root-locked caller may only restore nodes whose original
	// path lives inside its root (else it could resurrect another tenant's file).
	if root, confined := confine.RootFrom(r.Context()); confined {
		if node == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trash entry not found"})
			return nil, false
		}
		// A row that records no original path (trash.OriginalPath, known=false)
		// cannot be placed inside anybody's root, so it is outside every one.
		orig, known := trash.OriginalPath(node)
		if !known || !root.Within(h.storageName(r.Context(), node.StorageID), orig) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "path outside confined root"})
			return nil, false
		}
	}
	// RBAC: restoring writes the file back → require ≥editor on its original path.
	if h.ACL != nil {
		if node == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trash entry not found"})
			return nil, false
		}
		// ⚠ Judged on where the file came from, never on `.filex-trash/…`: a
		// grant on the bin says nothing about somebody else's deleted file.
		orig, known := trash.OriginalPath(node)
		if !known {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
			return nil, false
		}
		if v := aclCanID(r.Context(), h.ACL, h.Store, node.StorageID, orig, perm.FilesCreate); !v.ok {
			if !v.WritePerm(w, r) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
			}
			return nil, false
		}
	}
	return node, true
}

// maxRestoreBatch is how many entries one queued restore may name. Every
// entry is judged inside the request, a few lookups each, and the batch is one
// row of the queue: an unbounded list was an unbounded request.
const maxRestoreBatch = 1000

// restoreQueued is Restore asked with `queued=1`. Every entry is judged before
// anything is queued, so a batch is never half-allowed; what it allows is one
// job per storage, answered 202 `{ops}`. A place that is taken is found by the
// job and reported on its row.
func (h *Trash) restoreQueued(w http.ResponseWriter, r *http.Request, req restoreNodeReq) {
	ids := req.NodeIDs
	if len(ids) == 0 && req.NodeID > 0 {
		ids = []int64{req.NodeID}
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing node_ids"})
		return
	}
	if len(ids) > maxRestoreBatch {
		writeError(w, r, http.StatusBadRequest, "too_many", apierr.Params{"max": strconv.Itoa(maxRestoreBatch)}, "code", "TOO_MANY", "max", maxRestoreBatch)
		return
	}
	seen := make(map[int64]bool, len(ids))
	byStorage := map[int64][]string{}
	var order []int64
	var taken []*model.Node
	for _, id := range ids {
		if id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad node id"})
			return
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		node, ok := h.mayRestore(w, r, id)
		if !ok {
			return
		}
		if node == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trash entry not found"})
			return
		}
		if _, known := byStorage[node.StorageID]; !known {
			order = append(order, node.StorageID)
		}
		byStorage[node.StorageID] = append(byStorage[node.StorageID], strconv.FormatInt(id, 10))
		taken = append(taken, node)
	}
	queued := make([]*ops.Op, 0, len(order))
	for _, storageID := range order {
		op, err := h.Ops.Submit(r.Context(), ops.OpRestore, storageID, byStorage[storageID], "")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "restore: " + err.Error()})
			return
		}
		queued = append(queued, op)
	}
	auditRestored(r.Context(), taken)
	// What is on its way, said (ops.SayTrashBatch): the explorer and an agent
	// show this sentence; how each job ended is its row's own `summary`.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ops":     queued,
		"done":    len(taken),
		"failed":  0,
		"summary": ops.SayTrashBatch(trashLang(r), ops.OpRestore, true, len(taken), 0, "", ""),
	})
}

// trashLang is the language a trash answer's sentences are in: the language
// on the screen that asked (`?lang=`, which the explorer sends), else the
// account's, else the browser's (requestLang).
func trashLang(r *http.Request) string { return requestLang(r, r.URL.Query().Get("lang")) }

// trashBatch is the answer of a batch restore or purge: how many went (or,
// queued, are on their way), how many did not, why the first of those did not
// (an ops.Reason* code), and the server's sentence for all of it in the
// reader's language - what every surface shows (findings A15: the explorer
// sent one request per entry and composed this itself).
type trashBatch struct {
	Done       int    `json:"done"`
	Failed     int    `json:"failed"`
	ReasonCode string `json:"reason_code,omitempty"`
	// Taken names the entries a restore found their place taken for.
	Taken   []string  `json:"taken,omitempty"`
	Queued  bool      `json:"queued,omitempty"`
	Ops     []*ops.Op `json:"ops,omitempty"`
	Summary string    `json:"summary"`
	// name is the entry the first failure names (a taken place), for the
	// sentence only.
	name string
}

// fail counts one entry that did not go; the first one's reason is the batch's.
func (b *trashBatch) fail(reason, name string) {
	b.Failed++
	if b.ReasonCode == "" {
		b.ReasonCode, b.name = reason, name
	}
}

// judged is a ResponseWriter that keeps a refusal instead of sending it: a
// batch judges each entry with the one-entry checks (mayRestore, ownsNode),
// which write their refusal, and reports what they refused as a count and a
// reason code instead of ending the request.
type judged struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (j *judged) Header() http.Header {
	if j.header == nil {
		j.header = http.Header{}
	}
	return j.header
}

func (j *judged) Write(b []byte) (int, error) {
	if j.status == 0 {
		j.status = http.StatusOK
	}
	return j.body.Write(b)
}

func (j *judged) WriteHeader(code int) {
	if j.status == 0 {
		j.status = code
	}
}

// reason is the refusal as an ops.Reason* code.
func (j *judged) reason() string {
	switch j.status {
	case http.StatusNotFound:
		return ops.ReasonNotFound
	case http.StatusForbidden, http.StatusUnauthorized:
		return ops.ReasonForbidden
	case http.StatusConflict:
		return ops.ReasonExists
	}
	return ops.ReasonFailed
}

// restoreBatch is Restore asked with `node_ids` and no queue: every entry is
// judged and restored on its own, inside the request, and the answer says how
// many came back, how many did not and why - `{done, failed, reason_code,
// taken, summary}`, 200 whatever the mix. The explorer's Restore and its Undo
// of a delete send one of these instead of one request per entry.
func (h *Trash) restoreBatch(w http.ResponseWriter, r *http.Request, ids []int64) {
	if len(ids) > maxRestoreBatch {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("at most %d entries per restore", maxRestoreBatch),
			"code":  "TOO_MANY", "max": maxRestoreBatch,
		})
		return
	}
	// Finished even if the client leaves (detachedMutation), like one entry.
	ctx, cancel := detachedMutation(r.Context())
	defer cancel()
	var ans trashBatch
	var restored []*model.Node
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if id <= 0 {
			ans.fail(ops.ReasonNotFound, "")
			continue
		}
		j := &judged{}
		node, ok := h.mayRestore(j, r, id)
		if !ok {
			ans.fail(j.reason(), "")
			continue
		}
		if node == nil {
			// No row to restore (restoreQueued refuses it the same way).
			ans.fail(ops.ReasonNotFound, "")
			continue
		}
		if err := h.Service.Restore(ctx, id); err != nil {
			var conflict *trash.ConflictError
			switch {
			case errors.As(err, &conflict):
				name := path.Base(conflict.Path)
				ans.Taken = append(ans.Taken, name)
				ans.fail(ops.ReasonExists, name)
			case errors.Is(err, trash.ErrNotInTrash), errors.Is(err, sql.ErrNoRows):
				ans.fail(ops.ReasonNotFound, "")
			default:
				slog.Warn("trash: restore failed", slog.Int64("node", id), slog.String("err", err.Error()))
				ans.fail(ops.ReasonFailed, "")
			}
			continue
		}
		ans.Done++
		h.announceRestore(ctx, id)
		restored = append(restored, node)
	}
	auditRestored(r.Context(), restored)
	ans.Summary = ops.SayTrashBatch(trashLang(r), ops.OpRestore, false, ans.Done, ans.Failed, ans.ReasonCode, ans.name)
	writeJSON(w, http.StatusOK, ans)
}

// auditRestored names the entries a queued restore took, on the request's
// `file.restore` audit row — the way the synchronous restore names its one.
//
// ⚠ It named nothing: the route carries no id, and the queued answer went out
// before anything had been restored, so the row said "a restore" and not of
// what. One entry is named by its id and original path, a batch by its paths
// (the first few, and how many more) with every id in the row's detail.
func auditRestored(ctx context.Context, nodes []*model.Node) {
	if len(nodes) == 0 {
		return
	}
	const named = 5
	ids := make([]int64, 0, len(nodes))
	paths := make([]string, 0, min(len(nodes), named))
	for _, n := range nodes {
		ids = append(ids, n.ID)
		if len(paths) < named {
			orig, _ := trash.OriginalPath(n)
			paths = append(paths, orig)
		}
	}
	if len(nodes) == 1 {
		auth.SetAuditTarget(ctx, strconv.FormatInt(ids[0], 10), paths[0])
		return
	}
	label := strings.Join(paths, ", ")
	if more := len(nodes) - len(paths); more > 0 {
		label += fmt.Sprintf(" (+%d)", more)
	}
	auth.SetAuditTarget(ctx, "", label)
	auth.AddAuditDetail(ctx, "node_ids", ids)
}

// errNotThisJobsEntry is a queued restore's or purge's entry that does not
// live in the storage the job was queued for — in the words a missing entry
// gets.
//
// ⚠ Coded (apierr): the queue row keeps `not_in_trash` and says it in its
// reader's language when it is read, not this English.
var errNotThisJobsEntry = apierr.New("not_in_trash", nil, errors.New("trash entry not found"))

// queuedEntry reads the trash entry a queued job names and refuses one that
// is not in the job's storage.
//
// ⚠ The worker has no request and no caller to judge: what it holds is the
// row, and the storage the handler judged the entries on when it queued them
// (restoreQueued, Purge). An id that points anywhere else did not come from
// that judgement — the generic queue endpoint once passed a client's `restore`
// straight through — and is not this job's to touch.
func (h *Trash) queuedEntry(ctx context.Context, storageID, nodeID int64) (*model.Node, error) {
	n, err := h.Store.GetNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if n == nil || n.StorageID != storageID {
		return nil, errNotThisJobsEntry
	}
	return n, nil
}

// RestoreNode implements ops.Restorer: Restore's work for a queued restore.
// The bytes and the row come back, and the node is re-indexed, scanned and
// announced (announceRestore). The entry was judged when it was queued
// (RestoreQueued) on storageID, and one of any other storage is refused
// (queuedEntry). A place that is taken is an error in the words Restore
// answers with, and nothing of that entry moves.
func (h *Trash) RestoreNode(ctx context.Context, storageID, nodeID int64) error {
	if _, err := h.queuedEntry(ctx, storageID, nodeID); err != nil {
		return err
	}
	if err := h.Service.Restore(ctx, nodeID); err != nil {
		var conflict *trash.ConflictError
		if errors.As(err, &conflict) {
			// Coded (apierr): the queue row keeps `name_taken` and the name, and
			// is said in its reader's language (ops errcode.go sayErrors, say.go
			// rowReason); the English, ops.TakenPrefix and the name, stays the
			// detail.
			name := path.Base(conflict.Path)
			return apierr.New("name_taken", apierr.Params{"name": name}, errors.New(ops.TakenPrefix+name))
		}
		return err
	}
	h.announceRestore(ctx, nodeID)
	return nil
}

// PurgeNode implements ops.Purger: Purge's work for a queued purge. The entry
// was judged when it was queued (Purge: ownsNode) on storageID, and one of any
// other storage is refused (queuedEntry).
//
// An entry that is no longer there at all is DONE, not failed: what a purge
// is for has happened. That is the shape of a purge requeued at boot after
// the process died past HardDeleteNode, and of two administrators purging the
// same entry — both of which ended `failed` with a bare "sql: no rows in
// result set" on the second run.
func (h *Trash) PurgeNode(ctx context.Context, storageID, nodeID int64) error {
	_, err := h.queuedEntry(ctx, storageID, nodeID)
	if err == nil {
		err = h.Service.PurgeOne(ctx, nodeID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
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
// (EmptyStatus) keeps reporting until the run ends. 409 BUSY while the
// caller's tenant already has one going (with that run as `job`); 400 for a
// request it cannot read.
//
// `older_than_days` and `storage_id` arrive in the query or the JSON body
// (the admin SPA's trashApi.empty posts {storage_id, older_than_days}).
// 0/missing days wipes everything soft-deleted when it is asked; 0/missing
// storage is every storage the caller can reach.
//
// ⚠⚠ The purge used to run inside this request, and a large trash cannot be
// purged inside any request. 61,844 files needed the better part of an hour;
// nginx answered 504 at sixty seconds, the request's context was cancelled,
// and the purge died mid-batch — while the admin page, whose HTTP client had
// given up at thirty, showed nothing at all. The run is an ops job now
// (ops.SubmitTrashEmpty): in the operations centre and the admin tray,
// cancellable like every op, its tenant's alone, resumed after a restart —
// and this request only watches it for a moment.
func (h *Trash) AdminEmpty(w http.ResponseWriter, r *http.Request) {
	older, storageID, err := emptyRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if h.Ops == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	ctx := r.Context()
	lang := trashLang(r)
	op, err := h.Ops.SubmitTrashEmpty(ctx, ops.TrashEmptyRequest{
		StorageID:     storageID,
		OlderThanDays: older,
		Reach:         trash.Reach(ctx),
		Tenant:        ops.TenantKey(ctx),
	})
	if errors.Is(err, ops.ErrTrashBusy) {
		// The second press of a button that seemed to do nothing: told what
		// is already happening, with the run to follow — its own tenant's,
		// by construction; another tenant's run never refuses this one.
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "the trash is already being emptied",
			"code":    "BUSY",
			"job":     emptyStatusOf(op, lang),
			"message": srvtext.Text(lang, "server.trash.empty.busy", nil),
		})
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
	if done := h.Ops.TrashEmptyDone(op.ID); done != nil {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		case <-ctx.Done():
			return // the caller has gone; the run has not
		}
	}
	if cur, err := h.Ops.Get(ctx, op.ID); err == nil {
		op = cur
	}
	st := emptyStatusOf(op, lang)
	if st.Error != "" {
		// `error` stays the run's own (English) record for an operator's
		// second line; `message` and `summary` are what a person is shown -
		// never "504", never "see the server log" (finding A4).
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": st.Error, "message": st.Summary, "summary": st.Summary, "job": st,
		})
		return
	}
	code := http.StatusOK
	if st.Running {
		code = http.StatusAccepted
	}
	writeJSON(w, code, st)
}

// EmptyStatus reports the caller's tenant's latest "empty the trash now":
// queued, running, or how it ended. `{"running": false}` alone when it has
// asked for none.
func (h *Trash) EmptyStatus(w http.ResponseWriter, r *http.Request) {
	if h.Ops == nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false})
		return
	}
	op, err := h.Ops.LatestTrashEmpty(r.Context(), ops.TenantKey(r.Context()))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if op == nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false})
		return
	}
	writeJSON(w, http.StatusOK, emptyStatusOf(op, trashLang(r)))
}

// EmptyPreview is "empty the trash" asked as a dry run: what POST
// /api/admin/trash/empty would delete for the same `storage_id` and
// `older_than_days`, counted by the purge's own Tally over the caller's own
// Reach - `{dry_run: true, count, bytes, summary}`, nothing deleted.
//
// ⚠⚠ Why (finding D1, 0.54). The explorer's confirmation said "This
// permanently deletes 50 items (12 MB)" - the first page of the listing,
// counted in the browser - and the purge then took the whole trash: every
// entry of every storage the caller reaches, other people's deletes and the
// desktop app's swept working copies included, 61,844 items on one install.
// The last screen before an irreversible delete names the server's number.
func (h *Trash) EmptyPreview(w http.ResponseWriter, r *http.Request) {
	older, storageID, err := emptyRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "trash unavailable"})
		return
	}
	ctx := r.Context()
	n, b, err := h.Service.Tally(ctx, trash.EmptyJob{
		Before:    trash.EmptyCutoff(time.Now(), older),
		StorageID: storageID,
		Reach:     trash.Reach(ctx),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dry_run":         true,
		"count":           n,
		"bytes":           b,
		"storage_id":      storageID,
		"older_than_days": older,
		"summary":         ops.SayTrashEmptyPreview(trashLang(r), n, b),
	})
}

// emptyStatus is one run as POST and GET /api/admin/trash/empty report it.
// The counters keep the names this endpoint has always answered with (ok,
// purged, failed, scanned, bytes), so a caller written for the synchronous
// endpoint still reads a finished run correctly.
type emptyStatus struct {
	OK bool `json:"ok"`
	// OpID is the ops row: GET /api/files/ops/{id}, and its cancel.
	OpID          int64 `json:"op_id"`
	StorageID     int64 `json:"storage_id,omitempty"`
	OlderThanDays int   `json:"older_than_days"`
	// Running is true until the run has ended — queued behind another purge
	// (Queued) or under way.
	Running   bool `json:"running"`
	Queued    bool `json:"queued,omitempty"`
	Cancelled bool `json:"cancelled,omitempty"`
	// Total and TotalBytes are the rows in the run's scope when it was asked
	// for, and the bytes their files hold. A folder purged together with its
	// contents takes rows the sweep never reaches with it, so Purged can
	// finish below Total; Running false is the end, not Purged == Total.
	Total      int        `json:"total"`
	TotalBytes int64      `json:"total_bytes"`
	Scanned    int        `json:"scanned"`
	Purged     int        `json:"purged"`
	Failed     int        `json:"failed"`
	Bytes      int64      `json:"bytes"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// Summary is where the run stands, in the reader's language
	// (ops.SayTrashEmpty): what the explorer and the admin page show while it
	// runs and when it ends (finding A4).
	Summary string `json:"summary"`
}

// emptyStatusOf reads an OpTrashEmpty row as the endpoint's answer, said in
// lang. `error` is a run that could not go on (the trash could not be read);
// rows that could not be purged are `failed`, and a run stopped by somebody
// is `cancelled`.
func emptyStatusOf(op *ops.Op, lang string) emptyStatus {
	days, storageID, _, _ := op.TrashEmptyOf()
	st := emptyStatus{
		OK:            true,
		OpID:          op.ID,
		StorageID:     storageID,
		OlderThanDays: days,
		Running:       op.Status == ops.StatusPending || op.Status == ops.StatusRunning,
		Queued:        op.Status == ops.StatusPending,
		Cancelled:     op.Status == ops.StatusCancelled,
		Total:         op.Total,
		TotalBytes:    op.BytesTotal,
		Scanned:       op.Done,
		Purged:        max(op.Done-op.Failed, 0),
		Failed:        op.Failed,
		Bytes:         op.BytesDone,
		StartedAt:     op.CreatedAt,
		FinishedAt:    op.FinishedAt,
	}
	if op.Status == ops.StatusFailed {
		st.Error = op.Error
		if st.Error == "" {
			st.Error = "the purge failed"
		}
	}
	st.Summary = ops.SayTrashEmpty(lang, op)
	return st
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
//
// ⚠⚠ The rows a caller may see are judged one by one after the store has read
// them (trashListKeep), so the store's pagination and its count describe rows
// the caller may not see. `total` used to be the page's own length after the
// filters (and the offset stayed in the store's coordinates): a member shown
// 12 of the first 25 rows was told there were 12 in all, the client drew no
// pager, and everything after the first page was unreachable (lesson #413).
// The walk below reads the caller's trash in store batches, counts every
// entry the caller may see, and cuts the page at offset/limit in the caller's
// own coordinates.
//
// ⚠⚠ The answer also counts what the page does not carry (finding D1, 0.54):
// `total_bytes` (every entry the caller may see, not the page's), the newest
// deletion, the same three per storage (`storages`: count, bytes,
// newest_deleted_at) and a `summary` sentence. The explorer drew the trash's
// size, and its "empty the trash" confirmation, from the 50 rows it had
// loaded; the virtual `.trash` row of a storage asked `?storage=<name>`,
// which this handler did not read, and showed the newest 50 of EVERY storage
// (D2). `storage` (an adapter name) is read now, beside `storage_id`.
func (h *Trash) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var storagePtr *int64
	if v := q.Get("storage_id"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			storagePtr = &n
		}
	}
	if v := strings.TrimSuffix(q.Get("storage"), "://"); v != "" && storagePtr == nil {
		id := h.storageID(r.Context(), v)
		// An unknown name is a storage with nothing in its trash - never
		// "every storage", which is what ignoring it meant.
		storagePtr = &id
	}
	limit := 50
	offset := 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= trashListBatch {
			limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	keep := h.trashListKeep(r)
	entries := make([]trash.TrashEntry, 0, limit)
	total := 0
	var totalBytes int64
	var newest *time.Time
	perStorage := map[int64]*trashStorageSummary{}
	storages := make([]*trashStorageSummary, 0, 2)
	for off := 0; ; off += trashListBatch {
		batch, stored, err := h.Service.List(r.Context(), storagePtr, trashListBatch, off)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for _, e := range batch {
			if !keep(e) {
				continue
			}
			if total >= offset && len(entries) < limit {
				entries = append(entries, e)
			}
			total++
			totalBytes += e.Size
			newest = laterOf(newest, e.DeletedAt)
			s := perStorage[e.StorageID]
			if s == nil {
				s = &trashStorageSummary{StorageID: e.StorageID, StorageName: e.StorageName}
				perStorage[e.StorageID] = s
				storages = append(storages, s)
			}
			s.Count++
			s.Bytes += e.Size
			s.NewestDeletedAt = laterOf(s.NewestDeletedAt, e.DeletedAt)
		}
		if len(batch) < trashListBatch || off+trashListBatch >= stored {
			break
		}
	}
	// An item deleted from inside an end-to-end encrypted folder: say which
	// one, so the client can name it (its name may be encrypted) instead of
	// printing ciphertext. The folder's key file never needed restoring on
	// its own and is not listed.
	roots := newE2eRoots(h.Store)
	for i := range entries {
		entries[i].E2eRoot = roots.of(r.Context(), entries[i].StorageID, entries[i].StorageName, entries[i].Path)
	}
	// "You" is the client's word for the asker's own deletes, and the client
	// is not told who it is signed in as (the core package is embedded in
	// hosts that do not know): the listing says so, as it does for owners.
	if u := auth.UserFrom(r.Context()); u != nil {
		for i := range entries {
			if by := entries[i].DeletedByID; by != nil && *by == u.ID {
				entries[i].DeletedBySelf = true
			}
			// A discarded draft of the asker's (the only kind trashListKeep
			// lets through): it came from their Drafts, and the drafts area is
			// not a place a client is ever told about.
			if syspath.IsDraftOf(entries[i].Path, u.ID) {
				entries[i].Draft = true
				entries[i].Path = "/" + entries[i].Name
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":           entries,
		"total":             total,
		"total_bytes":       totalBytes,
		"newest_deleted_at": newest,
		"storages":          storages,
		"summary":           sayTrashList(trashLang(r), total, totalBytes),
		"limit":             limit,
		"offset":            offset,
	})
}

// trashStorageSummary is one storage's part of the caller's trash, as the
// listing counts it: what the virtual `.trash` row of that storage shows.
type trashStorageSummary struct {
	StorageID       int64      `json:"storage_id"`
	StorageName     string     `json:"storage_name,omitempty"`
	Count           int        `json:"count"`
	Bytes           int64      `json:"bytes"`
	NewestDeletedAt *time.Time `json:"newest_deleted_at"`
}

// laterOf is the later of cur and t (t's zero value is no date).
func laterOf(cur *time.Time, t time.Time) *time.Time {
	if t.IsZero() || (cur != nil && !t.After(*cur)) {
		return cur
	}
	return &t
}

// sayTrashList is the listing's one line: how much the caller's trash holds.
func sayTrashList(lang string, total int, size int64) string {
	count := srvtext.Vars{"count": srvtext.Number(lang, int64(total))}
	switch {
	case total == 0:
		return srvtext.Text(lang, "server.trash.list.empty", nil)
	case size > 0:
		count["size"] = srvtext.Bytes(lang, size)
		return srvtext.Plural(lang, "server.trash.list.summary", total, count)
	}
	return srvtext.Plural(lang, "server.trash.list.summary_nosize", total, count)
}

// storageID resolves an adapter name to its storage id, 0 when there is none.
func (h *Trash) storageID(ctx context.Context, name string) int64 {
	if h.Store == nil {
		return 0
	}
	if all, err := h.Store.ListStorages(ctx); err == nil {
		for _, st := range all {
			if st.Name == name {
				return st.ID
			}
		}
	}
	return 0
}

// trashListBatch is how many trash rows List reads from the store at a time
// while it judges them (the store's own ceiling for one read), and the most a
// page may ask for.
const trashListBatch = 500

// trashListKeep is the rule List judges each trash entry by, for the caller.
func (h *Trash) trashListKeep(r *http.Request) func(trash.TrashEntry) bool {
	ctx := r.Context()
	uid := currentUserID(ctx)
	scope, scoped := confinedScope(ctx)
	root, rooted := confine.RootFrom(ctx)
	var perms *aclByName
	if h.ACL != nil {
		perms = newACLByName(ctx, h.ACL, h.Store)
	}
	return func(e trash.TrashEntry) bool {
		// ⚠⚠ Only what the trash holds (issue #74). A row deleted where it
		// stood — the storage sync found the file gone from the storage, up
		// to 0.47 — has no bytes in the trash: listing it offered a Restore
		// that could bring nothing back.
		if e.Vanished {
			return false
		}
		// ⚠ A trashed item that CAME FROM one of filex's own directories is
		// not something the person deleted. In practice it is the desktop
		// app's open-with working copy: the app's sweep deletes
		// `.filex-open/<session>-<name>` once the edit is written back to the
		// original on the person's computer, and the delete goes through the
		// ordinary soft delete — so the trash listed `a1b2c3d4e5f6-Bütçe
		// Özeti.xlsx` with `.filex-open` for a location (measured
		// 2026-09-21). The bytes stay in the bin and age out with everything
		// else (the app relies on that); they are just never offered for
		// restore, which would put the working copy back into a folder nobody
		// can see.
		//
		// A discarded DRAFT is the exception, for its owner alone (issue #71):
		// "Discard" sends it here like any deleted file, so the person can
		// bring it back within the retention window (List presents it).
		if syspath.Hidden(e.Path) && !syspath.IsDraftOf(e.Path, uid) {
			return false
		}
		// Tenancy: only trashed nodes from storages the caller's tenant can
		// reach.
		//
		// ⚠ A DIFFERENT filter from the confine one below, and conflating the
		// two is what left the listing instance-wide: `confine.RootFrom`
		// answers about the EMBEDDED root (an X-Filex-Root header or a
		// `root:`-scoped token), and an ordinary browser login has none.
		// Tenancy and root confinement are two independent boundaries.
		if scoped && !scope.CanAccessStorage(e.StorageID) {
			return false
		}
		// ⚠ Both filters below read `e.Path`, which Service.List resolves
		// through trash.OriginalPath. An entry whose path is still a trash key
		// records no original path, so neither filter has a subject to judge
		// and it is not shown: a grant on `.filex-trash/` would otherwise
		// surface another account's deleted file, and the restore refuses the
		// row anyway.
		//
		// Confinement: only trashed nodes whose original path is inside the
		// caller's root — the embedded client's boundary.
		if rooted && (trash.IsTrashPath(e.Path) || !root.Within(e.StorageName, e.Path)) {
			return false
		}
		// RBAC: only trashed nodes the caller may see.
		if perms != nil && (trash.IsTrashPath(e.Path) || !perms.allow(e.StorageName, e.Path, acl.LevelViewer)) {
			return false
		}
		return true
	}
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
	// Asked with `queued=1` (the admin's Trash page asks): the purge is a job
	// of the queue. A folder is purged one object and one row at a time, and
	// the page's client gives up after 30 s.
	if h.Ops != nil && r.URL.Query().Get("queued") == "1" {
		n, err := h.Store.GetNode(r.Context(), id)
		if err != nil || n == nil || n.DeletedAt == nil {
			// A live row is not a trash entry: the job would refuse it anyway
			// (trash.ErrNotInTrash), and the answer is better now.
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trash entry not found"})
			return
		}
		op, err := h.Ops.Submit(r.Context(), ops.OpPurge, n.StorageID, []string{strconv.FormatInt(id, 10)}, "")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "purge: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"op": op})
		return
	}
	// Finished even if the client leaves: the admin SPA gives up after 30 s,
	// and a folder is purged one object and one row at a time
	// (detachedMutation).
	ctx, cancel := detachedMutation(r.Context())
	defer cancel()
	if err := h.Service.PurgeOne(ctx, id); err != nil {
		msg := err.Error()
		if errors.Is(err, trash.ErrNotInTrash) || strings.Contains(msg, "no rows in result set") || strings.Contains(msg, "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trash entry not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// PurgeBatch deletes a batch of trash entries for good.
//
// POST /api/admin/trash/purge {node_ids: [...]}; with `queued=1` (a server
// that runs purges on its queue) one job per storage.
//
// Every entry is judged the way Purge judges one (ownsNode, and it must be in
// the trash); what is refused is counted, not fatal. The answer is
// `{done, failed, reason_code, summary}` - `done` the entries deleted, or,
// queued, handed to the jobs (`ops`, 202) - and `summary` the server's
// sentence for it in the reader's language. ⚠ The explorer's "Delete
// permanently" sent one DELETE per selected entry and composed the summary
// in the browser (finding A15); it sends this once.
func (h *Trash) PurgeBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeIDs []int64 `json:"node_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if len(req.NodeIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing node_ids"})
		return
	}
	if len(req.NodeIDs) > maxRestoreBatch {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("at most %d entries per purge", maxRestoreBatch),
			"code":  "TOO_MANY", "max": maxRestoreBatch,
		})
		return
	}
	queued := h.Ops != nil && r.URL.Query().Get("queued") == "1"
	ans := trashBatch{Queued: queued}
	// Finished even if the client leaves (detachedMutation), like one entry.
	ctx, cancel := detachedMutation(r.Context())
	defer cancel()
	byStorage := map[int64][]string{}
	var order []int64
	seen := make(map[int64]bool, len(req.NodeIDs))
	for _, id := range req.NodeIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if id <= 0 {
			ans.fail(ops.ReasonNotFound, "")
			continue
		}
		j := &judged{}
		if !ownsNode(j, r, h.Store, id, "trash entry") {
			ans.fail(j.reason(), "")
			continue
		}
		n, err := h.Store.GetNode(r.Context(), id)
		if err != nil || n == nil || n.DeletedAt == nil {
			// A live row is not a trash entry: Purge answers it "not found".
			ans.fail(ops.ReasonNotFound, "")
			continue
		}
		if queued {
			if _, known := byStorage[n.StorageID]; !known {
				order = append(order, n.StorageID)
			}
			byStorage[n.StorageID] = append(byStorage[n.StorageID], strconv.FormatInt(id, 10))
			continue
		}
		if err := h.Service.PurgeOne(ctx, id); err != nil {
			msg := err.Error()
			if errors.Is(err, trash.ErrNotInTrash) || errors.Is(err, sql.ErrNoRows) || strings.Contains(msg, "not found") {
				ans.fail(ops.ReasonNotFound, "")
			} else {
				slog.Warn("trash: purge failed", slog.Int64("node", id), slog.String("err", msg))
				ans.fail(ops.ReasonFailed, "")
			}
			continue
		}
		ans.Done++
	}
	for _, storageID := range order {
		op, err := h.Ops.Submit(r.Context(), ops.OpPurge, storageID, byStorage[storageID], "")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "purge: " + err.Error()})
			return
		}
		ans.Ops = append(ans.Ops, op)
		ans.Done += len(byStorage[storageID])
	}
	ans.Summary = ops.SayTrashBatch(trashLang(r), ops.OpPurge, queued && ans.Done > 0, ans.Done, ans.Failed, ans.ReasonCode, ans.name)
	code := http.StatusOK
	if len(ans.Ops) > 0 {
		code = http.StatusAccepted
	}
	writeJSON(w, code, ans)
}
