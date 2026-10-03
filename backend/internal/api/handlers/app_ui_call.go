// Package handlers — app_ui_call.go
//
// POST /api/files/plugins/ui/{plugin}/{view}/call — an app's own interface
// asking its module (`ui_call`, docs/APP-PLUGINS-API.md → An app's own
// interface). The save half is app_ui.go.
package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"path"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// uiCallRequest is `engine.call` as the explorer posts it.
type uiCallRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Paths  []string        `json:"paths"`
}

// UICall is the app's own interface asking its module (`ui_call`). The files
// the interface was opened with are judged for the person asking exactly
// like a view's — confinement, ACL viewer, not in an encrypted folder — and
// handed to the module as refs.
func (h *AppPlugins) UICall(w http.ResponseWriter, r *http.Request) {
	if h.off(w) {
		return
	}
	p, v, ok := h.uiView(w, r)
	if !ok {
		return
	}
	var req uiCallRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if len(req.Paths) > maxPluginPaths {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many paths"})
		return
	}
	var storageID int64
	var rels []string
	if len(req.Paths) > 0 {
		sid, resolved, err := h.resolvePaths(r.Context(), 0, req.Paths)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !ownsStorage(w, r, sid, "storage") {
			return
		}
		lk, _ := h.Store.(e2e.NodeByPathLookup)
		for _, rel := range resolved {
			if !rootAllows(r.Context(), h.Store, sid, rel) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": "outside this token's root: " + rel})
				return
			}
			if !aclAllowID(r.Context(), h.ACL, h.Store, sid, rel, acl.LevelViewer) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": "insufficient permission: " + rel})
				return
			}
			if lk != nil && e2e.UnderEncrypted(r.Context(), lk, sid, "/"+rel) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "encrypted", "message": "an app cannot read files in an encrypted folder"})
				return
			}
			if !h.openAllowed(w, r, p, v, path.Base(rel)) {
				return
			}
		}
		storageID, rels = sid, resolved
	}
	ctx := wasmplugin.WithActorIP(r.Context(), clientIP(r))
	out, err := h.Registry.UICall(ctx, p.Row.Name, v.ID, storageID, rels, auth.UserFrom(r.Context()), pluginLang(r), req.Method, req.Params)
	if err != nil {
		h.callFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]json.RawMessage{"result": out})
}
