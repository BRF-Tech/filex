// Package handlers - ai_doors_people.go
//
// What an agent does around a file besides its bytes (task #119, item 4): the
// bell (notifications_list, notification_read), a star (file_star), comments
// (file_comments, file_comment_add, file_comment_delete) and who may open an
// item (file_permissions, file_permission_users, file_permission_set,
// file_permission_revoke) - each an MCP tool and a REST route under /api/ai.
//
// Like ai_doors.go these run the explorer's own handlers in process: the bell's
// (/api/notifications, root-confined), the star's, the comments' (with the
// comments.write permission its route asks) and the item permissions' (owner
// level and share.users, RBAC.md) - nothing is decided a second time here.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/perm"
)

// ── the bell ─────────────────────────────────────────────────────────────────

// NotificationsList is the caller's bell (GET /api/notifications): their own
// notices and the broadcasts they may see, newest first - for a `root:` token
// only the notices about files inside its root (and those that name no file).
func (a *aiOps) NotificationsList(ctx context.Context, unread bool, limit, offset int) (doorAnswer, error) {
	if a.doors == nil || a.doors.Notifications == nil {
		return doorsOff("the notification bell"), nil
	}
	q := url.Values{}
	if unread {
		q.Set("unread", "true")
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	target := "/api/notifications"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	return a.door(ctx, a.doors.Notifications.List, http.MethodGet, target, nil, nil), nil
}

// NotificationRead marks one notice read (POST /api/notifications/{id}/read),
// or every one the caller may see (`all`, POST /api/notifications/read-all).
// The caller's own bookkeeping: it needs `read`, as the bell's route, and is
// not audited, as there.
func (a *aiOps) NotificationRead(ctx context.Context, id int64, all bool) (doorAnswer, error) {
	if a.doors == nil || a.doors.Notifications == nil {
		return doorsOff("the notification bell"), nil
	}
	if all {
		return a.door(ctx, a.doors.Notifications.MarkAllRead, http.MethodPost, "/api/notifications/read-all", nil, nil), nil
	}
	if id <= 0 {
		return doorAnswer{}, badInput("id (a notice's id from notifications_list) or all: true is required")
	}
	sid := strconv.FormatInt(id, 10)
	return a.door(ctx, a.doors.Notifications.MarkRead, http.MethodPost, "/api/notifications/"+sid+"/read",
		map[string]string{"id": sid}, nil), nil
}

// ── a star ───────────────────────────────────────────────────────────────────

// Star stars (or unstars) the item at p for the caller: the explorer's star
// (POST /api/files/manager/star), which only the caller sees.
func (a *aiOps) Star(ctx context.Context, p string, star bool) (doorAnswer, error) {
	if a.doors == nil || a.doors.Meta == nil {
		return doorsOff("stars"), nil
	}
	s, rel, err := a.resolveStorage(ctx, p)
	if err != nil {
		return doorAnswer{}, err
	}
	if rel == "" {
		return doorAnswer{}, badInput("a storage root cannot be starred - name a file or folder")
	}
	n, err := catalogueOnDemand(ctx, a.store, a.resolver, a.sync(), s.ID, rel)
	if err != nil {
		return doorAnswer{}, err
	}
	return a.door(ctx, a.doors.Meta.SetStar, http.MethodPost, "/api/files/manager/star", nil,
		map[string]any{"node_id": n.ID, "starred": star}), nil
}

// ── comments ─────────────────────────────────────────────────────────────────

// commentNode is the catalogue row of the item at p, for the comment routes
// that take a node id (created on demand, as file_tags does).
func (a *aiOps) commentNode(ctx context.Context, p string) (int64, error) {
	s, rel, err := a.resolveStorage(ctx, p)
	if err != nil {
		return 0, err
	}
	if rel == "" {
		return 0, badInput("a storage root has no comments - name a file or folder")
	}
	n, err := catalogueOnDemand(ctx, a.store, a.resolver, a.sync(), s.ID, rel)
	if err != nil {
		return 0, err
	}
	return n.ID, nil
}

// Comments lists the comments on the item at p (GET /api/files/comments):
// anyone who can see the item reads them.
func (a *aiOps) Comments(ctx context.Context, p string) (doorAnswer, error) {
	if a.doors == nil || a.doors.Comments == nil {
		return doorsOff("comments"), nil
	}
	id, err := a.commentNode(ctx, p)
	if err != nil {
		return doorAnswer{}, err
	}
	return a.door(ctx, a.doors.Comments.List, http.MethodGet, "/api/files/comments?node_id="+strconv.FormatInt(id, 10), nil, nil), nil
}

// CommentAdd comments on the item at p (POST /api/files/comments, behind the
// comments.write permission its route asks): the people who can see the item
// read it, and its owner is told.
func (a *aiOps) CommentAdd(ctx context.Context, p, text string) (doorAnswer, error) {
	if a.doors == nil || a.doors.Comments == nil {
		return doorsOff("comments"), nil
	}
	if strings.TrimSpace(text) == "" {
		return doorAnswer{}, badInput("text is required")
	}
	id, err := a.commentNode(ctx, p)
	if err != nil {
		return doorAnswer{}, err
	}
	var wrap []func(http.Handler) http.Handler
	if a.doors.ACL != nil {
		wrap = append(wrap, RequirePermission(a.doors.ACL, perm.CommentsWrite))
	}
	return a.door(ctx, a.doors.Comments.Create, http.MethodPost, "/api/files/comments", nil,
		map[string]any{"node_id": id, "body": text}, wrap...), nil
}

// CommentDelete deletes a comment by its id (DELETE /api/files/comments/{id}):
// one's own, or any on one's tenant's files for an administrator.
func (a *aiOps) CommentDelete(ctx context.Context, id int64) (doorAnswer, error) {
	if a.doors == nil || a.doors.Comments == nil {
		return doorsOff("comments"), nil
	}
	if id <= 0 {
		return doorAnswer{}, badInput("id is required (a comment's id from file_comments)")
	}
	sid := strconv.FormatInt(id, 10)
	return a.door(ctx, a.doors.Comments.Delete, http.MethodDelete, "/api/files/comments/"+sid, map[string]string{"id": sid}, nil), nil
}

// ── who may open an item ─────────────────────────────────────────────────────

// Permissions lists the grants on the item at p and the folders above it (GET
// /api/files/permissions): the owner's view, as the explorer's Share dialog.
func (a *aiOps) Permissions(ctx context.Context, p string) (doorAnswer, error) {
	if a.doors == nil || a.doors.Grants == nil {
		return doorsOff("item permissions"), nil
	}
	_, rel, q, err := a.qualified(ctx, p)
	if err != nil {
		return doorAnswer{}, err
	}
	if rel == "" {
		return doorAnswer{}, badInput("name a file or folder")
	}
	return a.door(ctx, a.doors.Grants.List, http.MethodGet, "/api/files/permissions?"+url.Values{"path": {q}}.Encode(), nil, nil), nil
}

// PermissionUsers finds people to grant to (GET
// /api/files/permissions/users?q=): in the caller's tenant, by name or e-mail.
func (a *aiOps) PermissionUsers(ctx context.Context, q string) (doorAnswer, error) {
	if a.doors == nil || a.doors.Grants == nil {
		return doorsOff("item permissions"), nil
	}
	return a.door(ctx, a.doors.Grants.SearchUsers, http.MethodGet, "/api/files/permissions/users?"+url.Values{"q": {q}}.Encode(), nil, nil), nil
}

// PermissionSet grants a person (userID) or a group (groupID) a level on the
// item at p (POST /api/files/permissions): owner level on the item and the
// account's share.users, a viewer account never more than viewer - RBAC.md.
func (a *aiOps) PermissionSet(ctx context.Context, p string, userID, groupID int64, level string) (doorAnswer, error) {
	if a.doors == nil || a.doors.Grants == nil {
		return doorsOff("item permissions"), nil
	}
	if (userID <= 0) == (groupID <= 0) {
		return doorAnswer{}, badInput("exactly one of user_id and group_id is required")
	}
	if level == "" {
		return doorAnswer{}, badInput("level is required: viewer, editor or owner")
	}
	_, rel, q, err := a.qualified(ctx, p)
	if err != nil {
		return doorAnswer{}, err
	}
	if rel == "" {
		return doorAnswer{}, badInput("name a file or folder")
	}
	body := map[string]any{"path": q, "level": level}
	if userID > 0 {
		body["user_id"] = userID
	} else {
		body["group_id"] = groupID
	}
	return a.door(ctx, a.doors.Grants.Create, http.MethodPost, "/api/files/permissions", nil, body), nil
}

// PermissionRevoke removes a grant by its id (DELETE /api/files/permissions/{id},
// a group's /api/files/permissions/groups/{id} - the two are numbered apart).
func (a *aiOps) PermissionRevoke(ctx context.Context, id int64, group bool) (doorAnswer, error) {
	if a.doors == nil || a.doors.Grants == nil {
		return doorsOff("item permissions"), nil
	}
	if id <= 0 {
		return doorAnswer{}, badInput("id is required (a grant's id from file_permissions)")
	}
	sid := strconv.FormatInt(id, 10)
	if group {
		return a.door(ctx, a.doors.Grants.DeleteGroup, http.MethodDelete, "/api/files/permissions/groups/"+sid, map[string]string{"id": sid}, nil), nil
	}
	return a.door(ctx, a.doors.Grants.Delete, http.MethodDelete, "/api/files/permissions/"+sid, map[string]string{"id": sid}, nil), nil
}

// ── REST twins (/api/ai) ─────────────────────────────────────────────────────

// pathID reads the {id} of an /api/ai route.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	return opID(w, r)
}

// NotificationsList → GET /api/ai/notifications?unread=&limit=&offset=
func (h *AI) NotificationsList(w http.ResponseWriter, r *http.Request) {
	ans, err := h.ops.NotificationsList(withDoorOrigin(r.Context(), r), r.URL.Query().Get("unread") == "true", queryInt(r, "limit"), queryInt(r, "offset"))
	writeDoor(w, ans, err)
}

// NotificationRead → POST /api/ai/notifications/read {id} | {all: true}.
func (h *AI) NotificationRead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID  int64 `json:"id"`
		All bool  `json:"all"`
	}
	if !decodeAI(w, r, &body) {
		return
	}
	ans, err := h.ops.NotificationRead(withDoorOrigin(r.Context(), r), body.ID, body.All)
	writeDoor(w, ans, err)
}

// Star → POST /api/ai/star {path, star?} (star defaults to true).
func (h *AI) Star(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Star *bool  `json:"star"`
	}
	if !decodeAI(w, r, &body) {
		return
	}
	star := body.Star == nil || *body.Star
	ans, err := h.ops.Star(withDoorOrigin(r.Context(), r), body.Path, star)
	writeDoor(w, ans, err)
}

// CommentsList → GET /api/ai/comments?path=
func (h *AI) CommentsList(w http.ResponseWriter, r *http.Request) {
	ans, err := h.ops.Comments(withDoorOrigin(r.Context(), r), r.URL.Query().Get("path"))
	writeDoor(w, ans, err)
}

// CommentAdd → POST /api/ai/comments {path, text}.
func (h *AI) CommentAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	if !decodeAI(w, r, &body) {
		return
	}
	ans, err := h.ops.CommentAdd(withDoorOrigin(r.Context(), r), body.Path, body.Text)
	writeDoor(w, ans, err)
}

// CommentDelete → POST /api/ai/comments/{id}/delete
func (h *AI) CommentDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ans, err := h.ops.CommentDelete(withDoorOrigin(r.Context(), r), id)
	writeDoor(w, ans, err)
}

// PermissionsList → GET /api/ai/permissions?path=
func (h *AI) PermissionsList(w http.ResponseWriter, r *http.Request) {
	ans, err := h.ops.Permissions(withDoorOrigin(r.Context(), r), r.URL.Query().Get("path"))
	writeDoor(w, ans, err)
}

// PermissionUsers → GET /api/ai/permissions/users?q=
func (h *AI) PermissionUsers(w http.ResponseWriter, r *http.Request) {
	ans, err := h.ops.PermissionUsers(withDoorOrigin(r.Context(), r), r.URL.Query().Get("q"))
	writeDoor(w, ans, err)
}

// PermissionSet → POST /api/ai/permissions {path, user_id | group_id, level}.
func (h *AI) PermissionSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path    string `json:"path"`
		UserID  int64  `json:"user_id"`
		GroupID int64  `json:"group_id"`
		Level   string `json:"level"`
	}
	if !decodeAI(w, r, &body) {
		return
	}
	ans, err := h.ops.PermissionSet(withDoorOrigin(r.Context(), r), body.Path, body.UserID, body.GroupID, body.Level)
	writeDoor(w, ans, err)
}

// PermissionRevoke → POST /api/ai/permissions/{id}/revoke {group?}.
func (h *AI) PermissionRevoke(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// The body is optional: an empty one revokes a person's grant.
	var body struct {
		Group bool `json:"group"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	ans, err := h.ops.PermissionRevoke(withDoorOrigin(r.Context(), r), id, body.Group)
	writeDoor(w, ans, err)
}
