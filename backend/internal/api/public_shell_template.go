package api

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/templates"
)

/* The public link page's document from a template (internal/templates
page.shell): what an administrator writes around the application - the
<head>'s styles, a header and a footer around `{{ app }}` - while the card
itself is the SPA's (packages/core PublicShell), restyled by that CSS through
its documented class names (docs/TEMPLATES.md, "The public link page").

⚠⚠ The application's own markup is put in AFTER the template rendered and
was sanitized: the template is given two markers (a nonce of this response
in a comment), the sanitizer cannot take what it never saw, and the
markers are replaced by the built index.html's <head> and <body> - every
<script> of it carrying the response's nonce. The document then carries a
Content-Security-Policy (a <meta> at the top of <head>, so the HTTP header
secheaders adds - frame-ancestors, frame-src - stays as it is) whose
script-src is that nonce and 'strict-dynamic': the SPA's modules load, a
script the template managed to write would not run. */

var (
	headOpenRe = regexp.MustCompile(`(?is)<head(\s[^>]*)?>`)
	bodyOpenRe = regexp.MustCompile(`(?is)<body(\s[^>]*)?>`)
	scriptRe   = regexp.MustCompile(`(?i)<script(\s|>)`)
)

func shellNonce() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "filex"
	}
	return base64.RawStdEncoding.EncodeToString(b)
}

// shellCSP is the public link document's policy (a <meta>): only filex's own
// scripts run (the nonce, and what they load: 'strict-dynamic'), a worker is
// filex's, an <object> (a PDF preview) is filex's, no <base> points elsewhere.
func shellCSP(nonce string) string {
	return "script-src 'nonce-" + nonce + "' 'strict-dynamic' 'self'; worker-src 'self'; object-src 'self'; base-uri 'self'"
}

// templatedShell is doc (the built, branded index.html) inside the page.shell
// template of the request's link (handlers.ShellPageFrom). ok false: serve
// doc as it is (no target was set, the document has no head or body to take
// apart, or the template lost the application).
func templatedShell(rend *templates.Renderer, doc []byte, r *http.Request) ([]byte, bool) {
	if rend == nil {
		return nil, false
	}
	target, kind, ok := handlers.ShellPageFrom(r.Context())
	if !ok {
		return nil, false
	}
	s := string(doc)
	hOpen := headOpenRe.FindStringIndex(s)
	hClose := strings.Index(strings.ToLower(s), "</head>")
	bOpen := bodyOpenRe.FindStringIndex(s)
	bClose := strings.LastIndex(strings.ToLower(s), "</body>")
	if hOpen == nil || bOpen == nil || hClose < hOpen[1] || bClose < bOpen[1] || bOpen[0] < hClose {
		return nil, false
	}
	nonce := shellNonce()
	withNonce := func(part string) string {
		return scriptRe.ReplaceAllStringFunc(part, func(m string) string {
			return `<script nonce="` + nonce + `"` + m[len("<script"):]
		})
	}
	head := withNonce(s[hOpen[1]:hClose])
	app := withNonce(s[bOpen[1]:bClose])
	// ⚠ Fixed markers, not the nonce: the data then repeats for every visit
	// of a tenant's links in one language, and an edited shell is rendered in
	// the sandbox once and served from the renderer's cache. A template that
	// writes a marker itself only makes it appear twice, which is refused
	// below like a missing one.
	const headMark, appMark = "<!--filex:app-head-->", "<!--filex:app-->"
	out, err := rend.Page(r.Context(), target, "page.shell", map[string]any{
		"app_head": templates.HTML(headMark),
		"app":      templates.HTML(appMark),
		"link":     map[string]any{"kind": kind},
	})
	if err != nil || strings.Count(out, appMark) != 1 || strings.Count(out, headMark) != 1 {
		slog.Warn("templates: the public link document lost the application; served as built", slog.Any("err", err))
		return nil, false
	}
	out = strings.Replace(out, appMark, app, 1)
	out = strings.Replace(out, headMark, head, 1)
	meta := `<meta http-equiv="Content-Security-Policy" content="` + shellCSP(nonce) + `">`
	if loc := headOpenRe.FindStringIndex(out); loc != nil {
		out = out[:loc[1]] + meta + out[loc[1]:]
	} else {
		out = meta + out
	}
	var b bytes.Buffer
	b.WriteString(out)
	return b.Bytes(), true
}
