package onlyoffice

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"html"
	"net/url"
	"strings"
)

// ── The editor's frame (task #92) ─────────────────────────────────────────
//
// ONLYOFFICE's editor API is a script, api.js, that an integrator loads into
// its own page. Loaded into filex's page it runs with everything that page
// can do: it can read the bearer the web client keeps in sessionStorage, call
// filex's API as the signed-in person and read the page. So filex serves this
// page on ANOTHER ORIGIN and the explorer frames it: api.js then runs where
// filex's session storage and pages are out of its reach - the browser keeps
// those apart per origin, not per site, so the document server's own origin
// (docs.example.com beside files.example.com) is enough.
//
// Two places, the first that is set (api routes.go):
//
//   - FILEX_ONLYOFFICE_FRAME_ORIGIN, normally the document server's own
//     origin: its reverse proxy sends FrameHostPath to filex, and filex
//     answers that one path on that host and nothing else (api
//     officeFrameHost). api.js then runs on the origin it came from - no
//     new origin gets anything.
//   - FILEX_APP_UI_ORIGIN, the app-interface origin, at FramePath.
//
// What crosses between the explorer and this page is the editor
// configuration, as filex's server signed it, one way, and the editor's
// events, the other way (frame/frame.js has the protocol). Saving was never
// the browser's business: the document server fetches the document from filex
// and posts the save back, server to server.
//
// With neither, nothing here is served: api.js runs in filex's own page as it
// always has (docs/ONLYOFFICE.md → The editor in a frame of its own says why
// there is no in-between).

// FramePath is where the frame is served on the interface origin
// (FILEX_APP_UI_ORIGIN), below the base path; api.appUIHostSplit refuses the
// interface route on filex's own host. It is under the interface route on
// purpose: that host answers nothing else. `_onlyoffice` cannot be an app's
// name (an app's name starts with a letter or a digit), so it never shadows an
// app's interface.
const FramePath = "/_appui/_onlyoffice/editor"

// FrameHostPrefix is the one path prefix a frame origin's reverse proxy
// sends to filex (FILEX_ONLYOFFICE_FRAME_ORIGIN); everything else on that
// host is the document server's. At the host's root, whatever filex's own
// base path: it is the document server's host, not filex's. No document
// server path starts with it (theirs are /web-apps, /sdkjs, /fonts, /cache,
// /coauthoring, /<version>/…).
const FrameHostPrefix = "/filex-frame/"

// FrameHostPath is the frame on a frame origin.
const FrameHostPath = FrameHostPrefix + "editor"

// apiScriptPath is where a document server serves its editor API, below its
// base address - the same path the explorer's browser probe loads
// (packages/core lib/externalReach.ts).
const apiScriptPath = "/web-apps/apps/api/documents/api.js"

// frameMount is the element the editor replaces; frame.js names it too.
const frameMount = "filex-oo-editor"

//go:embed frame/frame.js
var frameScript string

// frameScriptHash is the policy source that allows exactly frame.js, inline.
var frameScriptHash = func() string {
	h := sha256.Sum256([]byte(frameScript))
	return "'sha256-" + base64.StdEncoding.EncodeToString(h[:]) + "'"
}()

// FramePage is the frame's page and the Content-Security-Policy it is served
// with, for the document server at dsURL (the configuration in force). ok is
// false when there is no document server, or its address cannot be named in a
// policy (FrameSource).
//
// The policy, directive by directive:
//
//   - script-src: frame.js by its hash and the document server, nothing else.
//     api.js finds its own <script> element to learn where the editor lives,
//     so it is loaded as a script element, from that server.
//   - frame-src, child-src: the document server, where api.js puts the editor.
//   - style-src 'unsafe-inline': the page's own few lines and the style
//     attributes api.js writes on the editor's frame.
//   - connect-src, img-src, font-src, media-src, form-action: the document
//     server only.
//   - no `sandbox` directive: the page keeps its own origin on purpose (the
//     document server's editor wants storage, and the origin is not filex's).
//     The explorer's frame element carries the sandbox flags.
//   - frame-ancestors *: the explorer is embedded in other sites by design
//     (<filex-explorer>, tenants on their own domains), and the page holds
//     nothing: what it shows comes from the framing page's config, signed for
//     a document that page's own session could open, which it could as well
//     hand to the document server directly. The checks that matter are the
//     framing page's (core lib/officeFrame.ts: this frame's window, this
//     origin, this session, once).
func FramePage(dsURL string) (page []byte, csp string, ok bool) {
	src, ok := FrameSource(dsURL)
	if !ok {
		return nil, "", false
	}
	apiURL := strings.TrimSuffix(src, "/") + apiScriptPath
	csp = strings.Join([]string{
		"default-src 'none'",
		"script-src " + frameScriptHash + " " + src,
		"style-src 'unsafe-inline' " + src,
		"img-src " + src + " data: blob:",
		"font-src " + src + " data:",
		"media-src " + src + " blob:",
		"connect-src " + src,
		"frame-src " + src,
		"child-src " + src,
		"worker-src 'none'",
		"object-src 'none'",
		"manifest-src 'none'",
		"form-action " + src,
		"base-uri 'none'",
		"frame-ancestors *",
	}, "; ")
	var b strings.Builder
	b.WriteString("<!doctype html>\n")
	b.WriteString(`<html lang="en" data-api="` + html.EscapeString(apiURL) + `">` + "\n")
	b.WriteString("<head>\n<meta charset=\"utf-8\">\n<title>ONLYOFFICE</title>\n")
	b.WriteString("<style>html,body{margin:0;padding:0;width:100%;height:100%;overflow:hidden;background:transparent}#" +
		frameMount + "{width:100%;height:100%}</style>\n")
	b.WriteString("</head>\n<body>\n")
	b.WriteString(`<div id="` + frameMount + `"></div>` + "\n")
	b.WriteString("<script>" + frameScript + "</script>\n")
	b.WriteString("</body>\n</html>\n")
	return []byte(b.String()), csp, true
}

// FrameSource is the document server's address as a policy source: scheme,
// host and path, ending in "/" (https://docs.example.com/, or
// https://example.com/ds/ for one under a path), so that everything the server
// serves below it matches and nothing else does. Refused: no address, a
// scheme other than http(s), a user, a query or fragment, and anything that
// could end a directive or name another source (a quote, a semicolon, a comma,
// a space, a wildcard).
func FrameSource(dsURL string) (string, bool) {
	v := strings.TrimSpace(dsURL)
	if v == "" || strings.ContainsAny(v, "'\";, *\t\r\n") {
		return "", false
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	p := strings.TrimRight(u.EscapedPath(), "/")
	return strings.ToLower(u.Scheme+"://"+u.Host) + p + "/", true
}
