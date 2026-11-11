package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/templates"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// # The deletion link: /x/{token}
//
// Every upload through the upload door answers with a deletion link
// (uploads.go mintDeletion): whoever holds it may move that ONE file to its
// owner's trash and end the public link it was shared with. ShareX keeps the
// link in its history and opens it in a browser; a phone shortcut shows it.
//
// ⚠⚠ GET NEVER DELETES. A link is opened by more than the person who means
// it: Slack, Discord, Teams and mail scanners fetch every URL they are shown
// to draw a preview, and a browser may prefetch one. So GET answers a small
// confirmation page - which file, what will happen, one button - and only the
// page's POST deletes. A deletion link pasted into a chat stays a link.
//
// The link names the file's catalogue row on a storage by id (never a path,
// which another file may take later) and runs as the file's owner as the
// account is NOW: an owner who may no longer delete it there is told so, and
// nothing happens. A link is spent when it deletes; a spent, unknown or
// garbled link reads the same page. Pages follow the public pages' one
// language per visitor (public_i18n.go).
type UploadDelete struct {
	ops         *aiOps
	limiter     *ipLimiter
	multiTenant bool
	// Branding and DefaultLocale give the page the instance's chrome and the
	// fallback language, as on every other public page.
	Branding      *BrandingSource
	DefaultLocale string
	// Templates renders the pages (internal/templates page.delete_confirm,
	// page.delete_done, page.error); nil = filex's own.
	Templates *templates.Renderer
}

// NewUploadDelete builds the deletion-link pages over the same aiOps core the
// rest of the uploads go through, so a deletion is the agent API's own delete
// (the trash, the catalogue, the write hook) and nothing of its own.
func NewUploadDelete(store db.Store, resolver func(int64) (storage.Driver, error)) *UploadDelete {
	return &UploadDelete{
		ops:     newAIOps(store, resolver, nil, ""),
		limiter: newIPLimiter(60, time.Hour),
	}
}

// AttachACL wires the permission resolver: the delete is the owner's, under
// the owner's grants and permissions.
func (h *UploadDelete) AttachACL(r *acl.Resolver) { h.ops.acl = r }

// AttachSearchIndex wires the search index: a deleted upload leaves it.
func (h *UploadDelete) AttachSearchIndex(idx *search.Index) { h.ops.attachSearchIndex(idx) }

// AttachBranding wires the shared branding source.
func (h *UploadDelete) AttachBranding(b *BrandingSource) { h.Branding = b }

// AttachTemplates wires the renderer the pages are made with.
func (h *UploadDelete) AttachTemplates(r *templates.Renderer) { h.Templates = r }

// AttachLocale sets the fallback language of the pages.
func (h *UploadDelete) AttachLocale(def string) { h.DefaultLocale = def }

// AttachMultiTenant tells the pages the mode in force: the owner is then
// asked again under their tenant's current scope.
func (h *UploadDelete) AttachMultiTenant(on bool) { h.multiTenant = on }

// Page serves GET /x/{token}: the confirmation, never the deletion.
func (h *UploadDelete) Page(w http.ResponseWriter, r *http.Request) {
	if !h.limiter.allow(clientIP(r)) {
		h.renderError(w, r, http.StatusTooManyRequests, "uploaddel_busy")
		return
	}
	d, node, key := h.find(r)
	if key != "" {
		h.renderError(w, r, statusOfDeletion(key), key)
		return
	}
	h.renderConfirm(w, r, d, node.Name)
}

// Delete serves POST /x/{token}: the confirmation page's button.
func (h *UploadDelete) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.limiter.allow(clientIP(r)) {
		h.renderError(w, r, http.StatusTooManyRequests, "uploaddel_busy")
		return
	}
	d, node, key := h.find(r)
	if key != "" {
		h.renderError(w, r, statusOfDeletion(key), key)
		return
	}
	// The page's own button, and nothing else: a POST that does not carry
	// the form's field gets the page to confirm on.
	if err := r.ParseForm(); err != nil || r.PostForm.Get("confirm") != "1" {
		h.renderConfirm(w, r, d, node.Name)
		return
	}
	ctx, qualified, refusal := h.asOwner(r.Context(), d, node)
	if refusal != "" {
		h.renderError(w, r, statusOfDeletion(refusal), refusal)
		return
	}
	if err := h.ops.Delete(ctx, qualified); err != nil {
		switch {
		case errors.Is(err, errAIForbidden):
			h.renderError(w, r, http.StatusForbidden, "uploaddel_refused")
		case errors.Is(err, storage.ErrNotFound):
			h.renderError(w, r, http.StatusGone, "uploaddel_gone")
		default:
			slog.Error("upload deletion link: delete failed",
				slog.Int64("node_id", d.NodeID), slog.Int64("storage_id", d.StorageID), slog.String("err", err.Error()))
			h.renderError(w, r, http.StatusServiceUnavailable, "uploaddel_failed")
		}
		return
	}
	store := h.ops.store
	if d.ShareID > 0 {
		if sh, err := store.GetShareByID(ctx, d.ShareID); err == nil && sh != nil && sh.RevokedAt == nil {
			if err := store.RevokeShare(ctx, sh.ID); err != nil {
				slog.Error("upload deletion link: link not revoked", slog.Int64("share_id", sh.ID), slog.String("err", err.Error()))
			}
		}
	}
	if _, err := store.DeleteUploadDeletion(ctx, d.ID); err != nil {
		slog.Error("upload deletion link: not spent", slog.Int64("id", d.ID), slog.String("err", err.Error()))
	}
	uid := d.UserID
	_ = store.InsertAuditEntry(ctx, &model.AuditEntry{
		UserID:     &uid,
		Action:     "uploader.file_delete",
		TargetType: "node",
		TargetID:   strconv.FormatInt(d.NodeID, 10),
		Metadata:   map[string]interface{}{"target_name": node.Name, "via": "deletion_link"},
		IP:         clientIP(r),
		CreatedAt:  time.Now(),
	})
	h.renderDone(w, r, d)
}

// find reads the link and the file it names. key is "" when both stand,
// otherwise the page that says why not: an unknown (or spent) link, or a
// file that is no longer there.
func (h *UploadDelete) find(r *http.Request) (*model.UploadDeletion, *model.Node, string) {
	tok := pathParam(r, "token")
	if tok == "" || len(tok) > 128 {
		return nil, nil, "uploaddel_unknown"
	}
	ctx := r.Context()
	store := h.ops.store
	d, err := store.GetUploadDeletion(ctx, hashSecret(tok))
	if err != nil || d == nil {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			slog.Error("upload deletion link: lookup failed", slog.String("err", err.Error()))
		}
		return nil, nil, "uploaddel_unknown"
	}
	node, err := store.GetNode(ctx, d.NodeID)
	if err != nil || node == nil || node.DeletedAt != nil || node.StorageID != d.StorageID {
		return nil, nil, "uploaddel_gone"
	}
	if !db.StampStands(ctx, store, d.TenantID, d.StorageID) {
		return nil, nil, "uploaddel_gone"
	}
	return d, node, ""
}

// asOwner is the context the delete runs in and the file's address as it
// reads now, asked of the owner as they are NOW: the account may still sign
// in, its tenant still has the storage, and the storage's current name still
// leads to the storage the link names. refusal is "" when all of it stands.
func (h *UploadDelete) asOwner(ctx context.Context, d *model.UploadDeletion, node *model.Node) (context.Context, string, string) {
	store := h.ops.store
	owner, err := store.GetUser(ctx, d.UserID)
	if err != nil || owner == nil || !auth.LoginAllowed(ctx, store, h.multiTenant, owner) {
		return nil, "", "uploaddel_refused"
	}
	uctx := asTicketMinter(ctx, store, h.multiTenant, owner, nil)
	if scope, ok := tenant.FromContext(uctx); ok && scope != nil && !scope.CanAccessStorage(d.StorageID) {
		return nil, "", "uploaddel_gone"
	}
	st, err := store.GetStorage(ctx, d.StorageID)
	if err != nil || st == nil {
		return nil, "", "uploaddel_gone"
	}
	qualified := joinAdapterPath(st.Name, node.Path)
	// The name is how aiOps addresses a storage; it must still lead to the
	// storage the link was made on (lesson #1787).
	if s, _, err := h.ops.resolveStorage(uctx, qualified); err != nil || s == nil || s.ID != d.StorageID {
		if err != nil && errors.Is(err, errAIForbidden) {
			return nil, "", "uploaddel_refused"
		}
		return nil, "", "uploaddel_gone"
	}
	return uctx, qualified, ""
}

// statusOfDeletion is the status a page is answered with.
func statusOfDeletion(key string) int {
	switch key {
	case "uploaddel_unknown":
		return http.StatusNotFound
	case "uploaddel_gone":
		return http.StatusGone
	case "uploaddel_refused":
		return http.StatusForbidden
	case "uploaddel_busy":
		return http.StatusTooManyRequests
	}
	return http.StatusServiceUnavailable
}

// renderError answers with the public error page (page.error):
// `err_<key>_title` and `err_<key>_body` of the visitor's language, in the
// ADDRESS's template - like every "not available" page, it says nothing about
// whose link it was.
func (h *UploadDelete) renderError(w http.ResponseWriter, r *http.Request, status int, key string) {
	w.Header().Set("Cache-Control", "no-store")
	renderPublicError(w, r, pageTemplates(h.Templates, h.Branding), h.DefaultLocale, h.Branding, status, key)
}

// deletionTarget is a deletion link's page target: the tenant the upload was
// made in.
func deletionTarget(r *http.Request, d *model.UploadDeletion, lang string) templates.Target {
	t := templates.Target{Host: r.Host, Lang: lang}
	if d != nil {
		t.TenantID = d.TenantID
	}
	return t
}

// renderConfirm answers with the confirmation (page.delete_confirm): the
// file's name, what the button does, the button. It posts to the address it
// was opened at.
func (h *UploadDelete) renderConfirm(w http.ResponseWriter, r *http.Request, d *model.UploadDeletion, name string) {
	lang, t, _, _ := publicPageLang(h.Branding, r, h.DefaultLocale)
	w.Header().Set("Cache-Control", "no-store")
	writePublicPage(w, r, pageTemplates(h.Templates, h.Branding), deletionTarget(r, d, lang), "page.delete_confirm", http.StatusOK,
		t["uploaddel_title"], map[string]any{
			"file": map[string]any{"name": leafname.Visible(name)},
			"form": map[string]any{"action": r.URL.Path},
		}, nil)
}

// renderDone answers a deletion that happened (page.delete_done).
func (h *UploadDelete) renderDone(w http.ResponseWriter, r *http.Request, d *model.UploadDeletion) {
	lang, t, _, _ := publicPageLang(h.Branding, r, h.DefaultLocale)
	w.Header().Set("Cache-Control", "no-store")
	writePublicPage(w, r, pageTemplates(h.Templates, h.Branding), deletionTarget(r, d, lang), "page.delete_done", http.StatusOK,
		t["uploaddel_done_title"], nil, nil)
}
