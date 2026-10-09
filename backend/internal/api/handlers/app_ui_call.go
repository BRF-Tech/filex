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
	"strconv"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
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
	if h.off(w, r) {
		return
	}
	p, v, ok := h.uiView(w, r)
	if !ok {
		return
	}
	var req uiCallRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	if len(req.Paths) > maxPluginPaths {
		writeError(w, r, http.StatusBadRequest, "too_many_paths", apierr.Params{"max": strconv.Itoa(maxPluginPaths)}, "max", maxPluginPaths)
		return
	}
	var storageID int64
	var rels []string
	if len(req.Paths) > 0 {
		sid, resolved, err := h.resolvePaths(r.Context(), 0, req.Paths)
		if err != nil {
			writePathsRefused(w, r, err)
			return
		}
		if !ownsStorage(w, r, sid, "storage") {
			return
		}
		for _, rel := range resolved {
			if !rootAllows(r.Context(), h.Store, sid, rel) {
				refuseOutsideRoot(w, r)
				return
			}
			if !aclAllowID(r.Context(), h.ACL, h.Store, sid, rel, acl.LevelViewer) {
				writeErrorSaid(w, r, http.StatusForbidden, "permission_denied", "app_input_denied", apierr.Params{"name": rel})
				return
			}
			if refuseEncryptedAtDoor(w, r, encryptedAtDoor(r.Context(), h.Store, sid, rel), rel, false) {
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
		h.callFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]json.RawMessage{"result": out})
}
