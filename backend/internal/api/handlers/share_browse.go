package handlers

/* wiring:d2 — public klasör paylaşımı gezinme sayfası + alt-dosya endpoint'i.

A folder share's plain GET /s/{token} now renders a browse page (see
internal/share/viewer.go for the layout rules + template): a gallery grid
when ≥60% of the folder's files are images/videos, a plain list
otherwise. The ZIP flow is untouched — it moved behind ?zip=… (the page's
"Tümünü indir" button), and ?zip=wait / ?zip=status keep their existing
contracts.

Sub-files are served by GET /s/{token}/f/{rel...}: PIN + expiry enforced
via the same share Resolve, the rel path is containment-checked under the
shared folder, and internal dirs (.filex-trash/.thumbs) stay invisible.
?thumb=1 marks a gallery <img> fetch: images stream inline and do NOT
count as downloads; everything else 404s (video tiles render a play badge
instead of a poster). A full file open counts one download. */

import (
	"context"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// browseSkipNames are filex-internal entries never shown on (or served
// from) a public share — mirrors streamFolderZip's skip list.
var browseSkipNames = map[string]bool{
	".filex-trash": true,
	".thumbs":      true,
	".keepdir":     true,
}

// cleanShareRel normalizes a client-supplied rel path under the shared
// root. Returns ok=false on traversal attempts ("..", absolute) or
// internal segments. "" (share root) is valid.
func cleanShareRel(rel string) (string, bool) {
	rel = strings.Trim(strings.ReplaceAll(rel, "\\", "/"), "/")
	if rel == "" {
		return "", true
	}
	cleaned := path.Clean(rel)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", false
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == ".." || browseSkipNames[seg] {
			return "", false
		}
	}
	return cleaned, true
}

// joinShareRel joins the shared node's storage path with a clean rel.
func joinShareRel(root, rel string) string {
	root = strings.Trim(root, "/")
	if rel == "" {
		return root
	}
	if root == "" {
		return rel
	}
	return root + "/" + rel
}

// escapePathSegments percent-encodes each path segment, keeping "/".
func escapePathSegments(rel string) string {
	if rel == "" {
		return ""
	}
	segs := strings.Split(rel, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// renderFolderBrowse lists ONE level of the shared folder (optionally a
// ?dir= subfolder) and renders the public browse page. The page chrome
// (style + footer) is passed into the share package template so the
// --px-* tokens and the "filex ile paylaşıldı" footer stay single-source.
func (h *Share) renderFolderBrowse(ctx context.Context, w http.ResponseWriter, r *http.Request, drv storage.Driver, node *model.Node, sh *model.Share, pin string) {
	rel, ok := cleanShareRel(r.URL.Query().Get("dir"))
	if !ok {
		h.renderErrorPage(w, http.StatusNotFound, "Not found", "This folder does not exist in the share.")
		return
	}
	dirPath := joinShareRel(node.Path, rel)
	objs, err := drv.List(ctx, dirPath)
	if err != nil {
		h.renderErrorPage(w, http.StatusNotFound, "Not found", "This folder does not exist in the share.")
		return
	}

	entries := make([]share.FolderEntry, 0, len(objs))
	for _, o := range objs {
		if browseSkipNames[o.Name] {
			continue
		}
		childRel := o.Name
		if rel != "" {
			childRel = rel + "/" + o.Name
		}
		entries = append(entries, share.FolderEntry{
			Name:    o.Name,
			RelPath: childRel,
			Kind:    share.ClassifyEntry(o.Name, o.Kind == storage.KindDirectory),
			Size:    o.Size,
			Mtime:   o.Mtime,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		di, dj := entries[i].Kind == share.EntryDir, entries[j].Kind == share.EntryDir
		if di != dj {
			return di
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	pinAmp := ""
	if pin != "" {
		pinAmp = "&pin=" + url.QueryEscape(pin)
	}
	base := shareURLPath(sh.Token)

	gallery := share.GalleryEligible(entries)
	page := share.FolderPageData{
		Style:   template.HTML(publicPageStyle),
		Footer:  template.HTML(publicFooterTR),
		Name:    node.Name,
		SubPath: rel,
		ZipHref: base + "?zip=1" + pinAmp,
		Gallery: gallery,
	}
	if rel != "" {
		up := path.Dir(rel)
		if up == "." || up == "/" {
			up = ""
		}
		switch {
		case up != "":
			page.UpHref = base + "?dir=" + url.QueryEscape(up) + pinAmp
		case pin != "":
			page.UpHref = base + "?pin=" + url.QueryEscape(pin)
		default:
			page.UpHref = base
		}
	}
	for _, e := range entries {
		row := share.FolderPageEntry{
			Name: e.Name,
			Kind: e.Kind,
		}
		if e.Kind == share.EntryDir {
			page.DirCount++
			row.Href = base + "?dir=" + url.QueryEscape(e.RelPath) + pinAmp
		} else {
			page.FileCnt++
			q := ""
			if pin != "" {
				q = "?pin=" + url.QueryEscape(pin)
			}
			row.Href = base + "/f/" + escapePathSegments(e.RelPath) + q
			row.SizeLabel = share.HumanSize(e.Size)
			if !e.Mtime.IsZero() {
				row.DateLabel = e.Mtime.Format("02.01.2006 15:04")
			}
			if gallery && e.Kind == share.EntryImage {
				sep := "?"
				if q != "" {
					sep = "&"
				}
				row.ThumbSrc = row.Href + sep + "thumb=1"
			}
		}
		page.Entries = append(page.Entries, row)
	}

	_ = share.RenderFolderPage(w, page)
}

// HandleBrowseFile streams a single file UNDER a shared folder:
// GET /s/{token}/f/{rel...}. Media endpoint → plain-text errors (the
// browse page is the human-facing surface).
func (h *Share) HandleBrowseFile(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	pin := h.extractPIN(r)

	resolved, err := h.Service.Resolve(r.Context(), tok, pin)
	switch {
	case errors.Is(err, share.ErrBadPIN):
		http.Error(w, "pin required", http.StatusUnauthorized)
		return
	case err != nil:
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if resolved.IsDrop() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	node, err := h.Store.GetNode(r.Context(), resolved.NodeID)
	if err != nil || node.Type != model.NodeTypeDirectory {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rel, ok := cleanShareRel(chi.URLParam(r, "*"))
	if !ok || rel == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	drv, err := h.StorageResolver(node.StorageID)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	full := joinShareRel(node.Path, rel)
	obj, err := drv.Stat(r.Context(), full)
	if err != nil || obj.Kind != storage.KindFile {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	kind := share.ClassifyEntry(obj.Name, false)
	thumb := r.URL.Query().Get("thumb") == "1"
	if thumb && kind != share.EntryImage {
		// Gallery tiles only inline images; a video/other "thumb" would
		// stream the full payload into an <img> — refuse instead.
		http.Error(w, "no thumb", http.StatusNotFound)
		return
	}

	rc, err := drv.Read(r.Context(), full)
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	disposition := "attachment"
	if kind == share.EntryImage || kind == share.EntryVideo || r.URL.Query().Get("inline") == "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", share.MimeForName(obj.Name))
	w.Header().Set("Content-Disposition", disposition+`; filename="`+sanitizeFilename(obj.Name)+`"`)
	if obj.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if thumb {
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	if _, err := io.Copy(w, rc); err != nil {
		return // headers already sent
	}
	if !thumb {
		_ = h.Service.IncrementDownload(r.Context(), resolved.ID)
	}
}
