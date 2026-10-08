package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/acl"
)

// wiring:e2 password — "the password of an encrypted folder was changed".
//
// Since 0.54 the SERVER says it, from what it saw change: the key file's
// rewrite (e2e/keyfilewatch) or the `.fxe` header's (e2e/fxewatch) is compared
// with the version the overwrite kept, and a changed password or recovery slot
// is recorded (`e2e.password_change`) and told to the owner
// (`e2e.password_changed`) by e2e/slotchange - whichever surface wrote it, and
// whether or not any client says so.
//
// Up to 0.53 both records came from HERE: the web UI announced the change once
// the key file was written, and this handler turned the announcement into the
// audit row and the owner's notification. `via` and `rekey` were whatever the
// client sent, and nothing had to have changed, so a person who could write a
// folder could have its owner told, as often as they liked, that its password
// had been reset with the recovery key.
//
// The door stays for clients older than 0.54 (a desktop app or an embed still
// calls it after a password change, and would show an error otherwise). It
// answers and records nothing; its checks stay, so it says no more than it
// used to about a path the caller cannot write. The explorer no longer calls
// it. To be removed in a later release (docs/E2E-ENCRYPTION.md → Who is told).

type e2ePasswordChangedReq struct {
	Path string `json:"path"` // wire path of the encrypted folder (or a path inside it), or of a `.fxe`
	// Via and Rekey are what a client before 0.54 sends. Read and ignored:
	// the server knows which slots changed from the key file itself.
	Via   string `json:"via"`
	Rekey bool   `json:"rekey"`
}

// PasswordChanged answers an older client's announcement and records nothing.
//
//	POST /api/files/e2e/password-changed {path, via, rekey} → {ok}
//
// Kept for older clients only: the server records a password change itself
// (e2e/slotchange).
func (h *E2E) PasswordChanged(w http.ResponseWriter, r *http.Request) {
	var req e2ePasswordChangedReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Via != "password" && req.Via != "recovery_key" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "via must be password or recovery_key"})
		return
	}
	st, rel := h.resolveDir(w, r, req.Path)
	if st == nil {
		return
	}
	root, _, ok := h.encryptedSubject(r.Context(), st.ID, rel)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": notEncrypted(rel)})
		return
	}
	if !aclAllowID(r.Context(), h.ACL, h.Store, st.ID, root, acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}
	// No audit row, no notification: a client's word is not a record.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
