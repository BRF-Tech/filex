package httpx

import (
	"mime"
	"net/http"
	"path"
	"strings"
)

// ── Serving a file's own bytes from filex's origin ──────────────────────
//
// A person's file, or a copy an app exposes, is answered from the SAME origin
// as filex's own pages: the explorer's preview, a share's `?inline=1`, a
// shared folder's entries, an app link's exposed copies. A browser shows most
// of those as themselves (a PDF, a picture, sound, video, plain text); a few
// kinds it runs as a document of the origin that served them — HTML, SVG,
// XML/XHTML — and a document of filex's origin has filex's cookies and
// storage in reach. So every such body carries a policy that gives it an
// opaque origin and no scripts, wherever it is opened. ONE implementation,
// so every door answers the same.

// InertCSP is the policy an active kind is served under: rendered, but as an
// inert document in an opaque origin — no script, no request anywhere, no
// form, nothing it could reach as the filex origin.
const InertCSP = "sandbox; default-src 'none'; img-src data:; media-src data:; font-src data:; style-src 'unsafe-inline'; form-action 'none'; base-uri 'none'"

// InertPolicy is the Content-Security-Policy a served file carries: "" for
// the kinds a browser shows as themselves, else a script-less sandbox.
//
// ⚠ A PDF stays unsandboxed on purpose: Chrome refuses to show a PDF in a
// sandboxed document, and the preview and the signing page exist to show one.
func InertPolicy(contentType string) string {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	}
	switch {
	case mt == "application/pdf", mt == "text/plain":
		return ""
	case strings.HasPrefix(mt, "image/") && mt != "image/svg+xml":
		return ""
	case strings.HasPrefix(mt, "video/"), strings.HasPrefix(mt, "audio/"):
		return ""
	}
	return InertCSP
}

// ServedType is the Content-Type a file's bytes are answered with: the type
// recorded for it, else the one its name implies, else opaque bytes. Never
// empty — net/http would otherwise guess one after the policy was decided.
func ServedType(recorded, name string) string {
	if t := strings.TrimSpace(recorded); t != "" {
		return t
	}
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// ProtectServedFile sets the headers every served file body carries: its
// Content-Type (never sniffed) and, for an active kind, InertPolicy.
func ProtectServedFile(h http.Header, contentType string) {
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	if p := InertPolicy(contentType); p != "" {
		h.Set("Content-Security-Policy", p)
	}
}
