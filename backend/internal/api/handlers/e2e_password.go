package handlers

import (
	"encoding/json"
	"net/http"
	"path"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// wiring:e2 password — "the password of an encrypted folder was changed".
//
// The change itself happens entirely in the browser: the client re-wraps the
// folder key under the new password and writes a new `.filex-e2e.json`. The
// server never sees either password and cannot tell a new key file from any
// other upload. So, as with the escrow report, the web UI ANNOUNCES the change
// once the key file is written, and this handler turns the announcement into
// the two records a person relies on: an audit-log row, and a notification to
// the folder's owner (`e2e.password_changed`) — the "your password was
// changed" message every account system sends, because a change you did not
// make is how you learn someone else holds your password or recovery key.
//
// ⚠ An announcement, not a gate, and docs/E2E-ENCRYPTION.md says so: a client
// that rewrites the key file some other way is not announced. What the
// handler does make sure of is that the announcement is not free: the caller
// must be able to WRITE the folder (the same right rewriting its key file
// takes), and the path must be an encrypted folder.
//
// wiring:e2 fxe — or a single encrypted file (`.fxe`), whose password lives in
// its own header: the same announcement, the same audit action, the owner of
// the FILE told. The server also sees the header rewrite itself, announced or
// not (e2e/fxewatch → e2e.fxe_header_rewritten).

type e2ePasswordChangedReq struct {
	Path string `json:"path"` // wire path of the encrypted folder (or a path inside it), or of a `.fxe`
	// Via is how the person proved they could change it: "password" (the
	// current one) or "recovery_key" (a reset — worth a warning).
	Via string `json:"via"`
	// Rekey is true when the folder key was replaced too (every file's key
	// re-wrapped), not only the password slot.
	Rekey bool `json:"rekey"`
}

// PasswordChanged records a folder's (or a single encrypted file's) password
// change and tells the owner.
//
//	POST /api/files/e2e/password-changed {path, via, rekey} → {ok, notified}
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
	root, file, ok := h.encryptedSubject(r.Context(), st.ID, rel)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": notEncrypted(rel)})
		return
	}
	// Rewriting the key file takes write access to the folder; announcing that
	// it was rewritten takes the same.
	if !aclAllowID(r.Context(), h.ACL, h.Store, st.ID, root, acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}

	actor := auth.UserFrom(r.Context())
	ownerID := h.folderOwner(r, st.ID, root)
	name := path.Base(root)
	if name == "." || name == "/" || name == "" {
		name = root
	}

	meta := map[string]any{
		"storage": st.Name,
		"folder":  root,
		"via":     req.Via,
		"rekey":   req.Rekey,
	}
	if file {
		delete(meta, "folder")
		meta["file"] = root
		meta["kind"] = "file"
	}
	var actorID *int64
	if actor != nil {
		actorID = &actor.ID
		meta["actor_email"] = actor.Email
	}
	target := ""
	if n, err := h.Store.GetNodeByPath(r.Context(), st.ID, pathkey.Hash(st.ID, root)); err == nil && n != nil {
		target = strconv.FormatInt(n.ID, 10)
	}
	_ = h.Store.InsertAuditEntry(r.Context(), &model.AuditEntry{
		UserID:     actorID,
		Action:     "e2e.password_change",
		TargetType: "node",
		TargetID:   target,
		Metadata:   meta,
		IP:         clientIP(r),
	})

	title := "Encrypted folder password changed"
	severity := notify.SeverityInfo
	if req.Via == "recovery_key" {
		title = "Encrypted folder password reset with its recovery key"
		severity = notify.SeverityWarning
	}
	if file {
		title = "Encrypted file password changed"
		if req.Via == "recovery_key" {
			title = "Encrypted file password reset with its recovery key"
		}
	}
	ev := notify.Event{
		Event:    notify.EventE2EPasswordChanged,
		Severity: severity,
		Title:    title,
		Body:     root,
		Node:     &notify.NodeRef{StorageID: st.ID, Path: root, Name: name},
		Target:   notify.DirTarget(root),
		Meta:     meta,
	}
	if file {
		ev.Target = notify.FileTarget(root)
	}
	if ownerID != nil {
		ev.UserID = ownerID
	}
	// Same delivery rule as the escrow report: to the OWNER (who may not be
	// the person who changed it), or to administrators when nobody owns it —
	// never scoped to the actor by default.
	emitEscrowEvent(r.Context(), ev, ownerID != nil)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "notified": ownerID != nil})
}
