package share

import (
	"encoding/json"
	"errors"
	"fmt"           /* wiring:d2 */
	"html/template" /* wiring:d2 */
	"mime"          /* wiring:d2 */
	"net/http"
	"path" /* wiring:d2 */
	"strconv"
	"strings" /* wiring:d2 */
	"time"    /* wiring:d2 */

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/storage"
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

	w.Header().Set("Content-Disposition", `attachment; filename="`+node.Name+`"`)
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

/* ===== wiring:d2 — public klasör paylaşımı gezinme sayfası (liste / galeri) =====

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

// HumanSize renders a byte count the way the public pages show it.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), []string{"KB", "MB", "GB", "TB", "PB"}[exp])
}

// FolderPageEntry is a display-ready row/tile for the template.
type FolderPageEntry struct {
	Name      string
	Href      string // open target (dir → ?dir=…, file → f/…)
	ThumbSrc  string // non-empty → gallery tile renders an <img>
	Kind      FolderEntryKind
	SizeLabel string
	DateLabel string
}

// FolderPageData feeds the folder browse template. Style and Footer are
// injected by the handler (single-source public page chrome).
type FolderPageData struct {
	Style    template.HTML
	Footer   template.HTML
	Name     string // shared folder display name
	SubPath  string // rel subdir being browsed ("" = share root)
	UpHref   string // one-level-up href ("" hides the button)
	ZipHref  string // "download all" target
	Gallery  bool
	DirCount int
	FileCnt  int
	Entries  []FolderPageEntry
}

// RenderFolderPage writes the browse page HTML.
func RenderFolderPage(w http.ResponseWriter, d FolderPageData) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return folderPageTemplate.Execute(w, d)
}

// Inline line-style icons (currentColor), matching the public-page set.
const (
	folderPageIconFolder = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3.5 7a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V7z"/></svg>`
	folderPageIconFile   = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M7 3.5h7l4 4V20a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 6 20V5A1.5 1.5 0 0 1 7.5 3.5z"/><path d="M13.5 3.5V8H18"/></svg>`
	folderPageIconImage  = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3.5" y="5" width="17" height="14" rx="2"/><circle cx="9" cy="10" r="1.6"/><path d="M4.5 17.5 10 12l4 4 2.5-2.5 3 3"/></svg>`
	folderPageIconPlay   = `<svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M8.5 6.2v11.6a1 1 0 0 0 1.52.86l9.2-5.8a1 1 0 0 0 0-1.72l-9.2-5.8a1 1 0 0 0-1.52.86z"/></svg>`
)

// folderPageTemplate is the dependency-free browse page for a shared
// folder. Turkish copy, matching the zip-wait/unlocked pages.
var folderPageTemplate = template.Must(template.New("sharefolder").Parse(`<!doctype html>
<html lang="tr"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Name}} — paylaşılan klasör</title>
{{.Style}}
<style>
.card--folder { width: 880px; max-width: 100%; text-align: left; padding: 26px 24px; }
.fhead { display: flex; align-items: center; gap: 12px; margin-bottom: 4px; }
.fhead .icon-badge { margin: 0; width: 46px; height: 46px; flex: none; }
.fhead .icon-badge svg { width: 24px; height: 24px; }
.fhead h1 { margin: 0; font-size: 1.15rem; overflow-wrap: anywhere; }
.fsub { margin: 0 0 16px; padding-left: 58px; color: var(--px-muted); font-size: 0.84rem; overflow-wrap: anywhere; }
.factions { display: flex; gap: 8px; margin: 0 0 18px; flex-wrap: wrap; }
.fbtn { display: inline-block; padding: 9px 16px; border-radius: 10px; font-size: 0.9rem; font-weight: 600; text-decoration: none; text-align: center; background: var(--px-accent); color: #fff; transition: background 0.15s ease; }
.fbtn:hover { background: var(--px-accent-hover); }
.fbtn--ghost { background: transparent; color: var(--px-accent); border: 1px solid var(--px-line); }
.fbtn--ghost:hover { background: var(--px-accent-soft); }
.flist { list-style: none; margin: 0; padding: 0; border: 1px solid var(--px-line); border-radius: 12px; overflow: hidden; }
.flist li + li { border-top: 1px solid var(--px-line); }
.flist a { display: flex; align-items: center; gap: 10px; padding: 10px 14px; color: inherit; text-decoration: none; font-size: 0.9rem; }
.flist a:hover { background: var(--px-accent-soft); }
.flist .ficon { width: 20px; height: 20px; flex: none; color: var(--px-muted); }
.flist .ficon svg { width: 100%; height: 100%; }
.flist .fname { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.flist .fmeta { color: var(--px-muted); font-size: 0.78rem; white-space: nowrap; font-variant-numeric: tabular-nums; }
.ggrid { display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); gap: 10px; }
.gtile { position: relative; display: block; border-radius: 12px; overflow: hidden; border: 1px solid var(--px-line); background: var(--px-bg2); aspect-ratio: 1 / 1; text-decoration: none; color: inherit; }
.gtile:hover { border-color: var(--px-accent); }
.gtile img { width: 100%; height: 100%; object-fit: cover; display: block; }
.gtile .gicon { position: absolute; inset: 0; display: grid; place-items: center; color: var(--px-muted); }
.gtile .gicon svg { width: 44px; height: 44px; }
.gtile .gname { position: absolute; left: 0; right: 0; bottom: 0; padding: 22px 10px 8px; font-size: 0.78rem; color: #fff; background: linear-gradient(transparent, rgba(0, 0, 0, 0.66)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.gtile .gbadge { position: absolute; top: 8px; right: 8px; width: 28px; height: 28px; border-radius: 50%; display: grid; place-items: center; background: rgba(0, 0, 0, 0.55); color: #fff; }
.gtile .gbadge svg { width: 15px; height: 15px; }
.fempty { padding: 34px 16px; text-align: center; color: var(--px-muted); border: 1px dashed var(--px-line); border-radius: 12px; font-size: 0.9rem; }
@media (max-width: 560px) { .fsub { padding-left: 0; } .ggrid { grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); } }
</style>
</head><body>
<main class="wrap">
<div class="card card--folder">
<div class="fhead">
<div class="icon-badge">` + folderPageIconFolder + `</div>
<h1>{{.Name}}</h1>
</div>
<p class="fsub">{{.DirCount}} klasör · {{.FileCnt}} dosya{{if .SubPath}} — {{.SubPath}}{{end}}</p>
<div class="factions">
{{if .UpHref}}<a class="fbtn fbtn--ghost" href="{{.UpHref}}">← Üst klasör</a>{{end}}
<a class="fbtn" href="{{.ZipHref}}">Tümünü indir (ZIP)</a>
</div>
{{if not .Entries}}
<div class="fempty">Bu klasör boş.</div>
{{else if .Gallery}}
<div class="ggrid">
{{range .Entries}}<a class="gtile" href="{{.Href}}"{{if ne .Kind "dir"}} target="_blank" rel="noopener"{{end}} title="{{.Name}}{{if .SizeLabel}} — {{.SizeLabel}}{{end}}">
{{if .ThumbSrc}}<img src="{{.ThumbSrc}}" alt="{{.Name}}" loading="lazy">{{else if eq .Kind "dir"}}<span class="gicon">` + folderPageIconFolder + `</span>{{else if eq .Kind "video"}}<span class="gicon">` + folderPageIconFile + `</span>{{else if eq .Kind "image"}}<span class="gicon">` + folderPageIconImage + `</span>{{else}}<span class="gicon">` + folderPageIconFile + `</span>{{end}}
{{if eq .Kind "video"}}<span class="gbadge">` + folderPageIconPlay + `</span>{{end}}
<span class="gname">{{.Name}}</span>
</a>
{{end}}</div>
{{else}}
<ul class="flist">
{{range .Entries}}<li><a href="{{.Href}}"{{if ne .Kind "dir"}} target="_blank" rel="noopener"{{end}}>
<span class="ficon">{{if eq .Kind "dir"}}` + folderPageIconFolder + `{{else if eq .Kind "image"}}` + folderPageIconImage + `{{else}}` + folderPageIconFile + `{{end}}</span>
<span class="fname">{{.Name}}</span>
{{if .SizeLabel}}<span class="fmeta">{{.SizeLabel}}</span>{{end}}
{{if .DateLabel}}<span class="fmeta">{{.DateLabel}}</span>{{end}}
</a></li>
{{end}}</ul>
{{end}}
</div>
{{.Footer}}
</main>
</body></html>`))

/* ===== /wiring:d2 ===== */
