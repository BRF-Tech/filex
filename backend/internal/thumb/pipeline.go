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

	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/model"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
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
	PDF    bool // ghostscript or pdftoppm present
	Office bool // libreoffice/soffice present
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
func (p *Pipeline) GenerateThumb(ctx context.Context, node *model.Node) error {
	if node == nil || node.Type != model.NodeTypeFile {
		return ErrSkipped
	}
	drv, ok := p.storages[node.StorageID]
	if !ok {
		return errors.New("thumb: no driver attached for storage")
	}
	t := &model.Thumbnail{NodeID: node.ID, State: "pending"}
	_ = p.store.UpsertThumbnail(ctx, t)

	mime := strings.ToLower(node.Mime)
	var err error
	switch {
	case strings.HasPrefix(mime, "image/"):
		err = p.generateImage(ctx, node, drv)
	case strings.HasPrefix(mime, "video/") && p.caps.Video:
		err = p.generateVideo(ctx, node, drv)
	case mime == "application/pdf" && p.caps.PDF:
		err = p.generatePDF(ctx, node, drv)
	case isOfficeMime(mime) && p.caps.Office:
		err = p.generateOffice(ctx, node, drv)
	default:
		_ = p.store.SetThumbnailState(ctx, node.ID, "skipped", "")
		return ErrSkipped
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
