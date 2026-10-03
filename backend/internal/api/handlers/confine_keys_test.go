package handlers_test

// Every path a handler reads out of a JSON request body is one
// confine.Middleware knows (lesson #543).
//
// The middleware confines a `root:` token by the keys it knows - it rewrites
// `path`, `item`, `target`, `sourceDir`, `source[]`, `paths[]`,
// `items[].path` and checks `dest`, `target_dir`, `sources[]`,
// `files[].source` (confine.BodyPathKeys) - and a path under any other key is
// not confined by it at all. That is how `paths` (2026-09-25) and then the
// archive routes' `sources`, `dest` and `files[].source` (2026-10-01: a
// `root:` token packed files from outside its folder into an archive inside
// it) went through. A handler's own rootAllows is the second line; this test
// keeps the first one whole: it reads the handlers' source, finds every body
// they decode, and goes red for a path-like key the middleware does not know,
// unless the key is listed below with the reason it is not a path, or not one
// a `root:` token reaches through the middleware.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/confine"
)

// pathishKey is a JSON key that names a place in a storage: the spellings the
// handlers use for one. Compared on the last segment, case-insensitively.
var pathishKey = regexp.MustCompile(`(?i)^(?:path|paths|item|items|source|sources|sourcedir|dest|destination|target|target_dir|dir|folder|from|to)$|(?:_path|_dir|_folder)$`)

// notConfinedByTheMiddleware are decoded keys that look like a path and are
// not one the middleware has to know, each with the reason. ⚠ A new entry
// here is a decision: a path a `root:` token can send under it reaches its
// handler unconfined unless the handler checks the root itself.
var notConfinedByTheMiddleware = map[string]string{
	"Settings.SMTPTest.req.to": "an e-mail address (Admin, Settings, the SMTP test), not a storage path",
}

// decodedBody is one request body a handler decodes: its type (or the
// function, for an inline struct) and its JSON keys, spelled as
// confine.BodyPathKeys spells them.
type decodedBody struct {
	name string
	keys []string
}

// handlerBodies parses the handlers' non-test source and returns every body
// decoded by a `….Decode(&x)` call whose x is a struct (named, or declared
// inline in the function) - the way the /api/files handlers read a request
// body (json.NewDecoder, jsonNewDecoder). ⚠ A body read another way (a helper
// that hides the Decode, json.Unmarshal of the raw bytes) is not seen: keep
// the /api/files handlers on a visible Decode.
func handlerBodies(t *testing.T) []decodedBody {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	structs := map[string]*ast.StructType{}
	var parsed []*ast.File
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		af, err := parser.ParseFile(fset, f, src, 0)
		require.NoError(t, err, f)
		parsed = append(parsed, af)
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, s := range gd.Specs {
				ts := s.(*ast.TypeSpec)
				if st, ok := ts.Type.(*ast.StructType); ok {
					structs[ts.Name.Name] = st
				}
			}
		}
	}

	seen := map[string]bool{}
	var out []decodedBody
	add := func(name string, st *ast.StructType) {
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, decodedBody{name: name, keys: structKeys(st, structs, "", 0)})
	}
	for _, af := range parsed {
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			fname := fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) == 1 {
				fname = typeName(fd.Recv.List[0].Type) + "." + fname
			}
			// The type of each local the function declares, by name.
			locals := map[string]ast.Expr{}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.ValueSpec:
					if x.Type != nil {
						for _, id := range x.Names {
							locals[id.Name] = x.Type
						}
					}
				case *ast.AssignStmt:
					for i, lhs := range x.Lhs {
						id, ok := lhs.(*ast.Ident)
						if !ok || i >= len(x.Rhs) {
							continue
						}
						rhs := x.Rhs[i]
						if u, ok := rhs.(*ast.UnaryExpr); ok && u.Op == token.AND {
							rhs = u.X
						}
						if cl, ok := rhs.(*ast.CompositeLit); ok && cl.Type != nil {
							locals[id.Name] = cl.Type
						}
					}
				case *ast.CallExpr:
					sel, ok := x.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Decode" || len(x.Args) != 1 {
						return true
					}
					arg := x.Args[0]
					if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.AND {
						arg = u.X
					}
					id, ok := arg.(*ast.Ident)
					if !ok {
						return true
					}
					switch tx := locals[id.Name].(type) {
					case *ast.Ident:
						if st, ok := structs[tx.Name]; ok {
							add(tx.Name, st)
						}
					case *ast.StructType:
						add(fname+"."+id.Name, tx)
					}
				}
				return true
			})
		}
	}
	return out
}

// typeName is the name of a receiver type (`*Archive` → Archive).
func typeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return typeName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return "?"
}

// structKeys lists st's JSON keys - a string or *string field as "key", a
// []string as "key[]", a list of structs as "key[].field", a nested struct
// as "key.field", an embedded struct's keys as its own (encoding/json
// promotes them). Other fields (numbers, booleans, maps, raw JSON) carry no
// path and are left out.
func structKeys(st *ast.StructType, structs map[string]*ast.StructType, prefix string, depth int) []string {
	if depth > 4 {
		return nil
	}
	var out []string
	for _, f := range st.Fields.List {
		key := ""
		if f.Tag != nil {
			tag, _ := strconv.Unquote(f.Tag.Value)
			key = strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
			if key == "-" {
				continue
			}
		}
		if len(f.Names) == 0 {
			// Embedded: its fields are this struct's.
			if id, ok := unstar(f.Type).(*ast.Ident); ok && key == "" {
				if inner, ok := structs[id.Name]; ok {
					out = append(out, structKeys(inner, structs, prefix, depth+1)...)
				}
			}
			continue
		}
		for _, n := range f.Names {
			if !n.IsExported() {
				continue
			}
			k := key
			if k == "" {
				k = n.Name
			}
			switch ft := unstar(f.Type).(type) {
			case *ast.Ident:
				if ft.Name == "string" {
					out = append(out, prefix+k)
				} else if inner, ok := structs[ft.Name]; ok {
					out = append(out, structKeys(inner, structs, prefix+k+".", depth+1)...)
				}
			case *ast.StructType:
				out = append(out, structKeys(ft, structs, prefix+k+".", depth+1)...)
			case *ast.ArrayType:
				switch el := unstar(ft.Elt).(type) {
				case *ast.Ident:
					if el.Name == "string" {
						out = append(out, prefix+k+"[]")
					} else if inner, ok := structs[el.Name]; ok {
						out = append(out, structKeys(inner, structs, prefix+k+"[].", depth+1)...)
					}
				case *ast.StructType:
					out = append(out, structKeys(el, structs, prefix+k+"[].", depth+1)...)
				}
			}
		}
	}
	return out
}

func unstar(e ast.Expr) ast.Expr {
	if s, ok := e.(*ast.StarExpr); ok {
		return s.X
	}
	return e
}

// lastSegment is a key's own name: `files[].source` → source.
func lastSegment(k string) string {
	k = strings.TrimSuffix(k, "[]")
	if i := strings.LastIndexAny(k, "."); i >= 0 {
		k = k[i+1:]
	}
	return strings.TrimSuffix(k, "[]")
}

func TestConfineKeys_EveryDecodedPathIsOneTheMiddlewareKnows(t *testing.T) {
	known := map[string]bool{}
	for _, k := range confine.BodyPathKeys() {
		known[strings.ToLower(k)] = true
	}
	bodies := handlerBodies(t)
	// Anti-vacuity: the handlers decode well over a hundred bodies; a small
	// number means the parse broke, not that the product shrank.
	require.Greater(t, len(bodies), 60, "found %d decoded bodies", len(bodies))
	names := map[string]bool{}
	for _, b := range bodies {
		names[b.name] = true
	}
	for _, must := range []string{"archiveCreateRequest", "archiveRequest", "opsRequest", "perVerbReq", "runRequest"} {
		require.True(t, names[must], "the scan no longer sees %s - it broke", must)
	}

	var missing []string
	used := map[string]bool{}
	for _, b := range bodies {
		for _, k := range b.keys {
			if !pathishKey.MatchString(lastSegment(k)) {
				continue
			}
			if known[strings.ToLower(k)] {
				continue
			}
			id := b.name + "." + k
			if _, ok := notConfinedByTheMiddleware[id]; ok {
				used[id] = true
				continue
			}
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing,
		"these request bodies carry a path under a key confine.Middleware does not know: a `root:` token reaches "+
			"anything in the storage through them. Teach the key to the middleware (confine.go) - or, when it is not "+
			"a path or not on a confined mount, list it in notConfinedByTheMiddleware with the reason")
	for id := range notConfinedByTheMiddleware {
		require.True(t, used[id], "notConfinedByTheMiddleware lists %s, which no decoded body has any more - remove it", id)
	}
}
