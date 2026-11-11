package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── "Get short link" (filex 0.60.0) ────────────────────────────────────────
//
// docs/APP-PLUGINS-API.md → The link-shortener role. filex keeps no short
// link: the app holding the role does, in its own data. These doors hand it
// one of filex's links and say what it answered.
//
//	GET  /api/files/short-links?share=<token>   the address the link has, "" for none
//	POST /api/files/short-links {"share": "<token>"}   one, made when there is none
//
// The link must be one the caller may mail (mailableShare: its creator or an
// administrator, still working, and what making it took on its item today),
// and the caller must hold the app's own permission when the role names one.

// ShortLinks is the "Get short link" door.
type ShortLinks struct {
	Grants   *Grants
	Apps     *AppPlugins
	Registry *wasmplugin.Registry
}

// Available reports whether the caller is offered "Get short link":
// a shortener app runs and the caller holds its permission.
func (h *ShortLinks) Available(r *http.Request) bool {
	if h == nil || h.Registry == nil {
		return false
	}
	p, ok := h.Registry.Shortener()
	if !ok {
		return false
	}
	return h.holds(r, p)
}

func (h *ShortLinks) holds(r *http.Request, p *wasmplugin.Installed) bool {
	return h.holdsUser(r.Context(), auth.UserFrom(r.Context()), p)
}

// holdsUser is holds for an account that is not the request's (an
// uploader's owner, whose key sent the upload).
func (h *ShortLinks) holdsUser(ctx context.Context, u *model.User, p *wasmplugin.Installed) bool {
	id := p.Manifest.LinkShortener.Requires
	if id == "" || h.Apps == nil || h.Apps.ACL == nil {
		return true
	}
	up, ok := p.Manifest.UserPermission(id)
	if !ok || u == nil {
		return false
	}
	res, err := h.Apps.ACL.Perms(ctx, u)
	if err != nil || res == nil {
		return false
	}
	_, allowed, _ := appPermHeld(res, p, up)
	return allowed
}

// ForUpload is the address an upload answers with when its uploader asks
// for a short one (model.Uploader.ShortLink): the shortener's, for the
// uploader's owner, or "" for none - the upload then answers the link
// itself. A busy or failing shortener never fails an upload.
func (h *ShortLinks) ForUpload(ctx context.Context, owner *model.User, target wire.ShortenTarget) string {
	if h == nil || h.Registry == nil || owner == nil {
		return ""
	}
	p, ok := h.Registry.Shortener()
	if !ok || !h.holdsUser(ctx, owner, p) {
		return ""
	}
	out, err := h.Registry.Shorten(ctx, owner, owner.Locale, wire.ShortenInput{Target: target})
	if err != nil || out == nil || len(out.Error) > 0 {
		return ""
	}
	return out.URL
}

// target is the filex link a share is, as the shortener is handed it.
func (h *ShortLinks) target(r *http.Request, m *mailableShare) wire.ShortenTarget {
	sh := m.share
	kind := wire.ShortenShare
	if sh.IsDrop() {
		kind = wire.ShortenFileRequest
	}
	name := m.node.Name
	if name == "" {
		name = baseName(m.rel)
	}
	return wire.ShortenTarget{Kind: kind, URL: publicLinkOf(h.Grants.Tenants.FromRequest(r), sh), Link: sh.Token, Name: name}
}

// Get answers GET /api/files/short-links?share=<token>.
func (h *ShortLinks) Get(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, strings.TrimSpace(r.URL.Query().Get("share")), true)
}

// Post answers POST /api/files/short-links {"share"}.
func (h *ShortLinks) Post(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Share string `json:"share"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	h.serve(w, r, strings.TrimSpace(req.Share), false)
}

func (h *ShortLinks) serve(w http.ResponseWriter, r *http.Request, token string, existing bool) {
	if h.Registry == nil {
		writeError(w, r, http.StatusConflict, "no_shortener", nil)
		return
	}
	p, ok := h.Registry.Shortener()
	if !ok {
		writeError(w, r, http.StatusConflict, "no_shortener", nil)
		return
	}
	if token == "" {
		writeError(w, r, http.StatusBadRequest, "missing share", nil)
		return
	}
	m, ok := h.Grants.mailableShare(w, r, token, 0, langOf(r))
	if !ok {
		return
	}
	if !h.holds(r, p) {
		// The door's own refusal, naming the permission (appUserPermOK).
		if h.Apps != nil && !h.Apps.appUserPermOK(w, r, p, p.Manifest.LinkShortener.Requires) {
			return
		}
		writeError(w, r, http.StatusForbidden, "permission_denied", nil)
		return
	}
	out, err := h.Registry.Shorten(r.Context(), auth.UserFrom(r.Context()), langOf(r),
		wire.ShortenInput{Target: h.target(r, m), Existing: existing})
	if err != nil {
		if errors.Is(err, wasmplugin.ErrNoRoute) {
			writeError(w, r, http.StatusConflict, "no_shortener", nil)
			return
		}
		writeAppCallFailure(w, r, err)
		return
	}
	if len(out.Error) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "shorten_refused", "message": out.Error.Get(langOf(r))})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": out.URL})
}

// MailAddress is the address a link is mailed (and handed to the OS share
// sheet) with: its short address when it has one the shortener still
// serves, else the link itself. Wired as Grants.ShortAddress.
func (h *ShortLinks) MailAddress(r *http.Request, sh *model.Share, long string) string {
	if h == nil || h.Registry == nil || sh == nil || sh.IsApp() {
		return long
	}
	if _, ok := h.Registry.Shortener(); !ok {
		return long
	}
	name := ""
	kind := wire.ShortenShare
	if sh.IsDrop() {
		kind = wire.ShortenFileRequest
	}
	out, err := h.Registry.Shorten(r.Context(), auth.UserFrom(r.Context()), langOf(r),
		wire.ShortenInput{Target: wire.ShortenTarget{Kind: kind, URL: long, Link: sh.Token, Name: name}, Existing: true})
	if err != nil || out == nil || out.URL == "" {
		return long
	}
	return out.URL
}
