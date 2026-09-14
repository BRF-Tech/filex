package db_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	sqlitedrv "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"
)

// TestSharedStoreStatementsPrepareOnMySQL asks a real MySQL server to PREPARE
// every SQL statement in the SQLite driver — the Store MySQL borrows wholesale
// — exactly as MySQL receives it, upserts rewritten.
//
// A server-side PREPARE parses the statement and resolves every table, column
// and function without running it. That is the check the write-path tests
// cannot be: they only reach the methods somebody thought to call. Statements
// nothing called shipped as syntax errors on MySQL through v0.40.0 (issue #23)
// — `MAX(0, x)` in quota accounting and `LIMIT -1` in the version prune both
// failed silently — and this gate reports both. Any SQLite-only function,
// clause or column name a future change adds to that file fails here instead.
//
// ⚠ A statement assembled from local variables at run time cannot be evaluated
// from the source and is skipped; those are listed in the verbose output. The
// third defect from #23, `datetime('now', …)` in the sync history, sat in one
// of those, which is why TestSyncHistoryOnEveryEngine exists beside this gate.
// The floor below keeps a broken extractor from passing by preparing nothing.
func TestSharedStoreStatementsPrepareOnMySQL(t *testing.T) {
	var mysqlEngine engine
	for _, e := range engines() {
		if e.name == "mysql" {
			mysqlEngine = e
		}
	}
	sqlDB, _ := openMigrated(t, mysqlEngine)

	stmts, skipped := extractStoreSQL(t, filepath.Join("drivers", "sqlite"))
	for _, s := range skipped {
		t.Logf("not evaluable from source, skipped: %s", s)
	}
	require.GreaterOrEqual(t, len(stmts), 200,
		"only %d statements were extracted from the SQLite store — the extractor has stopped understanding the file", len(stmts))

	ctx := context.Background()
	for _, s := range stmts {
		q := s.sql
		if s.upsert {
			q = sqlitedrv.UpsertForMySQL(q)
		}
		prepared, err := sqlDB.PrepareContext(ctx, q)
		if err != nil {
			t.Errorf("%s (%s) does not parse on MySQL: %v\n%s", s.at, s.fn, err, strings.Join(strings.Fields(q), " "))
			continue
		}
		_ = prepared.Close()
	}
}

type storeStatement struct {
	at, fn, sql string
	upsert      bool
}

// sqlArg is the position of the SQL string in each database/sql call.
var sqlArg = map[string]int{
	"ExecContext": 1, "QueryContext": 1, "QueryRowContext": 1, "PrepareContext": 1,
	"Exec": 0, "Query": 0, "QueryRow": 0, "Prepare": 0,
}

var looksLikeSQL = regexp.MustCompile(`(?is)^\s*(SELECT|INSERT|UPDATE|DELETE|WITH|REPLACE)\b`)

// extractStoreSQL evaluates the SQL argument of every database/sql call in the
// package's non-test files: string literals, `+`, package-level constants and
// variables, fmt.Sprintf over those, zero-argument functions that return one,
// and s.upsert(...). Anything else makes the statement unevaluable.
func extractStoreSQL(t *testing.T, dir string) (stmts []storeStatement, skipped []string) {
	t.Helper()

	fset := token.NewFileSet()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	require.NoError(t, err)
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		require.NoError(t, err)
		files = append(files, f)
	}
	require.NotEmpty(t, files, "no Go files under %s", dir)

	ev := &sqlEvaluator{named: map[string]ast.Expr{}, funcs: map[string]ast.Expr{}}
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				if d.Tok != token.CONST && d.Tok != token.VAR {
					continue
				}
				for _, spec := range d.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if i < len(vs.Values) {
							ev.named[name.Name] = vs.Values[i]
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv != nil || d.Type.Params.NumFields() != 0 || d.Body == nil || len(d.Body.List) != 1 {
					continue
				}
				if ret, ok := d.Body.List[0].(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
					ev.funcs[d.Name.Name] = ret.Results[0]
				}
			}
		}
	}

	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				idx, ok := sqlArg[sel.Sel.Name]
				if !ok || len(call.Args) <= idx {
					return true
				}
				pos := fset.Position(call.Pos())
				at := fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line)
				ev.upsert = false
				q, ok := ev.eval(call.Args[idx], 0)
				if !ok {
					skipped = append(skipped, at+" "+fd.Name.Name)
					return true
				}
				if looksLikeSQL.MatchString(q) {
					stmts = append(stmts, storeStatement{at: at, fn: fd.Name.Name, sql: q, upsert: ev.upsert})
				}
				return true
			})
		}
	}
	return stmts, skipped
}

type sqlEvaluator struct {
	named  map[string]ast.Expr
	funcs  map[string]ast.Expr
	upsert bool
}

func (ev *sqlEvaluator) eval(e ast.Expr, depth int) (string, bool) {
	if depth > 16 {
		return "", false
	}
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return ev.eval(v.X, depth+1)
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, ok := ev.eval(v.X, depth+1)
		if !ok {
			return "", false
		}
		r, ok := ev.eval(v.Y, depth+1)
		return l + r, ok
	case *ast.Ident:
		if x, ok := ev.named[v.Name]; ok {
			return ev.eval(x, depth+1)
		}
		return "", false
	case *ast.CallExpr:
		switch fn := v.Fun.(type) {
		case *ast.SelectorExpr:
			if fn.Sel.Name == "upsert" && len(v.Args) == 1 {
				ev.upsert = true
				return ev.eval(v.Args[0], depth+1)
			}
			if pkg, ok := fn.X.(*ast.Ident); ok && pkg.Name == "fmt" && fn.Sel.Name == "Sprintf" && len(v.Args) > 0 {
				format, ok := ev.eval(v.Args[0], depth+1)
				if !ok {
					return "", false
				}
				args := make([]any, 0, len(v.Args)-1)
				for _, a := range v.Args[1:] {
					s, ok := ev.eval(a, depth+1)
					if !ok {
						return "", false
					}
					args = append(args, s)
				}
				return fmt.Sprintf(format, args...), true
			}
		case *ast.Ident:
			if body, ok := ev.funcs[fn.Name]; ok && len(v.Args) == 0 {
				return ev.eval(body, depth+1)
			}
		}
	}
	return "", false
}
