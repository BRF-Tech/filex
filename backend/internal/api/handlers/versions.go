// Package handlers — versions.go
//
// Endpoints under /api/files/versions.
//
//	GET    /api/files/versions?node_id=…             (viewer) list snapshots
//	POST   /api/files/versions/snapshot              (editor) snapshot now
//	POST   /api/files/versions/restore               (editor) restore one
//	DELETE /api/admin/versions/{id}                  (admin)  hard delete
//
// ⚠⚠ Every one of them takes a raw node id, so every one of them resolves the
// node and authorizes it FIRST — see authorizedNode. Until that existed the
// only gate here was tenancy, which meant that on a single-tenant install (the
// vast majority) there was no gate at all: a `viewer` account could POST
// /restore and overwrite the live bytes of any file whose node id it could
// name, and POST /snapshot to write objects into any storage on the box.
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
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/versioning"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Versions wraps version-history HTTP routes.
type Versions struct {
	Store   db.Store
	Service *versioning.Service
	// Index keeps the restored content searchable. Optional; nil skips it.
	Index *search.Index
	// ACL is the RBAC resolver. Nil = unwired (tests) and, per the aclAllowID
	// contract, allows — the existence, tenancy and confinement checks above it
	// still run.
	ACL *acl.Resolver
}

// AttachACL wires the RBAC resolver. ⚠ This handler shipped without one: it
// was the only file surface in routes.go with no AttachACL call, which is
// precisely why nobody noticed that a read-only account could roll files back.
func (h *Versions) AttachACL(r *acl.Resolver) { h.ACL = r }

// AttachSearchIndex wires the search index. ⚠ Restoring a version rewrites the
// file's BYTES at an unchanged path, and nothing else ever revisits a
// document whose path did not change — so without this the index keeps the
// text of the version that was just rolled back. Measured: after restoring v1,
// a content search for a phrase only v2 ever contained still returned the
// file, and a phrase that IS in the restored file did not.
func (h *Versions) AttachSearchIndex(i *search.Index) { h.Index = i }

// NewVersions constructs the handler.
func NewVersions(store db.Store, svc *versioning.Service) *Versions {
	return &Versions{Store: store, Service: svc}
}

// authorizedNode resolves a client-supplied node_id and reports whether the
// caller may perform an operation of level `need` on it. It answers itself and
// returns ok=false when the caller may not.
//
// Four gates, in this order:
//
//  1. existence — the node is real and not TRASHED. ⚠ The trashed half is a
//     behaviour change, and a deliberate one: a trashed row's live path is its
//     `storage_key` (the place restore would put it back), so restoring a
//     version onto it wrote bytes to a path the catalogue says holds nothing.
//     The same exclusion `comments.visibleNode` makes, for the same reason;
//  2. tenancy — the node's storage is inside the caller's provider scope;
//  3. root confinement — a `root:`-scoped token (a host app proxying an
//     embedded explorer) stays inside its subtree. The node id is not a path,
//     so confine.Middleware's path rewriting cannot reach these routes;
//  4. RBAC — `need` on the node's path, capped by the account role. This is
//     what stops a `viewer`.
//
// `missing` is the 404 body, so a refusal can be spelled exactly like the
// genuine miss the same route already produces — the /api/admin route reaches
// a version by ITS id and says "version not found", and a refusal that said
// anything else would be the oracle this is meant to close.
//
// ⚠ 1-3 answer 404 with the same body, so a miss and a crossing are
// indistinguishable and the endpoint is not an existence oracle. 4 answers 403,
// because "this file exists and you may not write to it" is not a secret from
// somebody who can already read the folder.
//
// ⚠⚠ The node is resolved BEFORE the operation. An unknown node_id used to
// fall through to a 200 with an empty list (List) or a 500 carrying a database
// error string (Snapshot / Restore), so there was no coherent "no such node"
// answer for a refusal to imitate. Now there is.
func (h *Versions) authorizedNode(w http.ResponseWriter, r *http.Request, nodeID int64, need acl.Level, missing string) (*model.Node, bool) {
	ctx := r.Context()
	n, err := h.Store.GetNode(ctx, nodeID)
	if err != nil || n == nil || n.DeletedAt != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": missing})
		return nil, false
	}
	if scope, confined := confinedScope(ctx); confined && !scope.CanAccessStorage(n.StorageID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": missing})
		return nil, false
	}
	if root, ok := confine.RootFrom(ctx); ok {
		if !root.Within(h.storageName(ctx, n.StorageID), livePathOf(n)) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": missing})
			return nil, false
		}
	}
	if !aclAllowID(ctx, h.ACL, h.Store, n.StorageID, livePathOf(n), need) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return nil, false
	}
	return n, true
}

// storageName resolves a storage id to its adapter name for the confinement
// check. An unresolvable id yields "" and therefore fails Within — closed.
func (h *Versions) storageName(ctx context.Context, id int64) string {
	if h.Store == nil {
		return ""
	}
	st, err := h.Store.GetStorage(ctx, id)
	if err != nil || st == nil {
		return ""
	}
	return st.Name
}

// livePathOf is the storage-relative path a node's bytes actually live at —
// the same choice versioning.Service.Restore makes when it decides what to
// overwrite, so the path being authorized is the path being written.
func livePathOf(n *model.Node) string {
	if n.StorageKey != "" {
		return n.StorageKey
	}
	return n.Path
}

// List returns the version timeline for a node.
func (h *Versions) List(w http.ResponseWriter, r *http.Request) {
	nodeID, err := strconv.ParseInt(r.URL.Query().Get("node_id"), 10, 64)
	if err != nil || nodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad node_id"})
		return
	}
	// Viewer: the timeline is metadata about content the caller can already
	// read, and the read-only inspector is meant to show it.
	if _, ok := h.authorizedNode(w, r, nodeID, acl.LevelViewer, "not found"); !ok {
		return
	}
	versions, err := h.Service.List(r.Context(), nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if versions == nil {
		versions = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"versions": versions,
		"node_id":  nodeID,
	})
}

type restoreReq struct {
	NodeID          int64 `json:"node_id"`
	VersionID       int64 `json:"version_id"`
	SnapshotCurrent bool  `json:"snapshot_current,omitempty"`
}

// snapshotReq is the POST /api/files/versions/snapshot body.
type snapshotReq struct {
	NodeID int64 `json:"node_id"`
}

// Snapshot records the node's current content as a new version on demand
// (the inspector's "take a version now" button; writes normally snapshot
// implicitly, this is the explicit user-triggered path).
func (h *Versions) Snapshot(w http.ResponseWriter, r *http.Request) {
	var req snapshotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing fields"})
		return
	}
	// ⚠ Editor, not viewer: a snapshot WRITES an object into the node's
	// storage. A foreign node_id here was a way to burn another tenant's quota
	// on demand, and a read-only account's node_id a way to make a read-only
	// account write.
	if _, ok := h.authorizedNode(w, r, req.NodeID, acl.LevelEditor, "not found"); !ok {
		return
	}
	v, err := h.Service.Snapshot(r.Context(), req.NodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": v})
}

// Restore replaces the live content with a recorded version.
func (h *Versions) Restore(w http.ResponseWriter, r *http.Request) {
	var req restoreReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID <= 0 || req.VersionID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing fields"})
		return
	}
	// ⚠ The destructive one: this overwrites the live bytes at an unchanged
	// path. Measured on the pre-fix build, a plain user of one tenant posted
	// another tenant's node_id + version_id and got 200 {"ok":true} while the
	// victim's file on disk was replaced by the old version's contents — and,
	// on a single-tenant install, a `viewer` account did the same to any file
	// on the box.
	node, ok := h.authorizedNode(w, r, req.NodeID, acl.LevelEditor, "not found")
	if !ok {
		return
	}

	// ⚠⚠ Restore is a destructive WRITE, so it goes through the same pre-write
	// guard as every other write surface: the bytes about to be replaced are
	// snapshotted first, and a snapshot that cannot be taken refuses the write
	// (503) rather than destroying them. Before this, restore was the one write
	// in filex that bypassed the guard entirely — rolling back twice in a row
	// lost whatever was live in between.
	if err := writehook.BeforeOverwrite(r.Context(), node.StorageID, livePathOf(node)); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "could not preserve the existing file: " + err.Error(),
			"code":  "SNAPSHOT_FAILED",
		})
		return
	}
	// The guard just took the caller's `snapshot_current` for them. Asking the
	// service to take it again would record the identical bytes twice and spend
	// a retention slot on the duplicate — so the flag only does the work when
	// the guard is switched off (FILEX_VERSIONS_ON_OVERWRITE=0), where it is
	// the only thing left that can.
	snapshotCurrent := req.SnapshotCurrent && !writehook.OverwriteGuarded()
	if err := h.Service.Restore(r.Context(), req.NodeID, req.VersionID, snapshotCurrent); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if fresh, gerr := h.Store.GetNode(r.Context(), req.NodeID); gerr == nil && fresh != nil {
		protocolsync.New(h.Store, h.Index, nil, "").IndexNode(r.Context(), fresh)
		// ⚠⚠ The bytes in `.versions/` were never scanned. queue's Eligible()
		// skips that prefix outright, deliberately: snapshotting is now what
		// every destructive write does, so scanning each snapshot would
		// multiply the scan load by the edit rate for bytes nobody can
		// execute. Restoring is the moment they become live again, and it is
		// rare — so the scan happens HERE. Without it, overwriting an
		// infected file with a clean one and then rolling back was a way to
		// put an infected file live on an install where every upload is
		// scanned.
		//
		// Asynchronous, exactly like an upload: the file is live and
		// unscanned until the verdict lands. That window is not a compromise
		// specific to restore — it is the same window every uploaded file
		// has, and the queue is what makes a slow scanner unable to stall a
		// write. Blocking here would make restore the one write surface in
		// filex that waits on ClamAV.
		//
		// ⚠ Through writehook, not a bare enqueueAntivirusScan: the gate is
		// the single post-write seam, and routing the scan through it is also
		// what finally makes a restore emit `file.updated`. It did not before
		// — the bytes of a file changed and no webhook subscriber was told.
		writehook.OnFileWritten(r.Context(), fresh.StorageID, fresh,
			writehook.OriginManager, writehook.Replaced,
			map[string]any{"restored_version_id": req.VersionID})
		emitFolderChange(fresh.StorageID, path.Dir(fresh.Path), realtime.ChangeEvent{
			Action: "upload", Name: fresh.Name,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// HardDelete erases a version row + its storage object (admin only).
func (h *Versions) HardDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	// Ownership + permission: a version is reached by its own id, so the node
	// it belongs to has to be resolved before the delete rather than inferred
	// from the caller. version -> node -> storage.
	//
	// ⚠ Unconditional now, where it used to run only for a confined caller.
	// The route sits behind auth.RequireAdmin and an admin's ACL ceiling is
	// Owner, so the RBAC half is a no-op today — deliberately: the day this
	// route stops being admin-only, it must not silently become unauthorized
	// the way its /api/files siblings were.
	v, verr := h.Store.GetNodeVersion(r.Context(), id)
	if verr != nil || v == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "version not found"})
		return
	}
	if _, ok := h.authorizedNode(w, r, v.NodeID, acl.LevelEditor, "version not found"); !ok {
		return
	}
	if err := h.Service.HardDeleteVersion(r.Context(), id); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no rows in result set") || strings.Contains(msg, "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "version not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
