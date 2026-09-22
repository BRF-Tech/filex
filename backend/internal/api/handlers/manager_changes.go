package handlers

import (
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/realtime"
)

// AttachChangeLog wires the log `action=changes` answers from. Unwired, the
// action answers 501, which clients read as "walk the tree as before".
func (h *Manager) AttachChangeLog(l *realtime.ChangeLog) { h.Changes = l }

// vfChanges answers
//
//	GET /api/files/manager?action=changes&path=<adapter>://<rel>&since=<cursor>
//	    → {"cursor": "<opaque>", "changed": bool}
//
// "has anything under this folder changed since the cursor I got last time?"
// A sync client asks this every round instead of listing its whole tree, and
// walks only when the answer is yes (or on a slower safety-net timer, for
// changes the log cannot see; see realtime.ChangeLog). No `since` — or one
// from before a restart — is always "changed".
//
// Same visibility rules as `index`: the caller must be able to see the folder,
// and a change counts only if the caller can see what it touched.
func (h *Manager) vfChanges(w http.ResponseWriter, r *http.Request, s *model.Storage, rel string) {
	if h.Changes == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "action not implemented: changes"})
		return
	}
	rel = strings.Trim(strings.TrimSpace(rel), "/")
	if pathHasDotDot(rel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path"})
		return
	}
	set, err := h.aclSet(r.Context(), s)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if set != nil && !set.CanSee(rel) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	var visible func(string) bool
	if set != nil {
		visible = set.CanSee
	}
	changed, cursor := h.Changes.ChangedSinceFor(s.ID, rel, r.URL.Query().Get("since"), visible)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"cursor": cursor, "changed": changed})
}
