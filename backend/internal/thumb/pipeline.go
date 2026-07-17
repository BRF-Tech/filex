// Package thumb generates and serves thumbnail images for nodes.
//
// The pipeline is dispatcher-based: GenerateThumb inspects the source
// node's mime type and routes to the appropriate generator (image / video
// / pdf / office). Each generator writes an output JPEG/PNG to the
// configured cache storage and updates the thumbnails table.
//
// Generators that require external binaries (ffmpeg, gs, libreoffice)
// detect availability up-front via the capability package and gracefully
// skip when not present.
package thumb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// ErrSkipped is returned when no generator applies to the node — caller
// should mark the node's thumb state as "skipped" rather than "failed".
var ErrSkipped = errors.New("thumb: skipped")

// Pipeline coordinates thumbnail generation.
type Pipeline struct {
	store    db.Store
	storages map[int64]storage.Driver
	cacheDir string

	caps Capabilities
}

// Capabilities indicates which thumbnail backends are available at runtime.
type Capabilities struct {
	Image  bool // always true (Go stdlib + bundled imaging)
	Video  bool // ffmpeg present in PATH
	Audio  bool // ffmpeg present (same binary handles audio waveform)
	PDF    bool // ghostscript or pdftoppm present
	Office bool // libreoffice/soffice present
	SVG    bool // rsvg-convert present (vector→raster)
}

// New constructs a Pipeline.
func New(store db.Store, cacheDir string, caps Capabilities) *Pipeline {
	return &Pipeline{
		store:    store,
		storages: map[int64]storage.Driver{},
		cacheDir: cacheDir,
		caps:     caps,
	}
}

// AttachStorage registers a Driver for a storage ID — needed because the
// pipeline reads source bytes from the originating storage.
func (p *Pipeline) AttachStorage(id int64, drv storage.Driver) {
	p.storages[id] = drv
}

// GenerateThumb dispatches based on node MIME and updates the thumbnails
// row. Idempotent — safe to call repeatedly.
//
// Falls back to the file extension when `node.Mime` is empty — this is
// the common case for files discovered by sync's driver.List, where
// most drivers don't populate the mime field. Without the fallback every
// synced file got `state=skipped` and the pipeline never produced a
// thumbnail for the demo fixtures.
func (p *Pipeline) GenerateThumb(ctx context.Context, node *model.Node) error {
	if node == nil || node.Type != model.NodeTypeFile {
		return ErrSkipped
	}
	drv, ok := p.storages[node.StorageID]
	if !ok {
		return errors.New("thumb: no driver attached for storage")
	}
	/* wiring:e2 — files under an E2E-encrypted folder are ciphertext the
	   server cannot (and must not try to) render: skip before any byte is
	   read. Wasted CPU aside, a plaintext file mistakenly written into an
	   encrypted subtree via DAV/CLI would otherwise leak a readable thumb.
	   Upsert (not SetState — that is UPDATE-only) so the skip records even
	   though the pending row was never created. */
	if e2e.UnderEncrypted(ctx, p.store, node.StorageID, node.Path) {
		_ = p.store.UpsertThumbnail(ctx, &model.Thumbnail{
			NodeID: node.ID, State: "skipped", Error: "e2e-encrypted folder",
		})
		return ErrSkipped
	}
	/* /wiring:e2 */
	t := &model.Thumbnail{NodeID: node.ID, State: "pending"}
	_ = p.store.UpsertThumbnail(ctx, t)

	mime := strings.ToLower(node.Mime)
	if mime == "" {
		mime = mimeFromName(node.Name)
	}
	var err error
	switch {
	// SVG must come BEFORE the generic image/* branch — Go's stdlib
	// image.Decode can't parse SVG, so the regular generateImage
	// would fail with "unknown format". Route SVGs to rsvg-convert
	// when it's available, otherwise skip cleanly.
	case mime == "image/svg+xml" && p.caps.SVG:
		err = p.generateSVG(ctx, node, drv)
	case mime == "image/svg+xml":
		_ = p.store.SetThumbnailState(ctx, node.ID, "skipped", "rsvg-convert not in PATH")
		return ErrSkipped
	case strings.HasPrefix(mime, "image/"):
		err = p.generateImage(ctx, node, drv)
	case strings.HasPrefix(mime, "video/") && p.caps.Video:
		err = p.generateVideo(ctx, node, drv)
	case strings.HasPrefix(mime, "audio/") && p.caps.Audio:
		err = p.generateAudio(ctx, node, drv)
	case mime == "application/pdf" && p.caps.PDF:
		err = p.generatePDF(ctx, node, drv)
	case isOfficeMime(mime) && p.caps.Office:
		err = p.generateOffice(ctx, node, drv)
	default:
		// Everything else (3D models, archives, code, markdown, raw
		// docs, etc) gets a deterministic placeholder card so grid
		// views still show *something* legible. Cheap to render —
		// pure Go image stdlib, no external binary.
		err = p.generateGeneric(ctx, node)
	}
	if err != nil {
		_ = p.store.SetThumbnailState(ctx, node.ID, "failed", err.Error())
		slog.Warn("thumb generate failed",
			slog.Int64("node", node.ID),
			slog.String("mime", mime),
			slog.String("err", err.Error()))
		return err
	}
	now := time.Now()
	_ = p.store.UpsertThumbnail(ctx, &model.Thumbnail{
		NodeID:      node.ID,
		State:       "ready",
		StorageKey:  fmt.Sprintf("%s/%d.jpg", p.cacheDir, node.ID),
		GeneratedAt: &now,
	})
	return nil
}

// CachePath returns the disk path where a thumb is stored for node ID.
func (p *Pipeline) CachePath(nodeID int64) string {
	return fmt.Sprintf("%s/%d.jpg", p.cacheDir, nodeID)
}

// mimeFromName picks a thumbnail-pipeline-relevant MIME class from the
// file extension. This is INTENTIONALLY narrow — the pipeline only
// branches on image/* / video/* / application/pdf / office mime, so
// other extensions can stay empty and skip cleanly.
func mimeFromName(name string) string {
	dot := strings.LastIndex(name, ".")
	if dot < 0 {
		return ""
	}
	ext := strings.ToLower(name[dot+1:])
	switch ext {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "bmp":
		return "image/bmp"
	case "svg":
		return "image/svg+xml"
	case "heic":
		return "image/heic"
	case "avif":
		return "image/avif"
	case "tiff", "tif":
		return "image/tiff"
	case "mp3":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "ogg":
		return "audio/ogg"
	case "flac":
		return "audio/flac"
	case "m4a":
		return "audio/mp4"
	case "aac":
		return "audio/aac"
	case "opus":
		return "audio/opus"
	case "mp4":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "mov":
		return "video/quicktime"
	case "mkv":
		return "video/x-matroska"
	case "avi":
		return "video/x-msvideo"
	case "ogv":
		return "video/ogg"
	case "m4v":
		return "video/mp4"
	case "pdf":
		return "application/pdf"
	case "doc":
		return "application/msword"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "xls":
		return "application/vnd.ms-excel"
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "ppt":
		return "application/vnd.ms-powerpoint"
	case "pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case "odt":
		return "application/vnd.oasis.opendocument.text"
	case "ods":
		return "application/vnd.oasis.opendocument.spreadsheet"
	case "odp":
		return "application/vnd.oasis.opendocument.presentation"
	case "rtf":
		return "application/rtf"
	}
	return ""
}

func isOfficeMime(m string) bool {
	switch m {
	case "application/msword",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.ms-excel",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.ms-powerpoint",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"application/vnd.oasis.opendocument.text",
		"application/vnd.oasis.opendocument.spreadsheet",
		"application/vnd.oasis.opendocument.presentation":
		return true
	}
	return false
}
