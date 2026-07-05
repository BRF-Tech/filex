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
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Manager handles read-only browsing endpoints under /api/files/manager.
type Manager struct {
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
	// Index is consulted by `vfSearch` BEFORE falling back to SQL LIKE.
	// nil is fine — search degrades to LIKE-only.
	Index *search.Index
	// Thumbs, when wired, generates a thumbnail asynchronously after a
	// successful upload. nil is fine — uploads still succeed, callers
	// just don't get an automatic preview in the grid view.
	Thumbs ThumbPipeline
	// ACL enforces per-user/per-item access control. nil disables
	// enforcement (tests / list-only environments) → legacy all-access.
	ACL *acl.Resolver
}

// ThumbPipeline is the narrow surface manager_mutate needs to fire a
// thumbnail job after upload. Kept as an interface so the package
// doesn't have to import `internal/thumb` (which would create an
// import cycle through the storage resolver wiring).
type ThumbPipeline interface {
	GenerateThumb(ctx context.Context, node *model.Node) error
}

// AttachThumbPipeline wires the pipeline so vfUpload can dispatch a
// generation job per uploaded file.
func (h *Manager) AttachThumbPipeline(p ThumbPipeline) {
	h.Thumbs = p
}

// NewManager constructs a Manager handler.
//
// resolver may be nil for tests / list-only environments — the Read handler
// will return 503 in that case.
func NewManager(store db.Store, resolver func(int64) (storage.Driver, error)) *Manager {
	return &Manager{Store: store, StorageResolver: resolver}
}

// AttachSearchIndex wires the Bleve index into the manager. Optional —
// without it, vfSearch falls back to SQL LIKE only.
func (h *Manager) AttachSearchIndex(idx *search.Index) {
	h.Index = idx
}

// AttachACL wires the RBAC/ACL resolver so listings are filtered and reads
// are gated by the caller's grants. Optional — nil means no enforcement.
func (h *Manager) AttachACL(r *acl.Resolver) { h.ACL = r }

// aclSet loads the caller's ACL set for storage s (nil when ACL is unwired).
func (h *Manager) aclSet(ctx context.Context, s *model.Storage) (*acl.Set, error) {
	if h.ACL == nil {
		return nil, nil
	}
	return h.ACL.LoadSet(ctx, auth.UserFrom(ctx), s)
}

// aclSetByID resolves storageID to its row then loads the caller's ACL set.
func (h *Manager) aclSetByID(ctx context.Context, storageID int64) (*acl.Set, error) {
	if h.ACL == nil {
		return nil, nil
	}
	st, err := h.Store.GetStorage(ctx, storageID)
	if err != nil {
		return nil, err
	}
	return h.ACL.LoadSet(ctx, auth.UserFrom(ctx), st)
}

// allowed reports whether the caller has at least `need` on rel within s.
// Unwired ACL (tests) allows; a load error denies.
func (h *Manager) allowed(ctx context.Context, s *model.Storage, rel string, need acl.Level) bool {
	if h.ACL == nil {
		return true
	}
	set, err := h.ACL.LoadSet(ctx, auth.UserFrom(ctx), s)
	if err != nil || set == nil {
		return false
	}
	return set.Effective(rel) >= need
}

// allowedByID is allowed() keyed by storage id (for id-based read/stat).
func (h *Manager) allowedByID(ctx context.Context, storageID int64, rel string, need acl.Level) bool {
	if h.ACL == nil {
		return true
	}
	st, err := h.Store.GetStorage(ctx, storageID)
	if err != nil {
		return false
	}
	set, err := h.ACL.LoadSet(ctx, auth.UserFrom(ctx), st)
	if err != nil || set == nil {
		return false
	}
	return set.Effective(rel) >= need
}

// indexNode is a no-op if no index is wired. Errors are swallowed —
// search staleness is not worth failing a write.
func (h *Manager) indexNode(ctx context.Context, n *model.Node) {
	if h.Index == nil || n == nil {
		return
	}
	_ = h.Index.IndexNode(ctx, n)
}

// removeFromIndex mirrors indexNode for soft-delete / hard-delete paths.
func (h *Manager) removeFromIndex(ctx context.Context, id int64) {
	if h.Index == nil {
		return
	}
	_ = h.Index.DeleteNode(ctx, id)
}

// dispatchThumb fires the thumbnail pipeline asynchronously after an
// upload commits. Detached context: the HTTP request returns before
// the generation finishes, so we don't want a client disconnect to
// abort an office→PDF conversion mid-flight. Errors are swallowed —
// the pipeline already logs internally and the grid view falls back
// to the generic icon when no thumb is ready.
func (h *Manager) dispatchThumb(n *model.Node) {
	if h.Thumbs == nil || n == nil {
		return
	}
	go func(node *model.Node) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_ = h.Thumbs.GenerateThumb(ctx, node)
	}(n)
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
	// RBAC: hide the whole storage / individual entries the caller wasn't
	// granted. Admin + RBAC-off storages keep the full list.
	if set, serr := h.aclSetByID(r.Context(), storageID); serr == nil && set != nil {
		if !set.StorageVisible() {
			writeJSON(w, http.StatusOK, map[string]any{"nodes": []any{}})
			return
		}
		kept := nodes[:0]
		for _, n := range nodes {
			if set.CanSee(n.Path) {
				kept = append(kept, n)
			}
		}
		nodes = kept
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

	// RBAC: drop storages the caller can't see (admin + RBAC-off keep all).
	// A non-admin sees an RBAC-on storage only if they hold ≥1 grant there.
	if h.ACL != nil {
		user := auth.UserFrom(r.Context())
		vis := storages[:0]
		for _, s := range storages {
			set, err := h.ACL.LoadSet(r.Context(), user, s)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			if set.StorageVisible() {
				vis = append(vis, s)
			}
		}
		storages = vis
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
	case "preview":
		h.vfStream(w, r, current, rel, false)
		return
	case "download":
		h.vfStream(w, r, current, rel, true)
		return
	default:
		// Mutating verbs (newfolder/rename/move/delete/upload) live in
		// manager_mutate.go and are dispatched from the POST route.
		// GET fallthrough lands here for an unknown action.
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "action not implemented: " + action})
	}
}

// vfStream serves a file body for action=preview (inline) and
// action=download (attachment). Path is the SFC's relative form
// (no `<adapter>://` prefix, just `dir/file.ext`). Resolves the
// file via the storage Driver — does NOT consult the DB cache so
// freshly-uploaded files appear before the next sync run.
func (h *Manager) vfStream(w http.ResponseWriter, r *http.Request, s *model.Storage, rel string, asAttachment bool) {
	if h.StorageResolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage offline"})
		return
	}
	rel = strings.TrimSpace(strings.TrimPrefix(rel, "/"))
	if rel == "" || strings.Contains(rel, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path"})
		return
	}
	// RBAC: previewing/downloading a file needs ≥viewer on it.
	if !h.allowed(r.Context(), s, rel, acl.LevelViewer) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	drv, err := h.StorageResolver(s.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	stat, err := drv.Stat(r.Context(), rel)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if stat.Kind == storage.KindDirectory {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "is a directory"})
		return
	}
	rc, err := drv.Read(r.Context(), rel)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rc.Close()

	// MIME — extension lookup wins so svg/md/csv/html render the right
	// way in the browser (DB cached + driver-reported mime is often
	// `text/plain; charset=utf-8` after sync because Go's http.Detect
	// doesn't know markdown/csv/svg, breaking inline preview). Driver
	// value is the fallback for extensions we don't recognize.
	mime := mimeByExt(rel)
	if mime == "" {
		mime = stat.Mime
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	if stat.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size, 10))
	}
	if asAttachment {
		base := path.Base(rel)
		w.Header().Set("Content-Disposition", `attachment; filename="`+base+`"`)
	} else {
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	_, _ = io.Copy(w, rc)
}

// mimeByExt picks a Content-Type from the file extension. Used when
// the storage driver's Stat doesn't carry a MIME (e.g. files written
// outside filex). Keeps the table small — the browser handles the
// long tail via X-Content-Type-Options=nosniff inline.
func mimeByExt(name string) string {
	ext := strings.ToLower(name[strings.LastIndex(name, ".")+1:])
	switch ext {
	case "txt", "log":
		return "text/plain; charset=utf-8"
	case "md":
		return "text/markdown; charset=utf-8"
	case "json":
		return "application/json"
	case "yaml", "yml":
		return "text/yaml; charset=utf-8"
	case "xml":
		return "application/xml"
	case "html", "htm":
		return "text/html; charset=utf-8"
	case "css":
		return "text/css; charset=utf-8"
	case "js", "mjs":
		return "application/javascript; charset=utf-8"
	case "csv":
		return "text/csv; charset=utf-8"
	case "go", "py", "rs", "java", "rb", "ts", "vue":
		return "text/plain; charset=utf-8"
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "svg":
		return "image/svg+xml"
	case "pdf":
		return "application/pdf"
	case "mp3":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "ogg":
		return "audio/ogg"
	case "mp4":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "zip":
		return "application/zip"
	case "tar":
		return "application/x-tar"
	case "gz":
		return "application/gzip"
	}
	return ""
}

// vfIndex resolves a relative path inside a storage to a parent
// node ID, lists children, and returns the FileNode-shaped response.
//
// Cache-first, driver-fallback: when the DB cache doesn't yet know the
// requested dir (newly created via mkdir, just renamed, external write,
// pre-sync) we ask the backing driver directly so the SFC's reactive
// store still re-renders. The next sync run reconciles the cache.
func (h *Manager) vfIndex(w http.ResponseWriter, r *http.Request, s *model.Storage, rel string, storageNames []string, dirsOnly bool) {
	// RBAC: the caller must be able to see this directory (either they have
	// ≥viewer on it, or it's an ancestor folder on the way to a grant). The
	// child projector then filters entries to just the visible ones.
	set, aerr := h.aclSet(r.Context(), s)
	if aerr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": aerr.Error()})
		return
	}
	if set != nil && !set.CanSee(rel) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	parentID, dirname, err := h.resolveDirNode(r.Context(), s.ID, rel)
	if err != nil {
		// DB cache miss — try the driver. If the dir really doesn't
		// exist there either, surface the original 404.
		if h.vfIndexFromDriver(w, r, s, rel, storageNames, dirsOnly, set) {
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	nodes, err := h.Store.ListNodesByParent(r.Context(), s.ID, parentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Pre-sync escape hatch: brand-new storages have an empty cache
	// even though the driver may have hundreds of objects sitting on
	// disk/in the bucket. Trust the driver over the cache until the
	// first sync has run — afterwards the cache is authoritative
	// (truly-empty dirs return [] without firing an extra driver
	// list call).
	if len(nodes) == 0 && s.LastSyncAt == nil {
		if h.vfIndexFromDriver(w, r, s, rel, storageNames, dirsOnly, set) {
			return
		}
	}

	// Hydrate Thumb so projectFileNodes can emit thumb_url. The
	// store's ListNodesByParent doesn't JOIN thumbnails (kept lean for
	// sync/walker callers), so we patch each file's Thumb here. N+1 at
	// list time is fine for realistic dir sizes (≤ low thousands);
	// switch to a batched lookup if profiles ever flag it.
	for _, n := range nodes {
		if n.Type != model.NodeTypeFile {
			continue
		}
		if t, terr := h.Store.GetThumbnail(r.Context(), n.ID); terr == nil && t != nil {
			n.Thumb = t
		}
	}
	files := projectFileNodes(s.Name, nodes, dirsOnly, set)
	if dirsOnly {
		writeJSON(w, http.StatusOK, map[string]any{"folders": files})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"adapter":   s.Name,
		"storages":  storageNames,
		"dirname":   joinAdapterPath(s.Name, dirname),
		"read_only": s.ReadOnly,
		"perm":      permString(set, rel),
		"files":     files,
	})
}

// permString is the caller's effective level on rel as a string ("" when ACL
// is unwired). Fed to the FileExplorer SFC so it can gate edit/convert/manage
// affordances client-side (backend still enforces).
func permString(set *acl.Set, rel string) string {
	if set == nil {
		return ""
	}
	return set.Effective(rel).String()
}

// vfIndexFromDriver lists `rel` directly via the storage driver and
// writes the same vuefinder response shape vfIndex does. Used as a
// fallback when DB cache is missing the dir (post-mutation, pre-sync).
//
// Returns true iff a response was written. False means the driver also
// doesn't have the dir (or no resolver) — caller should write its own
// 404 with the cache-side error message.
func (h *Manager) vfIndexFromDriver(w http.ResponseWriter, r *http.Request, s *model.Storage, rel string, storageNames []string, dirsOnly bool, set *acl.Set) bool {
	if h.StorageResolver == nil {
		return false
	}
	drv, err := h.StorageResolver(s.ID)
	if err != nil {
		return false
	}
	clean := strings.Trim(rel, "/")
	// Use List (not Stat) to verify the dir — many drivers (S3, GCS,
	// blob stores) only know about objects, not "directories", and
	// HeadObject on a prefix returns 404 even when listing it shows
	// children. A successful List with no error is the canonical
	// "this dir is browsable" signal across every driver we ship.
	objs, err := drv.List(r.Context(), clean)
	if err != nil {
		return false
	}
	files := projectDriverObjects(s.Name, clean, objs, dirsOnly, set)
	if dirsOnly {
		writeJSON(w, http.StatusOK, map[string]any{"folders": files})
		return true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"adapter":   s.Name,
		"storages":  storageNames,
		"dirname":   joinAdapterPath(s.Name, clean),
		"read_only": s.ReadOnly,
		"perm":      permString(set, clean),
		"files":     files,
	})
	return true
}

// projectDriverObjects shapes storage.Object entries into the same
// FileNode contract projectFileNodes emits from DB rows. Used by the
// driver-fallback path in vfIndex when the cache is cold.
func projectDriverObjects(adapter, dir string, objs []storage.Object, dirsOnly bool, set *acl.Set) []map[string]any {
	out := make([]map[string]any, 0, len(objs))
	for _, o := range objs {
		isDir := o.Kind == storage.KindDirectory
		if dirsOnly && !isDir {
			continue
		}
		typ := "file"
		if isDir {
			typ = "dir"
		}
		// Hide the same internal entries the cache projector hides.
		if strings.Contains(o.Path, ".thumbs") || o.Name == ".keepdir" ||
			o.Name == ".versions" || strings.Contains(o.Path, ".versions") {
			continue
		}
		// Trash bucket — never expose in regular listings.
		if o.Name == ".filex-trash" || strings.Contains(o.Path, ".filex-trash") {
			continue
		}
		rel := o.Path
		if rel == "" {
			rel = path.Join(dir, o.Name)
		}
		// RBAC: drop entries the caller isn't allowed to see.
		if set != nil && !set.CanSee(rel) {
			continue
		}
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(o.Name), "."))
		entry := map[string]any{
			"path":      joinAdapterPath(adapter, rel),
			"basename":  o.Name,
			"type":      typ,
			"extension": ext,
			"size":      o.Size,
			"mime_type": o.Mime,
			"storage":   adapter,
		}
		if set != nil {
			entry["perm"] = set.Effective(acl.CleanRel(rel)).String()
		}
		if !o.Mtime.IsZero() {
			entry["last_modified"] = o.Mtime.UnixMilli()
		}
		out = append(out, entry)
	}
	return out
}

// vfSearch runs a search inside the storage and projects matches onto
// the FileNode shape. The dirname stays at the requested folder so the
// breadcrumb keeps its place.
//
// Strategy: try the Bleve full-text index first (handles content + name
// matching, fuzzy, prefix). Fall back to SQL LIKE on `nodes.name` when
// the index is missing, returns nothing, or errors.
func (h *Manager) vfSearch(w http.ResponseWriter, r *http.Request, s *model.Storage, rel, filter string, storageNames []string) {
	if filter == "" {
		h.vfIndex(w, r, s, rel, storageNames, false)
		return
	}

	// Cross-storage mode — when the SPA is showing the multi-storage
	// virtual root (rel == "") and more than one storage is enabled,
	// search every storage. Otherwise scope to the current storage.
	crossStorage := rel == "" && len(storageNames) > 1
	var nodes []*model.Node

	// 1) Bleve.
	if h.Index != nil {
		hits := h.Index.SafeSearch(r.Context(), filter, 250)
		for _, hit := range hits {
			n, err := h.Store.GetNode(r.Context(), hit.NodeID)
			if err != nil || n == nil || n.DeletedAt != nil {
				continue
			}
			if !crossStorage && n.StorageID != s.ID {
				continue
			}
			nodes = append(nodes, n)
		}
	}

	// 2) Fall back to SQL LIKE when the index didn't return anything.
	if len(nodes) == 0 {
		if crossStorage {
			// Walk every enabled storage with the LIKE fallback.
			storages, err := h.Store.ListEnabledStorages(r.Context())
			if err == nil {
				for _, st := range storages {
					rows, err := h.Store.SearchNodes(r.Context(), st.ID, search.SQLLike(filter), 100)
					if err != nil {
						continue
					}
					nodes = append(nodes, rows...)
				}
			}
		} else {
			fallback, err := h.Store.SearchNodes(r.Context(), s.ID, search.SQLLike(filter), 250)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			nodes = fallback
		}
	}

	// RBAC: drop hits the caller isn't allowed to see. Search can be
	// cross-storage, so resolve a per-storage ACL set (cached) and test
	// each hit against its own storage's grants.
	if h.ACL != nil {
		user := auth.UserFrom(r.Context())
		cache := map[int64]*acl.Set{}
		filtered := nodes[:0]
		for _, n := range nodes {
			set, ok := cache[n.StorageID]
			if !ok {
				st := s
				if n.StorageID != s.ID {
					st, _ = h.Store.GetStorage(r.Context(), n.StorageID)
				}
				set, _ = h.ACL.LoadSet(r.Context(), user, st)
				cache[n.StorageID] = set
			}
			if set == nil || set.CanSee(n.Path) {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

	// Hydrate thumb metadata so search results carry the same
	// thumb_url as the index listing (was always empty pre-v0.1.16).
	for _, n := range nodes {
		if n.Type != model.NodeTypeFile {
			continue
		}
		if t, terr := h.Store.GetThumbnail(r.Context(), n.ID); terr == nil && t != nil {
			n.Thumb = t
		}
	}

	files := projectFileNodes(s.Name, nodes, false, nil)
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
	// RBAC: metadata is a read — needs ≥viewer on the node's path.
	if !h.allowedByID(r.Context(), node.StorageID, node.Path, acl.LevelViewer) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
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
		aclRel    string // logical rel path for the ACL check (not StorageKey)
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
		aclRel = node.Path
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
		aclRel = filePath
		nodeName = path.Base(filePath)
	}

	// RBAC: reading file bytes needs ≥viewer on the logical path. This is
	// where the session-user gap finally closes for direct byte access.
	if !h.allowedByID(r.Context(), storageID, aclRel, acl.LevelViewer) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
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
// contract. The frontend keys it cares about: id, path, basename,
// type, extension, size, last_modified, mime_type, thumb_url. We
// always ship the adapter-qualified `path` so deep-link routing keeps
// working.
func projectFileNodes(adapter string, nodes []*model.Node, dirsOnly bool, set *acl.Set) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		if n.DeletedAt != nil {
			continue
		}
		// Hide internal buckets (trash, version history, thumbnails) from
		// regular listings — they have dedicated surfaces / are implementation
		// detail. The trash bucket lists via /admin/trash.
		if strings.HasPrefix(n.Path, "/.filex-trash") || strings.HasPrefix(n.Path, ".filex-trash") || n.Name == ".filex-trash" {
			continue
		}
		if n.Name == ".versions" || n.Name == ".thumbs" ||
			strings.Contains(n.Path, "/.versions") || strings.Contains(n.Path, "/.thumbs") {
			continue
		}
		// RBAC: drop entries the caller isn't allowed to see.
		if set != nil && !set.CanSee(n.Path) {
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
			"id":        n.ID,
			"path":      joinAdapterPath(adapter, n.Path),
			"basename":  n.Name,
			"type":      typ,
			"extension": ext,
			"size":      n.Size,
			"mime_type": n.Mime,
			"storage":   adapter,
		}
		if set != nil {
			entry["perm"] = set.Effective(acl.CleanRel(n.Path)).String()
		}
		// Thumbnail URL — populated when the pipeline rendered one.
		// The /api/files/thumb/{id} endpoint streams it. We allow
		// "ready" or any non-pending/non-failed state through to keep
		// the UI optimistic; if the file isn't actually there the
		// thumb endpoint 404s and the SFC falls back to its icon.
		if !isDir && n.Thumb != nil && (n.Thumb.State == "ready" || n.Thumb.State == "") && n.Thumb.StorageKey != "" {
			entry["thumb_url"] = "/api/files/thumb/" + strconv.FormatInt(n.ID, 10)
		}
		if n.BackendMtime != nil {
			entry["last_modified"] = n.BackendMtime.UnixMilli()
		} else if !n.CreatedAt.IsZero() {
			// No backend mtime (e.g. an empty folder on a synthetic-dir store —
			// nothing to aggregate one from) — fall back to when filex first saw
			// the node so the row still shows a date.
			entry["last_modified"] = n.CreatedAt.UnixMilli()
		}
		out = append(out, entry)
	}
	return out
}
