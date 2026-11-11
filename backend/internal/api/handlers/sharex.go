package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// ShareX is the DEPRECATED POST /api/sharex/upload: a ShareX capture
// authenticated with an API token (`write`), answered with {"url": …}. It is
// kept, unadvertised, so the ShareX configs installed before 0.60 keep
// working, and is removed in the next major release.
//
// ⚠ Its successor is the upload door, POST /api/uploads (uploads.go): an
// uploader's own key that opens nothing but that door, the folder and the
// link set in filex, a deletion link. Nothing new goes here - a fix that
// belongs to both goes into uploadCore and the helpers beside it.
//
// It writes through the same aiOps core the agent API uses, so an upload is
// stored AND indexed (a plain driver write into an un-indexed folder never
// creates the DB row a link needs), then mints a public /s/<token> link:
//
//	POST /api/sharex/upload   (multipart/form-data)
//	  file    → the captured bytes           (ShareX default FileFormName)
//	  folder  → optional target directory    (default: "sharex")
//	→ 200 {"url":"<PublicURL>/s/<token>?inline=1"}
//
// Every write stamps origin "sharex" (writehook.OriginShareX) until the
// endpoint goes, so a webhook consumer reading this path sees no change.
type ShareX struct {
	ops        *aiOps
	defaultDir string
}

// AttachSearchIndex wires the search index into the ops core — a ShareX
// capture is a write like any other and must be searchable immediately.
func (h *ShareX) AttachSearchIndex(idx *search.Index) { h.ops.attachSearchIndex(idx) }

// shareXMaxUpload caps a single ShareX capture. The bytes are streamed rather
// than buffered now, so this is no longer a memory ceiling — it bounds what one
// request may push into the staging filesystem. Matches the AI upload ceiling.
const shareXMaxUpload = 512 << 20 // 512 MiB

// shareXDefaultDir is where captures land when the request omits `folder`. For a
// confined token it is created UNDER the token's root; otherwise at the first
// enabled storage's root (aiOps.resolveStorage owns that resolution).
const shareXDefaultDir = "sharex"

// shareXDeprecatedOnce logs the deprecation once per process, not per upload.
var shareXDeprecatedOnce sync.Once

// NewShareX constructs the ShareX upload handler. publicURL is the base used to
// build the returned /s/<token> link (empty yields a relative link). shareSvc
// must be non-nil for sharing to work — it is the same *share.Service the rest
// of the app uses.
func NewShareX(store db.Store, resolver func(int64) (storage.Driver, error), shareSvc *share.Service, publicURL string) *ShareX {
	ops := newAIOps(store, resolver, shareSvc, publicURL)
	// Same aiOps core, distinct writehook origin: the deprecated endpoint's
	// writes keep stamping "sharex" (see the type's comment).
	ops.origin = writehook.OriginShareX
	return &ShareX{
		ops:        ops,
		defaultDir: shareXDefaultDir,
	}
}

// AttachTenants wires the shared origin resolver (internal/tenanturl).
func (h *ShareX) AttachTenants(rv tenanturl.Resolver) { h.ops.tenants = rv }

// AttachACL wires the RBAC resolver so the write + share both enforce the bound
// user's grants (≥editor on the target), mirroring the AI surface.
func (h *ShareX) AttachACL(r *acl.Resolver) { h.ops.acl = r }

// AttachBody wires the byte-source resolver (staging-aware reads).
func (h *ShareX) AttachBody(b *filebody.Resolver) { h.ops.attachBody(b) }

// AttachThumbs wires the thumbnail pipeline so captures get grid thumbnails
// like manager uploads (nil = thumbnails skipped).
func (h *ShareX) AttachThumbs(p *thumb.Pipeline) { h.ops.thumbs = p }

// AttachStaged routes captures above the chunk threshold through filex's
// staging area — a long screen recording is answered as soon as filex holds it
// instead of after the driver write (nil = synchronous writes).
func (h *ShareX) AttachStaged(s *StagedUpload) { h.ops.staged = s }

// Upload accepts a ShareX multipart capture (`file`), stores + indexes it, mints
// a public inline-viewable share, and returns {"url": …}.
func (h *ShareX) Upload(w http.ResponseWriter, r *http.Request) {
	// RFC 9745: the client (and whoever reads its traffic) is told the door
	// is going, with the page that says where to.
	w.Header().Set("Deprecation", "true")
	w.Header().Set("Link", `<https://docs.filex.sh/UPLOADERS>; rel="deprecation"`)
	shareXDeprecatedOnce.Do(func() {
		slog.Warn("POST /api/sharex/upload is deprecated and is removed in the next major release: make an uploader (account settings, Uploaders) and use POST /api/uploads")
	})
	// Spilled multipart temp files outlive the response unless dropped here —
	// see the note in AI.Upload.
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad multipart: " + err.Error()})
		return
	}
	f, fh, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file field"})
		return
	}
	defer f.Close()
	if fh.Size > shareXMaxUpload {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "capture too large"})
		return
	}

	folder := cleanUploadFolder(r.FormValue("folder"))
	if folder == "" {
		folder = h.defaultDir
	}
	name := uploadFilename(fh.Filename)
	dest := name
	if folder != "" {
		dest = folder + "/" + name
	}

	// The target folder must be indexed before the share is minted: a write into
	// an un-cached directory leaves no DB node for that file, so CreateShare would
	// report "not indexed yet". Mkdir each segment (idempotent on every driver) so
	// the parent chain — and therefore the freshly written file — gets a cache row.
	if folder != "" {
		if err := h.ensureFolder(r.Context(), folder); err != nil {
			writeAIError(w, err)
			return
		}
	}

	// Streamed, not buffered: a screen recording is a capture too, and above the
	// chunk threshold WriteStream stages it so this reply does not wait on the
	// backend write.
	if _, err := h.ops.WriteStream(r.Context(), dest, f, fh.Size); err != nil {
		if writeE2ERefusal(w, r, err) {
			return
		}
		writeAIError(w, err)
		return
	}

	res, err := h.ops.CreateShare(r.Context(), dest, false, 0, 0)
	if err != nil {
		writeAIError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": inlineURL(res.URL)})
}

// ensureFolder mkdirs every segment of folder (cumulatively) so the whole parent
// chain is present on the driver AND mirrored into the DB cache. Mkdir is
// idempotent on the local + S3 drivers, so re-uploading into an existing folder
// is a no-op rather than an error.
func (h *ShareX) ensureFolder(ctx context.Context, folder string) error {
	cur := ""
	for _, seg := range strings.Split(folder, "/") {
		if seg == "" {
			continue
		}
		if cur == "" {
			cur = seg
		} else {
			cur += "/" + seg
		}
		if _, err := h.ops.Mkdir(ctx, cur); err != nil {
			return err
		}
	}
	return nil
}
