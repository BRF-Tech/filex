package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/multioidc"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/db"
)

// Capabilities exposes /api/capabilities.
type Capabilities struct {
	Service *capability.Service
	// Store + MultiTenant power the per-tenant branding block: in multi-tenant
	// mode the (pre-auth, host-resolved) capabilities answer carries only THIS
	// host's tenant identity — never the existence of other tenants
	// (docs/MULTI-TENANCY.md §12 + isolation checklist).
	Store       db.Store
	MultiTenant bool
	/* kimlik:e3 cloud */
	// CloudEnabled mirrors FILEX_CLOUD (set by BuildRouter only when the flag
	// is on). While false — the default — the capabilities payload carries NO
	// cloud field at all, keeping the flag-off wire format byte-identical.
	CloudEnabled bool
}

// NewCapabilities constructs a Capabilities handler.
func NewCapabilities(svc *capability.Service, store db.Store, multiTenant bool) *Capabilities {
	return &Capabilities{Service: svc, Store: store, MultiTenant: multiTenant}
}

// Get returns the runtime feature snapshot.
//
// We emit BOTH the rich nested shape (filex-core admin SPA) AND a flat
// alias set (legacy embed.js + filex-core SFC fallback expected:
// `ffmpeg / ghostscript / libreoffice / max_chunk_mb / upload_limit_mb /
// onlyoffice_url / drawio_url`). Cheap to ship both — keeps the SFC
// happy without breaking the existing admin UI bindings.
func (h *Capabilities) Get(w http.ResponseWriter, r *http.Request) {
	c, err := h.Service.Get(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Build flat aliases.
	const mb = int64(1024 * 1024)
	flat := map[string]any{
		"ffmpeg":       c.Thumbs.Video,
		"imagemagick":  c.Thumbs.ImageMagick,
		"ghostscript":  c.Thumbs.PDF,
		"libreoffice":  c.Thumbs.Office,
		"max_chunk_mb": int64(0),
		"upload_limit_mb": func() int64 {
			if c.MaxUploadSize <= 0 {
				return 0
			}
			return c.MaxUploadSize / mb
		}(),
		"onlyoffice_url": "",
		"drawio_url":     "",
		"convert_url":    "",
	}
	if c.ChunkSize > 0 {
		flat["max_chunk_mb"] = c.ChunkSize / mb
	}
	if oo, ok := c.External["onlyoffice"]; ok && oo.Enabled {
		flat["onlyoffice_url"] = oo.URL
	}
	if dr, ok := c.External["drawio"]; ok && dr.Enabled {
		flat["drawio_url"] = dr.URL
	}
	if cv, ok := c.External["convert"]; ok && cv.Enabled {
		flat["convert_url"] = cv.URL
	}

	// Marshal the rich snapshot to a generic map so we can layer the
	// flat aliases on top (no struct tag wrestling).
	raw, _ := json.Marshal(c)
	merged := map[string]any{}
	_ = json.Unmarshal(raw, &merged)
	for k, v := range flat {
		// Don't clobber an existing nested field of the same name.
		if _, exists := merged[k]; !exists {
			merged[k] = v
		}
	}

	// Per-tenant branding: identify only the tenant this host belongs to.
	if h.MultiTenant && h.Store != nil {
		if p, _ := h.Store.GetProviderByHost(r.Context(), multioidc.RequestHost(r)); p != nil {
			merged["tenant"] = map[string]any{"slug": p.Slug, "name": p.Name}
		}
	}
	/* kimlik:e3 cloud */
	if h.CloudEnabled {
		merged["cloud"] = map[string]any{"enabled": true, "signup_url": "/api/cloud/signup"}
	}
	writeJSON(w, http.StatusOK, merged)
}
