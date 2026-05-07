// Package handlers contains one file per logical HTTP route group.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"gitlab.com/brftech/filemanager/backend/internal/auth"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/model"
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

// List dispatches between two query shapes on the same path:
//
//  1. Native (admin SPA, trash, etc.): ?storage=<id>&parent=<id>
//     Returns {nodes:[…model.Node]} from the DB cache.
//
//  2. Vuefinder/FileExplorer SFC: ?action=<verb>&path=<adapter://rel>
//     (?q=<verb> is also accepted as a legacy alias.) Returns the
//     {adapter, storages, dirname, read_only, files:[FileNode]} shape
//     that @brftech/filex-core expects. Only `index`, `search`,
//     `subfolders` are wired today — other actions return 501 so the
//     UI can still render and warn rather than 404.
//
// Keeping both behind one route avoids breaking the existing Explore
// page contract while letting the SFC mount unchanged.
func (h *Manager) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	action := q.Get("action")
	if action == "" {
		action = q.Get("q")
	}
	if action != "" {
		h.listVuefinder(w, r, action)
		return
	}

	storageID, err := strconv.ParseInt(q.Get("storage"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage id"})
		return
	}
	var parentPtr *int64
	if v := q.Get("parent"); v != "" {
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

// listVuefinder serves the @brftech/filex-core "Vuefinder-style"
// manager response. The contract:
//
//	GET /api/files/manager?action=index&path=<adapter>://<relpath>
//	    → {adapter, storages, dirname, read_only, files:[FileNode]}
//
// Adapter == storage name. We resolve it to a storage row, walk down
// the requested path inside the DB cache, and project the children
// onto the FileNode shape the SFC expects. No driver round-trip — the
// sync worker keeps the cache fresh.
func (h *Manager) listVuefinder(w http.ResponseWriter, r *http.Request, action string) {
	q := r.URL.Query()
	pathStr := q.Get("path")

	storages, err := h.Store.ListEnabledStorages(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	storageNames := make([]string, 0, len(storages))
	for _, s := range storages {
		storageNames = append(storageNames, s.Name)
	}

	// Pick the adapter (= storage name) from the path prefix; fall
	// back to the first storage when the caller didn't specify one.
	adapter, rel := splitAdapterPath(pathStr)
	if adapter == "" {
		if len(storages) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"adapter":   "",
				"storages":  storageNames,
				"dirname":   "",
				"read_only": false,
				"files":     []any{},
			})
			return
		}
		adapter = storages[0].Name
	}

	var current *model.Storage
	for _, s := range storages {
		if s.Name == adapter {
			current = s
			break
		}
	}
	if current == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown adapter: " + adapter})
		return
	}

	switch action {
	case "index", "subfolders":
		h.vfIndex(w, r, current, rel, storageNames, action == "subfolders")
		return
	case "search":
		filter := q.Get("filter")
		if filter == "" {
			filter = q.Get("q_filter")
		}
		h.vfSearch(w, r, current, rel, filter, storageNames)
		return
	default:
		// Mutating actions (newfolder, rename, move, delete, upload)
		// are intentionally not wired here — the SFC's read path is
		// what matters for the demo Explore page. Returning 501 is
		// surfaced to the user via the FileExplorer toast rather than
		// a route 404.
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "action not implemented: " + action})
	}
}

// vfIndex resolves a relative path inside a storage to a parent
// node ID, lists children, and returns the FileNode-shaped response.
func (h *Manager) vfIndex(w http.ResponseWriter, r *http.Request, s *model.Storage, rel string, storageNames []string, dirsOnly bool) {
	parentID, dirname, err := h.resolveDirNode(r.Context(), s.ID, rel)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	nodes, err := h.Store.ListNodesByParent(r.Context(), s.ID, parentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	files := projectFileNodes(s.Name, nodes, dirsOnly)
	if dirsOnly {
		writeJSON(w, http.StatusOK, map[string]any{"folders": files})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"adapter":   s.Name,
		"storages":  storageNames,
		"dirname":   joinAdapterPath(s.Name, dirname),
		"read_only": s.ReadOnly,
		"files":     files,
	})
}

// vfSearch runs a LIKE search inside the storage and projects the
// matches onto FileNode shape. The dirname stays at the requested
// folder so the breadcrumb keeps its place.
func (h *Manager) vfSearch(w http.ResponseWriter, r *http.Request, s *model.Storage, rel, filter string, storageNames []string) {
	if filter == "" {
		h.vfIndex(w, r, s, rel, storageNames, false)
		return
	}
	nodes, err := h.Store.SearchNodes(r.Context(), s.ID, filter, 250)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	files := projectFileNodes(s.Name, nodes, false)
	writeJSON(w, http.StatusOK, map[string]any{
		"adapter":   s.Name,
		"storages":  storageNames,
		"dirname":   joinAdapterPath(s.Name, rel),
		"read_only": s.ReadOnly,
		"files":     files,
	})
}

// resolveDirNode walks `rel` (slash-separated) under the storage root
// and returns the parent ID at which to list. An empty rel == root
// (parentID == nil). The returned dirname is normalised (no leading/
// trailing slashes) so callers can re-join it with the adapter.
func (h *Manager) resolveDirNode(ctx ctxAlias, storageID int64, rel string) (*int64, string, error) {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return nil, "", nil
	}
	parts := strings.Split(rel, "/")
	var parentPtr *int64
	for _, segment := range parts {
		if segment == "" {
			continue
		}
		nodes, err := h.Store.ListNodesByParent(ctx, storageID, parentPtr)
		if err != nil {
			return nil, "", err
		}
		matched := false
		for _, n := range nodes {
			if n.Name == segment && n.Type == model.NodeTypeDirectory {
				id := n.ID
				parentPtr = &id
				matched = true
				break
			}
		}
		if !matched {
			return nil, "", fmt.Errorf("directory not found: %s", segment)
		}
	}
	return parentPtr, rel, nil
}

// ctxAlias is just context.Context — declared as an alias here so
// resolveDirNode keeps a stable signature without dragging another
// import alias into the file.
type ctxAlias = context.Context

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

// splitAdapterPath separates `adapter://relative/path` into `adapter`
// and `relative/path`. Falls back to `("", path)` when the input is
// already a bare relative path (FileExplorer occasionally calls back
// with the dirname stripped).
func splitAdapterPath(raw string) (adapter string, rel string) {
	idx := strings.Index(raw, "://")
	if idx < 0 {
		return "", strings.Trim(raw, "/")
	}
	return raw[:idx], strings.Trim(raw[idx+3:], "/")
}

// joinAdapterPath does the reverse — `adapter://rel`. Empty rel
// degenerates to `adapter://`.
func joinAdapterPath(adapter, rel string) string {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return adapter + "://"
	}
	return adapter + "://" + rel
}

// projectFileNodes shapes DB nodes into the FileExplorer FileNode
// contract. The frontend keys it cares about: path, basename, type,
// extension, size, last_modified, mime_type. We always ship the
// adapter-qualified `path` so deep-link routing keeps working.
func projectFileNodes(adapter string, nodes []*model.Node, dirsOnly bool) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		if n.DeletedAt != nil {
			continue
		}
		isDir := n.Type == model.NodeTypeDirectory
		if dirsOnly && !isDir {
			continue
		}
		typ := "file"
		if isDir {
			typ = "dir"
		}
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(n.Name), "."))
		entry := map[string]any{
			"path":      joinAdapterPath(adapter, n.Path),
			"basename":  n.Name,
			"type":      typ,
			"extension": ext,
			"size":      n.Size,
			"mime_type": n.Mime,
			"storage":   adapter,
		}
		if n.BackendMtime != nil {
			entry["last_modified"] = n.BackendMtime.UnixMilli()
		}
		out = append(out, entry)
	}
	return out
}
