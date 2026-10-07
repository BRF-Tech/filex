package handlers

import (
	"net/http"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/onlyoffice"
)

// Frame serves the editor's frame (task #92): the page ONLYOFFICE's api.js
// runs in, on another origin than filex's own.
//
//	GET|HEAD /filex-frame/editor                on FILEX_ONLYOFFICE_FRAME_ORIGIN
//	GET|HEAD <base>/_appui/_onlyoffice/editor   on FILEX_APP_UI_ORIGIN
//
// Reached only on those hosts (api.officeFrameHost, api.appUIHostSplit).
// Credential-free and cookieless: the page is the same for everybody - a
// session cookie the browser sends along (the frame origin is usually the same
// site as filex) is not read - and what it opens is the signed config the
// explorer hands it. A 404 while no document server is configured, or while
// its address cannot be named in a policy.
//
// ⚠ Never cached: the policy names the document server in force, which the
// administrator can change without a restart.
func (h *OnlyOffice) Frame(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		hd.Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.Service.EnabledCtx(r.Context()) {
		http.NotFound(w, r)
		return
	}
	page, csp, ok := onlyoffice.FramePage(h.Service.LiveTarget(r.Context()).DocumentServerURL)
	if !ok {
		http.NotFound(w, r)
		return
	}
	hd.Del("Set-Cookie")
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Content-Security-Policy", csp)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Length", strconv.Itoa(len(page)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(page)
	}
}
