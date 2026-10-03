// Package handlers — plugins.go
//
// Admin surface for storage plugins (internal/plugin, docs/PLUGINS.md):
//
//	GET    /api/admin/plugins              — every plugin with its live state
//	POST   /api/admin/plugins              — install: multipart {name, file[, source]} | JSON {name,url,sha256[,source]} | JSON {name,source} | JSON {name,kind:"remote",address,token}
//	GET    /api/admin/plugins/{id}
//	PATCH  /api/admin/plugins/{id}         — {"enabled"?: bool, "source"?: string}
//	POST   /api/admin/plugins/{id}/restart
//	POST   /api/admin/plugins/{id}/upgrade — multipart {file} | JSON {"from_source": true}
//	POST   /api/admin/plugins/updates/check — read every source now; installs nothing
//
// ⚠⚠ Nothing updates itself (plugin/updates.go): a binary plugin that names a
// source is checked daily and its row says when a newer version is there;
// the administrator's {"from_source": true} upgrade installs it.
//
//	DELETE /api/admin/plugins/{id}
//
// ⚠⚠ Install, upgrade, PATCH and DELETE need an administrator SIGNED IN to
// the panel (requireSession): an API key gets 403 and is pointed at
// /api/admin/plugin-requests, where it may leave a request instead. Reading
// (the list, one plugin, the update check) stays open to an admin-scoped key.
//
// ⚠ Instance-wide, never tenant-scoped: a plugin is a PROCESS filex runs (or a
// service it trusts with storage credentials), so in multi-tenant mode only
// the supertenant may touch this surface. A tenant admin gets 403, not an
// empty list, so the boundary is visible.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/plugin"
)

// Plugins is the handler set. Manager may be nil when the subsystem is
// disabled by configuration; every route then answers 503 with the reason.
type Plugins struct {
	Manager *plugin.Manager
	// MultiTenant is kept because the constructor is called with it from two
	// places and the value is genuinely about this handler, but the gate no
	// longer reads it: requireSupertenant decides from the request's tenant
	// scope, which is attached only in multi-tenant mode, so the flag and the
	// scope can never disagree.
	MultiTenant bool
}

// NewPlugins constructs the handler.
func NewPlugins(m *plugin.Manager, multiTenant bool) *Plugins {
	return &Plugins{Manager: m, MultiTenant: multiTenant}
}

func (h *Plugins) gate(w http.ResponseWriter, r *http.Request) bool {
	// ⚠ Tenancy first, subsystem state second. Both orders refuse the same
	// requests, but the other one tells an admin who may not touch this
	// surface at all whether the operator has plugins switched on — a fact
	// about the instance, answered to somebody who has no standing on it. On a
	// single-tenant install nothing changes: no scope is attached, the gate
	// passes, and a disabled subsystem still answers 503.
	if !requireSupertenant(w, r, "storage plugins are managed by the platform operator") {
		return false
	}
	if h.Manager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":   "plugins_disabled",
			"message": "storage plugins are disabled on this instance (FILEX_PLUGINS_DISABLED)",
		})
		return false
	}
	return true
}

func (h *Plugins) List(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	list, err := h.Manager.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plugins": list,
		"dir":     h.Manager.Dir(),
		// Surfaces show these so nobody discovers the rules from a rejection.
		"requires_signature": h.Manager.RequiresSignature(),
		// conformance_MODE, not "conformance": each plugin already carries a
		// `conformance` report, and one name meaning two things is how a
		// surface ends up reading the wrong one.
		"conformance_mode": h.Manager.ConformanceMode(),
		"update_check":     h.Manager.BackgroundUpdates(),
		"updates_checked_at": func() any {
			if t := h.Manager.LastUpdateCheck(r.Context()); !t.IsZero() {
				return t
			}
			return nil
		}(),
	})
}

// CheckUpdates reads every binary plugin's source now and answers the list
// redrawn with the report. It installs nothing.
func (h *Plugins) CheckUpdates(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rep, err := h.Manager.CheckUpdates(context.WithoutCancel(r.Context()))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	list, err := h.Manager.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": rep, "plugins": list})
}

func (h *Plugins) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	st, err := h.Manager.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// Logs answers a storage plugin's log - its starts and failures, and the
// storage sync's answers it could not make sense of (an entry it could not
// say exists or not, issue #104) - the same shape as an app plugin's
// (`?after=<seq>` → `{lines, next}`, internal/pluginlog). A line repeated
// comes back with the same `id` and a higher `count`.
func (h *Plugins) Logs(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	lines, next, err := h.Manager.Logs(r.Context(), id, after)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "next": next})
}

type pluginInstallReq struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Address string `json:"address"`
	Token   string `json:"token"`
	// Signature is a detached ed25519 signature over the binary's sha256.
	// Required only when the instance configures trusted keys.
	Signature string `json:"signature"`
	// Source is where newer versions are published (plugin/updates.go):
	// alone, the plugin is installed from it; beside a url or a file, it is
	// only kept for the daily check.
	Source string `json:"source"`
}

// Install accepts three shapes; the Content-Type decides which.
func (h *Plugins) Install(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "installing a storage plugin") {
		return
	}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		// The binary itself. 32 MiB in memory, the rest spills to disk - up
		// to the binary cap and no further (multipartBody).
		if !h.parseMultipart(w, r) {
			return
		}
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()
		name := strings.TrimSpace(r.FormValue("name"))
		f, hdr, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file is required"})
			return
		}
		defer f.Close()
		st, err := h.Manager.InstallBinary(r.Context(), name, hdr.Filename, f, r.FormValue("signature"))
		if err == nil && strings.TrimSpace(r.FormValue("source")) != "" {
			st, err = h.Manager.SetSource(r.Context(), st.ID, r.FormValue("source"))
		}
		if err != nil {
			writeJSON(w, installStatus(err), map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, st)
		return
	}
	var req pluginInstallReq
	if !decodeAdminJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	var (
		st  *plugin.Status
		err error
	)
	switch {
	case req.Kind == "remote" || (req.Address != "" && req.URL == ""):
		st, err = h.Manager.InstallRemote(r.Context(), req.Name, strings.TrimSpace(req.Address), req.Token)
	case req.URL != "":
		st, err = h.Manager.InstallFromURL(r.Context(), req.Name, strings.TrimSpace(req.URL), req.SHA256, req.Signature)
		if err == nil && strings.TrimSpace(req.Source) != "" {
			st, err = h.Manager.SetSource(r.Context(), st.ID, req.Source)
		}
	case strings.TrimSpace(req.Source) != "":
		st, err = h.Manager.InstallFromSource(r.Context(), req.Name, req.Source)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send a multipart file, or {url, sha256}, or {source}, or {kind:\"remote\", address, token}"})
		return
	}
	if err != nil {
		writeJSON(w, installStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, st)
}

// pluginJSONMax bounds an administrator's JSON body here: a handful of short
// fields (a name, an address, a sha256, a signature). Read whole otherwise,
// a body of any size was decoded into memory before a field was looked at.
const pluginJSONMax = 64 << 10

// decodeAdminJSON decodes a bounded JSON body into dst, or answers: 413 when
// the body is larger than pluginJSONMax, 400 when it is not JSON.
func decodeAdminJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, pluginJSONMax)).Decode(dst)
	if err == nil {
		return true
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
		return false
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
	return false
}

// parseMultipart parses a binary upload bounded by the manager's binary cap
// (plus room for the form's other fields), or answers: 413 past it, 400 for
// a form that does not parse. ParseMultipartForm keeps 32 MiB in memory and
// spills the rest to temporary files, so without the bound the disk took
// whatever was sent before the manager's own cap ever saw a byte.
func (h *Plugins) parseMultipart(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, h.Manager.MaxBinaryBytes()+1<<20)
	err := r.ParseMultipartForm(32 << 20)
	if err == nil {
		return true
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
		return false
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad multipart: " + err.Error()})
	return false
}

func installStatus(err error) int {
	if errors.Is(err, plugin.ErrBadName) {
		return http.StatusBadRequest
	}
	// Typed first. The string matching below is a net for older errors, not
	// a scheme: whether a caller's mistake is reported as 400 or 500 should
	// not depend on which words the message happens to contain.
	var rejected plugin.RejectedError
	if errors.As(err, &rejected) {
		return http.StatusBadRequest
	}
	s := err.Error()
	if strings.Contains(s, "already exists") {
		return http.StatusConflict
	}
	if strings.Contains(s, "required") || strings.Contains(s, "must be") || strings.Contains(s, "sha256") ||
		strings.Contains(s, "FILEX_SECRET_KEY") || strings.Contains(s, "address") || strings.Contains(s, "download") ||
		strings.Contains(s, "empty file") || strings.Contains(s, "larger than") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// Upgrade replaces a binary plugin's file, keeping its registration and every
// storage built on it. A failed upgrade rolls back to the previous binary.
func (h *Plugins) Upgrade(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "upgrading a storage plugin") {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		// The administrator's approval of the newer version the plugin's
		// source has (plugin/updates.go).
		var req struct {
			FromSource bool `json:"from_source"`
		}
		if !decodeAdminJSON(w, r, &req) {
			return
		}
		if !req.FromSource {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send a multipart file, or {\"from_source\": true}"})
			return
		}
		st, err := h.Manager.UpgradeFromSource(context.WithoutCancel(r.Context()), id)
		if err != nil {
			writeJSON(w, installStatus(err), map[string]any{"error": err.Error(), "plugin": st})
			return
		}
		writeJSON(w, http.StatusOK, st)
		return
	}
	if !h.parseMultipart(w, r) {
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file is required"})
		return
	}
	defer f.Close()
	st, err := h.Manager.Upgrade(r.Context(), id, hdr.Filename, f, r.FormValue("signature"))
	if err != nil {
		// A rollback still returns the status, so the page shows what is
		// running now rather than leaving the operator guessing.
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "plugin": st})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// Patch switches a plugin on or off and names its source. Both are the
// administrator's: switching one on runs its binary, and the source is where
// its next approved version comes from.
func (h *Plugins) Patch(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "changing a storage plugin") {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var req struct {
		Enabled *bool   `json:"enabled"`
		Source  *string `json:"source"`
	}
	if !decodeAdminJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil && req.Source == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be {\"enabled\": true|false} and/or {\"source\": \"owner/name\"}"})
		return
	}
	var st *plugin.Status
	if req.Source != nil {
		if st, err = h.Manager.SetSource(r.Context(), id, *req.Source); err != nil {
			writeJSON(w, installStatus(err), map[string]string{"error": err.Error()})
			return
		}
	}
	if req.Enabled != nil {
		if st, err = h.Manager.SetEnabled(r.Context(), id, *req.Enabled); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *Plugins) Restart(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	st, err := h.Manager.Restart(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *Plugins) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "removing a storage plugin") {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.Manager.Remove(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
