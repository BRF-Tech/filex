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

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// OnlyOffice exposes the editor config + fetch + callback endpoints.
type OnlyOffice struct {
	Service         *onlyoffice.Service
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
	ACL             *acl.Resolver
}

// AttachACL wires the RBAC resolver: opening a document needs ≥viewer; an
// editable config additionally needs ≥editor (else it's downgraded to view).
func (h *OnlyOffice) AttachACL(r *acl.Resolver) { h.ACL = r }

// NewOnlyOffice constructs the handler. Pass nil svc to disable the routes
// (handlers will return 503 — easier than gating in routes.go).
func NewOnlyOffice(svc *onlyoffice.Service, store db.Store, resolver func(int64) (storage.Driver, error)) *OnlyOffice {
	return &OnlyOffice{Service: svc, Store: store, StorageResolver: resolver}
}

// Config returns the editor descriptor for an iframe to render.
//
// Accepts both forms:
//
//	GET  /api/files/onlyoffice/config?id=<node-id>&lang=tr
//	POST /api/files/onlyoffice/config  { "path": "adapter://rel", "mode": "edit"|"view" }
//
// The GET form is what the standalone Editor.vue route hands the SFC's
// PreviewModal when the embedder passes a numeric node id. The POST
// form is what the modal itself sends from inside the explore page —
// it has the adapter-qualified path handy but not the node id, so the
// handler must resolve path → node before continuing.
func (h *OnlyOffice) Config(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "onlyoffice not configured"})
		return
	}
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var (
		node *model.Node
		lang string
		mode string
	)
	q := r.URL.Query()
	lang = q.Get("lang")
	mode = q.Get("mode")

	if r.Method == http.MethodPost {
		var body struct {
			Path   string `json:"path"`
			NodeID int64  `json:"node_id"`
			Mode   string `json:"mode"`
			Lang   string `json:"lang"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
		if body.Lang != "" {
			lang = body.Lang
		}
		if body.Mode != "" {
			mode = body.Mode
		}
		if body.NodeID > 0 {
			n, err := h.Store.GetNode(r.Context(), body.NodeID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			node = n
		} else if body.Path != "" {
			n, err := h.resolveNodeByPath(r.Context(), body.Path)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			node = n
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path or node_id"})
			return
		}
	} else {
		idStr := q.Get("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
			return
		}
		n, err := h.Store.GetNode(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		node = n
	}

	// RBAC: must be able to view the doc at all; an edit request without
	// ≥editor is downgraded to a read-only view (viewers can preview office
	// files but never edit/convert).
	if node != nil && h.ACL != nil {
		if !aclAllowID(r.Context(), h.ACL, h.Store, node.StorageID, node.Path, acl.LevelViewer) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
			return
		}
		if mode == "edit" && !aclAllowID(r.Context(), h.ACL, h.Store, node.StorageID, node.Path, acl.LevelEditor) {
			mode = "view"
		}
	}

	/* wiring:e2 — E2E-encrypted files can never open in OnlyOffice: the DS
	   would fetch ciphertext (and a save callback would clobber it). Sniff
	   the 'filexe2e' magic before building a config. Read errors fall
	   through — the fetch path will surface them as before. */
	if node != nil && h.StorageResolver != nil {
		if drv, derr := h.StorageResolver(node.StorageID); derr == nil {
			if rc, rerr := drv.Read(r.Context(), node.Path); rerr == nil {
				head := make([]byte, len(e2e.MagicPrefix))
				n, _ := io.ReadFull(rc, head)
				_ = rc.Close()
				if n == len(head) && e2e.HasMagicPrefix(head) {
					writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "file is e2e-encrypted"})
					return
				}
			}
		}
	}
	/* /wiring:e2 */
	cfg, err := h.Service.BuildConfigForNode(node, user, lang, mode)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// resolveNodeByPath looks up a node from a `<adapter>://<rel>` or bare
// `<rel>` path. The bare form falls back to the first enabled storage,
// matching the SFC's path-stripping convention.
func (h *OnlyOffice) resolveNodeByPath(ctx context.Context, raw string) (*model.Node, error) {
	adapter, rel := splitAdapterPath(raw)
	rel = strings.Trim(path.Clean("/"+rel), "/")
	if rel == "" {
		return nil, errors.New("empty path")
	}
	storages, err := h.Store.ListEnabledStorages(ctx)
	if err != nil || len(storages) == 0 {
		return nil, errors.New("no storages")
	}
	var st *model.Storage
	if adapter != "" {
		for _, s := range storages {
			if s.Name == adapter {
				st = s
				break
			}
		}
	}
	if st == nil {
		st = storages[0]
	}
	hash := pathkey.Hash(st.ID, rel)
	node, err := h.Store.GetNodeByPath(ctx, st.ID, hash)
	if err != nil || node == nil {
		return nil, errors.New("not found")
	}
	return node, nil
}

// Fetch streams document bytes back to the OnlyOffice document server.
//
// Public: no session required, but the URL must be HMAC-signed via the
// onlyoffice service.
//
// GET /api/files/onlyoffice/fetch?n=<id>&exp=<unix>&sig=<b64url>
func (h *OnlyOffice) Fetch(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "onlyoffice not configured"})
		return
	}
	q := r.URL.Query()
	id, err := strconv.ParseInt(q.Get("n"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad n"})
		return
	}
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad exp"})
		return
	}
	if err := h.Service.VerifyFetchSignature(id, exp, q.Get("sig")); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	node, err := h.Store.GetNode(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	drv, err := h.StorageResolver(node.StorageID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver"})
		return
	}
	rc, err := drv.Read(r.Context(), node.Path)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rc.Close()
	mime := node.Mime
	if mime == "" {
		mime = "application/octet-stream"
	}
	// Belt-and-suspenders: legacy rows scanned before the sniff fix
	// still carry "application/zip" for office files. OnlyOffice DS
	// rejects pptx with that Content-Type even when fileType matches
	// in the JWT config — refine on the way out so existing demos
	// don't need a full rescan after the deploy.
	mime = storage.RefineOfficeMime(mime, node.Name)
	w.Header().Set("Content-Type", mime)
	if node.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(node.Size, 10))
	}
	_, _ = io.Copy(w, rc)
}

// Callback receives save events from the OnlyOffice document server.
//
// POST /api/files/onlyoffice/callback?node=<id>
//
// Public — relies on the JWT in the body or Authorization header for
// integrity.
func (h *OnlyOffice) Callback(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil || !h.Service.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": 1, "message": "onlyoffice not configured"})
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("node"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": 1, "message": "bad node"})
		return
	}
	resp, err := h.Service.HandleCallback(r, id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": 1, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
