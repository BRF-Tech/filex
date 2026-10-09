// Package secheaders sets the response headers filex sends for the browser's
// sake on every answer, whatever serves it and wherever filex is deployed —
// so every self-hosted install has them, not only one whose reverse proxy
// adds them:
//
//   - X-Content-Type-Options: nosniff — a body is what its Content-Type says,
//     never what the browser guesses from its bytes;
//   - Referrer-Policy: strict-origin-when-cross-origin — another site is told
//     which filex a link came from, never the path (a folder or file name);
//   - Content-Security-Policy: frame-ancestors 'self' [allowed…] — on the
//     pages filex draws (HTML), who may show them inside a frame;
//   - and, on the same pages, frame-src — what THEY may show inside a frame:
//     filex itself BY PATH (the app interfaces, the download frame;
//     WithOwnFrames, else 'self'), the external editors the operator set up
//     (draw.io, ONLYOFFICE) and the origin app interfaces are served from
//     (WithFrameSources).
//
// ⚠⚠ frame-src is the one wall around an app interface's own navigation. An
// app's interface runs in a sandboxed frame with connect-src 'none', and a
// frame may still navigate ITSELF: to the author's server (with data in the
// address), or to a data:/blob: document of its own making — a fresh realm the
// WebRTC bootstrap never ran in. Measured 2026-09-27 (docs/research, harness
// variants B-sep-nohostcsp and B-bootstrap-nohostcsp): without the host page's
// frame-src all three browsers followed those navigations; with it none did.
// So data: and blob: are never frame sources here, whatever a caller passes.
//
// ⚠ frame-ancestors goes on HTML only. A file's own bytes are framed by
// design: the explorer embedded in another site (the web component, which is
// not a frame) shows a PDF from filex in an <embed> on the HOST's page, and a
// download is started through a hidden frame. Neither is a page with buttons
// to trick somebody into pressing, and refusing them would break every embed.
//
// A handler's own values win: nosniff and the referrer policy are set before
// the handler runs, so a handler may replace them, and a handler's own CSP is
// kept — frame-ancestors is added to it only when it names none, unless the
// handler said its page names none ON PURPOSE (OpenFraming).
package secheaders

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

const (
	headerCSP      = "Content-Security-Policy"
	headerNoSniff  = "X-Content-Type-Options"
	headerReferrer = "Referrer-Policy"

	noSniff  = "nosniff"
	referrer = "strict-origin-when-cross-origin"
)

// Split reads the operator's list as FILEX_FRAME_ANCESTORS spells it: origins
// separated by commas, spaces, or both.
func Split(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

// Normalize checks the pages allowed to frame filex besides filex itself
// (`frame_ancestors`, FILEX_FRAME_ANCESTORS) and returns them in the form the
// header carries. Accepted, each on its own:
//
//	https://home.example.com        an origin (scheme, host, optional port)
//	https://*.example.com           every subdomain of a host
//	*                               any page at all — the protection is off
//
// Anything else stops the server with a message that says what to write: a
// value that is silently dropped would leave filex unframeable for the one
// dashboard the operator was trying to allow, with nothing to say why.
func Normalize(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		src, err := source(v)
		if err != nil {
			return nil, fmt.Errorf("frame_ancestors: %q %v - write an origin such as https://home.example.com, https://*.example.com, or * for any page", v, err)
		}
		if !seen[src] {
			seen[src] = true
			out = append(out, src)
		}
	}
	return out, nil
}

func source(v string) (string, error) {
	if v == "*" {
		return v, nil
	}
	if strings.ContainsAny(v, "'\";,") {
		return "", fmt.Errorf("is not an origin (no quotes, semicolons or commas)")
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("is not an http(s) origin")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("carries more than an origin (a path, a query or a user)")
	}
	host := u.Hostname()
	if strings.HasPrefix(host, "*.") {
		host = host[2:]
	}
	if host == "" || strings.Contains(host, "*") {
		return "", fmt.Errorf("has a wildcard that is not a whole leading label (*.example.com)")
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}

// FrameAncestors is the policy the HTML pages carry.
func FrameAncestors(allowed []string) string {
	return strings.Join(append([]string{"frame-ancestors 'self'"}, allowed...), " ")
}

// Option adjusts the middleware.
type Option func(*options)

type options struct {
	frames func(*http.Request) []string
	own    func(*http.Request) []string
}

// WithOwnFrames names, per request, the paths of filex ITSELF its pages may
// show in a frame, as host sources with a path — `files.example.com/_appui/`,
// `files.example.com/z/` — in place of 'self'. ⚠ Security review UI-11: with
// 'self' an app's sandboxed frame could navigate itself to any page of
// filex; named by path, it reaches only another interface or a download.
// No scheme: a source without one takes the page's own (and its upgrade).
// An entry not of that plain `host[:port]/path/` form is left out; with none
// left, the pages frame 'self' as before.
func WithOwnFrames(f func(*http.Request) []string) Option {
	return func(o *options) { o.own = f }
}

var ownFrameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9.-]*[a-z0-9])?|\[[0-9a-f:.]+\])(:[0-9]{1,5})?(/[A-Za-z0-9_~-][A-Za-z0-9._~-]*)*/$`)

// WithFrameSources names, per request, the origins a filex page may show in a
// frame besides filex itself: the external editors and the app-interface
// origin. Each entry is reduced to its origin (a path is dropped: the
// browser's own frame src carries it); anything that is not an http(s) URL —
// data:, blob:, a keyword, a relative path (that is filex itself, 'self') —
// is left out. It is asked once per HTML answer, so it must be cheap.
func WithFrameSources(f func(*http.Request) []string) Option {
	return func(o *options) { o.frames = f }
}

// Middleware sets the headers. `allowed` is Normalize's answer.
func Middleware(allowed []string, opts ...Option) func(http.Handler) http.Handler {
	policy := FrameAncestors(allowed)
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set(headerNoSniff, noSniff)
			h.Set(headerReferrer, referrer)
			f := &framing{}
			r = r.WithContext(context.WithValue(r.Context(), framingKey{}, f))
			next.ServeHTTP(&writer{ResponseWriter: w, policy: policy, frames: o.frames, own: o.own, req: r, framing: f}, r)
		})
	}
}

type framingKey struct{}

// framing is what a handler tells the middleware about the page it answers.
type framing struct{ open bool }

// OpenFraming tells the middleware that the page this request answers carries
// NO frame-ancestors on purpose, so none is added: any page may frame it,
// a page with an opaque origin included. That is an app interface's page
// (internal/wasmplugin uiserve.go): the explorer that frames it is embedded in
// sites filex cannot list, and with `ui:frame-package` the page that frames
// one of its pages is the interface itself - a sandboxed, opaque origin that
// no source expression matches, not even `*` (Chromium refused the framed
// page under `frame-ancestors *`, 2026-10-08). Such a page holds nothing for
// whoever frames it: its sandbox and its bridge are what protect it. Nothing
// happens outside this middleware.
func OpenFraming(r *http.Request) {
	if f, ok := r.Context().Value(framingKey{}).(*framing); ok {
		f.open = true
	}
}

// FrameSrc is the frame-src directive of a filex page: filex's own paths
// (own; 'self' when none is usable) and the origin of every usable entry of
// extra, once each, in the order given.
func FrameSrc(own, extra []string) string {
	out := []string{"frame-src"}
	seen := map[string]bool{}
	for _, o := range own {
		if !ownFrameRe.MatchString(o) || strings.Contains(o, "/../") || strings.Contains(o, "/./") || seen[o] {
			continue
		}
		seen[o] = true
		out = append(out, o)
	}
	if len(out) == 1 {
		out = append(out, "'self'")
	}
	for _, raw := range extra {
		o, ok := frameOrigin(raw)
		if !ok || seen[o] {
			continue
		}
		seen[o] = true
		out = append(out, o)
	}
	return strings.Join(out, " ")
}

// frameOrigin reduces an address to the origin a frame-src names: an absolute
// http(s) URL with a plain host (no wildcard, no user, nothing that could end
// the directive). Everything else is refused.
func frameOrigin(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" || strings.ContainsAny(v, "'\";, *") {
		return "", false
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", false
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), true
}

// writer decides at the moment the headers go out, because only then is the
// Content-Type known.
type writer struct {
	http.ResponseWriter
	policy  string
	frames  func(*http.Request) []string
	own     func(*http.Request) []string
	req     *http.Request
	framing *framing
	decided bool
}

func (w *writer) decide() {
	if w.decided {
		return
	}
	w.decided = true
	h := w.Header()
	if !isHTML(h.Get("Content-Type")) {
		return
	}
	cur := strings.TrimSpace(h.Get(headerCSP))
	switch {
	case w.framing != nil && w.framing.open && cur != "":
		// The handler's own policy names no frame-ancestors on purpose
		// (OpenFraming); one added here would refuse the frames it is for.
	case cur == "":
		// filex's own page: who may frame it, and what it may frame. A
		// handler that wrote its own policy decides its own frames (below):
		// appending frame-src there could only widen it.
		var own, extra []string
		if w.own != nil {
			own = w.own(w.req)
		}
		if w.frames != nil {
			extra = w.frames(w.req)
		}
		h.Set(headerCSP, w.policy+"; "+FrameSrc(own, extra))
	case !strings.Contains(strings.ToLower(cur), "frame-ancestors"):
		h.Set(headerCSP, strings.TrimRight(cur, "; ")+"; "+w.policy)
	}
}

func (w *writer) WriteHeader(code int) {
	// An informational answer (103 Early Hints) is not the response.
	if code >= 200 || code < 100 {
		w.decide()
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *writer) Write(p []byte) (int, error) {
	if !w.decided && w.Header().Get("Content-Type") == "" && len(p) > 0 {
		// What net/http would set on this same Write: said here, so a page
		// written without a Content-Type is still known to be a page.
		w.Header().Set("Content-Type", http.DetectContentType(p))
	}
	w.decide()
	return w.ResponseWriter.Write(p)
}

// FlushError is what http.ResponseController calls first: a flush sends the
// headers, so the decision is made before it.
func (w *writer) FlushError() error {
	w.decide()
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController (and the WebSocket upgrade, which
// hijacks through it) reach the connection underneath.
func (w *writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func isHTML(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	}
	return mt == "text/html" || mt == "application/xhtml+xml"
}
