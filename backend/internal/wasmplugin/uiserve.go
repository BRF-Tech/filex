package wasmplugin

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// ── Serving an interface: GET <base>/_appui/<app>/<sha16>/<path> ─────────
//
// Unauthenticated and cookieless by design: the route reads no session and
// sets no cookie. What it serves is the approved package — public bytes — and
// what makes it safe to run is the policy each answer carries (UIPolicy), not
// who asked for it. docs/APP-PLUGINS-API.md → "Serving".

// UIPrefix is the route's first segment.
const UIPrefix = "/_appui/"

// uiImmutable is how long a package's fixed file is cached: its address
// carries the package hash, and its headers are the same for every grant.
const uiImmutable = "public, max-age=31536000, immutable"

// UIHandler answers the interface route. `origin` says, per request, the
// origin (with the base path, no trailing slash) the browser reached this
// route at — `<P>` is built from it — and whether the request may be served
// here at all (false: 404, e.g. a separate interface origin is configured and
// this is not it).
func (r *Registry) UIHandler(origin func(*http.Request) (string, bool)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Nothing but a served file is cached (security review UI-4): a 404
		// for a package that is installed a minute later must not stick.
		w.Header().Set("Cache-Control", "no-store")
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		base, ok := origin(req)
		if !ok {
			http.NotFound(w, req)
			return
		}
		app, short, rel, ok := splitUIPath(req)
		if !ok {
			http.NotFound(w, req)
			return
		}
		p, found := r.ByName(app)
		if !found {
			http.NotFound(w, req)
			return
		}
		if state, _ := p.State(); state != StateRunning {
			http.NotFound(w, req)
			return
		}
		b, grants := p.UIBundle(short), p.Grants
		if b == nil {
			// A tab still open on the version an upgrade replaced keeps
			// working, under the grant that version ran with (versions.go).
			if b, grants = r.previousUIBundle(p, short); b == nil {
				http.NotFound(w, req)
				return
			}
		}
		pkg := base + UIPrefix + app + "/" + b.short + "/"
		r.serveUIFile(w, req, grants, b, rel, pkg)
	})
}

// splitUIPath reads <app>/<sha16>/<path> off the request, refusing any spelling
// that is not already normal: an encoded slash or backslash, an empty, `.` or
// `..` segment, a NUL. Nothing is cleaned — a path that needs cleaning is not
// a path of the package.
func splitUIPath(req *http.Request) (app, short, rel string, ok bool) {
	if raw := strings.ToLower(req.URL.RawPath); strings.Contains(raw, "%2f") || strings.Contains(raw, "%5c") || strings.Contains(raw, "%00") {
		return "", "", "", false
	}
	rest, found := strings.CutPrefix(req.URL.Path, UIPrefix)
	if !found {
		return "", "", "", false
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 || !nameRe.MatchString(parts[0]) || len(parts[1]) != uiShortLen || !isHex(parts[1]) || !uiPathOK(parts[2]) {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// serveUIFile writes one file of the package with the headers its kind needs.
func (r *Registry) serveUIFile(w http.ResponseWriter, req *http.Request, grants Grants, b *uiBundle, rel, pkg string) {
	h := w.Header()
	// Public bytes, no credentials: a module script or a font a sandboxed
	// (opaque, Origin: null) frame loads is a CORS request.
	h.Set("Access-Control-Allow-Origin", "*")
	h.Del("Access-Control-Allow-Credentials")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-DNS-Prefetch-Control", "off")
	// ⚠ Security review UI-4: only a SUCCESSFUL answer of a fixed file is
	// immutable. An error is never cached, and the HTML — whose policy is
	// built from the grant — is revalidated every time (below).
	h.Set("Cache-Control", "no-store")
	h.Del("Set-Cookie")

	if mf, ok := b.mirrors[rel]; ok {
		f, err := os.Open(mf.file)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		defer f.Close()
		h.Set("Content-Type", mirrorType(mf.as, rel))
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		h.Set("Content-Length", strconv.FormatInt(mf.size, 10))
		h.Set("Cache-Control", uiImmutable)
		w.WriteHeader(http.StatusOK)
		if req.Method != http.MethodHead {
			_, _ = io.Copy(w, f)
		}
		return
	}

	rc, size, err := b.open(rel)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	defer rc.Close()
	ct, _ := uiTypeOf(rel)
	h.Set("Content-Type", ct)
	if !isUIHTML(rel) {
		// A file that is not a page, opened as a document anyway (an SVG, a
		// JSON file, a script typed into the address bar), runs nothing: it
		// has no bootstrap and must not need one.
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		h.Set("Content-Length", strconv.FormatInt(size, 10))
		h.Set("Cache-Control", uiImmutable)
		w.WriteHeader(http.StatusOK)
		if req.Method != http.MethodHead {
			_, _ = io.Copy(w, rc)
		}
		return
	}
	doc, err := io.ReadAll(io.LimitReader(rc, maxUIHTMLBytes+1))
	if err != nil || len(doc) > maxUIHTMLBytes {
		http.Error(w, "unreadable page", http.StatusInternalServerError)
		return
	}
	doc = uiInjectBootstrap(doc)
	csp, allow := UIPolicy(grants, pkg)
	h.Set("Content-Security-Policy", csp)
	h.Set("Connection-Allowlist", allow)
	h.Set("Permissions-Policy", uiPermissionsPolicy)
	// ⚠ UI-4 · UI-12: the policy is the GRANT's and names the host it was
	// served on, so the page is revalidated (no-cache + a validator over the
	// bytes AND the policy) and varied on Host: a narrowed grant takes effect
	// at the next opening, and no shared cache hands one host's policy to
	// another.
	sum := sha256.New()
	sum.Write([]byte(csp))
	sum.Write([]byte{0})
	sum.Write([]byte(allow))
	sum.Write([]byte{0})
	sum.Write(doc)
	etag := `"` + hex.EncodeToString(sum.Sum(nil)[:12]) + `"`
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", etag)
	h.Add("Vary", "Host")
	if match := req.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.FormatInt(int64(len(doc)), 10))
	w.WriteHeader(http.StatusOK)
	if req.Method != http.MethodHead {
		_, _ = w.Write(doc)
	}
}
