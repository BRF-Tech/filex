// Package thumb generates and serves thumbnail images for nodes.
//
// The pipeline is dispatcher-based: GenerateThumb asks the file's chain of
// handlers (filex's own drawer, the OnlyOffice document server for office
// documents, apps) in order, and the first that draws wins. filex's own
// drawer routes by the file's type (image / video / audio / pdf / text /
// archive). Each writes a JPEG to the cache and the thumbnails row records
// what happened.
//
// Generators that require external binaries (ffmpeg, gs or pdftoppm,
// ImageMagick) detect availability up-front via the capability package and
// skip with the reason when they are not present (notool.go); office
// documents need OnlyOffice configured (office.go).
package thumb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb/svgwasm"
)

// ErrSkipped is returned when no generator applies to the node — caller
// should mark the node's thumb state as "skipped" rather than "failed".
var ErrSkipped = errors.New("thumb: skipped")

// Pipeline coordinates thumbnail generation.
type Pipeline struct {
	store db.Store
	// storagesMu guards storages. ⚠ AttachStorage is called from the server's
	// storage resolver, which runs on every queue worker goroutine (content
	// index, thumbnails) as well as on requests, while GenerateThumb reads the
	// map on others. Unguarded, two first touches of storages at once killed
	// the whole process — "fatal error: concurrent map writes" at
	// pipeline.go:85, measured in a full e2e run on 2026-09-21 (every later
	// spec then failed with ECONNREFUSED).
	storagesMu sync.RWMutex
	storages   map[int64]storage.Driver
	cacheDir   string
	// body resolves where a node's bytes are: the storage driver, or filex's
	// staging area while a staged upload is still transferring. Nil-safe.
	body *filebody.Resolver

	caps Capabilities

	// settings is where the SVG limits are read from (AttachSettings).
	settings *svgSettings
	// svgRender draws an SVG: svgwasm.Render. A field so a test can count
	// how often the engine was reached.
	svgRender func(ctx context.Context, svg []byte, maxW, maxH int) (image.Image, error)

	// apps: the handlers apps add, and how to ask one (apps.go). Nil: filex's
	// own drawer alone.
	apps AppThumbs

	// office draws an office document through OnlyOffice (office.go). Nil:
	// no office thumbnails. officeSlot bounds how many at once.
	office     OfficeDrawer
	officeSlot officeSlots
}

// AttachBody wires the byte-source resolver, so a file that is still being
// transferred gets its thumbnail from the staged bytes instead of failing
// against a driver that does not have the object yet.
func (p *Pipeline) AttachBody(b *filebody.Resolver) { p.body = b }

// openSource is the ONE door every generator reads its source bytes through.
// Generators must not call drv.Read directly — that is what made them blind to
// staged uploads, and a per-generator exception is how one file starts
// behaving differently depending on which thumbnailer picked it up.
//
// wiring:e2 fxe — and it is where ciphertext is caught: a file that starts
// with either encrypted magic (a file of an encrypted folder that escaped
// the marker walk, or a single encrypted file under any name) answers
// errEncryptedContent, which GenerateThumb records as `skipped`. One sniff
// here covers every generator, present and future.
func (p *Pipeline) openSource(ctx context.Context, drv storage.Driver, node *model.Node) (io.ReadCloser, error) {
	src, err := p.body.Resolve(ctx, drv, node.StorageID, node.Path, node)
	if err != nil {
		return nil, err
	}
	rc, err := src.Open(ctx)
	if err != nil {
		return nil, err
	}
	head := make([]byte, len(e2e.MagicPrefix))
	n, _ := io.ReadFull(rc, head)
	if n == len(head) && e2e.HasEncryptedPrefix(head) {
		_ = rc.Close()
		return nil, errEncryptedContent
	}
	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head[:n]), rc), rc}, nil
}

// errEncryptedContent: the source is end-to-end encrypted (see openSource).
var errEncryptedContent = errors.New("thumb: end-to-end encrypted content")

// encryptedContent reports whether node's bytes start with either encrypted
// magic (openSource's sniff), for a path that would otherwise not read them.
// A source that cannot be opened is not called encrypted.
func (p *Pipeline) encryptedContent(ctx context.Context, drv storage.Driver, node *model.Node) bool {
	rc, err := p.openSource(ctx, drv, node)
	if errors.Is(err, errEncryptedContent) {
		return true
	}
	if err == nil {
		_ = rc.Close()
	}
	return false
}

// Capabilities indicates which thumbnail backends are available at runtime.
type Capabilities struct {
	Image bool // always true (Go stdlib + bundled imaging)
	Video bool // ffmpeg present in PATH
	Audio bool // ffmpeg present (same binary handles audio waveform)
	PDF   bool // ghostscript or pdftoppm present
	// Office documents are not a capability of this process: OnlyOffice
	// draws them while it is configured (AttachOffice, office.go).
	// SVG: an SVG can be drawn. Always true since 0.50, when the built-in
	// engine (thumb/svgwasm) arrived; false only in tests that ask for it.
	SVG bool
	// RSVG: rsvg-convert is on PATH, the fallback for a file the built-in
	// engine could not draw.
	RSVG bool
	// HEIF: ImageMagick is installed, which reads HEIC, HEIF and AVIF
	// through libheif (the full image carries imagemagick-heic).
	//
	// ⚠ Installed, not able: whether it DECODES a HEIC is measured separately
	// (enginebin.HEIC, a sample drawn once), and a HEIC it cannot decode is
	// skipped as "no_tool:heic_codec" (notool.go).
	HEIF bool
}

// New constructs a Pipeline.
func New(store db.Store, cacheDir string, caps Capabilities) *Pipeline {
	return &Pipeline{
		store:     store,
		storages:  map[int64]storage.Driver{},
		cacheDir:  cacheDir,
		caps:      caps,
		svgRender: svgwasm.Render,
	}
}

// AttachStorage registers a Driver for a storage ID — needed because the
// pipeline reads source bytes from the originating storage.
func (p *Pipeline) AttachStorage(id int64, drv storage.Driver) {
	p.storagesMu.Lock()
	p.storages[id] = drv
	p.storagesMu.Unlock()
}

// GenerateThumb dispatches based on node MIME and updates the thumbnails
// row. Idempotent — safe to call repeatedly.
//
// Falls back to the file extension when `node.Mime` is empty — this is
// the common case for files discovered by sync's driver.List, where
// most drivers don't populate the mime field. Without the fallback every
// synced file got `state=skipped` and the pipeline never produced a
// thumbnail for the demo fixtures.
//
// A panic while drawing is that file's failure (survive), never the
// process's: every caller - the upload's goroutine, the backfill and repair
// walkers, an ops job - comes through here.
func (p *Pipeline) GenerateThumb(ctx context.Context, node *model.Node) (out error) {
	defer p.survive(ctx, node, &out)
	if node == nil || node.Type != model.NodeTypeFile {
		return ErrSkipped
	}
	p.storagesMu.RLock()
	drv, ok := p.storages[node.StorageID]
	p.storagesMu.RUnlock()
	if !ok {
		return errors.New("thumb: no driver attached for storage")
	}
	// What this render is of, and when it started. Both are recorded on the
	// row whatever the render ends in (Assess reads them back): a failure or a
	// skip on this content is not asked for again, and a change of content is.
	sig := SourceSig(node)
	attempted := time.Now()
	record := func(state, errMsg string) {
		_ = p.store.UpsertThumbnail(ctx, &model.Thumbnail{
			NodeID: node.ID, State: state, Error: errMsg, SourceSig: sig, AttemptedAt: &attempted,
		})
	}
	/* wiring:e2 — files under an E2E-encrypted folder are ciphertext the
	   server cannot (and must not try to) render: skip before any byte is
	   read. Wasted CPU aside, a plaintext file mistakenly written into an
	   encrypted subtree via DAV/CLI would otherwise leak a readable thumb.
	   Upsert (not SetState — that is UPDATE-only) so the skip records even
	   though the pending row was never created. */
	if e2e.UnderEncrypted(ctx, p.store, node.StorageID, node.Path) {
		record("skipped", "e2e-encrypted folder")
		return ErrSkipped
	}
	/* wiring:e2 fxe — a single encrypted file: skipped by its name before a
	   byte is read (the generic card would otherwise be drawn for it). Under
	   any other name the content sniff in openSource catches it. */
	if e2e.LooksEncryptedFile(node.Name) {
		record("skipped", "e2e-encrypted file")
		return ErrSkipped
	}
	/* /wiring:e2 */

	// ⚠ A row that is `ready` keeps serving its picture while the new one is
	// drawn: only the attempt is stamped (so the loop guard holds). Writing
	// `pending` here, as 0.49 did for every render, blanked the card of a file
	// whose content changed until the render finished — and forever, when it
	// did not. The JPEG is replaced by the generator's own write at the end.
	// The row as it was is kept for the document server's tries (office.go:
	// a retry counts on from the failures recorded on this content).
	prev, _ := p.store.GetThumbnail(ctx, node.ID)
	if prev != nil && prev.State == "ready" {
		keep := *prev
		keep.AttemptedAt = &attempted
		_ = p.store.UpsertThumbnail(ctx, &keep)
	} else {
		record("pending", "")
	}

	mime := routeMime(node)
	chain := p.chainFor(ctx, node)
	if len(chain.On) == 0 {
		if chain.AllOff() {
			// Something could draw this kind, and the administrator switched
			// every one of them off (Admin → Plugins → Default apps).
			_ = p.store.UpsertThumbnail(ctx, &model.Thumbnail{
				NodeID: node.ID, State: "skipped", Error: SkipNoHandler, SourceSig: sig, AttemptedAt: &attempted, Attempts: "[]",
			})
			return ErrSkipped
		}
		if officeKind(node) {
			// An office document, and OnlyOffice is not configured: nobody
			// here draws it. Skipped with the program it needs named, not
			// given the placeholder card; the chain names the document server
			// once it is configured, and the row is drawn then (office.go).
			// Ciphertext under an office name says so first: it is the
			// file's reason, and it holds whoever is configured.
			if p.encryptedContent(ctx, drv, node) {
				record("skipped", "e2e-encrypted content")
				return ErrSkipped
			}
			_ = p.store.UpsertThumbnail(ctx, &model.Thumbnail{
				NodeID: node.ID, State: "skipped", Error: noToolReason(ToolOffice), SourceSig: sig, AttemptedAt: &attempted, Attempts: "[]",
			})
			return ErrSkipped
		}
		// Everything else (3D models, code, raw docs, etc) gets a deterministic
		// placeholder card so grid views still show *something* legible.
		// Cheap to render - pure Go image stdlib, no external binary.
		if err := p.generateGeneric(ctx, node); err != nil {
			record("failed", err.Error())
			return err
		}
		now := time.Now()
		_ = p.store.UpsertThumbnail(ctx, &model.Thumbnail{
			NodeID: node.ID, State: "ready", StorageKey: p.CachePath(node.ID), GeneratedAt: &now, SourceSig: sig, AttemptedAt: &attempted,
		})
		return nil
	}

	// The chain (docs/thumbnails.md → Thumbnails drawn by apps): the handlers
	// in the administrator's order, the first that draws wins. The row keeps
	// the FIRST one's state and reason when nobody drew - that is what Fix and
	// the freshness rules read (a tool that comes back, a limit raised) - and
	// says who was asked in `attempts`.
	var (
		trail        []Attempt
		first        drawOutcome
		firstErr     error
		generatorKey string
	)
	for i, h := range chain.On {
		var out drawOutcome
		switch {
		case h.IsBuiltin():
			out = p.drawBuiltin(ctx, node, drv, mime)
		case h.IsOnlyOffice():
			out = p.drawOffice(ctx, node, drv, sig, prev)
		default:
			out = p.drawApp(ctx, node, drv, h)
		}
		if out.encrypted {
			// Ciphertext is nobody's to draw: not a handler's failure, so the
			// next one is not asked.
			record("skipped", "e2e-encrypted content")
			return ErrSkipped
		}
		if out.state == "ready" {
			trail = append(trail, Attempt{H: h.Key(), R: "ok"})
			generatorKey = h.Key()
			break
		}
		trail = append(trail, Attempt{H: h.Key(), R: out.reason})
		if i == 0 {
			first, firstErr = out, out.err
		}
	}
	attempts := encodeAttempts(trail)
	if generatorKey == "" {
		_ = p.store.UpsertThumbnail(ctx, &model.Thumbnail{
			NodeID: node.ID, State: first.state, Error: first.reason, SourceSig: sig, AttemptedAt: &attempted, Attempts: attempts,
		})
		if first.state == "failed" {
			if firstErr == nil {
				firstErr = errors.New(first.reason)
			}
			return firstErr
		}
		return ErrSkipped
	}
	now := time.Now()
	_ = p.store.UpsertThumbnail(ctx, &model.Thumbnail{
		NodeID:      node.ID,
		State:       "ready",
		StorageKey:  p.CachePath(node.ID),
		GeneratedAt: &now,
		SourceSig:   sig,
		AttemptedAt: &attempted,
		Generator:   generatorKey,
		Attempts:    attempts,
	})
	return nil
}

// drawOutcome is what one handler made of a file: "ready" (its JPEG is
// written), "skipped" or "failed" with the reason the row records, or
// encrypted content nobody may draw.
type drawOutcome struct {
	state     string
	reason    string
	err       error
	encrypted bool
}

// drawBuiltin is filex's own drawer: the generator the file's type routes to.
// Only asked for a kind it draws (BuiltinDraws); its skips and failures are
// the reasons 0.49 already recorded.
func (p *Pipeline) drawBuiltin(ctx context.Context, node *model.Node, drv storage.Driver, mime string) drawOutcome {
	var err error
	switch {
	// SVG must come BEFORE the generic image/* branch - Go's stdlib
	// image.Decode can't parse SVG, so the regular generateImage
	// would fail with "unknown format".
	case mime == svgMime && p.caps.SVG:
		// Over the size limit: skipped before a byte is read, and the row
		// says which limit (svglimits.go).
		lim := p.SVGLimits()
		if node.Size > lim.MaxBytes {
			return drawOutcome{state: "skipped", reason: skipReason(SkipSVGTooLarge, lim.MaxBytes)}
		}
		err = p.generateSVG(ctx, node, drv, lim)
		var over *svgOverLimit
		if errors.As(err, &over) {
			return drawOutcome{state: "skipped", reason: over.reason}
		}
	case mime == svgMime:
		return drawOutcome{state: "skipped", reason: SkipNoSVGEngine}
	// A kind whose program this install lacks is skipped with the reason
	// (notool.go), not failed and not given the placeholder card. That
	// includes a HEIC the ImageMagick here cannot decode (heic_codec): before
	// the probe it was handed to ImageMagick and every one ended `failed`.
	case p.missingTool(mime) != "":
		return drawOutcome{state: "skipped", reason: noToolReason(p.missingTool(mime))}
	case isHEIF(mime):
		err = p.generateHEIF(ctx, node, drv)
	case strings.HasPrefix(mime, "image/"):
		err = p.generateImage(ctx, node, drv)
	case strings.HasPrefix(mime, "video/"):
		err = p.generateVideo(ctx, node, drv)
	case strings.HasPrefix(mime, "audio/"):
		err = p.generateAudio(ctx, node, drv)
	case mime == "application/pdf":
		err = p.generatePDF(ctx, node, drv)
	// A text file is drawn as its first lines, an archive as the list of
	// what is in it (text.go, archive.go), by extension: neither needs a tool.
	case isTextThumb(node.Name):
		err = p.generateText(ctx, node, drv)
	case archiveKind(node.Name) != "":
		err = p.generateArchive(ctx, node, drv, archiveKind(node.Name))
	default:
		return drawOutcome{state: "skipped", reason: SkipNoHandler}
	}
	switch {
	case err == nil:
		return drawOutcome{state: "ready"}
	case errors.Is(err, errEncryptedContent):
		return drawOutcome{encrypted: true}
	case errors.Is(err, errArchiveEncrypted):
		return drawOutcome{state: "skipped", reason: SkipArchiveEncrypted}
	case errors.Is(err, errArchiveLimits):
		return drawOutcome{state: "skipped", reason: SkipArchiveTooLarge}
	}
	// Report the PATH, not just the node id. A decode failure almost always
	// means the stored bytes are damaged, and the next question is always
	// "which file?" - with only a node id that costs a manual lookup in the
	// catalogue database before the investigation can even start.
	slog.Warn("thumb generate failed",
		slog.Int64("node", node.ID),
		slog.String("path", node.Path),
		slog.Int64("size", node.Size),
		slog.String("mime", mime),
		slog.String("err", err.Error()))
	return drawOutcome{state: "failed", reason: err.Error(), err: err}
}

// survive turns a panic inside GenerateThumb into that file's failure.
//
// ⚠ Most callers run GenerateThumb on a goroutine of its own (dispatchThumb
// after an upload, protocolsync, the backfill and repair walkers), where a
// panic is not recovered by anything and ends the whole server. One text
// thumbnail did exactly that in a full e2e run on 2026-10-01 (a font face
// shared between goroutines, textdraw.go), and every request after it was
// refused. A decoder that trips on a hostile file is the same shape. So: the
// stack is logged, the row is marked failed for this content with the attempt
// stamped (Assess then leaves it until the file changes, instead of drawing
// it - and panicking - on every listing), and the caller gets an error.
func (p *Pipeline) survive(ctx context.Context, node *model.Node, out *error) {
	rv := recover()
	if rv == nil {
		return
	}
	attrs := []any{slog.Any("panic", rv), slog.String("stack", string(debug.Stack()))}
	if node != nil {
		attrs = append(attrs, slog.Int64("node", node.ID), slog.String("path", node.Path))
	}
	slog.Error("thumb: generator panicked", attrs...)
	*out = fmt.Errorf("thumb: generator panicked: %v", rv)
	if node == nil || p.store == nil {
		return
	}
	attempted := time.Now()
	_ = p.store.UpsertThumbnail(context.WithoutCancel(ctx), &model.Thumbnail{
		NodeID: node.ID, State: "failed", Error: (*out).Error(), SourceSig: SourceSig(node), AttemptedAt: &attempted,
	})
}

// CacheDir is the directory the cached JPEGs live in.
func (p *Pipeline) CacheDir() string { return p.cacheDir }

// CachePath returns the disk path where a thumb is stored for node ID.
func (p *Pipeline) CachePath(nodeID int64) string {
	return fmt.Sprintf("%s/%d.jpg", p.cacheDir, nodeID)
}

// extMimes is the MIME class the pipeline gives a file by its extension. It
// is INTENTIONALLY narrow — the pipeline only branches on image/* / video/* /
// audio/* / application/pdf, and the office types name the document for
// OnlyOffice (office.go); other extensions stay empty and get the
// placeholder card.
var extMimes = map[string]string{
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"png":  "image/png",
	"webp": "image/webp",
	"gif":  "image/gif",
	"bmp":  "image/bmp",
	"svg":  "image/svg+xml",
	"heic": "image/heic",
	"heif": "image/heif",
	"avif": "image/avif",
	"tiff": "image/tiff",
	"tif":  "image/tiff",
	"mp3":  "audio/mpeg",
	"wav":  "audio/wav",
	"ogg":  "audio/ogg",
	"flac": "audio/flac",
	"m4a":  "audio/mp4",
	"aac":  "audio/aac",
	"opus": "audio/opus",
	"mp4":  "video/mp4",
	"webm": "video/webm",
	"mov":  "video/quicktime",
	"mkv":  "video/x-matroska",
	"avi":  "video/x-msvideo",
	"ogv":  "video/ogg",
	"m4v":  "video/mp4",
	"pdf":  "application/pdf",
	"doc":  "application/msword",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"xls":  "application/vnd.ms-excel",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"ppt":  "application/vnd.ms-powerpoint",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"odt":  "application/vnd.oasis.opendocument.text",
	"ods":  "application/vnd.oasis.opendocument.spreadsheet",
	"odp":  "application/vnd.oasis.opendocument.presentation",
	"rtf":  "application/rtf",
}

// svgMime is the type the pipeline draws with the SVG engine.
const svgMime = "image/svg+xml"

// routeMime is the MIME type the pipeline routes n by.
//
// ⚠ The catalogue's type is a content sniff for uploads and for the local
// driver (net/http DetectContentType), and the sniff knows a handful of
// formats. What it cannot name it records as a generic type: an SVG as
// text/xml or text/plain, a Word 97 document, an Excel sheet, a HEIC photo,
// a QuickTime movie as application/octet-stream. And a container it does
// name says nothing about what is inside: an .m4a is "video/mp4", an .ogg
// and an .ogv are both "application/ogg". Routing by that type sent all of
// them to the placeholder card, SVGs included (0.49 did the same).
//
// So when the recorded type is one of those (sniffedAmbiguous) and the
// extension is one the pipeline knows (extMimes), the extension decides. A
// file that is not what its name says fails in its generator, visibly.
// Parameters (`; charset=utf-8`) are dropped.
func routeMime(n *model.Node) string {
	m := strings.ToLower(strings.TrimSpace(n.Mime))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	if byName := mimeFromName(n.Name); byName != "" && (m == "" || sniffedAmbiguous(m)) {
		return byName
	}
	return m
}

// sniffedAmbiguous: the types a content sniff gives a file it could not name,
// and the containers whose name does not say what is in them.
func sniffedAmbiguous(m string) bool {
	switch m {
	case "text/plain", "text/xml", "application/xml",
		"application/octet-stream", "binary/octet-stream",
		"application/zip", "application/ogg", "video/mp4", "video/webm":
		return true
	}
	return false
}

// mimeFromName picks a thumbnail-pipeline-relevant MIME class from the
// file extension (extMimes).
func mimeFromName(name string) string {
	dot := strings.LastIndex(name, ".")
	if dot < 0 {
		return ""
	}
	return extMimes[strings.ToLower(name[dot+1:])]
}
