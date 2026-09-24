// Package handlers — shares_admin.go
//
// Admin views/actions over all shares (every user, not just current user).
//
//	GET    /api/admin/shares
//	POST   /api/admin/shares/{id}/revoke
//	DELETE /api/admin/shares/{id}
package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// SharesAdmin handles /api/admin/shares.
type SharesAdmin struct {
	Store db.Store
	// Tenants resolves the origin every listed link is built on — the same
	// resolver the share dialog's links use (handlers.Share). Zero value yields
	// relative "/s/<token>" links, which every existing test constructing this
	// handler by hand gets.
	Tenants tenanturl.Resolver
	// Apps is told when a link an app opened is revoked or deleted here, so
	// the app wakes and finds out (appLinkEnded). nil: app plugins are off.
	Apps *wasmplugin.Registry
}

// NewSharesAdmin constructs the handler.
func NewSharesAdmin(store db.Store) *SharesAdmin { return &SharesAdmin{Store: store} }

// AttachTenants wires the shared per-request origin resolver (internal/tenanturl).
func (h *SharesAdmin) AttachTenants(rv tenanturl.Resolver) { h.Tenants = rv }

// AttachApps wires the app-plugin registry (nil = app plugins disabled).
func (h *SharesAdmin) AttachApps(reg *wasmplugin.Registry) { h.Apps = reg }

// List returns all shares with optional creator/active filters. Rows carry
// `plugin_name` / `page_id` when an app plugin opened the link (00046), so the
// Shares table can show a "plugin / page" column without a lookup per row.
func (h *SharesAdmin) List(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, false)
}

// ListAppPluginShares is the same rows, narrowed to the links APPS opened, so
// an app's own admin panel can draw its table (`?plugin=sign`). It is the same
// query, the same tenant filter and the same envelope as List — there is no
// second listing to keep in step.
//
// ⚠ `?plugin=` takes the app's NAME, not its row id: a panel drawn for an app
// knows what the app is called and should not have to look its id up first.
func (h *SharesAdmin) ListAppPluginShares(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, true)
}

func (h *SharesAdmin) list(w http.ResponseWriter, r *http.Request, appsOnly bool) {
	q := r.URL.Query()

	var creatorID *int64
	if v := q.Get("creator_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			creatorID = &id
		}
	}
	activeOnly := q.Get("active") == "true"

	// ⚠ ONE reader for both share listings (shares_mine.go → sharePaging), so
	// the admin page and a person's own page cannot come to disagree about
	// what `limit=0` or an out-of-range page means.
	limit, offset := sharePaging(q)

	var (
		rows  []*db.ShareWithMeta
		total int64
		err   error
	)
	if appsOnly {
		var pluginID int64
		if name := strings.TrimSpace(q.Get("plugin")); name != "" {
			p, perr := h.Store.GetAppPluginByName(r.Context(), name)
			if perr != nil || p == nil {
				// An app that is not installed has no links. Answering an
				// empty page rather than 404 keeps a panel that polls one app
				// working through an uninstall.
				pluginID = -1
			} else {
				pluginID = p.ID
			}
		}
		if pluginID < 0 {
			rows, total = []*db.ShareWithMeta{}, 0
		} else {
			rows, total, err = h.Store.ListAppPluginShares(r.Context(), pluginID, activeOnly, limit, offset)
		}
	} else {
		rows, total, err = h.Store.ListAllShares(r.Context(), creatorID, activeOnly, limit, offset)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Multi-tenant: a tenant-admin only sees shares on its own storages
	// (docs/MULTI-TENANCY.md §9). The scoped store's ListStorages is already
	// tenant-confined, so its name set IS the allowed set; a row without a
	// resolvable storage name stays hidden (fail closed). Total is recomputed
	// from the filtered page — approximate across pages, acceptable for a
	// name-level admin view.
	if kept, narrowed := sharesInTenant(r, h.Store, rows); narrowed {
		rows, total = kept, int64(len(kept))
	}
	// An empty result must serialise as `[]`, never `null`. A nil Go slice
	// marshals to JSON null, and both envelopes below promise arrays — every
	// consumer does `.length` / `.map` / `v-for` on them, so a fresh instance
	// with no shares yet handed the admin SPA a null and broke the page that
	// exists precisely to say "you have no shares".
	if rows == nil {
		rows = []*db.ShareWithMeta{}
	}
	// The canonical link, from the configured public origin. Issue #32: the
	// Shares page built it from window.location.origin, so an administrator
	// signed in on http://localhost:5212 copied a localhost link even with
	// FILEX_PUBLIC_URL set — and on a proxied or multi-tenant host, the wrong
	// origin. The share dialog has always used the server's answer; now so
	// does this list.
	creators := make([]int64, 0, len(rows))
	for _, row := range rows {
		if row != nil && row.Share != nil && row.Share.CreatedBy != nil {
			creators = append(creators, *row.Share.CreatedBy)
		}
	}
	names := personNames(r.Context(), h.Store, creators)
	base := h.Tenants.FromRequest(r)
	for _, row := range rows {
		if row != nil && row.Share != nil && row.Share.CreatedBy != nil {
			row.CreatorName = names[*row.Share.CreatedBy]
		}
		if row != nil && row.Share != nil && row.Share.Token != "" {
			row.URL = base + "/s/" + row.Share.Token
		}
		if row != nil && h.Apps != nil {
			row.App = h.Apps.LinkOf(row.Share)
		}
	}
	// Dual envelope: `items/total/page/page_size` is what the admin
	// SPA expects (PaginatedResponse); `entries/limit/offset` keeps
	// any older consumers happy.
	pageSize := limit
	if pageSize <= 0 {
		pageSize = 1
	}
	page := (offset / pageSize) + 1
	writeJSON(w, http.StatusOK, map[string]any{
		"items":     rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		// Legacy aliases:
		"entries": rows,
		"limit":   limit,
		"offset":  offset,
	})
}

// Revoke soft-revokes by setting expiration to now (keeps audit trail).
func (h *SharesAdmin) Revoke(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if !h.ownsShare(w, r, id) {
		return
	}
	sh, _ := h.Store.GetShareByID(r.Context(), id)
	if err := h.Store.RevokeShare(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	appLinkEnded(r.Context(), h.Apps, sh, "revoked")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete hard-deletes a share row.
func (h *SharesAdmin) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if !h.ownsShare(w, r, id) {
		return
	}
	// Read BEFORE the delete: afterwards there is no row to say which app,
	// if any, opened this link.
	sh, _ := h.Store.GetShareByID(r.Context(), id)
	if err := h.Store.DeleteShare(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	appLinkEnded(r.Context(), h.Apps, sh, "deleted")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ownsShare resolves a share to the storage holding its node and asks whether
// the caller's tenant may reach it.
//
// List (above) filters by storage name, so a tenant admin never SAW another
// tenant's share; revoke and delete beside it took the id raw, and a share id
// is a small integer. So the row you could not see was still one you could
// destroy — and shares are how a customer's users hand files to people outside
// the platform, so revoking them silently breaks a business process rather
// than merely losing a row.
func (h *SharesAdmin) ownsShare(w http.ResponseWriter, r *http.Request, id int64) bool {
	// ⚠ The lookup is unconditional, not confined-only. RevokeShare and
	// DeleteShare both answer {"ok":true} for an id that names nothing, so a
	// 404 raised only for a foreign id would announce that the row exists.
	// Both answers agree now, and the bogus-id 200 was misleading anyway.
	sh, err := h.Store.GetShareByID(r.Context(), id)
	if err != nil || sh == nil {
		return notFound(w, "share")
	}
	return ownsNode(w, r, h.Store, sh.NodeID, "share")
}
