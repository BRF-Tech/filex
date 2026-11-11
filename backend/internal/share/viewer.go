package share

import (
	"encoding/json"
	"errors"
	"mime" /* wiring:d2 */
	"net/http"
	"path" /* wiring:d2 */
	"strconv"
	"strings" /* wiring:d2 */
	"time"    /* wiring:d2 */

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/storage"

	"github.com/brf-tech/filex/backend/internal/httpx"
)

// Viewer renders the public file at /s/{token} (or the metadata at
// /api/files/share/{token}).
//
// On GET with PIN-protected shares, the controller expects ?pin= in the
// querystring (or the X-Filex-Pin header).
type Viewer struct {
	Service *Service
	Store   db.Store

	// StorageResolver returns a constructed storage.Driver for a given
	// storage ID. Wired by the server.
	StorageResolver func(int64) (storage.Driver, error)
}

// HandleMetadata returns share metadata as JSON — used by the embed.js
// viewer to decide whether to show the PIN input or jump straight to download.
func (v *Viewer) HandleMetadata(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	if tok == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing token"})
		return
	}
	sh, err := v.Service.store.GetShareByToken(r.Context(), tok)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	resp := map[string]any{
		"has_pin":        sh.PinHash != "",
		"expires_at":     sh.ExpiresAt,
		"download_count": sh.DownloadCount,
		"max_downloads":  sh.MaxDownloads,
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleDownload streams the shared file (or returns inline preview).
func (v *Viewer) HandleDownload(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		pin = r.Header.Get("X-Filex-Pin")
	}
	sh, err := v.Service.Resolve(r.Context(), tok, pin)
	switch {
	case errors.Is(err, ErrExpired):
		writeJSON(w, http.StatusGone, map[string]string{"error": "expired"})
		return
	case errors.Is(err, ErrBadPIN):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad pin"})
		return
	case err != nil:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	node, err := v.Store.GetNode(r.Context(), sh.NodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "node missing"})
		return
	}
	drv, err := v.StorageResolver(node.StorageID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver"})
		return
	}
	rc, err := drv.Read(r.Context(), node.Path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read"})
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Disposition", httpx.ContentDisposition("attachment", node.Name))
	w.Header().Set("Content-Type", "application/octet-stream")
	if node.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(node.Size, 10))
	}
	_, _ = copyToWriter(w, rc)

	_ = v.Service.IncrementDownload(r.Context(), sh.ID)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// copyToWriter is a tiny io.Copy wrapper that swallows the count.
func copyToWriter(w http.ResponseWriter, r interface {
	Read([]byte) (int, error)
}) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return written, nil
			}
			return written, err
		}
	}
}

/* ===== wiring:d2 — public folder-share browse page (list / gallery) =====

A folder share's GET /s/{token} renders a browse page instead of jumping
straight to the ZIP. The layout auto-selects: when ≥60% of the folder's
FILES are images/videos the page uses a large-thumbnail gallery grid
(click opens the media inline — no lightbox), otherwise a plain list.
The actual HTTP glue (driver listing, PIN threading, /f/ sub-file
streaming) lives in handlers/share_browse.go; this file owns the
classification rules and the dependency-free HTML template, in the same
design language as the other public pages (style + footer are passed in
by the handler so the --px-* tokens and the "filex ile paylaşıldı"
footer stay single-source). */

// FolderEntryKind classifies one listed entry of a shared folder.
type FolderEntryKind string

// Folder entry kinds.
const (
	EntryDir   FolderEntryKind = "dir"
	EntryImage FolderEntryKind = "image"
	EntryVideo FolderEntryKind = "video"
	EntryFile  FolderEntryKind = "file"
)

// Browser-renderable raster/vector image extensions (candidates for a
// gallery <img> tile).
var galleryImageExts = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true,
	"avif": true, "bmp": true, "svg": true, "ico": true,
}

// Common video extensions — counted toward the gallery ratio; tiles render
// a play badge (no server-side poster dependency).
var galleryVideoExts = map[string]bool{
	"mp4": true, "webm": true, "mov": true, "m4v": true, "mkv": true,
	"avi": true, "ogv": true,
}

// ClassifyEntry buckets a name into dir / image / video / other-file by
// extension (public pages have no DB mime handy — driver listings often
// don't populate mime either, mirroring the thumb pipeline's fallback).
func ClassifyEntry(name string, isDir bool) FolderEntryKind {
	if isDir {
		return EntryDir
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	switch {
	case galleryImageExts[ext]:
		return EntryImage
	case galleryVideoExts[ext]:
		return EntryVideo
	default:
		return EntryFile
	}
}

// FolderEntry is one classified row of a shared folder listing.
type FolderEntry struct {
	Name    string
	RelPath string // slash-relative path under the shared root
	Kind    FolderEntryKind
	Size    int64
	Mtime   time.Time
}

// GalleryEligible reports whether the browse page should use the gallery
// layout: at least one visual file AND ≥60% of the FILES (dirs excluded)
// are images/videos.
func GalleryEligible(entries []FolderEntry) bool {
	files, visual := 0, 0
	for _, e := range entries {
		switch e.Kind {
		case EntryDir:
			continue
		case EntryImage, EntryVideo:
			visual++
			files++
		default:
			files++
		}
	}
	if files == 0 || visual == 0 {
		return false
	}
	return float64(visual)/float64(files) >= 0.6
}

// MimeForName resolves a Content-Type for the public sub-file endpoint by
// extension, defaulting to application/octet-stream.
func MimeForName(name string) string {
	if m := mime.TypeByExtension(path.Ext(name)); m != "" {
		return m
	}
	return "application/octet-stream"
}

// The browse page itself is a template (internal/templates page.folder,
// rendered by handlers/share_browse.go): an administrator can change it.

/* ===== /wiring:d2 ===== */
