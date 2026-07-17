package handlers

import (
	"context"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// ShareX is the token-authenticated endpoint that lets ShareX (the Windows
// screenshot / upload tool) push a file, image, or text capture into filex and
// get back a public, browser-viewable link in one round trip.
//
// It is a thin wrapper over the same aiOps core the /api/ai surface uses, so an
// upload is stored AND indexed (a plain driver write into an un-indexed folder
// never creates the DB cache row a share lookup needs), then a public /s/<token>
// share is minted for it. The reply is the exact JSON ShareX's custom-uploader
// URL parser expects:
//
//	POST /api/sharex/upload   (multipart/form-data)
//	  file    → the captured bytes           (ShareX default FileFormName)
//	  folder  → optional target directory    (default: "sharex")
//	→ 200 {"url":"<PublicURL>/s/<token>?inline=1"}
//
// The link carries ?inline=1 so images/text render in the browser instead of
// forcing a download (see Share.HandleDownload). Every capture is stored under a
// random-prefixed filename so a fresh, collision-free share is minted each time
// (re-using a name would silently repoint the previous share at the new bytes).
type ShareX struct {
	ops        *aiOps
	defaultDir string
}

// shareXMaxUpload caps a single ShareX upload's in-memory read. Matches the AI
// upload ceiling; larger captures are rejected rather than exhausting memory.
const shareXMaxUpload = 512 << 20 // 512 MiB

// shareXDefaultDir is where captures land when the request omits `folder`. For a
// confined token it is created UNDER the token's root; otherwise at the first
// enabled storage's root (aiOps.resolveStorage owns that resolution).
const shareXDefaultDir = "sharex"

// NewShareX constructs the ShareX upload handler. publicURL is the base used to
// build the returned /s/<token> link (empty yields a relative link). shareSvc
// must be non-nil for sharing to work — it is the same *share.Service the rest
// of the app uses.
func NewShareX(store db.Store, resolver func(int64) (storage.Driver, error), shareSvc *share.Service, publicURL string) *ShareX {
	ops := newAIOps(store, resolver, shareSvc, publicURL, "")
	// Same aiOps core, distinct writehook origin — ShareX captures stamp
	// their file events (and AV scans) as origin "sharex", not "ai".
	ops.origin = writehook.OriginShareX
	return &ShareX{
		ops:        ops,
		defaultDir: shareXDefaultDir,
	}
}

// AttachACL wires the RBAC resolver so the write + share both enforce the bound
// user's grants (≥editor on the target), mirroring the AI surface.
func (h *ShareX) AttachACL(r *acl.Resolver) { h.ops.acl = r }

// AttachThumbs wires the thumbnail pipeline so captures get grid thumbnails
// like manager uploads (nil = thumbnails skipped).
func (h *ShareX) AttachThumbs(p *thumb.Pipeline) { h.ops.thumbs = p }

// Upload accepts a ShareX multipart capture (`file`), stores + indexes it, mints
// a public inline-viewable share, and returns {"url": …}.
func (h *ShareX) Upload(w http.ResponseWriter, r *http.Request) {
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
	data, err := io.ReadAll(io.LimitReader(f, shareXMaxUpload))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	folder := shareXCleanFolder(r.FormValue("folder"))
	if folder == "" {
		folder = h.defaultDir
	}
	name := shareXFilename(fh.Filename)
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
			writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
			return
		}
	}

	if _, err := h.ops.Write(r.Context(), dest, data); err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}

	res, err := h.ops.CreateShare(r.Context(), dest, false, 0, 0)
	if err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": shareXInlineURL(res.URL)})
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

// shareXInlineURL appends ?inline=1 (respecting an existing query string) so the
// share's download endpoint serves the bytes with Content-Disposition: inline —
// images and text then render in the browser instead of downloading.
func shareXInlineURL(u string) string {
	if u == "" {
		return u
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "inline=1"
}

// shareXFilename derives a safe, unique storage name from the multipart part's
// filename. It strips any directory component (Windows clients may send a full
// path), sanitizes the basename, and prefixes a short random token so concurrent
// or same-named captures never collide (which would repoint an earlier share).
func shareXFilename(raw string) string {
	raw = strings.ReplaceAll(raw, "\\", "/")
	base := strings.TrimSpace(path.Base(raw))
	if base == "" || base == "." || base == "/" {
		base = "upload"
	}
	// sanitizeFilename neutralizes quote/backslash/newline; drop any residual
	// separators so the name stays a single path segment.
	base = strings.ReplaceAll(sanitizeFilename(base), "/", "_")
	return randHex6() + "-" + base
}

// shareXCleanFolder normalizes an optional `folder` form value into a safe bare
// relative path: it drops any adapter:// prefix, strips "."/".." segments
// (traversal defense in depth on top of resolveStorage's own checks), and
// sanitizes each remaining segment. An empty/invalid input yields "".
func shareXCleanFolder(raw string) string {
	raw = strings.ReplaceAll(raw, "\\", "/")
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	out := make([]string, 0)
	for _, seg := range strings.Split(raw, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		out = append(out, sanitizeFilename(seg))
	}
	return strings.Join(out, "/")
}
