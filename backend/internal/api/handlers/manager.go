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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/metrics"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/thumb"

	"github.com/brf-tech/filex/backend/internal/httpx"
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
	// ThumbSigner stamps the `thumb_url` this listing hands out, so a bare
	// `<img src>` in an embed can fetch it with no header and no cookie. nil
	// emits an unsigned URL, which authenticated clients still fetch fine.
	ThumbSigner *thumb.Signer
	// Staged, when wired, takes large whole-body uploads into filex's own
	// staging area and lets the ops worker move them to the driver. nil (or a
	// deployment with no staging directory configured) keeps the synchronous
	// write — the feature degrades, it never blocks an upload.
	Staged *StagedUpload
	// Body resolves where a file's bytes actually are — the storage driver,
	// or filex's staging area while a staged upload is still transferring.
	// nil is fine and degrades to driver-only reads (filebody.Resolver).
	Body *filebody.Resolver
	// Changes answers `action=changes` (manager_changes.go). nil = 501, and a
	// sync client falls back to walking its tree.
	Changes *realtime.ChangeLog
	// Quota enforces the per-user ceiling on the SYNCHRONOUS write paths.
	// Large writes reach the staged path, which checks at `begin`; without
	// this the small-file path had no ceiling at all, and a user could sail
	// past their limit a few megabytes at a time. nil disables enforcement.
	Quota *quota.Service
	// Lazy answers what a listing needs from the catalogue's own state: is it
	// complete, can it vouch for this folder, and (on a lazy storage) please
	// catalogue what was just opened. See lazy_listing.go. nil = the catalogue
	// is whatever the last scan left.
	Lazy LazyCatalogue
}

// checkQuota refuses a write that would put the acting account over its
// ceiling. The identity comes from quotastore.OwnerFrom, not from the session,
// so the public drop link is measured against the LINK CREATOR's quota — they
// are the account whose disk is being filled.
func (h *Manager) checkQuota(ctx context.Context, size int64) error {
	if h.Quota == nil || size <= 0 {
		return nil
	}
	if err := h.Quota.CheckCanWrite(ctx, quotastore.OwnerFrom(ctx), size); err != nil {
		if errors.Is(err, quota.ErrQuotaExceeded) {
			metrics.GuardRefusals.WithLabelValues(metrics.GuardQuota).Inc()
		}
		return err
	}
	return nil
}

// AttachStaged wires the staged ingest path so every surface that writes
// through IngestFile — today the public drop link — answers as soon as the
// bytes are safe inside filex rather than after the driver write. One switch,
// every surface: see the note at the top of upload_staged_ingest.go.
func (h *Manager) AttachStaged(s *StagedUpload) { h.Staged = s }

// AttachBody wires the byte-source resolver, so every read surface on this
// handler serves a file that is still being transferred out of staging
// instead of asking a driver that does not have it yet.
func (h *Manager) AttachBody(b *filebody.Resolver) { h.Body = b }

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
	// Multi-tenant: `storage` is a raw id from the client and
	// ListNodesByParent is not one of the three methods tenantstore confines,
	// so nothing below asks whose storage this is. Unfixed, this returned
	// another customer's whole catalogue — names, paths, sizes and the node
	// ids that are the key to /api/files/read?id= and /api/files/stat?id=.
	//
	// ⚠ The refusal here is an EMPTY 200, not the 404 the id-taking admin
	// routes use, and that is deliberate rather than sloppy. A storage id that
	// names nothing already answers `200 {"nodes":[]}` on this route (the
	// lookup simply finds no rows), and the handler's own existing "this
	// storage is not yours to see" answer — `!set.StorageVisible()` three lines
	// below — is the same empty 200. A 404 would therefore be the ONE answer
	// that only a real-but-foreign id produces, turning the route into a census
	// of how many storages the platform has. Borrowing the shape the handler
	// already produces is what makes the refusal invisible.
	if scope, confined := confinedScope(r.Context()); confined && !scope.CanAccessStorage(storageID) {
		writeJSON(w, http.StatusOK, map[string]any{"nodes": []any{}})
		return
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
	// Root confinement: this raw branch is addressed by ?storage=&?parent= ids,
	// which confine.Middleware cannot rewrite, so a confined token listed a
	// folder outside its root. Drop the out-of-root rows (inert unconfined).
	nodes = confineNodesToRoot(r.Context(), h.Store, nodes)
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
	infos := make([]storageInfo, 0, len(storages))
	for _, s := range storages {
		storageNames = append(storageNames, s.Name)
		infos = append(infos, storageInfo{Name: s.Name, ReadOnly: s.ReadOnly, SortOrder: s.SortOrder, Coverage: h.coverageOf(r.Context(), s)})
	}
	r = r.WithContext(withStorageInfo(r.Context(), infos))

	// Pick the adapter (= storage name) from the path prefix; fall
	// back to the first storage when the caller didn't specify one.
	adapter, rel := splitAdapterPath(pathStr)
	// ⚠⚠ The LISTING branch had no traversal guard while download, preview and
	// every mutating verb did, and that asymmetry was the whole exploit: on a
	// Windows host `?action=index&path=..\depo-gizli` answered 200 with a
	// directory outside the storage root, while `action=download` on the very
	// same path answered 400 "bad path". Names, sizes and timestamps are not
	// nothing — with numbered roots (`storage1` reaching `storage10`) that is
	// another tenant's file list.
	//
	// ⚠ This is defence in depth, NOT the fix. The fix is in the driver, which
	// now cleans host separators before it cleans the path and tests the root
	// boundary as a path rather than as a string prefix. This guard is here
	// because it protects EVERY driver — including a third-party plugin
	// backend with its own idea of resolve — and because a listing endpoint
	// that is the only unguarded one is how this went unnoticed.
	if pathHasDotDot(rel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path"})
		return
	}
	// ⚠⚠ filex's own trash, version history and thumbnail trees are never
	// served by path — not listed, not searched, not previewed, not
	// downloaded. Each has its own API keyed by something other than a path
	// (the trash by original path + node id, versions by node id, thumbnails
	// by node id), so no legitimate caller asks for these here.
	//
	// Measured 2026-09-21 before this guard: a `file.trashed` notification
	// targeted `.filex-trash/<key>`, and clicking it answered this index with
	// 200 and a breadcrumb reading `docs › .filex-trash`. The answer is the
	// same 404 a folder that does not exist gets, so the refusal says nothing
	// about what is inside.
	//
	// `.filex-open` is deliberately NOT refused here (syspath.OpenWith): the
	// desktop app lists, uploads to and downloads from it by exact path, and a
	// refusal would read to it as "unchanged" and drop the edit. It is kept out
	// of every listing, search and view instead.
	if syspath.Sealed(rel) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if adapter == "" {
		if len(storages) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"adapter":      "",
				"storages":     storageNames,
				"storage_info": infos,
				"dirname":      "",
				"read_only":    false,
				"files":        []any{},
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
	case "changes":
		h.vfChanges(w, r, current, rel)
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
// (no `<adapter>://` prefix, just `dir/file.ext`).
//
// The byte source comes from filebody: the storage driver normally, and
// filex's staging area while a staged upload's background transfer is
// still running — a file must be openable while its bytes are still on
// their way to the backend. The catalogue is consulted only to answer
// that question; a path with no node still resolves to the driver, so
// freshly-written, not-yet-synced files keep appearing here as before.
//
// Drivers that implement storage.RangeReader are served through
// http.ServeContent, so this endpoint speaks Range: seeking in
// video/audio, resume after a dropped connection, and a retry that costs
// the remaining bytes instead of the whole object. Drivers that cannot
// range keep the previous whole-object io.Copy.
//
// ⚠ This is the app's own authenticated download; it has no download
// counter. Public share links (/s/, /s/{token}/f/, /d/) do NOT come
// through here — they serve their own bodies and reserve a slot off the
// link's cap before any byte leaves, which is why they stay on the
// unranged path (see share.go claimDownload).
func (h *Manager) vfStream(w http.ResponseWriter, r *http.Request, s *model.Storage, rel string, asAttachment bool) {
	h.streamBody(w, r, s, rel, bodyMode{attachment: asAttachment})
}

// bodyMode is how one file body is handed over.
type bodyMode struct {
	// attachment: a download (Content-Disposition: attachment) rather than an
	// inline preview.
	attachment bool
	// linked: the request is a credential-free one-file link (#71,
	// download_link.go), redeemed by the browser's own download stack. Two
	// answers change, and both for the same reason — whatever body arrives is
	// what lands on the person's desktop:
	//   - it is never told "not yet" (the 202 / wait page of a slow-and-big
	//     file). A browser drop saves that page as the file. A link waits for
	//     the bytes instead, exactly as a non-browser caller does;
	//   - the body is `no-store`: a single-use link's response is not
	//     something any cache should keep.
	linked bool
}

// ServeLinkedFile streams one file for a credential-free download link (the
// one-file tickets of archive_download.go / download_link.go).
//
// ⚠ The link's OWNER is already on the request context. The ≥viewer check in
// streamBody is therefore the owner's reach NOW — at the drop — and not the
// reach they had when the link was minted; a grant revoked in between refuses
// the download. That is the property that lets the redeem carry no credential.
func (h *Manager) ServeLinkedFile(w http.ResponseWriter, r *http.Request, s *model.Storage, rel string) {
	if syspath.Sealed(rel) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	h.streamBody(w, r, s, rel, bodyMode{attachment: true, linked: true})
}

func (h *Manager) streamBody(w http.ResponseWriter, r *http.Request, s *model.Storage, rel string, mode bodyMode) {
	asAttachment := mode.attachment
	if h.StorageResolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage offline"})
		return
	}
	rel = strings.TrimSpace(strings.TrimPrefix(rel, "/"))
	if rel == "" || pathHasDotDot(rel) {
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
	src, err := h.Body.Resolve(r.Context(), drv, s.ID, rel, nil)
	if err != nil {
		writeStagingGone(w, err)
		return
	}
	stat, err := src.Stat(r.Context())
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

	// Slow-and-big: prepare a local copy, and say so while it happens
	// (internal/filecache). Once it is ready every read below comes off local
	// disk — including the ranged ones — because filebody serves the prepared
	// copy transparently, with no code here to notice it.
	//
	// Two deliberate narrowings, both of them "never make it worse":
	//
	//   - Only a DOWNLOAD starts a preparation. A preview is somebody scrubbing
	//     a video or opening a PDF page; making them wait for a whole 4 GB
	//     prefetch before the first frame is worse than the ranged read they
	//     would otherwise have got. A preview still USES a copy that exists.
	//   - Only a request with NO Range header can be answered "not yet". A
	//     Range is a resume or a seek from a client already committed to a
	//     body, and 202 is not an answer it can use.
	//   - Only a caller that can USE "not yet" gets it: a browser navigation
	//     (the wait page) or a client that sends X-Filex-Accept-Prepare
	//     (acceptsPrepare). Everyone else asked for a file and gets the file,
	//     and no preparation is started behind its back. Until v0.42 every
	//     non-browser caller got the 202 JSON, and filex's own sync client took
	//     the 2xx for the file: it wrote the JSON to disk and uploaded it over
	//     the real one. Old clients stay in the field for months, so the fix
	//     has to live here, where it protects all of them at once.
	if r.URL.Query().Get("cache") == "status" && !mode.linked {
		writeCacheStatus(w, src.Status(r.Context(), stat))
		return
	}
	if asAttachment && !mode.linked && r.Header.Get("Range") == "" && (wantsHTML(r) || acceptsPrepare(r)) {
		if prep := src.Prepare(r.Context(), stat); prep != nil && !prep.Ready {
			writeCachePreparing(w, r, path.Base(rel), prep)
			return
		}
	}

	// MIME — extension lookup wins so svg/md/csv/html render the right
	// way in the browser (DB cached + driver-reported mime is often
	// `text/plain; charset=utf-8` after sync because Go's http.Detect
	// doesn't know markdown/csv/svg, breaking inline preview). Driver
	// value is the fallback for extensions we don't recognize.
	//
	// Setting it here also stops http.ServeContent from sniffing (it only
	// sniffs when Content-Type is unset), which would cost a read of the
	// first 512 bytes plus a rewind.
	mime := mimeByExt(rel)
	if mime == "" {
		mime = stat.Mime
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	if asAttachment {
		base := path.Base(rel)
		w.Header().Set("Content-Disposition", httpx.ContentDisposition("attachment", base))
	} else {
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}
	if mode.linked {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=60")
	}

	// Ranged path — the driver can start a transfer at an offset, so
	// http.ServeContent gets a real seeker and answers 206 /
	// Content-Range / If-Range / 416 on its own. That is what makes video
	// and audio seekable and a dropped download resumable instead of
	// restarting from byte 0.
	//
	// ⚠ Requires a known size: http.ServeContent trusts the seeker's end
	// position, so a driver whose Stat reports 0 for a non-empty object
	// would serve an empty body. Size 0 keeps the whole-object path, which
	// is byte-identical for a genuinely empty file.
	if src.CanRange() && stat.Size > 0 {
		seek := newRangeSeeker(r.Context(), src, stat.Size)
		defer seek.Close()
		// Without a Range header the request is a plain download: open
		// the one stream up front, exactly where the old code did, so a
		// backend error is still a clean 500 rather than a truncated 200.
		if r.Header.Get("Range") == "" {
			rc, err := src.Open(r.Context())
			if err != nil {
				writeStagingGone(w, err)
				return
			}
			seek.adopt(rc)
		}
		http.ServeContent(w, r, path.Base(rel), stat.Mtime, seek)
		return
	}

	// Fallback — the source cannot range: the whole-object stream this
	// endpoint has always served. Say so rather than let a client assume
	// resume works.
	rc, err := src.Open(r.Context())
	if err != nil {
		writeStagingGone(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Accept-Ranges", "none")
	if stat.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size, 10))
	}
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
//
// …and when the cache knows the folder but cannot VOUCH for it — a storage
// whose first scan has not finished, a lazily catalogued folder that is not
// watched — the folder is listed from the driver with the catalogue laid over
// it (vfIndexMerged), so nothing reads as missing and nothing only the
// catalogue knows (ids, owners, thumbnails) is lost.
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

	// Asked before the cache is read: on a lazy storage this is also the
	// "somebody opened this folder" that gets it catalogued (never blocks).
	vouched := h.catalogueVouches(s, rel)

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

	// Pre-sync escape hatch, widened: a storage whose first scan has not
	// finished — or a lazy folder the catalogue does not vouch for — is
	// listed from the driver with the catalogue overlaid. It used to apply
	// only while the folder had NO catalogued children, so the root of a big
	// storage showed the handful of entries the first scan had reached, for
	// as long as that scan ran. Afterwards the cache is authoritative (a
	// truly empty folder returns [] without an extra driver call).
	if !vouched {
		if h.vfIndexMerged(w, r, s, rel, dirname, storageNames, dirsOnly, set, nodes) {
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
	files := projectFileNodes(s.Name, nodes, dirsOnly, set, h.ThumbSigner, h.hydrateOwnerNames(r.Context(), nodes))
	h.respondIndex(w, r, s, rel, dirname, storageNames, dirsOnly, set, files, nil)
}

// respondIndex writes the listing response every index path shares — from
// the catalogue, from the driver, or merged. objs is the driver listing when
// there was one (nil from the catalogue): its encrypted-folder marker flags a
// folder whose marker row does not exist yet.
func (h *Manager) respondIndex(w http.ResponseWriter, r *http.Request, s *model.Storage, rel, dirname string,
	storageNames []string, dirsOnly bool, set *acl.Set, files []map[string]any, objs []storage.Object) {
	h.annotateSizes(r.Context(), s, files, h.coverageOf(r.Context(), s))
	if dirsOnly {
		writeJSON(w, http.StatusOK, map[string]any{"folders": files})
		return
	}
	annotateAppBadges(r.Context(), h.Store, s.ID, files)
	resp := map[string]any{
		"adapter":      s.Name,
		"storages":     storageNames,
		"storage_info": storageInfoFrom(r.Context()),
		"dirname":      joinAdapterPath(s.Name, dirname),
		"read_only":    s.ReadOnly,
		"perm":         permString(set, rel),
		"files":        files,
	}
	/* wiring:e2 — E2E-encrypted folder awareness: badge encrypted dir rows
	   (e2e:true) and, when the listed dir sits inside an encrypted subtree,
	   tell the client where the marker lives (e2e_root) so it can show the
	   lock screen + fetch the marker. Server stays crypto-blind. */
	h.annotateE2e(r.Context(), s, rel, files, resp)
	/* cold-cache: a freshly-created encrypted folder (marker uploaded seconds
	   ago, sync not yet run) must still present its lock screen. The marker
	   object is right there in the driver listing, so flag directly. */
	for _, o := range objs {
		if o.Name == e2e.MarkerName {
			resp["e2e"] = true
			resp["e2e_root"] = joinAdapterPath(s.Name, strings.Trim(rel, "/"))
			break
		}
	}
	/* /wiring:e2 */
	writeJSON(w, http.StatusOK, resp)
}

/* wiring:e2 — listing-level encrypted-folder annotations (see vfIndex). */
func (h *Manager) annotateE2e(ctx context.Context, s *model.Storage, rel string, files []map[string]any, resp map[string]any) {
	// Dir rows: one indexed marker lookup per subdirectory. Realistic dir
	// counts keep this cheap (same N+1 budget as the thumbnail hydration
	// above); the lookup is a PK-style path_hash hit.
	for _, entry := range files {
		if entry["type"] != "dir" {
			continue
		}
		p, _ := entry["path"].(string)
		_, childRel := splitAdapterPath(p)
		if childRel == "" {
			continue
		}
		if root, ok := e2e.FindRoot(ctx, h.Store, s.ID, childRel); ok && root == strings.Trim(childRel, "/") {
			entry["e2e"] = true
		}
	}
	// Current dir: inside (or at the root of) an encrypted subtree?
	if root, ok := e2e.FindRoot(ctx, h.Store, s.ID, rel); ok {
		resp["e2e"] = root == strings.Trim(rel, "/")
		resp["e2e_root"] = joinAdapterPath(s.Name, root)
	}
}

/* /wiring:e2 */

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
	// …except that blob stores also "list" a NONEXISTENT prefix as an
	// empty success, which used to render phantom folders as browsable
	// empty dirs. Zero objects alone can't prove the dir exists, so
	// confirm with Stat: a real empty dir (local/SFTP) stats as a
	// directory; a phantom prefix doesn't. Legit empty dirs created via
	// filex carry a .keepdir marker (len(objs) > 0), and the storage
	// root ("") is always browsable.
	if len(objs) == 0 && clean != "" {
		st, serr := drv.Stat(r.Context(), clean)
		if serr != nil || st.Kind != storage.KindDirectory {
			return false
		}
	}
	files := projectDriverObjects(s.Name, clean, objs, dirsOnly, set)
	h.respondIndex(w, r, s, clean, clean, storageNames, dirsOnly, set, files, objs)
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
		// filex's own entries — the same syspath.IsName rule the cache
		// projector applies, so a cold listing and a warm one agree. Judged
		// by the entry's NAME: what is listed here are the children of the
		// folder that was asked for (see projectFileNodes for why not the path).
		if syspath.IsName(o.Name) {
			continue
		}
		/* wiring:e2 — hide the encrypted-folder marker (same contract as
		   the DB projector; detection flags come from the response). */
		if o.Name == e2e.MarkerName {
			continue
		}
		/* /wiring:e2 */
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
		// ⚠ A driver reports KindSymlink only for a link it will NOT follow —
		// out of the storage root with follow_symlinks off, broken, or remote
		// and unresolvable. `type` stays inside the closed 'file' | 'dir'
		// union the explorer's FileNode declares, so an older client renders
		// exactly the row it rendered before; these two keys are additive and
		// let a client that knows about them say WHY the row will not open.
		// Without them the user gets issue #34's original complaint back: a
		// 0-byte file, no explanation.
		if o.Kind == storage.KindSymlink {
			entry["symlink"] = true
			if st := o.Metadata[storage.MetaLinkState]; st != "" {
				entry["link_state"] = st
			}
		}
		out = append(out, entry)
	}
	return out
}

// The toolbar search's page sizes. The index is asked for managerSearchPage
// hits; without it, the SQL fallback reads a window of FallbackOverFetch times
// that from the storage — or managerCrossStoragePage times that from EACH
// storage when the search spans them.
const (
	managerSearchPage       = 250
	managerCrossStoragePage = 100
)

// vfSearch runs a search inside the storage and projects matches onto
// the FileNode shape. The dirname stays at the requested folder so the
// breadcrumb keeps its place.
//
// Strategy: try the Bleve full-text index first (handles content + name
// matching, fuzzy, prefix). Fall back to SQL LIKE on `nodes.name` when
// the index is missing, returns nothing, or errors.
//
// The response carries `truncated`: true when more rows matched than came
// back — the index filled its page, or the fallback filled its window — so a
// client can say "narrow your search" instead of letting a cut list read as
// the whole answer.
func (h *Manager) vfSearch(w http.ResponseWriter, r *http.Request, s *model.Storage, rel, filter string, storageNames []string) {
	if filter == "" {
		h.vfIndex(w, r, s, rel, storageNames, false)
		return
	}
	// Somebody is using the storage: a lazy catalogue's background filler
	// slows down while they do.
	if h.Lazy != nil {
		h.Lazy.NoteActivity(s.ID)
	}

	// Cross-storage mode — when the SPA is showing the multi-storage
	// virtual root (rel == "") and more than one storage is enabled,
	// search every storage. Otherwise scope to the current storage.
	crossStorage := rel == "" && len(storageNames) > 1
	var nodes []*model.Node

	// The toolbar is the box people actually type into, so it speaks the
	// same query language as /api/files/search: `tag:` is parsed out as a
	// filter and never reaches the text query. Without this, typing
	// `tag:invoice` here would search for a FILE named "tag:invoice" and
	// come back empty — the same feature behaving differently depending
	// on which box you used.
	parsed := search.ParseQuery(filter)
	tagFilter, tagged, terr := resolveTagFilter(r.Context(), h.Store, parsed)
	if terr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": terr.Error()})
		return
	}

	// Multi-tenant: in cross-storage mode `keep` used to accept every hit
	// regardless of which storage it came from, and this file consulted no
	// tenant scope at all — so the toolbar returned another customer's file
	// names and paths. Worse, `projectFileNodes` stamps every hit with the
	// CURRENT adapter's name, so the leaked rows arrived labelled as if they
	// were the caller's own: nothing in the response even said where they came
	// from.
	//
	// The filter goes here rather than at either call site because `keep` is
	// the single choke point both entrances pass through — the index branch and
	// the bare `tag:` branch (tags live on node_meta and are shared across
	// users, so a shared label is its own way across). Same predicate the
	// sibling handler already applies to /api/files/search
	// (handlers/search.go): scope absent ⇒ unscoped ⇒ unchanged.
	//
	// ⚠ The SQL-LIKE fallback further down runs its rows through this SAME keep
	// (see accept()): its cross-storage walk enumerates ListEnabledStorages
	// (tenant-confined) but that says nothing about the token's `root:` subtree,
	// so it must pass keep or a confined token gets the leak back on an index
	// miss. One choke point, both branches.
	scope, confined := confinedScope(r.Context())
	keep := func(n *model.Node) bool {
		if n == nil || n.DeletedAt != nil {
			return false
		}
		// A hit inside filex's own directories is not a result. The index
		// holds every catalogued node, the desktop's open-with working copies
		// and version snapshots included, so without this the toolbar found
		// `.filex-open/<session>-Plan.docx` for "Plan" (measured 2026-09-21).
		// Judged by the WHOLE path: a search spans the storage, not one folder.
		if syspath.Hidden(n.Path) {
			return false
		}
		if confined && !scope.CanAccessStorage(n.StorageID) {
			return false
		}
		// Root confinement: the toolbar walks a whole storage's index, so a
		// `root:`-confined token saw name hits from OUTSIDE its folder — the
		// same leak as /api/files/search, closed with the same primitive. The
		// ?path= that picks the adapter was rewritten within-root by
		// confine.Middleware, but that only scopes the storage, not the subtree.
		if !rootAllows(r.Context(), h.Store, n.StorageID, n.Path) {
			return false
		}
		return crossStorage || n.StorageID == s.ID
	}

	truncated := false
	switch {
	case parsed.HasTagFilter() && parsed.Text == "":
		// A bare `tag:x` lists the tagged nodes; there is no text to score.
		for _, n := range tagged {
			if keep(n) {
				nodes = append(nodes, n)
			}
		}
	case h.Index != nil:
		hits := h.Index.SafeSearchFiltered(r.Context(), parsed.Text, managerSearchPage, search.ScopeName, tagFilter)
		// The index returns at most a page; a full page is a cut answer.
		truncated = len(hits) >= managerSearchPage
		for _, hit := range hits {
			n, err := h.Store.GetNode(r.Context(), hit.NodeID)
			if err != nil || !keep(n) {
				continue
			}
			nodes = append(nodes, n)
		}
	}

	// 2) Fall back to SQL LIKE when the index didn't return anything.
	if len(nodes) == 0 && parsed.Text != "" {
		plan := search.PlanFallback(parsed.Text)
		// What is said about the answer is said about the fallback's rows now.
		truncated = false
		// window reads one storage's share: every word of the query a
		// condition, ranked in SQL before the LIMIT (the longest word — exact
		// and prefix names first), and one row past it, because a row beyond
		// the window is the only proof it was full.
		window := func(storageID int64, size int) ([]*model.Node, error) {
			rows, err := plan.Candidates(r.Context(), h.Store, storageID, size+1)
			if err != nil {
				return nil, err
			}
			if len(rows) > size {
				truncated = true
				rows = rows[:size]
			}
			return rows, nil
		}
		accept := func(rows []*model.Node) {
			for _, n := range rows {
				// ⚠ Same choke point as keep() above — the index branch runs
				// hits through keep(), and this SQL-LIKE fallback must apply the
				// same tenant + root confinement or a confined token gets the
				// leak back the moment the index misses. rootAllows/keep are
				// inert for unconfined callers.
				if !keep(n) {
					continue
				}
				if plan.Accepts(n.Name, n.Path) && tagFilterAccepts(tagFilter, n.ID) {
					nodes = append(nodes, n)
				}
			}
		}
		if crossStorage {
			// Walk every enabled storage with the LIKE fallback.
			storages, err := h.Store.ListEnabledStorages(r.Context())
			if err == nil {
				for _, st := range storages {
					rows, err := window(st.ID, managerCrossStoragePage*search.FallbackOverFetch)
					if err != nil {
						continue
					}
					accept(rows)
				}
			}
		} else {
			fallback, err := window(s.ID, managerSearchPage*search.FallbackOverFetch)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			accept(fallback)
		}
		// The toolbar is the box people actually type into, so its
		// index-less answer is ranked by the same tiers as everybody
		// else's. SQL ranked only by the longest word, to decide which rows
		// make the window; this orders them by the whole query.
		sort.SliceStable(nodes, func(a, b int) bool {
			ra := plan.Rank(nodes[a].Name, nodes[a].Path)
			rb := plan.Rank(nodes[b].Name, nodes[b].Path)
			if ra != rb {
				return ra < rb
			}
			if len(nodes[a].Path) != len(nodes[b].Path) {
				return len(nodes[a].Path) < len(nodes[b].Path)
			}
			return nodes[a].Name < nodes[b].Name
		})
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

	files := projectFileNodes(s.Name, nodes, false, nil, h.ThumbSigner, h.hydrateOwnerNames(r.Context(), nodes))
	annotateAppBadges(r.Context(), h.Store, s.ID, files)
	writeJSON(w, http.StatusOK, map[string]any{
		"adapter":      s.Name,
		"storages":     storageNames,
		"storage_info": storageInfoFrom(r.Context()),
		"dirname":      joinAdapterPath(s.Name, rel),
		"read_only":    s.ReadOnly,
		"files":        files,
		"truncated":    truncated,
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
	// Multi-tenant: GetNode takes a raw id and tenantstore does not confine it,
	// so without this a member of one customer could read the name, full path,
	// size, mime and owner of any file on the instance by walking the id range.
	// The refusal reuses the miss above BYTE FOR BYTE — same status, same body
	// — so a foreign id is indistinguishable from one that never existed.
	if scope, confined := confinedScope(r.Context()); confined && !scope.CanAccessStorage(node.StorageID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	// Root confinement: the id is not a path, so confine.Middleware never saw
	// it (see confine_guard.go). Same 404 as the miss above, so a foreign id is
	// indistinguishable from one that never existed.
	if !rootAllows(r.Context(), h.Store, node.StorageID, node.Path) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	// A row inside filex's own trash / version / thumbnail trees is not
	// described here either — the same 404 as a miss (see Read).
	if syspath.Sealed(node.Path) {
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
		known     *model.Node // the row when the caller addressed by id
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
		known = node
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

	// Multi-tenant: this is the FILE BYTES, and both ways in name their storage
	// from the request — `?id=` through an unconfined GetNode, `?storage=` as a
	// bare integer handed straight to StorageResolver. The only gate below is
	// the ACL, and the ACL is tenant-blind: on an rbac_enabled=false storage
	// (the migration default) acl.Effective answers roleBase(role), i.e. Editor
	// for a plain `user`. So it stopped nobody, and one authenticated GET
	// returned another customer's file byte for byte — with no audit row, since
	// the audit middleware filters GETs.
	//
	// Asked BEFORE the ACL so the refusal is uniform whether or not grants
	// happen to exist, and shaped like the by-id miss above so a foreign id
	// cannot be told apart from one that never existed.
	if scope, confined := confinedScope(r.Context()); confined && !scope.CanAccessStorage(storageID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	// Root confinement. The `?path=` shape was already rewritten within-root by
	// confine.Middleware; the `?id=` shape it could not reach, so a confined
	// token read another folder's BYTES by id. Same 404 as the miss above.
	if !rootAllows(r.Context(), h.Store, storageID, aclRel) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	// filex's own trash / version / thumbnail trees are never served by path
	// or by the id of a row that lives in them (a trashed row's path IS its
	// trash key). Same rule, same 404, as the manager's listVuefinder guard.
	if syspath.Sealed(aclRel) || syspath.Sealed(filePath) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
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

	src, err := h.Body.Resolve(r.Context(), drv, storageID, filePath, known)
	if err != nil {
		writeStagingGone(w, err)
		return
	}

	// Fall back to Stat for mime/size when caller passed storage+path. While
	// the file is staged that answer comes from the committed values, not from
	// a driver that has nothing to describe yet.
	if nodeMime == "" || nodeSize == 0 {
		if obj, err := src.Stat(r.Context()); err == nil {
			if nodeMime == "" {
				nodeMime = obj.Mime
			}
			if nodeSize == 0 {
				nodeSize = obj.Size
			}
		}
	}

	rc, err := src.Open(r.Context())
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeStagingGone(w, err)
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
	w.Header().Set("Content-Disposition", httpx.ContentDisposition(disposition, nodeName))
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
// hydrateOwnerNames resolves the display name of every owner and last-actor in
// one query for the whole page, and returns the id of the person looking.
//
// ⚠ This is the reason the Owner column is affordable. The ids ride along in
// the node row that was already read (they are columns on `nodes`), so the only
// extra work is ONE `... WHERE id IN (…)` over `users` per listing — not one
// per row. A folder of 5 000 files owned by three people costs one query with
// three ids in it; asking per row would have cost 5 000.
//
// A failure is not fatal: the ids are still true, and the client's fallback for
// a name it does not have is the same "System"/id it uses for an ownerless row.
func (h *Manager) hydrateOwnerNames(ctx context.Context, nodes []*model.Node) int64 {
	var viewer int64
	if u := auth.UserFrom(ctx); u != nil {
		viewer = u.ID
	}
	seen := map[int64]bool{}
	ids := make([]int64, 0, 8)
	for _, n := range nodes {
		for _, id := range []*int64{n.OwnerID, n.LastActorID} {
			if id == nil || *id <= 0 || seen[*id] {
				continue
			}
			seen[*id] = true
			ids = append(ids, *id)
		}
	}
	if len(ids) == 0 {
		return viewer
	}
	names, err := h.Store.GetUserDisplayNames(ctx, ids)
	if err != nil {
		return viewer
	}
	for _, n := range nodes {
		if n.OwnerID != nil {
			n.OwnerName = names[*n.OwnerID]
		}
		if n.LastActorID != nil {
			n.LastActorName = names[*n.LastActorID]
		}
	}
	return viewer
}

func projectFileNodes(adapter string, nodes []*model.Node, dirsOnly bool, set *acl.Set, signer *thumb.Signer, viewer int64) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		if n.DeletedAt != nil {
			continue
		}
		// filex's own entries (syspath.IsName: trash, version history,
		// thumbnails, the desktop's open-with working area, keep markers)
		// never appear as a row. They have dedicated surfaces or are
		// implementation detail.
		//
		// ⚠⚠ Judged by NAME, not by path. Every caller hands this either the
		// children of the folder that was asked for, or hits that vfSearch's
		// keep() has already judged by their whole path. Judging children by
		// their full path would also hide the contents of `.filex-open` from
		// the one client that is supposed to read them: the desktop app lists
		// that folder to see whether the editor saved its copy, and reads an
		// empty listing as "nothing changed" — the edit would never reach the
		// original, and nobody would be told.
		//
		// ⚠ The rule used to be hand-written here and in projectDriverObjects,
		// the two copies disagreeing (this one did not hide the keep marker;
		// that one used `strings.Contains(o.Path, ".thumbs")`, which also hid a
		// person's own `my.thumbs.txt`), and neither knew `.filex-open`, so it
		// was listed to everyone (2026-09-21, the owner's report).
		if syspath.IsName(n.Name) {
			continue
		}
		/* wiring:e2 — the encrypted-folder marker is an implementation
		   detail: hidden from every listing/search projection (the client
		   detects encryption via the response-level e2e/e2e_root flags and
		   reads the marker itself through the preview endpoint). */
		if n.Name == e2e.MarkerName {
			continue
		}
		/* /wiring:e2 */
		// RBAC: drop entries the caller isn't allowed to see.
		if set != nil && !set.CanSee(n.Path) {
			continue
		}
		isDir := n.Type == model.NodeTypeDirectory
		if dirsOnly && !isDir {
			continue
		}
		// ⚠ The wire type stays a closed 'file' | 'dir' union — the explorer's
		// FileNode declares it that way and widening it would be a breaking
		// change for every embedder of @brftech/filex. A symlink row is one
		// the driver refused to follow (out of root, broken, or remote and
		// unresolved); it is reported as a file and FLAGGED, so a client can
		// explain it instead of drawing issue #34's unexplained 0-byte file.
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
		if n.Type == model.NodeTypeSymlink {
			entry["symlink"] = true
		}
		if n.Etag != "" {
			entry["etag"] = n.Etag
		}
		// Ownership (migrations 00004 + 00038). Every key here is OMITTED when
		// it has nothing to say, so a client that does not know about owners
		// sees exactly the row it saw before: a system row carries no owner
		// key at all, which is also the wire's way of saying "nobody".
		if n.OwnerID != nil {
			entry["owner_id"] = *n.OwnerID
			if n.OwnerName != "" {
				entry["owner_name"] = n.OwnerName
			}
			// The client says "You" without being told who it is. The core
			// package is embedded in hosts that have no idea which filex
			// account the session belongs to, so answering that here is the
			// difference between a working column and a prop nobody can pass.
			if viewer > 0 && *n.OwnerID == viewer {
				entry["owner_self"] = true
			}
		}
		if n.LastActorID != nil {
			entry["last_actor_id"] = *n.LastActorID
			if n.LastActorName != "" {
				entry["last_actor_name"] = n.LastActorName
			}
			if viewer > 0 && *n.LastActorID == viewer {
				entry["last_actor_self"] = true
			}
		}
		if n.ExternalUpload {
			entry["external_upload"] = true
		}
		if set != nil {
			entry["perm"] = set.Effective(acl.CleanRel(n.Path)).String()
		}
		// Thumbnail URL — populated when the pipeline rendered one.
		// The /api/files/thumb/{id} endpoint streams it. We allow
		// "ready" or any non-pending/non-failed state through to keep
		// the UI optimistic; if the file isn't actually there the
		// thumb endpoint 404s and the SFC falls back to its icon.
		if !isDir && thumbServable(n.Thumb) {
			// Stamped with a short-lived signature (thumbURL): this listing is
			// the only place that knows the caller was allowed to see the node,
			// and an <img> cannot carry that decision in a header.
			entry["thumb_url"] = thumbURL(signer, n.ID)
		}
		// No backend mtime (e.g. an empty folder on a synthetic-dir store —
		// nothing to aggregate one from) — listingMtimeMillis falls back to
		// when filex first saw the node so the row still shows a date. ⚠ The
		// upload precondition (upload_expect.go) compares against this same
		// value; keep it the one definition.
		if ms, ok := listingMtimeMillis(n); ok {
			entry["last_modified"] = ms
		}
		out = append(out, entry)
	}
	return out
}

// storageInfo is what a listing says about each storage the caller can see,
// beside the bare `storages` names older clients read.
//
// ⚠ Why it exists: `read_only` was only ever said for the storage being
// LISTED, so a person who could not read `/api/admin/storages` never learnt
// that a drive was read-only until they were inside it — the admin saw
// "Salt okunur" on the drive and no "New" button there, the non-admin saw a
// "New" menu of greyed entries and no reason (QA, 2026-09-21). The SPA also
// fired `GET /api/admin/storages` on every non-admin page load to try to find
// out, and got a 403 each time. The root listing is RBAC-filtered already, so
// saying it here tells the caller nothing about a drive they cannot open.
type storageInfo struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"read_only"`
	// Coverage is set while the storage's catalogue does not cover all of
	// it — a first scan still running, a lazy catalogue still filling, or
	// one that only catalogues the folders people open. Search, folder sizes
	// and drive usage say so from this (docs/LAZY-CATALOGUE.md).
	Coverage *syncpkg.CatalogueCoverage `json:"coverage,omitempty"`
	// SortOrder is the position the admin gave the storage (issue #57);
	// absent when it has none. `storages` is already in this order
	// (ListEnabledStorages), so a client need not sort by it.
	SortOrder *int64 `json:"sort_order,omitempty"`
}

type storageInfoKey struct{}

// withStorageInfo carries the list into the index/search responders without
// widening their signatures (the mutating verbs call them too, and answer
// without it — `storage_info` is then null and a client keeps what it had).
func withStorageInfo(ctx context.Context, infos []storageInfo) context.Context {
	return context.WithValue(ctx, storageInfoKey{}, infos)
}

func storageInfoFrom(ctx context.Context) []storageInfo {
	v, _ := ctx.Value(storageInfoKey{}).([]storageInfo)
	return v
}
