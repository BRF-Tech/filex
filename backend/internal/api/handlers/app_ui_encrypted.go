package handlers

import (
	"net/http"

	"github.com/brf-tech/filex/backend/internal/acl"
)

// refuseEncryptedRead answers `403 encrypted` when an app's interface asks
// for the bytes of a file the server holds only as ciphertext, and reports
// whether it did. Which files those are is the rule every app door asks
// (encryptedAtDoor, the rule every file row's `encrypted` says): one in an
// end-to-end encrypted folder or a vault, or a single encrypted file
// (`.fxe`) - as an app's action, its screen's call and its save are refused
// at their own doors (app_plugins.go, app_ui_call.go, app_ui.go). The
// interface's read goes through the explorer's preview route, so it is asked
// here (filex 0.55, sec055 S7). The explorer offers such a file no app
// either (packages/core lib/encryptedRow).
//
// Asked only of a path this person may read - their tenant's storage, inside
// their root, at least viewer - so the answer tells nobody whether a folder
// they cannot open is encrypted; everything else is left to the preview
// route's own refusals.
func (h *AppPlugins) refuseEncryptedRead(w http.ResponseWriter, r *http.Request, target string) bool {
	sid, rels, err := h.resolvePaths(r.Context(), 0, []string{target})
	if err != nil || len(rels) != 1 || !ownsStorageQuiet(r, sid) {
		return false
	}
	rel := rels[0]
	if !rootAllows(r.Context(), h.Store, sid, rel) || !aclAllowID(r.Context(), h.ACL, h.Store, sid, rel, acl.LevelViewer) {
		return false
	}
	return refuseEncryptedAtDoor(w, r, encryptedAtDoor(r.Context(), h.Store, sid, rel), rel, false)
}
