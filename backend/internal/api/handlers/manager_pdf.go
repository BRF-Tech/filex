package handlers

import "net/http"

// PDFPreviewPath is where a PDF's preview is answered on a path of its own
// (GitHub #124): `GET /api/files/pdf/{name}?path=<storage>://<file>`.
//
// ⚠⚠ Why a path of its own. filex's pages frame filex itself only BY PATH
// (security review UI-11, api.pageOwnFrames), and Chrome checks a PDF in an
// <object> against the page's frame-src: the manager's preview URL was
// refused there and the explorer drew its "could not show" fallback instead
// of the document. Measured 2026-10-20 in Chrome with the policy filex sends:
// "Framing '…/api/files/manager?action=preview&path=…' violates the following
// Content Security Policy directive: frame-src …/_appui/ …/z/ …/_print/".
// Naming the manager in frame-src would let an app interface's frame navigate
// itself to any of the manager's answers; this path answers one thing, a
// PDF's preview, so it is the only API path a filex page may frame.
const PDFPreviewPath = "/api/files/pdf/"

// PDF serves a PDF's preview and nothing else.
//
// `{name}` is for the browser's viewer only (its title and the name it saves
// under); the file is `?path=`, exactly as on the manager, so root
// confinement rewrites it the same way. The verb is always preview: an
// `?action=download` or `?q=` sent here is overwritten, so the path cannot
// be turned into a download or any other manager operation. Everything else
// is the manager's own preview - the storages the caller can see, the
// traversal and drafts guards, the token's root, the viewer level the preview
// asks and Range.
//
// A path that does not name a PDF is refused before the storage is asked:
// the same extension decides the type the body is served with (mimeByExt), so
// what this path answers is always `application/pdf`, never a page.
func (h *Manager) PDF(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if mimeByExt(q.Get("path")) != "application/pdf" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "only a PDF is previewed here"})
		return
	}
	q.Set("action", "preview")
	q.Del("q")
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = q.Encode()
	h.listVuefinder(w, r2, "preview")
}
