// Package handlers - file_types_admin.go
//
// Admin → Plugins → Default apps (internal/assoc, docs/APP-PLUGINS.md →
// Default apps):
//
//	GET    /api/admin/file-types           - every kind something besides filex handles, and every kind with a rule
//	PUT    /api/admin/file-types/{ext}     - {open?: {order, off} | null, thumbnail?: {order, off} | null}; null = back to the default
//	DELETE /api/admin/file-types/{ext}     - both capabilities back to the default
//
// ⚠ Instance-wide, like the apps whose handlers it orders: a tenant
// administrator gets 403 supertenant_only. A change needs an administrator
// signed in to the panel (an API key reads, and gets 403 session_required):
// switching a thumbnail handler back on hands an app the bytes of every file
// of that kind, which is the same kind of decision as an action override
// (PutOverrides). Refused on the demo instance. Each change writes its audit
// row (file_association.update / .reset) with what it was and what it is.
package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/auth"
)

// FileTypesAdmin serves the Default apps screen.
type FileTypesAdmin struct {
	Assoc *assoc.Service
	// Demo refuses every change (the admin account of a demo is public).
	Demo bool
}

// NewFileTypesAdmin constructs the handler. A nil service (app plugins off)
// still lists filex's own kinds - none - and refuses changes with 503.
func NewFileTypesAdmin(svc *assoc.Service, demo bool) *FileTypesAdmin {
	return &FileTypesAdmin{Assoc: svc, Demo: demo}
}

func (h *FileTypesAdmin) gate(w http.ResponseWriter, r *http.Request) bool {
	return requireSupertenant(w, r, "which app opens a kind of file, and which draws its thumbnails, is decided by the platform operator")
}

// fileTypesBody is the answer of GET and of every change.
type fileTypesBody struct {
	Kinds []assoc.Kind `json:"kinds"`
	// Enabled: app plugins run here (false: nothing but filex handles any
	// kind, and changes are refused).
	Enabled bool `json:"enabled"`
	// Editable: this caller may change it (a signed-in administrator, not on
	// a demo).
	Editable bool `json:"editable"`
}

func (h *FileTypesAdmin) body(r *http.Request) fileTypesBody {
	b := fileTypesBody{Kinds: []assoc.Kind{}, Enabled: h.Assoc != nil}
	if h.Assoc != nil {
		b.Kinds = h.Assoc.Kinds(r.Context())
	}
	b.Editable = b.Enabled && !h.Demo && auth.TokenFrom(r.Context()) == nil
	return b
}

// List answers GET /api/admin/file-types.
func (h *FileTypesAdmin) List(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, h.body(r))
}

// writable refuses what may not change the rules, answering itself.
func (h *FileTypesAdmin) writable(w http.ResponseWriter, r *http.Request, what string) bool {
	if !h.gate(w, r) || !requireSession(w, r, what) {
		return false
	}
	if h.Demo {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "demo_refused", "message": "default apps cannot be changed on the demo instance"})
		return false
	}
	if h.Assoc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "app_plugins_disabled", "message": "app plugins are off here: filex alone opens and draws every kind"})
		return false
	}
	return true
}

// putFileTypeRequest is a change for one kind. A capability absent from the
// body is left as it is; `null` puts it back to the default.
type putFileTypeRequest struct {
	Open      json.RawMessage `json:"open"`
	Thumbnail json.RawMessage `json:"thumbnail"`
}

// Put answers PUT /api/admin/file-types/{ext}.
func (h *FileTypesAdmin) Put(w http.ResponseWriter, r *http.Request) {
	if !h.writable(w, r, "changing which app opens or draws a kind of file") {
		return
	}
	ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(chi.URLParam(r, "ext")), "."))
	if !assoc.ValidExt(ext) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_kind", "message": "a kind is a file extension: lower-case letters and digits, no dot"})
		return
	}
	var req putFileTypeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	type change struct {
		capability string
		rule       *assoc.Rule
	}
	var changes []change
	for _, c := range []struct {
		capability string
		raw        json.RawMessage
	}{{assoc.CapOpen, req.Open}, {assoc.CapThumbnail, req.Thumbnail}} {
		raw := strings.TrimSpace(string(c.raw))
		if raw == "" {
			continue
		}
		if raw == "null" {
			changes = append(changes, change{capability: c.capability})
			continue
		}
		var rule assoc.Rule
		if err := json.Unmarshal(c.raw, &rule); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json", "message": c.capability + ": {order: [...], off: [...]}"})
			return
		}
		changes = append(changes, change{capability: c.capability, rule: &rule})
	}
	if len(changes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "give open and/or thumbnail"})
		return
	}
	ctx := r.Context()
	// Every rule is checked before any is written: a body whose second half
	// is refused changes nothing.
	for _, c := range changes {
		if c.rule != nil {
			if err := h.Assoc.Check(c.capability, ext, *c.rule); err != nil {
				h.fail(w, err)
				return
			}
		}
	}
	by := actorIDOf(r)
	before := map[string]any{}
	after := map[string]any{}
	for _, c := range changes {
		before[c.capability] = ruleOrDefault(h.Assoc.Rule(ctx, c.capability, ext))
		if c.rule == nil {
			if _, err := h.Assoc.Reset(ctx, c.capability, ext); err != nil {
				h.fail(w, err)
				return
			}
			after[c.capability] = "default"
			continue
		}
		stored, err := h.Assoc.Put(ctx, c.capability, ext, *c.rule, by)
		if err != nil {
			h.fail(w, err)
			return
		}
		after[c.capability] = stored
	}
	auth.SetAuditTarget(ctx, ext, "."+ext)
	auth.AddAuditDetail(ctx, "ext", ext)
	auth.AddAuditDetail(ctx, "before", before)
	auth.AddAuditDetail(ctx, "after", after)
	writeJSON(w, http.StatusOK, h.body(r))
}

// Reset answers DELETE /api/admin/file-types/{ext}: both capabilities back
// to the default order.
func (h *FileTypesAdmin) Reset(w http.ResponseWriter, r *http.Request) {
	if !h.writable(w, r, "changing which app opens or draws a kind of file") {
		return
	}
	ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(chi.URLParam(r, "ext")), "."))
	if !assoc.ValidExt(ext) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_kind", "message": "a kind is a file extension: lower-case letters and digits, no dot"})
		return
	}
	ctx := r.Context()
	before := map[string]any{}
	for _, c := range []string{assoc.CapOpen, assoc.CapThumbnail} {
		before[c] = ruleOrDefault(h.Assoc.Rule(ctx, c, ext))
		if _, err := h.Assoc.Reset(ctx, c, ext); err != nil {
			h.fail(w, err)
			return
		}
	}
	auth.SetAuditTarget(ctx, ext, "."+ext)
	auth.AddAuditDetail(ctx, "ext", ext)
	auth.AddAuditDetail(ctx, "before", before)
	writeJSON(w, http.StatusOK, h.body(r))
}

func ruleOrDefault(r *assoc.Rule) any {
	if r == nil {
		return "default"
	}
	return r
}

func (h *FileTypesAdmin) fail(w http.ResponseWriter, err error) {
	var bad *assoc.ErrInvalid
	if errors.As(err, &bad) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_rule", "message": bad.Message})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
