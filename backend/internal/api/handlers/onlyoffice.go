package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/filebody"
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
	// Body resolves where the document's bytes are: the driver, or filex's
	// staging area while a staged upload is still transferring. Nil-safe.
	Body *filebody.Resolver
}

// AttachBody wires the byte-source resolver so a document that is still being
// transferred opens in the editor.
func (h *OnlyOffice) AttachBody(b *filebody.Resolver) { h.Body = b }

// AttachACL wires the RBAC resolver: opening a document needs ≥viewer; an
// editable config additionally needs ≥editor (else it's downgraded to view).
func (h *OnlyOffice) AttachACL(r *acl.Resolver) { h.ACL = r }

// NewOnlyOffice constructs the handler.
//
// ⚠ The handler is ALWAYS wired now, even when nothing is configured yet: what
// "configured" means is a question for the `external_services` row at request
// time, not for whatever env carried at boot. Every entry point asks
// Service.EnabledCtx, which is nil-safe, so a nil service still answers 503.
func NewOnlyOffice(svc *onlyoffice.Service, store db.Store, resolver func(int64) (storage.Driver, error)) *OnlyOffice {
	return &OnlyOffice{Service: svc, Store: store, StorageResolver: resolver}
}

// Probe answers the reverse-path check: a one-shot, unguessable URL the
// document server is asked to download so filex can see whether the request
// arrives (see onlyoffice.VerifyReversePath).
//
// ⚠ Unauthenticated by necessity — the caller is another container, not a
// person — and therefore it serves a fixed sentence and nothing else. An
// unknown or expired token is a plain 404: a stranger who guesses at this
// endpoint learns nothing, not even that it exists for something.
func (h *OnlyOffice) Probe(w http.ResponseWriter, r *http.Request) {
	body, ok := h.Service.ServeProbe(r.URL.Query().Get("t"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="filex-probe.txt"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, body)
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
	if !h.Service.EnabledCtx(r.Context()) {
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

	// Whose node is it? Both id-taking shapes above (POST body `node_id`, GET
	// query `id`) call GetNode, which tenantstore does not confine, so a
	// client-supplied node id crossed tenants. The `path` shape is safe and
	// always was, because resolveNodeByPath goes through the CONFINED
	// ListEnabledStorages — the same "two shapes, different security
	// properties" split as /api/files/share.
	//
	// ⚠ docs/MULTI-TENANCY.md argued this endpoint was safe because "storage
	// is derived server-side from the node". That is true and it is not a
	// defence: the NODE ID is client-supplied, so deriving the storage from
	// it derives nothing about the caller. The RBAC block below is not a
	// boundary either — with storages.rbac_enabled off, which is the default,
	// Effective() returns the account-role base for every path.
	//
	// ⚠ What this handed over is BYTES, not metadata: the config carries an
	// HMAC-signed, credential-free fetch URL redeemed at the PUBLIC
	// /api/files/onlyoffice/fetch, for every extension OnlyOffice opens.
	// Refused with the same 404 an unknown node id already produced.
	if node != nil {
		if scope, confined := confinedScope(r.Context()); confined && !scope.CanAccessStorage(node.StorageID) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
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
			src, serr := h.Body.Resolve(r.Context(), drv, node.StorageID, node.Path, node)
			if serr != nil {
				writeStagingGone(w, serr)
				return
			}
			if rc, rerr := src.Open(r.Context()); rerr == nil {
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
	cfg, err := h.Service.BuildConfigForNode(r.Context(), node, user, lang, mode)
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
//
// Every refusal here is logged with its reason. The only person who ever sees
// this endpoint fail is an operator reading "Download failed" in the editor —
// a message from the document server that says nothing about which of the five
// things went wrong. Without a line naming the reason, the status code in the
// access log is all they have, and a 500 from a bad signature and a 500 from
// an unreachable bucket look identical (issue #17).
func (h *OnlyOffice) Fetch(w http.ResponseWriter, r *http.Request) {
	if !h.Service.EnabledCtx(r.Context()) {
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
	if err := h.Service.VerifyFetchSignatureCtx(r.Context(), id, exp, q.Get("sig")); err != nil {
		// Expiry and a wrong secret are the two shapes: a link the editor held
		// on to for too long, or a JWT secret changed under a running editor.
		ooFetchFailed(id, 0, "signature refused", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	node, err := h.Store.GetNode(r.Context(), id)
	if err != nil {
		ooFetchFailed(id, 0, "no catalogue row for this document", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	drv, err := h.StorageResolver(node.StorageID)
	if err != nil {
		// The storage this document lives on could not be opened at all —
		// wrong endpoint, wrong credentials, backend down.
		ooFetchFailed(id, node.StorageID, "the storage could not be opened", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver"})
		return
	}
	src, err := h.Body.Resolve(r.Context(), drv, node.StorageID, node.Path, node)
	if err != nil {
		ooFetchFailed(id, node.StorageID, "the document body could not be located", err)
		writeStagingGone(w, err)
		return
	}
	rc, err := src.Open(r.Context())
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			// The catalogue has the row, the storage does not have the object:
			// a stale catalogue, or the file moved behind filex's back.
			ooFetchFailed(id, node.StorageID, "the object is not on the storage", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		ooFetchFailed(id, node.StorageID, "reading the object failed", err)
		writeStagingGone(w, err)
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
	declareBodyLength(r.Context(), w, src, node)
	_, _ = io.Copy(w, rc)
}

// ooFetchFailed writes the one line an operator needs to tell five different
// "Download failed" errors apart.
func ooFetchFailed(nodeID, storageID int64, why string, err error) {
	attrs := []any{slog.Int64("node", nodeID), slog.String("reason", why)}
	if storageID != 0 {
		attrs = append(attrs, slog.Int64("storage", storageID))
	}
	if err != nil {
		attrs = append(attrs, slog.String("err", err.Error()))
	}
	slog.Warn("onlyoffice: the document server could not download this file", attrs...)
}

// Callback receives save events from the OnlyOffice document server.
//
// POST /api/files/onlyoffice/callback?node=<id>
//
// Public — relies on the JWT in the body or Authorization header for
// integrity.
func (h *OnlyOffice) Callback(w http.ResponseWriter, r *http.Request) {
	if !h.Service.EnabledCtx(r.Context()) {
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
