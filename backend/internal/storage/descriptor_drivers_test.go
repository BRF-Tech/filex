package storage_test

// Conformance tests for the driver config descriptors.
//
// The point of the whole descriptor mechanism is that a driver cannot add
// (or drop) a config key without every surface hearing about it. So this
// file parses each driver's source, collects the config keys its Init
// actually reads, and compares that set against what the descriptor
// declares — in BOTH directions. Add `cfg["region"]` to a driver without
// declaring it and this test fails; declare a field nothing reads and it
// fails too.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/storage"

	// Every registered driver must be linked in for Names() to be complete.
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/ftp"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/s3"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/sftp"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/smb"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/webdav"
)

// TestEveryRegisteredDriverHasADescriptor — a driver the admin UI can pick
// but whose config nobody can describe is exactly how three of the four
// offered drivers became uncreatable.
func TestEveryRegisteredDriverHasADescriptor(t *testing.T) {
	names := storage.Names()
	if len(names) < 5 {
		t.Fatalf("expected the five built-in drivers to be registered, got %v", names)
	}
	for _, name := range names {
		d, ok := storage.DescriptorFor(name)
		if !ok {
			t.Errorf("driver %q is registered but declares no descriptor "+
				"(add descriptor.go next to the driver)", name)
			continue
		}
		if d.Driver != name {
			t.Errorf("driver %q: descriptor names itself %q", name, d.Driver)
		}
		if d.Label == "" || d.I18nKey == "" {
			t.Errorf("driver %q: descriptor needs both a Label (English fallback) and an I18nKey", name)
		}
		if len(d.Fields) == 0 {
			t.Errorf("driver %q: descriptor declares no fields", name)
		}
		if _, has := d.RootField(); !has {
			t.Errorf("driver %q: no field flagged Root — ValidateNonRootPath would let it mount the backend root", name)
		}
		for _, f := range d.Fields {
			if f.Label == "" || f.I18nKey == "" {
				t.Errorf("driver %q field %q: needs both Label (English fallback) and I18nKey", name, f.Key)
			}
			if f.Help != "" && f.HelpI18nKey == "" {
				t.Errorf("driver %q field %q: help text without an i18n key would ship untranslated", name, f.Key)
			}
			if f.Secret && f.Type != storage.FieldPassword {
				t.Errorf("driver %q field %q: secret fields must render as %q", name, f.Key, storage.FieldPassword)
			}
			if f.Type == storage.FieldSelect && len(f.Options) == 0 {
				t.Errorf("driver %q field %q: select without options", name, f.Key)
			}
		}
	}
}

// TestDescriptorMatchesInit is the anti-drift test: descriptor keys vs the
// keys the driver source actually reads out of its config map.
func TestDescriptorMatchesInit(t *testing.T) {
	for _, name := range storage.Names() {
		t.Run(name, func(t *testing.T) {
			d, ok := storage.DescriptorFor(name)
			if !ok {
				t.Skip("no descriptor — covered by TestEveryRegisteredDriverHasADescriptor")
			}
			dir := filepath.Join("drivers", name)
			read := configKeysReadBy(t, dir)
			declared := map[string]bool{}
			for _, k := range d.Keys() {
				declared[k] = true
			}
			for k := range read {
				if !declared[k] {
					t.Errorf("%s reads config key %q but the descriptor does not declare it "+
						"(add a Field, or list it under Aliases of the field it is a legacy spelling of)", name, k)
				}
			}
			for k := range declared {
				if !read[k] {
					t.Errorf("%s descriptor declares config key %q but no Init reads it — "+
						"a surface would collect a value that goes nowhere", name, k)
				}
			}
			if t.Failed() {
				t.Logf("declared: %v", sortedKeys(declared))
				t.Logf("read:     %v", sortedKeys(read))
			}
		})
	}
}

// TestDescriptorRootFieldDrivesValidation — the validator must read the
// field the descriptor flags as the root, aliases included. This is the
// exact drift that broke the admin form: it sent "base_path", the
// validator read "root", every submit came back 400.
func TestDescriptorRootFieldDrivesValidation(t *testing.T) {
	for _, name := range storage.Names() {
		d, _ := storage.DescriptorFor(name)
		f, ok := d.RootField()
		if !ok {
			continue
		}
		for _, key := range append([]string{f.Key}, f.Aliases...) {
			if err := storage.ValidateNonRootPath(name, map[string]any{key: "fileman"}); err != nil {
				t.Errorf("%s: config {%s: fileman} rejected: %v", name, key, err)
			}
		}
		for _, bad := range []map[string]any{{}, {f.Key: ""}, {f.Key: "/"}, {f.Key: "  "}} {
			if err := storage.ValidateNonRootPath(name, bad); err == nil {
				t.Errorf("%s: config %v should be rejected as a root mount", name, bad)
			}
		}
	}
}

// TestDescriptorsExposeCapabilities — surfaces render "no presigned URLs"
// straight off the descriptor list, so it has to carry the probe result.
func TestDescriptorsExposeCapabilities(t *testing.T) {
	var s3seen, localseen bool
	for _, d := range storage.Descriptors() {
		if !d.Capabilities.Read {
			t.Errorf("%s: descriptor capabilities look unpopulated: %+v", d.Driver, d.Capabilities)
		}
		switch d.Driver {
		case "s3":
			s3seen = true
			if !d.Capabilities.Presign {
				t.Errorf("s3 should advertise presigned URLs")
			}
		case "local":
			localseen = true
			if d.Capabilities.Presign {
				t.Errorf("local cannot presign")
			}
		}
	}
	if !s3seen || !localseen {
		t.Fatalf("Descriptors() did not return the built-in drivers")
	}
}

// ── source scanning ──────────────────────────────────────────────────

// configKeysReadBy parses every non-test file in dir and returns the set
// of config keys read out of any `map[string]any` parameter — both
// `cfg["key"]` indexing and `helper(cfg, "key", "legacy_key")` calls.
func configKeysReadBy(t *testing.T, dir string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	keys := map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || fn.Type.Params == nil {
					continue
				}
				for _, param := range fn.Type.Params.List {
					if !isStringAnyMap(param.Type) {
						continue
					}
					for _, ident := range param.Names {
						collectConfigKeys(fn.Body, ident.Name, keys)
					}
				}
			}
		}
	}
	if len(keys) == 0 {
		t.Fatalf("%s: found no config-map reads — the scanner is broken, not the driver", dir)
	}
	return keys
}

func isStringAnyMap(expr ast.Expr) bool {
	m, ok := expr.(*ast.MapType)
	if !ok {
		return false
	}
	k, ok := m.Key.(*ast.Ident)
	if !ok || k.Name != "string" {
		return false
	}
	switch v := m.Value.(type) {
	case *ast.Ident:
		return v.Name == "any"
	case *ast.InterfaceType:
		return v.Methods == nil || len(v.Methods.List) == 0
	}
	return false
}

func collectConfigKeys(body *ast.BlockStmt, param string, out map[string]bool) {
	add := func(e ast.Expr) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		if s, err := strconv.Unquote(lit.Value); err == nil {
			out[s] = true
		}
	}
	isParam := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == param
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.IndexExpr:
			if isParam(x.X) {
				add(x.Index)
			}
		case *ast.CallExpr:
			if len(x.Args) > 1 && isParam(x.Args[0]) {
				for _, a := range x.Args[1:] {
					add(a)
				}
			}
		}
		return true
	})
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
