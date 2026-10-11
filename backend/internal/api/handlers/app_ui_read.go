package handlers

import (
	"net/http"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// UIRead hands an app's interface the bytes of a file it was opened with.
//
//	GET /api/files/plugins/ui/{plugin}/{view}/read?path=<qualified>
//
// ⚠ The read used to be decided in the page alone (filex #211, audit B19):
// AppFrame checked `files:read` itself and then fetched the file through the
// explorer's own preview route, which knows nothing of apps. The save has
// always been checked here (UISave), and now the read is too: the app is
// running, the view is its interface, the person may run apps, the app was
// granted files:read, and the administrator did not turn the app off for this
// kind (openAllowed). What follows is the preview read itself - the person's
// read permission, confinement, the storage, ranges - so an app reads nothing
// its person could not read. Nor a file the server holds only as ciphertext
// (refuseEncryptedRead): its row says `encrypted`, and the app is refused it
// as its other doors refuse an encrypted folder - unless the explorer asks
// through the encrypted folder's door (`e2e=1`, app_ui_e2e.go), where a file
// whose stored name is ciphertext (level 2) is not held to a kind the server
// cannot read: the browser asked the administrator's rule of the decrypted
// name.
func (h *AppPlugins) UIRead(w http.ResponseWriter, r *http.Request) {
	if h.off(w, r) {
		return
	}
	p, v, ok := h.uiView(w, r)
	if !ok {
		return
	}
	if !p.Grants.Has(wasmplugin.PermFilesRead) {
		writeError(w, r, http.StatusForbidden, "not_granted", apierr.Params{"permission": string(wasmplugin.PermFilesRead)})
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("path"))
	if target == "" {
		writeError(w, r, http.StatusBadRequest, "bad_path", nil)
		return
	}
	name := path.Base(strings.ReplaceAll(target, "\\", "/"))
	// `e2e=1` (filex 0.56, app_ui_e2e.go): the explorer holds the folder's key
	// and decrypts in the browser - an app granted files:e2e-plaintext is
	// handed the ciphertext the storage holds. Without it, an encrypted file
	// is refused as before.
	if wantsE2E(r) {
		handover, ok := h.e2eRead(w, r, p, target)
		if !ok {
			return
		}
		// The administrator's rule for the kind, wherever the server can
		// read the kind: a stored name that is ciphertext (level 2) tells it
		// nothing, and the browser asked that rule of the decrypted name.
		if (handover == nil || !handover.kindUnread) && !h.openAllowed(w, r, p, v, name) {
			return
		}
		if handover != nil {
			h.auditE2E(r, p, v, handover.storageID, handover.rel, "read", 0)
		}
	} else {
		if !h.openAllowed(w, r, p, v, name) {
			return
		}
		if h.refuseEncryptedRead(w, r, target) {
			return
		}
	}
	if h.Preview == nil {
		writeErrorSaid(w, r, http.StatusNotFound, "not_found", "path_missing", nil)
		return
	}
	q := r.URL.Query()
	q.Set("action", "preview")
	q.Set("path", target)
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = q.Encode()
	h.Preview(w, r2)
}
