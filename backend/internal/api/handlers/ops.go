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
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/ops"
)

// Ops handles async copy/move/delete tasks.
//
// State is persisted in the pending_ops table — restart-safe so a crash
// doesn't lose in-flight work. The actual execution happens in the worker
// goroutine launched in server.New (see ops.Service.Run).
type Ops struct {
	Service *ops.Service
	Store   db.Store // for path → storage_id resolution in the per-verb endpoints
	ACL     *acl.Resolver
}

// NewOps constructs an Ops handler.
func NewOps(svc *ops.Service, store db.Store) *Ops {
	return &Ops{Service: svc, Store: store}
}

// AttachACL wires the RBAC resolver so async copy/move/delete require ≥editor
// on their sources (and destination) at submit time — the async worker itself
// runs without a user, so authorization is a submit-time gate.
func (o *Ops) AttachACL(r *acl.Resolver) { o.ACL = r }

// errors used by the per-verb wrappers.
var (
	errMixedAdapters = errsString("sources span multiple adapters")
	errBadPath       = errsString("bad source path")
)

func errUnknownAdapter(name string) error { return errsString("unknown adapter: " + name) }

type errsString string

func (e errsString) Error() string { return string(e) }

// opsRequest is the body of POST /api/files/ops.
type opsRequest struct {
	Kind      string   `json:"kind"` // copy, move, delete
	StorageID int64    `json:"storage_id"`
	Sources   []string `json:"sources"`
	Dest      string   `json:"dest,omitempty"`
}

// Submit queues a new op and returns the opID.
func (o *Ops) Submit(w http.ResponseWriter, r *http.Request) {
	if o.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	var req opsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	// RBAC: require ≥editor on each source (and, for copy/move, the dest).
	for _, s := range req.Sources {
		_, rel := splitAdapterPath(s)
		if rel == "" {
			rel = strings.Trim(s, "/")
		}
		if !aclAllowID(r.Context(), o.ACL, o.Store, req.StorageID, rel, acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission: " + s})
			return
		}
	}
	if req.Kind != "delete" && req.Dest != "" {
		_, drel := splitAdapterPath(req.Dest)
		if drel == "" {
			drel = strings.Trim(req.Dest, "/")
		}
		if !aclAllowID(r.Context(), o.ACL, o.Store, req.StorageID, drel, acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission (dest)"})
			return
		}
	}

	op, err := o.Service.Submit(r.Context(), req.Kind, req.StorageID, req.Sources, req.Dest)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, op)
}

// Per-verb wrappers for the SFC. The SFC's `useFileApi` posts to:
//
//	POST /api/files/copy   { source: ["<adapter>://<rel>", …], target: "<adapter>://<rel>" }
//	POST /api/files/move   { source: ..., target: ..., sourceDir: "<adapter>://<rel>" }
//	POST /api/files/delete { source: ... }
//
// We translate to the unified ops.Submit by splitting the adapter
// prefix from the first source path. Mixed-adapter batches reject —
// `Mover.Move` etc. are storage-bound.

type perVerbReq struct {
	Source    []string `json:"source"`
	Target    string   `json:"target,omitempty"`
	SourceDir string   `json:"sourceDir,omitempty"`
}

func (o *Ops) submitPerVerb(w http.ResponseWriter, r *http.Request, kind string) {
	if o.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	var req perVerbReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if len(req.Source) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing source"})
		return
	}

	storageID, sources, err := o.resolveBatch(r.Context(), req.Source)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// RBAC: require ≥editor on every source (the async worker runs userless,
	// so authorize here at submit time).
	for _, rel := range sources {
		if !aclAllowID(r.Context(), o.ACL, o.Store, storageID, rel, acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission: " + rel})
			return
		}
	}

	dest := ""
	if req.Target != "" {
		// SFC's per-verb endpoints model `target` as a directory
		// (the destination FOLDER for copy/move). The unified ops
		// worker's `joinIntoDir(dest, src)` keys off a trailing
		// slash to choose drop-into-dir vs rename-to-literal. The
		// SFC may or may not send the trailing slash — force one on
		// here so the user-facing semantics match the docs.
		// Bypass splitAdapterPath (which strips both ends) and
		// extract the relative manually so we keep the slash.
		raw := req.Target
		if idx := strings.Index(raw, "://"); idx >= 0 {
			raw = raw[idx+3:]
		}
		raw = strings.TrimLeft(raw, "/") // drop leading slashes
		if raw == "" {
			// Storage root — drop sources at the root with their
			// own basename.
			dest = ""
		} else if strings.HasSuffix(raw, "/") {
			dest = raw
		} else {
			dest = raw + "/"
		}
	}

	// RBAC: copy/move write into the destination dir — require ≥editor there.
	if kind != "delete" && dest != "" {
		if !aclAllowID(r.Context(), o.ACL, o.Store, storageID, strings.Trim(dest, "/"), acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission (dest)"})
			return
		}
	}

	op, err := o.Service.Submit(r.Context(), kind, storageID, sources, dest)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"op": op})
}

// resolveBatch splits adapter prefixes off each path, ensures all
// sources live in the same storage, and returns the resolved storage
// id + bare relative paths.
func (o *Ops) resolveBatch(ctx context.Context, sources []string) (int64, []string, error) {
	var storageID int64
	out := make([]string, 0, len(sources))
	for i, s := range sources {
		adapter, rel := splitAdapterPath(s)
		if adapter == "" {
			// Fall back to first storage so legacy embedders that drop
			// the prefix still work.
			storages, err := o.Store.ListEnabledStorages(ctx)
			if err != nil || len(storages) == 0 {
				return 0, nil, errNoStorages
			}
			adapter = storages[0].Name
		}
		st, err := o.Store.GetStorageByName(ctx, adapter)
		if err != nil || st == nil {
			return 0, nil, errUnknownAdapter(adapter)
		}
		if i == 0 {
			storageID = st.ID
		} else if st.ID != storageID {
			return 0, nil, errMixedAdapters
		}
		rel = strings.Trim(path.Clean("/"+rel), "/")
		if rel == "" || strings.Contains(rel, "..") {
			return 0, nil, errBadPath
		}
		out = append(out, rel)
	}
	return storageID, out, nil
}

// SubmitCopy / SubmitMove / SubmitDelete are the per-verb endpoints.

func (o *Ops) SubmitCopy(w http.ResponseWriter, r *http.Request) {
	o.submitPerVerb(w, r, "copy")
}
func (o *Ops) SubmitMove(w http.ResponseWriter, r *http.Request) {
	o.submitPerVerb(w, r, "move")
}
func (o *Ops) SubmitDelete(w http.ResponseWriter, r *http.Request) {
	o.submitPerVerb(w, r, "delete")
}

// List returns ops filtered by ?status=… (e.g. "running"). Used by the
// SPA's PendingOpsTray which polls every 2 s. Empty status returns the
// most-recent rows across all statuses (capped at 200 service-side).
//
// Response shape mirrors what the SPA's `opsApi.list` already
// understands: `{ "ops": [Op, …] }`. The frontend's `normalizeOp`
// adapter then translates the backend's raw shape into the SPA's
// `PendingOp` contract.
func (o *Ops) List(w http.ResponseWriter, r *http.Request) {
	if o.Service == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ops": []any{}})
		return
	}
	status := r.URL.Query().Get("status")
	list, err := o.Service.List(r.Context(), status)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if list == nil {
		list = []*ops.Op{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ops": list})
}

// Status returns the live or final state of a submitted op.
func (o *Ops) Status(w http.ResponseWriter, r *http.Request) {
	if o.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	op, err := o.Service.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown op"})
		return
	}
	writeJSON(w, http.StatusOK, op)
}
