package handlers

/* The pages filex shows a stranger without JavaScript, and the few that run
a script of their own (the ZIP and copy progress, "PIN accepted", the file
request uploader), rendered from templates an administrator can change
(internal/templates: page.pin, page.unlocked, page.error, page.zip_wait,
page.folder, page.file_request, page.app, page.cache_wait, all framed by
page.layout).

⚠⚠ Whose template a page wears (the owner's decision, 0.60):

	- a page of a LINK (its PIN gate, its folder, its uploader, its ZIP) wears
	  the templates and the branding of the tenant the link was made in -
	  model.Share.TenantID - on whatever address it is opened at;
	- a page that belongs to no link, and every "this link is not available"
	  page, wears the ADDRESS's: a visitor learns nothing about whose link a
	  token was from the page that says it is not available (the uniform miss
	  the public SPA keeps too).

⚠⚠ No template can run a script. What a template renders passes the
sanitizer (internal/templates/htmlx), and the page is served with a
Content-Security-Policy whose script-src is one nonce, made per response and
given to filex's own script alone - which is added here, after the template
has rendered, never by it. A page whose script needs elements of the page
(the progress bar, the uploader's fields) names them as the template's hooks,
and a template without them is not saved (templates.Def.Hooks). */

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/templates"
)

// pageNonce is a fresh CSP nonce.
func pageNonce() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "filex"
	}
	return base64.RawStdEncoding.EncodeToString(b)
}

// publicPageCSP is a templated page's policy: its own nonce'd script and
// nothing else runs; it loads its images, styles and fonts, posts its forms
// and makes its requests at filex itself (images and web fonts may come from
// elsewhere: a logo, a stylesheet a template names); it frames nothing and
// points no <base> anywhere. frame-ancestors is added by internal/secheaders.
func publicPageCSP(nonce string) string {
	return strings.Join([]string{
		"default-src 'none'",
		"script-src 'nonce-" + nonce + "'",
		"style-src 'self' 'unsafe-inline' https:",
		"img-src 'self' data: blob: https: http:",
		"font-src 'self' data: https:",
		"connect-src 'self'",
		"form-action 'self'",
		"frame-src 'none'",
		"object-src 'none'",
		"base-uri 'none'",
	}, "; ")
}

// pageScript is filex's own part of a page: markup and a script, added after
// the template rendered. Script is the JavaScript alone; Markup goes before it
// (the hidden form "PIN accepted" posts).
type pageScript struct {
	Markup string
	Script string
}

// linkTarget is a link page's template target: the tenant the link was made
// in, the visitor's language.
func linkTarget(r *http.Request, sh *model.Share, lang string) templates.Target {
	t := templates.Target{Host: r.Host, Lang: lang}
	if sh != nil {
		t.TenantID = sh.TenantID
	}
	return t
}

// addressTarget is the target of a page that belongs to no link: the address's
// templates and branding.
func addressTarget(r *http.Request, lang string) templates.Target {
	return templates.Target{Host: r.Host, ByHost: true, Lang: lang}
}

// writePublicPage renders the page key and writes it with status.
func writePublicPage(w http.ResponseWriter, r *http.Request, rend *templates.Renderer, t templates.Target, key string, status int, title string, data map[string]any, own *pageScript) {
	if data == nil {
		data = map[string]any{}
	}
	data["page"] = map[string]any{"title": title}
	out, err := orDefaultTemplates(rend).Page(r.Context(), t, key, data)
	if err != nil {
		// filex's own template failed: a defect of this build, not of an
		// administrator's (theirs falls back to this one).
		slog.Error("templates: filex's own page did not render", slog.String("template", key), slog.String("err", err.Error()))
		http.Error(w, html.EscapeString(title), status)
		return
	}
	nonce := pageNonce()
	if own != nil {
		out = beforeBodyEnd(out, own.Markup+`<script nonce="`+nonce+`">`+own.Script+`</script>`)
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", publicPageCSP(nonce))
	h.Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(out))
}

// beforeBodyEnd puts s before the page's last </body>, or at its end.
func beforeBodyEnd(page, s string) string {
	i := strings.LastIndex(strings.ToLower(page), "</body>")
	if i < 0 {
		return page + s
	}
	return page[:i] + s + page[i:]
}

// shellPage is whose public-link document (page.shell) a request gets, and
// what kind of link it is - set by Share and Drop before they hand a
// JavaScript browser the SPA (api publicShellHandler).
type shellPage struct {
	Target templates.Target
	Kind   string // "share" | "file_request"
}

type shellPageKey struct{}

// ShellPageFrom is the shell target Share or Drop set on ctx; ok false when
// none did (the document is then served as built).
func ShellPageFrom(ctx context.Context) (templates.Target, string, bool) {
	p, ok := ctx.Value(shellPageKey{}).(shellPage)
	return p.Target, p.Kind, ok
}

// withShellPage is r carrying the shell target of the link its token names:
// the link's tenant when the link is live, the address's when it is unknown,
// expired or closed - the SPA then says "not available", and the frame around
// it must not say whose link the token was.
func withShellPage(r *http.Request, store db.Store, resolver *acl.Resolver, multiTenant bool, defLocale, kind string) *http.Request {
	lang := publicLocale(r, defLocale)
	t := addressTarget(r, lang)
	if sh := liveLink(r.Context(), store, resolver, multiTenant, pathParam(r, "token"), kind == "file_request"); sh != nil {
		t = linkTarget(r, sh, lang)
	}
	return r.WithContext(context.WithValue(r.Context(), shellPageKey{}, shellPage{Target: t, Kind: kind}))
}

// liveLink is the link token names when it is live: of the kind asked for (a
// file request when drop), not expired, its creator still allowed to make it.
// Nil for anything else. It is the one test behind every "whose link is this"
// answer of the public link page - its document (withShellPage) and its
// branding (PublicAPI.LinkBranding) - so a dead link answers like an unknown
// one in both, and neither says whose link the token was.
func liveLink(ctx context.Context, store db.Store, resolver *acl.Resolver, multiTenant bool, token string, drop bool) *model.Share {
	if store == nil {
		return nil
	}
	sh, err := store.GetShareByToken(ctx, strings.ToLower(strings.TrimSpace(token)))
	if err != nil || sh == nil || sh.IsDrop() != drop || sh.IsExpired(time.Now()) {
		return nil
	}
	if !linkCreatorAllows(ctx, resolver, store, sh, multiTenant) {
		return nil
	}
	return sh
}
