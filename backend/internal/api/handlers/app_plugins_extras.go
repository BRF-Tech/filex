// Package handlers - app_plugins_extras.go
//
// The files an administrator adds to one app (internal/wasmplugin extras.go,
// docs/APP-PLUGINS-API.md → Files an administrator adds to an app): the
// dictionaries the office editor reads for a language its package does not
// carry.
//
//	GET    /api/admin/app-plugins/{id}/extras        - the files, the ceilings, whether the app's interface reads them
//	PUT    /api/admin/app-plugins/{id}/extras/<path> - add or replace one; the body is the file, <path> is under extra/
//	DELETE /api/admin/app-plugins/{id}/extras/<path> - remove one
//	GET    /api/admin/app-plugins/{id}/extras-catalog/{kind}        - what the app's release offers of a kind (0.57)
//	POST   /api/admin/app-plugins/{id}/extras-catalog/{kind}/{item} - download one item, every file checked, and add it
//
// ⚠ Instance-wide like the rest of this surface (requireSupertenant): an
// app is the platform's, and so is what it is handed. A write needs an
// administrator SIGNED IN (requireSession), like every change to what an app
// is handed (its thumbnail limits, its overrides); the list stays open to an
// admin-scoped key. The audit row (auth/audit_middleware.go
// app_plugin.extra_add / extra_remove) names the app, the path, and for an
// added file its size and sha256.
package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// Extras answers GET /api/admin/app-plugins/{id}/extras.
func (h *AppPluginsAdmin) Extras(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	ans, err := h.Registry.Extras(p.Row.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

// PutExtra answers PUT /api/admin/app-plugins/{id}/extras/<path>: the body,
// whole, is the file.
func (h *AppPluginsAdmin) PutExtra(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "adding a file to an app") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	rel := chi.URLParam(r, "*")
	if r.ContentLength > wasmplugin.MaxExtraFileBytes {
		writeExtraFailure(w, r, rel, wasmplugin.ErrExtraTooLarge)
		return
	}
	f, err := h.Registry.PutExtra(p.Row.ID, rel, r.Body)
	if err != nil {
		writeExtraFailure(w, r, rel, err)
		return
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(p.Row.ID, 10), p.Row.Name)
	auth.AddAuditDetail(r.Context(), "path", wasmplugin.ExtraPrefix+f.Path)
	auth.AddAuditDetail(r.Context(), "size", f.Size)
	auth.AddAuditDetail(r.Context(), "sha256", f.SHA256)
	writeJSON(w, http.StatusOK, f)
}

// DeleteExtra answers DELETE /api/admin/app-plugins/{id}/extras/<path>.
func (h *AppPluginsAdmin) DeleteExtra(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "removing a file added to an app") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	rel := chi.URLParam(r, "*")
	if err := h.Registry.DeleteExtra(p.Row.ID, rel); err != nil {
		writeExtraFailure(w, r, rel, err)
		return
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(p.Row.ID, 10), p.Row.Name)
	auth.AddAuditDetail(r.Context(), "path", wasmplugin.ExtraPrefix+rel)
	w.WriteHeader(http.StatusNoContent)
}

// ExtraCatalog answers GET /api/admin/app-plugins/{id}/extras-catalog/{kind}:
// what the app's release offers of that kind (a dictionary per language),
// each with whether it is installed. Read from where the app was installed,
// held to the manifest's sha256 (wasmplugin/extras_catalog.go).
func (h *AppPluginsAdmin) ExtraCatalog(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	kind := chi.URLParam(r, "kind")
	ans, err := h.Registry.ExtraCatalog(r.Context(), p.Row.ID, kind)
	if err != nil {
		writeExtraFailure(w, r, kind, err)
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

// InstallExtraItem answers POST …/extras-catalog/{kind}/{item}: every file
// of the item downloaded and held to the catalog's size and sha256, then
// added as an upload would be. The audit row (app_plugin.extra_add) names
// the item and each file with its sha256.
func (h *AppPluginsAdmin) InstallExtraItem(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "adding a file to an app") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	kind, item := chi.URLParam(r, "kind"), chi.URLParam(r, "item")
	files, err := h.Registry.InstallExtraItem(r.Context(), p.Row.ID, kind, item)
	if err != nil {
		writeExtraFailure(w, r, kind+"/"+item, err)
		return
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(p.Row.ID, 10), p.Row.Name)
	auth.AddAuditDetail(r.Context(), "catalog", kind+"/"+item)
	paths := make([]string, 0, len(files))
	sums := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, wasmplugin.ExtraPrefix+f.Path)
		sums = append(sums, f.SHA256)
	}
	auth.AddAuditDetail(r.Context(), "path", strings.Join(paths, ", "))
	auth.AddAuditDetail(r.Context(), "sha256", strings.Join(sums, ", "))
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// writeExtraFailure says why an added file was refused, in the reader's
// words; anything else is the app surface's own failure (writeAppFailure).
func writeExtraFailure(w http.ResponseWriter, r *http.Request, rel string, err error) {
	path := wasmplugin.ExtraPrefix + rel
	var ce *wasmplugin.ExtraCatalogError
	switch {
	case errors.Is(err, wasmplugin.ErrExtraUndeclared):
		writeErrorSaid(w, r, http.StatusConflict, "extras_unused", "extras_unused", nil)
	case errors.Is(err, wasmplugin.ErrExtraNoCatalog), errors.Is(err, wasmplugin.ErrExtraNoCatalogSource):
		writeErrorSaid(w, r, http.StatusNotFound, "extras_no_catalog", "extras_no_catalog", nil)
	case errors.Is(err, wasmplugin.ErrExtraItemMissing):
		writeErrorSaid(w, r, http.StatusNotFound, "not_found", "extra_missing", apierr.Params{"path": path})
	case errors.As(err, &ce):
		writeErrorSaid(w, r, http.StatusBadGateway, "extras_catalog", "extras_catalog", apierr.Params{"why": ce.Error()})
	case errors.Is(err, wasmplugin.ErrExtraPath):
		writeErrorSaid(w, r, http.StatusBadRequest, "extra_path", "extra_path", apierr.Params{"path": path}, "kinds", wasmplugin.ExtraKinds())
	case errors.Is(err, wasmplugin.ErrExtrasUnused):
		writeErrorSaid(w, r, http.StatusConflict, "extras_unused", "extras_unused", nil)
	case errors.Is(err, wasmplugin.ErrExtraTooLarge):
		writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", apierr.Params{"max": srvtext.Bytes(langOf(r), wasmplugin.MaxExtraFileBytes)})
	case errors.Is(err, wasmplugin.ErrExtrasFull):
		writeErrorSaid(w, r, http.StatusInsufficientStorage, "extras_full", "extras_full", apierr.Params{"max": srvtext.Bytes(langOf(r), wasmplugin.MaxExtrasBytes)})
	case errors.Is(err, wasmplugin.ErrExtraNotText):
		writeErrorSaid(w, r, http.StatusUnprocessableEntity, "extra_not_text", "extra_not_text", apierr.Params{"path": path})
	case errors.Is(err, wasmplugin.ErrExtraMissing):
		writeErrorSaid(w, r, http.StatusNotFound, "not_found", "extra_missing", apierr.Params{"path": path})
	default:
		writeAppFailure(w, langOf(r), err)
	}
}
