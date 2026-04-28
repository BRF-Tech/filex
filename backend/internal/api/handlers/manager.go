// Package handlers contains one file per logical HTTP route group.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"

	"gitlab.com/brftech/filemanager/backend/internal/auth"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
)

// Manager handles read-only browsing endpoints under /api/files/manager.
type Manager struct {
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
}

// NewManager constructs a Manager handler.
//
// resolver may be nil for tests / list-only environments — the Read handler
// will return 503 in that case.
func NewManager(store db.Store, resolver func(int64) (storage.Driver, error)) *Manager {
	return &Manager{Store: store, StorageResolver: resolver}
}

// List returns the children of a node by ID, or root if no id given.
//
// Query: ?storage=<id>&parent=<id>
func (h *Manager) List(w http.ResponseWriter, r *http.Request) {
	storageID, err := strconv.ParseInt(r.URL.Query().Get("storage"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage id"})
		return
	}
	var parentPtr *int64
	if v := r.URL.Query().Get("parent"); v != "" {
		pid, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad parent id"})
			return
		}
		parentPtr = &pid
	}
	nodes, err := h.Store.ListNodesByParent(r.Context(), storageID, parentPtr)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
	})
}

// Stat returns metadata for a single node.
//
// Query: ?id=<id>
func (h *Manager) Stat(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	node, err := h.Store.GetNode(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, node)
}

// Read streams a file by node ID or by storage_id+path.
//
// Query params:
//
//	?id=<node id>             primary lookup (preferred)
//	?storage=<id>&path=<p>    fallback when caller has the path but no id
//	?download=1               force attachment Content-Disposition
//
// Auth: requires an authenticated user (route is mounted behind the auth
// middleware). Future RBAC checks slot in here once per-storage ACLs land.
func (h *Manager) Read(w http.ResponseWriter, r *http.Request) {
	if h.StorageResolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no storage resolver"})
		return
	}
	if u := auth.UserFrom(r.Context()); u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	q := r.URL.Query()
	var (
		storageID int64
		filePath  string
		nodeName  string
		nodeMime  string
		nodeSize  int64
	)
	if idStr := q.Get("id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
			return
		}
		node, err := h.Store.GetNode(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		storageID = node.StorageID
		filePath = node.Path
		if node.StorageKey != "" {
			filePath = node.StorageKey
		}
		nodeName = node.Name
		nodeMime = node.Mime
		nodeSize = node.Size
	} else {
		sid, err := strconv.ParseInt(q.Get("storage"), 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing id or storage+path"})
			return
		}
		filePath = q.Get("path")
		if filePath == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path"})
			return
		}
		storageID = sid
		nodeName = path.Base(filePath)
	}

	drv, err := h.StorageResolver(storageID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}

	// Fall back to driver Stat for mime/size when caller passed storage+path.
	if nodeMime == "" || nodeSize == 0 {
		if obj, err := drv.Stat(r.Context(), filePath); err == nil {
			if nodeMime == "" {
				nodeMime = obj.Mime
			}
			if nodeSize == 0 {
				nodeSize = obj.Size
			}
		}
	}

	rc, err := drv.Read(r.Context(), filePath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read: " + err.Error()})
		return
	}
	defer rc.Close()

	if nodeMime == "" {
		nodeMime = "application/octet-stream"
	}
	disposition := "inline"
	if q.Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", nodeMime)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, sanitizeFilename(nodeName)))
	if nodeSize > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(nodeSize, 10))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(w, rc); err != nil {
		// Headers are already flushed; nothing to do but log.
		return
	}
}

// sanitizeFilename strips characters that break Content-Disposition values.
func sanitizeFilename(s string) string {
	if s == "" {
		return "file"
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\\' || c == '\r' || c == '\n' {
			out = append(out, '_')
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
