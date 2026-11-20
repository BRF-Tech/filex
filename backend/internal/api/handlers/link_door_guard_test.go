package handlers_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLinkAt_IsTheOnlyPublicLookup holds the shape of the fix for
// GHSA-rcrp-r83c-g8hv: a public door resolves a link token through linkAt,
// which decides the link's kind for that door (model.Share.Opens), and never
// through Store.GetShareByToken directly. The /s/ page did, asked only
// whether the link was an app's page, and a file request's token opened
// there listed and zipped its folder; the doors beside it each asked their
// own question and happened to ask the right one.
//
// The other callers are signed-in people acting on a link they manage (the
// AI surface's revoke, the share mail, an uploader's deletion link), never a
// visitor holding a token. A new caller has to be one of those, named here
// with the reason, or go through linkAt.
func TestLinkAt_IsTheOnlyPublicLookup(t *testing.T) {
	allowed := map[string]string{
		"linkAt":                    "the public doors' one lookup",
		"(*aiOps).RevokeShare":      "a signed-in caller revoking its own link (shareRevokeRefusal)",
		"(*Grants).mailableShare":   "a signed-in caller mailing a link it manages (shareRevokeRefusal)",
		"(uploadCore).mintDeletion": "an uploader's own new upload, tying its deletion link to the share it made",
	}
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	callers := map[string][]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err, name)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "GetShareByToken" {
					key := lookupCallerName(fn)
					callers[key] = append(callers[key], fset.Position(call.Pos()).String())
				}
				return true
			})
		}
	}
	var stray []string
	for fn, at := range callers {
		if _, ok := allowed[fn]; !ok {
			stray = append(stray, fn+" at "+strings.Join(at, ", "))
		}
	}
	sort.Strings(stray)
	assert.Empty(t, stray, "these resolve a link token without linkAt, so nothing decides whether the link's kind may come in at that door (a file request at a read door is GHSA-rcrp-r83c-g8hv): call linkAt(ctx, store, token, model.DoorRead|DoorUpload), or add the function to the allowed list with the reason no visitor reaches it")
	for fn := range allowed {
		assert.Contains(t, callers, fn, "%s no longer looks a token up: take it off the allowed list", fn)
	}
}

func lookupCallerName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	switch t := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return "(*" + id.Name + ")." + fn.Name.Name
		}
	case *ast.Ident:
		return "(" + t.Name + ")." + fn.Name.Name
	}
	return fn.Name.Name
}
