package onlyoffice

// Who may answer for an editing session (filex 0.54).
//
// "Write mine" and "Keep the outside version" (POST /api/files/onlyoffice/
// session, session_base.go) decide what becomes of a session's save: the
// second one throws it away. Until 0.54 the answer was taken from anybody who
// could modify the file and sent the session's key, and every viewer's editor
// configuration carries that key - so somebody who never edited could drop the
// save of somebody who did.
//
// An answer is taken only from a person filex handed an EDITING session of
// that key, and only for a key filex made for that document (KeyBelongsTo):
//
//   - this process's record of who was handed it (openersOf,
//     callback_identity.go), or
//   - the editor configuration they were handed (its `token`, which the page
//     sends with the answer): filex signed it, so any instance - after a
//     restart, behind a load balancer - can check that it names this key, this
//     person and the edit mode.
//
// Every answer taken is recorded: an audit row (file.office_session_answered)
// with the person and the answer, and a log line.

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
)

// AuditActionSessionAnswered: a person answered for an editing session whose
// document changed outside it (meta `answer`: "mine" or "theirs").
const AuditActionSessionAnswered = "file.office_session_answered"

// MayAnswer reports whether uid may answer for the editing session with key on
// node (see the header). editorToken is the `token` of the editor
// configuration the person was handed, or empty.
func (s *Service) MayAnswer(ctx context.Context, node *model.Node, key string, uid int64, editorToken string) bool {
	key = strings.TrimSpace(key)
	if s == nil || node == nil || key == "" || uid <= 0 || !s.KeyBelongsTo(ctx, key, node) {
		return false
	}
	if opened, ok := s.openersOf(key); ok && opened[uid] {
		return true
	}
	return s.editorTokenNames(ctx, editorToken, key, uid)
}

// editorTokenNames: tok is an editor configuration signed with the secret in
// force, for an EDITING session of key, handed to uid.
func (s *Service) editorTokenNames(ctx context.Context, tok, key string, uid int64) bool {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return false
	}
	_, secret := s.settings(ctx)
	if secret == "" {
		return false
	}
	claims, err := verifyHS256(tok, secret)
	if err != nil {
		return false
	}
	doc, _ := claims["document"].(map[string]any)
	ec, _ := claims["editorConfig"].(map[string]any)
	if doc == nil || ec == nil {
		return false
	}
	user, _ := ec["user"].(map[string]any)
	docKey, _ := doc["key"].(string)
	mode, _ := ec["mode"].(string)
	id, _ := user["id"].(string)
	return docKey == key && mode == "edit" && id == strconv.FormatInt(uid, 10)
}

// NoteAnswer records who answered for an editing session, and what.
func (s *Service) NoteAnswer(ctx context.Context, node *model.Node, uid int64, answer string) {
	if node == nil {
		return
	}
	slog.Info("onlyoffice session: answered",
		slog.Int64("storage", node.StorageID), slog.String("path", node.Path),
		slog.Int64("user", uid), slog.String("answer", answer))
	s.audit(ctx, AuditActionSessionAnswered, []int64{uid}, node.ID, map[string]any{
		"target_name": node.Path, "storage_id": node.StorageID, "answer": answer,
	})
}
