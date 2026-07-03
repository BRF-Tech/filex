// Package handlers — meta.go
//
// Tags / Starred / Recently-opened endpoints. All require an authenticated
// user; tags are SHARED across users (stored on node_meta), starred and
// recent are PER-USER (stored on user_node_meta).
//
//	POST /api/files/manager/tags        body {node_id, tags: []string}
//	GET  /api/files/manager/tags?node_id=…  OR ?storage_id=…
//	POST /api/files/manager/star        body {node_id, starred: bool}
//	GET  /api/files/manager/starred?storage_id=…&limit=
//	POST /api/files/manager/recent      body {node_id}
//	GET  /api/files/manager/recent?limit=
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

const (
	userMetaKeyStarred = "starred"
	userMetaKeyOpened  = "last_opened"
)

// Meta hosts the tags / star / recent endpoints.
type Meta struct {
	Store db.Store
}

// NewMeta constructs the handler.
func NewMeta(store db.Store) *Meta { return &Meta{Store: store} }

// ─────────────────── Tags ───────────────────

type tagsSetReq struct {
	NodeID int64    `json:"node_id"`
	Tags   []string `json:"tags"`
}

// SetTags replaces the full tag list for a node.
func (h *Meta) SetTags(w http.ResponseWriter, r *http.Request) {
	var req tagsSetReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad node_id"})
		return
	}
	cleaned := make([]string, 0, len(req.Tags))
	for _, t := range req.Tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || len(t) > 64 {
			continue
		}
		cleaned = append(cleaned, t)
	}
	if err := h.Store.SetNodeTags(r.Context(), req.NodeID, cleaned); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"tags": cleaned,
	})
}

// GetTags lists the tags for one node (?node_id=) or every distinct tag in
// a storage (?storage_id=). Exactly one of the two query params must be set.
func (h *Meta) GetTags(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if v := q.Get("node_id"); v != "" {
		nodeID, err := strconv.ParseInt(v, 10, 64)
		if err != nil || nodeID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad node_id"})
			return
		}
		tags, err := h.Store.GetNodeTags(r.Context(), nodeID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"node_id": nodeID, "tags": tags})
		return
	}
	if v := q.Get("storage_id"); v != "" {
		storageID, err := strconv.ParseInt(v, 10, 64)
		if err != nil || storageID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage_id"})
			return
		}
		tags, err := h.Store.ListAllTagsForStorage(r.Context(), storageID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"storage_id": storageID, "tags": tags})
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "node_id or storage_id required"})
}

// ListAllTags lists every distinct tag across all storages (alphabetical).
// Powers the "Tagged files" page's tag-chip list.
func (h *Meta) ListAllTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.Store.ListAllTags(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

// TaggedNodes lists non-deleted nodes carrying the given tag (?tag=…),
// newest-first, capped by an optional ?limit=. Empty tag → 400.
func (h *Meta) TaggedNodes(w http.ResponseWriter, r *http.Request) {
	tag := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tag")))
	if tag == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tag required"})
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 500, 1000)
	nodes, err := h.Store.ListNodesByTag(r.Context(), tag, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "tag": tag})
}

// ─────────────────── Starred ───────────────────

type starReq struct {
	NodeID  int64 `json:"node_id"`
	Starred bool  `json:"starred"`
}

// SetStar toggles the starred flag for the current user on a node.
func (h *Meta) SetStar(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var req starReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad node_id"})
		return
	}
	var err error
	if req.Starred {
		err = h.Store.SetUserNodeMeta(r.Context(), u.ID, req.NodeID, userMetaKeyStarred, "1")
	} else {
		err = h.Store.DeleteUserNodeMeta(r.Context(), u.ID, req.NodeID, userMetaKeyStarred)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"starred": req.Starred,
		"node_id": req.NodeID,
	})
}

// ListStarred returns the user's starred nodes (newest-first by star time).
func (h *Meta) ListStarred(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 500)
	nodes, err := h.Store.ListNodesByUserMeta(r.Context(), u.ID, userMetaKeyStarred, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if v := r.URL.Query().Get("storage_id"); v != "" {
		if storageID, err := strconv.ParseInt(v, 10, 64); err == nil && storageID > 0 {
			nodes = filterByStorage(nodes, storageID)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "limit": limit})
}

// ─────────────────── Recently opened ───────────────────

type recentReq struct {
	NodeID int64 `json:"node_id"`
}

// SetRecent records that the current user opened a node.
func (h *Meta) SetRecent(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var req recentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad node_id"})
		return
	}
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	if err := h.Store.SetUserNodeMeta(r.Context(), u.ID, req.NodeID, userMetaKeyOpened, ts); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ListRecent returns nodes the current user opened recently (newest-first).
func (h *Meta) ListRecent(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 20, 200)
	nodes, err := h.Store.ListNodesByUserMeta(r.Context(), u.ID, userMetaKeyOpened, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "limit": limit})
}

// parseLimit defaults / clamps a string query param.
func parseLimit(s string, def, max int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// filterByStorage narrows the slice to nodes in a particular storage.
func filterByStorage(in []*model.Node, storageID int64) []*model.Node {
	out := in[:0]
	for _, n := range in {
		if n.StorageID == storageID {
			out = append(out, n)
		}
	}
	return out
}
