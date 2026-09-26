// Package basepath serves filex under a path prefix — a "sub-path
// deployment", `https://example.com/filex/` behind a reverse proxy — and
// answers, for the code that builds browser-facing URLs, "what is this
// install's base?".
//
// # The contract (docs/DEPLOYMENT.md, "Serving filex under a sub-path")
//
// The reverse proxy passes the FULL path. A browser asks for
// `/filex/api/files/manager`, filex receives `/filex/api/files/manager`, and
// this package takes `/filex` off before the router sees the request, so every
// route, middleware and handler keeps reading the paths it always read
// (`/api/...`, `/admin/...`, `/dav/...`). A proxy that strips the prefix
// (Caddy's `handle_path`, nginx `proxy_pass http://filex/;` with a trailing
// slash) is NOT supported: filex could not tell a stripped `/api/x` from a
// request that never went through the proxy, which is exactly the path
// confusion a prefix must not open.
//
// # What stays outside the base
//
// Nothing reaches a handler from outside the base, with one exception:
// `/healthz`. It answers both at `<base>/healthz` and at the root, because the
// container images bake `wget http://127.0.0.1:5212/healthz` into their
// HEALTHCHECK and an orchestrator probes the pod directly, without the proxy.
// It carries no data and no credential, so answering it twice opens nothing.
//
// # Where the base lives at request time
//
// On the request's context (With / From). The router's first own middleware
// puts it there, so a handler that builds a redirect, an HTML link or a cookie
// path reads it with no wiring of its own — and a handler test that builds a
// request without it gets the root deployment, which is what it always
// measured.
package basepath

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Normalize validates a configured base path and returns it in canonical form:
// "" for the root, otherwise "/seg[/seg…]" with no trailing slash.
//
// ⚠ Deliberately strict, because the value ends up in every redirect, cookie
// path and link filex hands out:
//
//   - it must start with "/" (a relative base has nothing to be relative to);
//   - it must NOT end with "/" — `/filex/` and `/filex` would otherwise be two
//     spellings of one base, and a cookie scoped to one does not always match
//     the other;
//   - no empty, "." or ".." segment — a base that climbs out of itself is a
//     path-confusion hole, not a prefix;
//   - segments use only the URL's unreserved characters (letters, digits and
//     `- . _ ~`). Anything else would need percent-encoding, and then the
//     escaped path the browser sends and the decoded path the router reads
//     start differently, which is where a prefix check can be talked past.
//
// "" and "/" both mean the root.
func Normalize(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "/" {
		return "", nil
	}
	if !strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("base path %q must start with a slash, for example /filex", raw)
	}
	if strings.HasSuffix(s, "/") {
		return "", fmt.Errorf("base path %q must not end with a slash: use %q", raw, strings.TrimRight(s, "/"))
	}
	for _, seg := range strings.Split(s[1:], "/") {
		switch seg {
		case "":
			return "", fmt.Errorf("base path %q has an empty segment (//)", raw)
		case ".", "..":
			return "", fmt.Errorf("base path %q must not contain a %q segment", raw, seg)
		}
		for _, c := range seg {
			if !unreserved(c) {
				return "", fmt.Errorf("base path %q may only use letters, digits and - . _ ~ (found %q)", raw, string(c))
			}
		}
	}
	return s, nil
}

// unreserved reports whether c is one of RFC 3986's unreserved characters.
func unreserved(c rune) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '-', c == '.', c == '_', c == '~':
		return true
	}
	return false
}

// FromURL returns the normalized path of an absolute URL, "" when it has none.
//
// A trailing slash is forgiven here and not in Normalize: `FILEX_PUBLIC_URL`
// has always been accepted as `https://example.com/` and trimmed, and the same
// URL with a path (`https://example.com/filex/`) is the same address.
// A string that is not an absolute URL has no path to offer and yields "".
func FromURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", nil
	}
	p := u.EscapedPath()
	if strings.Contains(p, "%") {
		return "", fmt.Errorf("the path of %q must not be percent-encoded", raw)
	}
	p = strings.TrimRight(p, "/")
	return Normalize(p)
}

type ctxKey struct{}

// With returns ctx carrying base. The router does this once per request; a
// test does it to build a request that arrived under a base.
func With(ctx context.Context, base string) context.Context {
	if base == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, base)
}

// From returns the base the request arrived under: "" at the root.
func From(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	b, _ := ctx.Value(ctxKey{}).(string)
	return b
}

// Path returns p (an absolute application path such as "/admin/") as the
// browser has to ask for it: with the base in front.
//
// ⚠ For what the BROWSER resolves — a Location header, a link or a form in a
// page the server renders, the target of a bounce. A relative URL inside a
// JSON answer (`thumb_url`, a `/z/` ticket) stays relative to the server root:
// its reader joins it with its own API base, which already carries the prefix
// and which a proxying embed may have mapped somewhere else entirely.
func Path(ctx context.Context, p string) string {
	return From(ctx) + p
}

// CookiePath is the Path attribute for a cookie filex sets: the base, or "/"
// at the root. A cookie scoped to the base is not sent to the other
// applications on the same host.
func CookiePath(ctx context.Context) string {
	if b := From(ctx); b != "" {
		return b
	}
	return "/"
}

// Strip removes base from an absolute path the client sent in a HEADER (a
// WebDAV `Destination`), reporting false when the path is outside the base.
// A root base returns p unchanged.
func Strip(base, p string) (string, bool) {
	if base == "" {
		return p, true
	}
	if p == base {
		return "/", true
	}
	if strings.HasPrefix(p, base+"/") {
		return p[len(base):], true
	}
	return "", false
}

// Middleware serves the router under base. With base "" it returns next
// itself: the root deployment runs exactly the code it ran before this
// package existed.
//
// Under a base:
//
//   - `<base>/…` has the base taken off the path (and the escaped path) and
//     the base put on the context, then goes on;
//   - `<base>` itself is a directory, like `/admin`: a 301 to `<base>/`;
//   - `/healthz` goes on unchanged (see the package comment);
//   - anything else is a plain 404, before any handler, cookie or auth chain.
//
// ⚠ When the escaped path is set (the client percent-encoded something), it
// has to carry the base LITERALLY as well. The router routes on the escaped
// path when there is one, so a request whose decoded path starts with the base
// while its escaped path does not (`/fil%65x/…`, `/filex%2Fapi/…`) would have
// the two halves disagree about where the prefix ends. Such a request is
// refused rather than guessed at.
func Middleware(base string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if base == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			switch {
			case strings.HasPrefix(p, base+"/"):
				raw := r.URL.RawPath
				if raw != "" && !strings.HasPrefix(raw, base+"/") {
					http.NotFound(w, r)
					return
				}
				u := *r.URL
				u.Path = p[len(base):]
				if raw != "" {
					u.RawPath = raw[len(base):]
				}
				r2 := r.WithContext(With(r.Context(), base))
				r2.URL = &u
				next.ServeHTTP(w, r2)
			case p == base:
				target := base + "/"
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusMovedPermanently)
			case p == "/healthz":
				next.ServeHTTP(w, r)
			default:
				http.NotFound(w, r)
			}
		})
	}
}
