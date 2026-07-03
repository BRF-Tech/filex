// Package confine implements per-request root confinement for the /api/files
// surface. It lets a host app (e.g. work.example.com proxying the embedded filex
// explorer) lock a caller into a single sub-folder so a multi-tenant deploy
// can never let one project read or mutate another's files.
//
// The confinement root comes from two trusted sources, combined narrowest-wins:
//
//  1. An API token's `root:<adapter>://<rel>` scope — the HARD ceiling. The
//     browser never holds the token (the host injects it server-side), so a
//     token-confined caller cannot escape its root.
//  2. The `X-Filex-Root: <adapter>://<rel>` request header — narrows further
//     within the token root (or, absent a token root, sets the root). Set by
//     the host's trusted proxy per request; a stray client header can only
//     narrow, never widen past the token root.
//
// Enforcement is a single chi middleware that rewrites/validates every
// path-bearing field of the request (query `?path=` and the JSON body
// `path`/`item`/`target`/`sourceDir`/`source[]`/`items[].path`). Anything
// outside the root is rejected 403; a root/empty path is rewritten to the
// confined folder so listings open there. Because it sits in one place it
// cannot miss an endpoint.
package confine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// ErrOutOfRoot is returned when a requested path escapes the confinement root.
var ErrOutOfRoot = errors.New("path outside confined root")

// Root is a confinement scope: a single storage adapter + a clean relative
// prefix within it (no leading/trailing slash; "" == the storage root).
type Root struct {
	Adapter string
	Rel     string
}

const headerName = "X-Filex-Root"
const scopePrefix = "root:"

// split parses "<adapter>://<rel>" (or a bare "<rel>") into its parts with the
// rel cleaned of surrounding slashes and any traversal collapsed.
func split(raw string) (adapter, rel string) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "://"); i >= 0 {
		adapter = raw[:i]
		rel = raw[i+3:]
	} else {
		rel = raw
	}
	rel = strings.Trim(path.Clean("/"+rel), "/")
	if rel == "." {
		rel = ""
	}
	return adapter, rel
}

// parseRoot turns "<adapter>://<rel>" into a Root, or ok=false if empty.
func parseRoot(raw string) (Root, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Root{}, false
	}
	a, rel := split(raw)
	if a == "" {
		return Root{}, false // a confinement root must name its storage
	}
	return Root{Adapter: a, Rel: rel}, true
}

// contains reports whether `r` confines (is an ancestor of or equal to) `c`.
func (r Root) contains(c Root) bool {
	if r.Adapter != c.Adapter {
		return false
	}
	if r.Rel == "" {
		return true
	}
	return c.Rel == r.Rel || strings.HasPrefix(c.Rel, r.Rel+"/")
}

// FromRequest derives the effective confinement root for r: the token's
// `root:` scope narrowed by an X-Filex-Root header. Returns ok=false when the
// request is unrestricted (no token root and no header) — admins / the native
// panel keep full access.
func FromRequest(r *http.Request) (Root, bool, error) {
	var tokenRoot Root
	haveToken := false
	if tok := auth.TokenFrom(r.Context()); tok != nil {
		for _, s := range strings.Split(tok.Scopes, ",") {
			s = strings.TrimSpace(s)
			if strings.HasPrefix(s, scopePrefix) {
				if rt, ok := parseRoot(strings.TrimPrefix(s, scopePrefix)); ok {
					tokenRoot, haveToken = rt, true
				}
			}
		}
	}

	hdr := strings.TrimSpace(r.Header.Get(headerName))
	if hdr == "" {
		if haveToken {
			return tokenRoot, true, nil
		}
		return Root{}, false, nil
	}

	hdrRoot, ok := parseRoot(hdr)
	if !ok {
		// A malformed header on a token-confined request must not widen access.
		if haveToken {
			return tokenRoot, true, nil
		}
		return Root{}, false, nil
	}
	if haveToken && !tokenRoot.contains(hdrRoot) {
		// Header tried to escape the token ceiling → reject the whole request.
		return tokenRoot, true, ErrOutOfRoot
	}
	return hdrRoot, true, nil
}

// RootFromToken derives a confinement Root from the API token on ctx alone
// (its `root:` scope), without an *http.Request. The AI surface (/api/ai) does
// not pass through Middleware, so aiOps calls this to honor a token's path
// ceiling. Returns ok=false for unconfined tokens / cookie sessions.
func RootFromToken(ctx context.Context) (Root, bool) {
	tok := auth.TokenFrom(ctx)
	if tok == nil {
		return Root{}, false
	}
	for _, s := range strings.Split(tok.Scopes, ",") {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, scopePrefix) {
			if rt, ok := parseRoot(strings.TrimPrefix(s, scopePrefix)); ok {
				return rt, true
			}
		}
	}
	return Root{}, false
}

// enforce validates a single client path against the root and returns the
// qualified, normalized form. Empty / storage-root paths resolve to the root
// itself so a "list root" request opens the confined folder.
func (r Root) enforce(p string) (string, error) {
	a, rel := split(p)
	if a == "" {
		a = r.Adapter // client omitted the adapter — assume the confined one
	}
	if a != r.Adapter {
		return "", ErrOutOfRoot
	}
	if rel == "" {
		rel = r.Rel
	}
	target := Root{Adapter: a, Rel: rel}
	if !r.contains(target) {
		return "", ErrOutOfRoot
	}
	if rel == "" {
		return a + "://", nil
	}
	return a + "://" + rel, nil
}

// EnforcePath is the exported form of enforce: validate/normalize a client path
// against the root, returning the qualified path or ErrOutOfRoot. The AI
// surface (aiOps) uses it directly since it bypasses Middleware.
func (r Root) EnforcePath(p string) (string, error) { return r.enforce(p) }

// Middleware enforces the effective root on every /api/files request. Mount it
// AFTER the auth middleware so the token is on the context.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root, confined, err := FromRequest(r)
		if err != nil {
			forbid(w, err)
			return
		}
		if !confined {
			next.ServeHTTP(w, r)
			return
		}

		// 1) query ?path=
		q := r.URL.Query()
		if q.Has("path") {
			np, err := root.enforce(q.Get("path"))
			if err != nil {
				forbid(w, err)
				return
			}
			q.Set("path", np)
			r.URL.RawQuery = q.Encode()
		}

		// 2) JSON body path fields (move/delete/copy/share/upload/...)
		if r.Body != nil && hasJSON(r) {
			body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
			_ = r.Body.Close()
			nb, err := confineBody(root, body)
			if err != nil {
				forbid(w, err)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(nb))
			r.ContentLength = int64(len(nb))
			r.Header.Set("Content-Length", itoa(len(nb)))
		}

		// Stash the root so id-based handlers (trash) can filter by it.
		next.ServeHTTP(w, r.WithContext(withRoot(r.Context(), root)))
	})
}

func confineBody(root Root, body []byte) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return body, nil
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return body, nil // not a JSON object — nothing to confine here
	}
	for _, key := range []string{"path", "item", "target", "sourceDir"} {
		if v, ok := m[key].(string); ok && v != "" {
			np, err := root.enforce(v)
			if err != nil {
				return nil, err
			}
			m[key] = np
		}
	}
	if src, ok := m["source"].([]any); ok {
		for i, s := range src {
			if ss, ok := s.(string); ok {
				np, err := root.enforce(ss)
				if err != nil {
					return nil, err
				}
				src[i] = np
			}
		}
	}
	if items, ok := m["items"].([]any); ok {
		for _, it := range items {
			if im, ok := it.(map[string]any); ok {
				if p, ok := im["path"].(string); ok {
					np, err := root.enforce(p)
					if err != nil {
						return nil, err
					}
					im["path"] = np
				}
			}
		}
	}
	return json.Marshal(m)
}

func hasJSON(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return false
	}
	return strings.Contains(r.Header.Get("Content-Type"), "json")
}

func forbid(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":"` + err.Error() + `"}`))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// ── ctx plumbing so id-based handlers (trash list/restore) can confine too ──

type ctxKey struct{}

func withRoot(ctx context.Context, r Root) context.Context {
	return context.WithValue(ctx, ctxKey{}, r)
}

// RootFrom returns the confinement root stashed by Middleware, if any.
func RootFrom(ctx context.Context) (Root, bool) {
	v, ok := ctx.Value(ctxKey{}).(Root)
	return v, ok
}

// Within reports whether a storage-relative path (no adapter prefix) is inside
// the root for the given adapter name.
func (r Root) Within(adapter, rel string) bool {
	_, c := split(adapter + "://" + rel)
	return r.contains(Root{Adapter: adapter, Rel: c})
}
