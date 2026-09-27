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
//     pages filex draws (HTML), who may show them inside a frame.
//
// ⚠ frame-ancestors goes on HTML only. A file's own bytes are framed by
// design: the explorer embedded in another site (the web component, which is
// not a frame) shows a PDF from filex in an <embed> on the HOST's page, and a
// download is started through a hidden frame. Neither is a page with buttons
// to trick somebody into pressing, and refusing them would break every embed.
//
// A handler's own values win: nosniff and the referrer policy are set before
// the handler runs, so a handler may replace them, and a handler's own CSP is
// kept — frame-ancestors is added to it only when it names none.
package secheaders

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
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
			return nil, fmt.Errorf("frame_ancestors: %q %v — write an origin such as https://home.example.com, https://*.example.com, or * for any page", v, err)
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

// Middleware sets the three headers. `allowed` is Normalize's answer.
func Middleware(allowed []string) func(http.Handler) http.Handler {
	policy := FrameAncestors(allowed)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set(headerNoSniff, noSniff)
			h.Set(headerReferrer, referrer)
			next.ServeHTTP(&writer{ResponseWriter: w, policy: policy}, r)
		})
	}
}

// writer decides at the moment the headers go out, because only then is the
// Content-Type known.
type writer struct {
	http.ResponseWriter
	policy  string
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
	case cur == "":
		h.Set(headerCSP, w.policy)
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
