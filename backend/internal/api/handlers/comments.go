// Package handlers — comments.go (calisma:d3, v0.6 "Çalışma")
//
// Node comment endpoints under /api/files:
//
//	GET    /api/files/comments?node_id=…   (auth) chronological live list
//	POST   /api/files/comments             (auth) {node_id, body} add
//	DELETE /api/files/comments/{id}        (auth) author-or-admin soft delete
//
// ACL: anyone who can SEE the node (≥viewer, same aclAllowID gate the
// other file handlers use) may read and write its thread; delete is
// author-or-admin regardless of node level (enforced in internal/comments).
// Trashed/missing nodes 404 on both read and write.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/comments"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// commentEventExcerptLen caps the comment body copied into the
// comment.added webhook payload meta (spec: first 200 chars).
const commentEventExcerptLen = 200

// Comments wraps the node-comment HTTP routes.
type Comments struct {
	Store   db.Store
	Service *comments.Service
	ACL     *acl.Resolver
}

// NewComments constructs the handler (service built in-place — no other
// subsystem needs it, the trash purge hook talks to the store directly).
func NewComments(store db.Store) *Comments {
	return &Comments{Store: store, Service: comments.New(store)}
}

// AttachACL wires the RBAC resolver (nil = ACL unwired → allow, matching
// every other file handler).
func (h *Comments) AttachACL(r *acl.Resolver) { h.ACL = r }

// visibleNode resolves node_id to a live node the caller may SEE
// (≥viewer). Returns nil after writing the error response.
func (h *Comments) visibleNode(w http.ResponseWriter, r *http.Request, nodeID int64) *model.Node {
	if nodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad node_id"})
		return nil
	}
	node, err := h.Store.GetNode(r.Context(), nodeID)
	if err != nil || node == nil || node.DeletedAt != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil
	}
	if !aclAllowID(r.Context(), h.ACL, h.Store, node.StorageID, node.Path, acl.LevelViewer) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return nil
	}
	return node
}

// List returns a node's live comments, oldest first, with author names
// and a per-row `can_delete` for the caller.
//
//	GET /api/files/comments?node_id=N → {comments: […], node_id: N}
func (h *Comments) List(w http.ResponseWriter, r *http.Request) {
	nodeID, _ := strconv.ParseInt(r.URL.Query().Get("node_id"), 10, 64)
	if h.visibleNode(w, r, nodeID) == nil {
		return
	}
	list, err := h.Service.List(r.Context(), nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	user := auth.UserFrom(r.Context())
	for _, c := range list {
		c.CanDelete = comments.CanDelete(c, user)
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": list, "node_id": nodeID})
}

// commentCreateReq is the POST body.
type commentCreateReq struct {
	NodeID int64  `json:"node_id"`
	Body   string `json:"body"`
}

// Create adds a comment to a node the caller can see and emits the
// comment.added event (webhook v2 targets filter on the name).
//
//	POST /api/files/comments {node_id, body} → {comment: {…}}
func (h *Comments) Create(w http.ResponseWriter, r *http.Request) {
	var req commentCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	node := h.visibleNode(w, r, req.NodeID)
	if node == nil {
		return
	}
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	c, err := h.Service.Add(r.Context(), node.ID, user.ID, req.Body)
	if err != nil {
		if errors.Is(err, comments.ErrBadBody) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.CanDelete = true // the author may always delete their own comment

	excerpt := c.Body
	if len(excerpt) > commentEventExcerptLen {
		// Rune-safe cut (the wire contract says "first 200 chars").
		runes := []rune(excerpt)
		if len(runes) > commentEventExcerptLen {
			runes = runes[:commentEventExcerptLen]
		}
		excerpt = string(runes)
	}
	emitFileEvent(r.Context(), notify.Event{
		Event: notify.EventCommentAdded,
		Body:  node.Path,
		Node:  &notify.NodeRef{StorageID: node.StorageID, Path: node.Path, Name: node.Name, Size: node.Size},
		Meta:  map[string]any{"comment_id": c.ID, "body": excerpt},
	})

	writeJSON(w, http.StatusOK, map[string]any{"comment": c})
}

// Delete soft-deletes a comment (author or admin only).
//
//	DELETE /api/files/comments/{id} → {ok: true}
func (h *Comments) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	user := auth.UserFrom(r.Context())
	if _, err := h.Service.Delete(r.Context(), id, user); err != nil {
		switch {
		case errors.Is(err, comments.ErrForbidden):
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		case errors.Is(err, comments.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
