package handlers

// writePublicJSON is the ONE door to a `public` /api answer (api.APINoStore
// makes every other one `no-store`). These two tests keep that door narrow:
// who may call it, and what it says when a person is behind the request.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

// The four instance-identity answers, by the method that writes each one.
// ⚠ A new caller is a decision that its answer is the same for every visitor
// of a host (and it must be mounted outside every auth chain) — see
// public_cache.go. It is never a way to make this test pass.
var publicIdentityWriters = []string{
	"(*Appearance).Get",     // GET /api/appearance
	"(*Branding).Get",       // GET /api/branding
	"(*PublicAPI).Branding", // GET /api/public/branding
	"(*PublicAPI).UILocale", // GET /api/public/ui-locales/{code}
}

func TestWritePublicJSON_OnlyTheFourIdentityAnswers(t *testing.T) {
	dir := "."
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	fset := token.NewFileSet()
	var callers []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		require.NoError(t, perr, e.Name())
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == "writePublicJSON" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "writePublicJSON" {
					callers = append(callers, funcName(fn))
				}
				return true
			})
		}
	}
	sort.Strings(callers)
	require.Equal(t, publicIdentityWriters, callers,
		"writePublicJSON answers `public` — kept by every shared cache in front of filex. A CDN rule that "+
			"cached everything once served one administrator's /api/auth/me to every visitor (PR #41). "+
			"Only an answer that is the same for everybody may use it.")
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	switch rt := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := rt.X.(*ast.Ident); ok {
			return "(*" + id.Name + ")." + fn.Name.Name
		}
	case *ast.Ident:
		return "(" + rt.Name + ")." + fn.Name.Name
	}
	return fn.Name.Name
}

// A request somebody authenticated is never answered `public`, whatever
// handler wrote it: the four identity routes are mounted outside every auth
// chain, so this only ever fires for a caller that should not have used the
// helper — and then the answer stays out of shared caches instead of leaking.
func TestWritePublicJSON_APrincipalMakesItPrivate(t *testing.T) {
	anon := httptest.NewRecorder()
	writePublicJSON(anon, httptest.NewRequest(http.MethodGet, "/api/branding", nil), map[string]string{"name": "filex"})
	require.Equal(t, "public, no-cache", anon.Header().Get("Cache-Control"))

	req := httptest.NewRequest(http.MethodGet, "/api/branding", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &model.User{ID: 7, Email: "ayse@example.test"}))
	rec := httptest.NewRecorder()
	writePublicJSON(rec, req, map[string]string{"name": "filex"})
	require.Equal(t, "private, no-cache", rec.Header().Get("Cache-Control"))
	require.Equal(t, anon.Header().Get("ETag"), rec.Header().Get("ETag"), "same bytes, only the audience changed")
}
