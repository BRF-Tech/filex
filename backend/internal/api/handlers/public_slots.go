package handlers

/* The public link page's text slots (internal/templates slot.share.*,
slot.file_request.*): short pieces of text an administrator writes in
Liquid - a title, an introduction, a footer line - which the page (the SPA's
PublicShell) shows in fixed places of its card. Rendered here, on the
server, in the sandbox when edited, sanitized; the page shows them as they
come (and through DOMPurify, the second wall). An empty slot is the page's own
words.

⚠ Only a live link has slots: a dead one (expired, used up, withdrawn, a
creator who may no longer share) answers exactly as the info call does, so
the "not available" screen is the same for every token and says nothing
about whose link it was. And like the page's title, a slot is told the
item's name only once the PIN gate is open (describe: "the file's name is
BEHIND the PIN"). */

import (
	"net/http"
	"time"

	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/templates"
)

// PublicSlots are the three slots of a link page; "" = the page's own words.
type PublicSlots struct {
	Title  string `json:"title"`
	Intro  string `json:"intro"`
	Footer string `json:"footer"`
}

// ShareSlots answers a share link's slots.
//
//	GET /api/public/s/{token}/slots
func (h *PublicAPI) ShareSlots(w http.ResponseWriter, r *http.Request) {
	h.headers(w)
	sh, ok := h.loadShare(w, r, model.ShareKindDownload)
	if !ok {
		return
	}
	if sh.IsApp() {
		writeJSON(w, http.StatusOK, PublicSlots{})
		return
	}
	h.writeSlots(w, r, sh, "share")
}

// DropSlots answers a file request link's slots.
//
//	GET /api/public/d/{token}/slots
func (h *PublicAPI) DropSlots(w http.ResponseWriter, r *http.Request) {
	h.headers(w)
	sh, ok := h.loadShare(w, r, model.ShareKindDrop)
	if !ok {
		return
	}
	h.writeSlots(w, r, sh, "file_request")
}

func (h *PublicAPI) writeSlots(w http.ResponseWriter, r *http.Request, sh *model.Share, kind string) {
	lang := publicLocale(r, h.DefaultLocale)
	unlocked := h.unlocked(r, sh)
	item := map[string]any{"name": "", "kind": "file", "size": 0, "size_text": ""}
	if node, err := h.Store.GetNode(r.Context(), sh.NodeID); err == nil && node != nil {
		if node.Type == model.NodeTypeDirectory {
			item["kind"] = "folder"
		}
		if unlocked || kind == "file_request" {
			// A file request's folder is named on its page before the PIN
			// too (DropState.Folder): it is where the files go, not what is
			// being guarded.
			item["name"] = leafname.Visible(node.Name)
			if node.Type != model.NodeTypeDirectory && unlocked {
				item["size"] = node.Size
				item["size_text"] = srvtext.Bytes(lang, node.Size)
			}
		}
	}
	expires := ""
	if sh.ExpiresAt != nil {
		expires = sh.ExpiresAt.UTC().Format(time.RFC3339)
	}
	data := map[string]any{
		"item": item,
		"link": map[string]any{"kind": kind, "expires_at": expires, "message": sh.Subject},
	}
	rend := pageTemplates(h.Templates, h.Brand)
	t := templates.Target{TenantID: sh.TenantID, Host: r.Host, Lang: lang}
	writeJSON(w, http.StatusOK, PublicSlots{
		Title:  rend.Slot(r.Context(), t, "slot."+kind+".title", data),
		Intro:  rend.Slot(r.Context(), t, "slot."+kind+".intro", data),
		Footer: rend.Slot(r.Context(), t, "slot."+kind+".footer", data),
	})
}
