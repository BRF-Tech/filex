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
// path-bearing field of the request (query `?path=` and the JSON body keys
// BodyPathKeys lists, matched case-insensitively as encoding/json matches
// them). Anything outside the root is rejected 403; a root/empty path is
// rewritten to the confined folder so listings open there. It covers an
// endpoint only through the keys it knows: a guard test holds every handler
// request body to that list (api/handlers/confine_keys_test.go).
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

// ParseRoot exposes parseRoot to the protocol servers, which read a
// confinement out of an API token's `root:` scope themselves — they
// authenticate outside the HTTP chain, so the middleware in this package never
// runs for them. Returns ok=false for an empty or storage-less spec.
func ParseRoot(raw string) (Root, bool) { return parseRoot(raw) }

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

// The JSON body keys Middleware confines.
//
// ⚠⚠ A path a handler reads from a key that is NOT here is not confined by
// this layer at all, and a `root:` token reaches anything in the storage
// through it (lesson #543: `paths`, 2026-09-25; `sources`, `dest` and
// `files[].source` of the archive routes, 2026-10-01). The guard test
// api/handlers/confine_keys_test.go walks every request body the handlers
// decode and goes red for a path-like key that is missing here.
var (
	// rewrittenKeys and rewrittenListKeys are rewritten to the qualified
	// form, an empty path becoming the root itself - so a listing opens the
	// confined folder.
	rewrittenKeys = []string{"path", "item", "target", "sourceDir"}
	// ⚠ `paths` too: the app doors (run, view events) and the selection
	// archive take their files as a `paths` array, and before it was listed
	// here a root-confined token reached any file of the storage through them
	// (2026-09-25). The handlers check the root as well (rootAllows); this is
	// the layer that catches a door that forgets to.
	rewrittenListKeys = []string{"source", "paths"}
	// rewrittenItemKeys: a list of objects whose field is a path.
	rewrittenItemKeys = map[string]string{"items": "path"}

	// checkedKeys, checkedListKeys and checkedItemKeys are only CHECKED: the
	// value must lie inside the root as the handler reads it (holds) and is
	// passed on unchanged. A rewrite would lose what these spellings mean to
	// their handler: the trailing slash of the operations queue's `dest`
	// ("copy INTO this folder"), a `/` that is the storage root, a path
	// relative to the storage the body names by `storage_id`.
	checkedKeys     = []string{"dest", "target_dir"}
	checkedListKeys = []string{"sources"}
	checkedItemKeys = map[string]string{"files": "source"}
)

// BodyPathKeys lists every JSON body key Middleware confines, spelled "key"
// (a path), "key[]" (a list of paths) and "key[].field" (a list of objects
// whose field is a path) - what the handlers' guard test holds their request
// bodies to.
func BodyPathKeys() []string {
	var out []string
	out = append(out, rewrittenKeys...)
	out = append(out, checkedKeys...)
	for _, k := range append(append([]string{}, rewrittenListKeys...), checkedListKeys...) {
		out = append(out, k+"[]")
	}
	for k, f := range rewrittenItemKeys {
		out = append(out, k+"[]."+f)
	}
	for k, f := range checkedItemKeys {
		out = append(out, k+"[]."+f)
	}
	return out
}

// holds reports whether the client path p, read the way a handler reads it,
// lies inside the root: a bare path is on the confined storage, and an empty
// one (or `/`) is that storage's ROOT - not the confinement root enforce
// turns it into.
func (r Root) holds(p string) bool {
	a, rel := split(p)
	if a == "" {
		a = r.Adapter
	}
	return r.contains(Root{Adapter: a, Rel: rel})
}

// oneOf answers which of keys k is, compared the way encoding/json compares
// an object's key with a struct field's tag: case-insensitively (Unicode
// simple folding - strings.EqualFold folds exactly as json does). "" for none.
//
// ⚠⚠ An exact comparison was a bypass of this whole layer: `{"PATH": …}` is
// not the key "path" to a map, and IS the field tagged `json:"path"` to the
// handler that decodes the body - so the path went through unchecked and the
// handler used it (2026-10-01, save-text wrote outside a `root:` token's
// folder that way).
func oneOf(k string, keys []string) string {
	for _, want := range keys {
		if strings.EqualFold(k, want) {
			return want
		}
	}
	return ""
}

func confineBody(root Root, body []byte) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return body, nil
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return body, nil // not a JSON object — nothing to confine here
	}
	itemKeys := func(km map[string]string) []string {
		out := make([]string, 0, len(km))
		for k := range km {
			out = append(out, k)
		}
		return out
	}
	for key, val := range m {
		switch {
		case oneOf(key, rewrittenKeys) != "":
			if v, ok := val.(string); ok && v != "" {
				np, err := root.enforce(v)
				if err != nil {
					return nil, err
				}
				m[key] = np
			}
		case oneOf(key, rewrittenListKeys) != "":
			if src, ok := val.([]any); ok {
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
		case oneOf(key, checkedKeys) != "":
			if v, ok := val.(string); ok && v != "" && !root.holds(v) {
				return nil, ErrOutOfRoot
			}
		case oneOf(key, checkedListKeys) != "":
			if src, ok := val.([]any); ok {
				for _, s := range src {
					if ss, ok := s.(string); ok && ss != "" && !root.holds(ss) {
						return nil, ErrOutOfRoot
					}
				}
			}
		case oneOf(key, itemKeys(rewrittenItemKeys)) != "":
			field := rewrittenItemKeys[oneOf(key, itemKeys(rewrittenItemKeys))]
			if items, ok := val.([]any); ok {
				for _, it := range items {
					im, ok := it.(map[string]any)
					if !ok {
						continue
					}
					for fk, fv := range im {
						if p, ok := fv.(string); ok && strings.EqualFold(fk, field) {
							np, err := root.enforce(p)
							if err != nil {
								return nil, err
							}
							im[fk] = np
						}
					}
				}
			}
		case oneOf(key, itemKeys(checkedItemKeys)) != "":
			field := checkedItemKeys[oneOf(key, itemKeys(checkedItemKeys))]
			if items, ok := val.([]any); ok {
				for _, it := range items {
					im, ok := it.(map[string]any)
					if !ok {
						continue
					}
					for fk, fv := range im {
						if p, ok := fv.(string); ok && p != "" && strings.EqualFold(fk, field) && !root.holds(p) {
							return nil, ErrOutOfRoot
						}
					}
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
