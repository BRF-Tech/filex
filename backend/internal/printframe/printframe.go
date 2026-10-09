// Package printframe serves the page filex prints an app's PDF from
// (`ui.print`, task #189).
//
// An app's interface runs in a frame sandboxed with `allow-scripts` only, and
// a sandboxed frame without `allow-modals` may not open the browser's print
// dialog (measured 2026-10-08: Chromium ignores `print()` there and says so,
// Firefox ignores it silently). So the interface hands its PDF to filex over
// the bridge, and filex prints it here.
//
// Why a page of its own and not a frame of the explorer's page: filex's pages
// never frame a `blob:` or `data:` document (internal/secheaders: frame-src is
// the wall around an app interface's own navigation, and `blob:` in it would
// let an interface navigate its frame to a document of its own making). This
// page holds no app code and frames nothing but the `blob:` PDF it made
// itself from the bytes its parent posted, so it may.
//
// The page, under the base path:
//
//	GET|HEAD <base>/_print/
//
// Credential-free and cookieless, the same for everybody: it reads nothing on
// the server and says nothing about anybody. The explorer (core
// lib/printPdf.ts) draws it in a frame, waits for its `filex:print-ready`,
// and posts `{type: "filex:print", pdf, label, look}` with a MessagePort. The
// page checks the first bytes are a PDF's, frames them as a `blob:` address
// it made itself (always `application/pdf`, whatever type the bytes came
// with), draws ONE button - the parent's words (filex's "Allow") in the
// parent's look - and answers `{state: "ask", width, height}`: the explorer
// shows the frame as the Allow button of its question row. The button wakes
// when the parent says `arm` (the question has been on screen a moment), and
// only the person's own click on it prints: a trusted click while the
// browser reports the person's activation on THIS page
// (`navigator.userActivation.isActive`). Nothing else calls `print()` - not
// the PDF loading, not a message, not a script's click. The answer is
// `{ok: true}`, or `{ok: false, code}`; `cancel` from the parent drops it all.
//
// ⚠ Why the click must land IN this page: a click on the explorer's own
// button is the person's activation on the explorer's page, and a browser
// shares it only with frames of the SAME origin (and not in every engine).
// The explorer is embedded in other sites (the web component) and the desktop
// app's page is an origin of its own (`app://filex`): there, an "Allow" drawn
// by the explorer would never count here. The button drawn here always does.
//
// Who may frame it (Policy): filex itself, the desktop app (DesktopOrigin)
// and the pages the operator allows to frame filex (FILEX_FRAME_ANCESTORS,
// `frame_ancestors` - the site an embed runs on). Any other site's frame of
// it is refused by the browser: a page any site could frame would let any
// site open the print dialog with a PDF of its own under filex's name.
package printframe

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/secheaders"
)

// Path is the page's path below the base path. Filex's own pages may frame
// it (api pageOwnFrames).
const Path = "/_print/"

// DesktopOrigin is the origin of the desktop app's own page (desktop/src
// main.ts APP_ORIGIN): its explorer frames the print page from there, an
// origin no list of sites names.
const DesktopOrigin = "app://filex"

//go:embed print.js
var script string

// scriptHash is the policy source that allows exactly print.js, inline.
var scriptHash = func() string {
	h := sha256.Sum256([]byte(script))
	return "'sha256-" + base64.StdEncoding.EncodeToString(h[:]) + "'"
}()

// CSP is the page's own Content-Security-Policy, directive by directive, but
// for who may frame it (Policy adds that):
//
//   - script-src: print.js by its hash, nothing else;
//   - style-src: the page's own style and the button's look (inline);
//   - frame-src, child-src, object-src: `blob:` only - the PDF the page made
//     from the bytes it was posted; never data:, never a network address;
//   - connect-src, img-src, font-src, media-src, worker-src, form-action:
//     none - the page fetches nothing.
var CSP = strings.Join([]string{
	"default-src 'none'",
	"script-src " + scriptHash,
	"style-src 'unsafe-inline'",
	"frame-src blob:",
	"child-src blob:",
	"object-src blob:",
	"connect-src 'none'",
	"img-src 'none'",
	"font-src 'none'",
	"media-src 'none'",
	"worker-src 'none'",
	"manifest-src 'none'",
	"form-action 'none'",
	"base-uri 'none'",
}, "; ")

// Policy is the policy the page is served with: CSP, and who may frame it -
// filex itself ('self'), `ancestors` (the operator's FILEX_FRAME_ANCESTORS, as
// secheaders.Normalize answered it) and the desktop app. The page names its
// frame-ancestors itself, so the middleware adds none.
func Policy(ancestors []string) string {
	allowed := append(append([]string(nil), ancestors...), DesktopOrigin)
	return CSP + "; " + secheaders.FrameAncestors(allowed)
}

// style is the page's look: a transparent page, the PDF's frame laid out but
// never seen (a browser may not print a PDF it does not draw), and the one
// button - its look replaced by the parent's (print.js).
const style = "html,body{margin:0;padding:0;background:transparent;color-scheme:normal}" +
	"body{padding:3px;overflow:hidden}" +
	"iframe.pdf{position:absolute;inset-inline-start:0;top:0;width:1px;height:1px;border:0;opacity:0;pointer-events:none}" +
	"button{display:inline-block;margin:0;padding:6px 12px;border:1px solid #2563eb;border-radius:6px;background:#2563eb;color:#fff;font:13px/1.2 system-ui,sans-serif;white-space:nowrap;cursor:pointer}" +
	"button:disabled{opacity:.55;cursor:default}" +
	"button:focus-visible{outline:2px solid #2563eb;outline-offset:1px}"

// Page is the page's HTML: an empty body and print.js inline.
func Page() []byte {
	var b strings.Builder
	b.WriteString("<!doctype html>\n")
	b.WriteString(`<html lang="en">` + "\n")
	b.WriteString("<head>\n<meta charset=\"utf-8\">\n<title>filex print</title>\n")
	b.WriteString("<style>" + style + "</style>\n")
	b.WriteString("</head>\n<body>\n")
	b.WriteString("<script>" + script + "</script>\n")
	b.WriteString("</body>\n</html>\n")
	return []byte(b.String())
}

var page = Page()

// Handler answers GET and HEAD with the page under Policy(ancestors).
func Handler(ancestors []string) http.HandlerFunc {
	policy := Policy(ancestors)
	return func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			hd.Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		hd.Del("Set-Cookie")
		hd.Set("Cache-Control", "no-cache")
		hd.Set("Content-Type", "text/html; charset=utf-8")
		hd.Set("Content-Security-Policy", policy)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("Content-Length", strconv.Itoa(len(page)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(page)
		}
	}
}
