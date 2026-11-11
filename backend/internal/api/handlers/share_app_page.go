package handlers

import (
	"net/http"

	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// THE NO-JS ANSWER FOR AN APP LINK.
//
// An app plugin's public page is a surface the SPA renders — a signing
// screen, a review flow. There is no honest way to run that without
// JavaScript, and pretending otherwise would mean a second renderer for every
// node type, drifting against the first.
//
// So this page does the one thing that IS possible and useful: it says what
// the link is, and it hands over the files the app exposed, as plain links.
// A signer on a locked-down browser can at least READ the document they were
// sent, which is most of what they were sent for.
//
// ⚠ The links point at /api/public/s/{token}/file/{ref} — the same route the
// SPA uses, behind the same PIN gate. The visitor reached this page by
// answering the PIN, so they carry the unlock cookie.

// renderAppPage is the no-JS body of an app link.
func (h *Share) renderAppPage(w http.ResponseWriter, r *http.Request, token string, sh *model.Share) {
	// ⚠⚠ The same reading the SPA's doors make (linkCreator, public_api.go):
	// when the account that opened this link is switched off or gone, the link
	// is over. Without it this page stays the ONE surface that still hands the
	// document out — it lists the exposed copies straight off the share row,
	// so it does not need the app, the ACL or anything else that was already
	// closed elsewhere.
	//
	// ⚠ The generic dead-link page, exactly as an unknown token gets above:
	// a visitor learns that the link is finished, never why, and never that
	// there is an account behind it at all.
	if _, live := linkCreator(r.Context(), h.Store, sh); !live {
		h.renderErrorPage(w, r, http.StatusNotFound, "notfound")
		return
	}

	lang, t, _ := h.pub(r)
	title := sh.Subject
	// ⚠ The copies come off the ROW, not from the running app: a visitor who
	// was sent a document must be able to read it even on an instance whose
	// plugin runtime is unavailable. The registry is consulted only for the
	// page's own title, which is the one thing the row does not carry.
	files := wasmplugin.ExposedFiles(sh)
	// …unless its creator may no longer download the page's document: then
	// the copies made from it are not listed (appLinkCopiesServed, which the
	// file route asks too). The page itself still renders.
	if len(files) > 0 && !appLinkCopiesServed(r.Context(), h.ACL, h.Store, sh) {
		files = nil
	}
	if title == "" && h.Apps != nil && h.Apps.Registry != nil {
		if p, ok := h.Apps.Registry.ByID(sh.PluginID); ok {
			if info := h.Apps.Registry.PageInfo(sh, p, true); info != nil {
				title = info.Title.Get(lang)
			}
		}
	}
	if title == "" {
		title = t["app_heading"]
	}
	rows := make([]any, 0, len(files))
	for _, f := range files {
		// ?download=1: a link a person follows to take the file is a
		// download, and counted as one (PublicAPI.serveExposed).
		rows = append(rows, map[string]any{"name": leafname.Visible(f.Name), "url": basepath.Path(r.Context(), "/api/public/s/"+token+"/file/"+f.Ref+"?download=1")})
	}
	w.Header().Set("Cache-Control", "no-store")
	writePublicPage(w, r, pageTemplates(h.Templates, h.Branding), linkTarget(r, sh, lang), "page.app", http.StatusOK, title, map[string]any{
		"app":   map[string]any{"title": title},
		"files": rows,
	}, nil)
}
