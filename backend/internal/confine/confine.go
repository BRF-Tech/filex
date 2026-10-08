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
//
// The body is read as JSON whatever its Content-Type, because that is how the
// handlers read it; only the routes whose body is the bytes of a file
// (rawBodyRoutes) are passed on unread.
package confine

import (
	"bufio"
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

// ErrBadBody is returned for a body that begins as a JSON object and does not
// parse as one: what this layer cannot read it cannot hold to the root.
var ErrBadBody = errors.New("bad json")

// ErrBodyTooLarge is returned for a body that begins as a JSON object and is
// larger than maxBody: past the cut its keys would go unseen.
var ErrBodyTooLarge = errors.New("request body too large")

// maxBody is the most of a request body Middleware reads to hold it to the
// root.
const maxBody = 8 << 20

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
	adapter, rel = splitRaw(raw)
	return adapter, cleanRel(rel)
}

// splitRaw is split without the cleaning: the rel as the client wrote it.
func splitRaw(raw string) (adapter, rel string) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "://"); i >= 0 {
		return raw[:i], raw[i+3:]
	}
	return "", raw
}

func cleanRel(rel string) string {
	rel = strings.Trim(path.Clean("/"+rel), "/")
	if rel == "." {
		rel = ""
	}
	return rel
}

// heldBothWays reports whether the client path p, on adapter, is inside r
// when a backslash in it is read as a separator too.
//
// ⚠⚠ The checks in this package clean a path the POSIX way, where `\` is part
// of a name; a storage on a Windows host reads it as a separator (the local
// driver folds the host's own separators before it resolves). So
// `kutu/x\..\..\disari` was inside a root of `kutu` to every check here and
// `disari` to the driver that served it (GHSA-8gvc-6w52-6c7j). Folding alone
// would be wrong the other way: on Linux `kutu\gizli` is a sibling of `kutu`,
// not inside it. Both readings must therefore be inside - the same answer on
// every host, whatever its separator.
func (r Root) heldBothWays(adapter, p string) bool {
	_, rel := splitRaw(p)
	if !strings.ContainsRune(rel, '\\') {
		return true
	}
	return r.contains(Root{Adapter: adapter, Rel: cleanRel(strings.ReplaceAll(rel, `\`, "/"))})
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
	if haveToken && (!tokenRoot.contains(hdrRoot) || !tokenRoot.heldBothWays(hdrRoot.Adapter, hdr)) {
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
	if !r.contains(target) || !r.heldBothWays(a, p) {
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

		// 2) body path fields (move/delete/copy/share/upload/...)
		if r.Body != nil && r.Body != http.NoBody && !rawBody(r) {
			if err := holdRequestBody(root, r); err != nil {
				refuse(w, err)
				return
			}
		}

		// Stash the root so id-based handlers (trash) can filter by it.
		next.ServeHTTP(w, r.WithContext(withRoot(r.Context(), root)))
	})
}

// holdRequestBody holds r's body to the root the way the handlers will read
// it: as JSON, whatever its Content-Type and method.
//
// ⚠⚠ Up to 0.52 a body was read only when its Content-Type contained "json",
// while the handlers decode theirs with json.Decoder under any label: the
// same object sent as text/plain, or with no Content-Type, reached them as the
// client wrote it (GHSA-8gvc-6w52-6c7j). Every handler now asks the root
// itself too; this layer is the one that catches the handler that forgets.
//
//   - A body whose first value is not an object (a multipart form, a list,
//     nothing at all) names no path a handler reads, and is passed on as it
//     was sent, at any size: only its leading white space is read here.
//   - An object is read whole, held key by key (confineBody) and passed on
//     re-encoded.
//   - An object larger than maxBody is refused (ErrBodyTooLarge), and so is one
//     that does not parse (ErrBadBody). Passing either on would hand the
//     handler keys this layer never saw.
func holdRequestBody(root Root, r *http.Request) error {
	orig := r.Body
	br := bufio.NewReader(orig)
	var lead []byte
	for {
		b, err := br.ReadByte()
		if err == io.EOF {
			r.Body = &replay{Reader: bytes.NewReader(lead), Closer: orig}
			return nil
		}
		if err != nil {
			return ErrBadBody
		}
		lead = append(lead, b)
		if !isJSONSpace(b) {
			break
		}
		if len(lead) > maxBody {
			return ErrBodyTooLarge
		}
	}
	if lead[len(lead)-1] != '{' {
		r.Body = &replay{Reader: io.MultiReader(bytes.NewReader(lead), br), Closer: orig}
		return nil
	}
	rest, err := io.ReadAll(io.LimitReader(br, int64(maxBody+1-len(lead))))
	if err != nil {
		return ErrBadBody
	}
	body := append(lead, rest...)
	if len(body) > maxBody {
		return ErrBodyTooLarge
	}
	_ = orig.Close()
	nb, err := confineBody(root, body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(nb))
	r.ContentLength = int64(len(nb))
	r.Header.Set("Content-Length", itoa(len(nb)))
	return nil
}

// replay is a body whose first bytes were read here: they are read again,
// then the rest, and closing it closes the request's own body.
type replay struct {
	io.Reader
	io.Closer
}

// isJSONSpace is white space as encoding/json skips it before a value.
func isJSONSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// beginsObject reports whether the first JSON value in body is an object.
func beginsObject(body []byte) bool {
	for _, b := range body {
		if !isJSONSpace(b) {
			return b == '{'
		}
	}
	return false
}

// rawBodyRoutes are the requests whose body is the bytes of a file, passed on
// unread: not cut at maxBody, not re-encoded, whatever the Content-Type says.
// A file's bytes may well begin with `{` (a .json saved) and be of any size;
// holding them as a request object would rewrite or refuse the file. Their
// paths come in the query, which is still held. A "*" is one path segment,
// never empty.
//
//   - PUT /api/files/upload/{id} - one part of a staged upload
//     (handlers/upload_staged.go).
//   - PUT /api/files/plugins/ui/{plugin}/{view}/save - an app's interface
//     saving a file, whole or a chunk of it (handlers/app_ui.go,
//     app_ui_chunks.go).
//
// ⚠⚠ Exempt a route here only when its handler reads the body as bytes and
// never as JSON: a route on this list is a route this layer does not read. A
// multipart form needs no entry - it begins with its boundary, never with an
// object, and passes untouched - and is deliberately not exempted by its
// Content-Type: the label is the client's to choose, and a JSON object
// labelled multipart is still read as JSON by a handler that decodes it.
var rawBodyRoutes = []struct {
	method string
	path   []string
}{
	{http.MethodPut, []string{"api", "files", "upload", "*"}},
	{http.MethodPut, []string{"api", "files", "plugins", "ui", "*", "*", "save"}},
	// A vault's pack and index file (handlers/e2e_vault.go): up to 16 and
	// 64 MiB of ciphertext, the vault folder in `?path=`.
	{http.MethodPut, []string{"api", "files", "e2e", "vault", "pack"}},
	{http.MethodPut, []string{"api", "files", "e2e", "vault", "index"}},
}

// rawBody reports whether r is one of rawBodyRoutes. The path is the one chi
// routes on (the escaped form when the request has one), so a request is
// exempt only when it reaches the very route that is listed.
func rawBody(r *http.Request) bool {
	p := r.URL.RawPath
	if p == "" {
		p = r.URL.Path
	}
	if !strings.HasPrefix(p, "/") {
		return false
	}
	segs := strings.Split(p[1:], "/")
	for _, route := range rawBodyRoutes {
		if r.Method != route.method || len(segs) != len(route.path) {
			continue
		}
		match := true
		for i, want := range route.path {
			if segs[i] == "" || (want != "*" && segs[i] != want) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
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
	return r.contains(Root{Adapter: a, Rel: rel}) && r.heldBothWays(a, p)
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

// confineBody holds a request body to the root: a body whose first value is
// not an object is returned as it is, an object is held key by key and
// returned re-encoded, and an object that does not parse is ErrBadBody.
func confineBody(root Root, body []byte) ([]byte, error) {
	if !beginsObject(body) {
		return body, nil // no object here: a handler's decoder reads no path from it
	}
	var m map[string]any
	// Read it the way the handlers do — json.Decoder, which takes the FIRST
	// JSON value and ignores anything after it. json.Unmarshal rejects a tail,
	// so it left `{…} trailing` untouched while the handler read the object and
	// used its (unconfined) path. Decoding only the first object and
	// re-marshalling it drops the tail, which the handler would have ignored.
	//
	// ⚠⚠ UseNumber, and an object that does not decode is refused, never
	// passed on: up to 0.52 a number no float64 holds (`"n":1e999`) failed this
	// decoding, the body went on untouched, and the handler's struct - which
	// has no field for that number and so never converts it - decoded the
	// `path` beside it, outside the root, in a body labelled JSON too.
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if dec.Decode(&m) != nil {
		return nil, ErrBadBody
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
	// Written back as it was read: numbers as their literals (no float64
	// rounding of an id past 2^53) and text without HTML escaping.
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, ErrBadBody
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

func forbid(w http.ResponseWriter, err error) {
	answer(w, http.StatusForbidden, err)
}

// refuse writes this layer's answer to err: 413 for a body too large to hold,
// 400 for one that does not parse, 403 for a path outside the root.
func refuse(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrBodyTooLarge):
		answer(w, http.StatusRequestEntityTooLarge, ErrBodyTooLarge)
	case errors.Is(err, ErrBadBody):
		answer(w, http.StatusBadRequest, ErrBadBody)
	default:
		forbid(w, err)
	}
}

func answer(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
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

// CallerRoot is the confinement root of the call on ctx: the one Middleware
// stashed (a token's `root:` narrowed by X-Filex-Root) or, where a request
// did not pass through Middleware (the /api/ai surfaces), the token's own
// `root:` scope (RootFromToken). ok=false for an unconfined caller.
func CallerRoot(ctx context.Context) (Root, bool) {
	if root, ok := RootFrom(ctx); ok {
		return root, true
	}
	return RootFromToken(ctx)
}

// String is the root spelled `<adapter>://<rel>`, which ParseRoot reads back
// as the same root.
func (r Root) String() string { return r.Adapter + "://" + r.Rel }

// HoldBody holds a request body a handler reads as JSON to the root exactly as
// Middleware holds one: the same keys, rewritten or checked the same way,
// refused with ErrOutOfRoot, and an object that does not parse refused with
// ErrBadBody. A body Middleware has already held passes through unchanged.
func HoldBody(r Root, body []byte) ([]byte, error) { return confineBody(r, body) }

// Refuse writes Middleware's own answer to a path outside the root, so a
// handler that refuses one answers byte for byte what Middleware answers.
func Refuse(w http.ResponseWriter) { forbid(w, ErrOutOfRoot) }

// RefuseFor writes Middleware's own answer to an error HoldBody returned:
// 403 for a path outside the root, 400 for a body that does not parse.
func RefuseFor(w http.ResponseWriter, err error) { refuse(w, err) }

// Within reports whether a storage-relative path (no adapter prefix) is inside
// the root for the given adapter name.
func (r Root) Within(adapter, rel string) bool {
	_, c := split(adapter + "://" + rel)
	return r.contains(Root{Adapter: adapter, Rel: c}) && r.heldBothWays(adapter, adapter+"://"+rel)
}
