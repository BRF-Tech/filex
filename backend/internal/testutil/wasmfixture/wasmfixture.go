// Package wasmfixture guards the tests that load the echo app fixture
// (internal/wasmplugin/testdata/echo/echo.wasm): the module is built, and it
// was built from the sources beside it.
//
// ⚠⚠ echo.wasm is a build artefact nobody commits. A tree that merged a change
// to the fixture's main.go or manifest.json keeps the module it built before:
// on 2026-09-25 four tests of #64 failed on main with a bare 502 ("gather" is
// not an action of the module that was loaded) while the branch they came
// from was green. CI builds the module first and is not affected; a developer
// tree is. A stale module is a failure with the command to run, never a skip.
//
// Stdlib only: internal/testutil imports the API, which imports wasmplugin,
// so wasmplugin's own tests cannot use it.
package wasmfixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const rebuild = "bash scripts/build-wasm-fixture.sh"

// Stale reports the newest source file in the module's directory that is
// newer than the module, or "" when the module is at least as new as all of
// them. Sources are the .go and .json files and go.mod/go.sum.
func Stale(wasm string) (string, error) {
	mod, err := os.Stat(wasm)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(filepath.Dir(wasm))
	if err != nil {
		return "", err
	}
	newest := ""
	newestAt := mod.ModTime()
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !(strings.HasSuffix(n, ".go") || strings.HasSuffix(n, ".json") || n == "go.mod" || n == "go.sum") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return "", err
		}
		if info.ModTime().After(newestAt) {
			newest, newestAt = n, info.ModTime()
		}
	}
	return newest, nil
}

// Require skips a test when the module was never built (it fails instead
// where FILEX_REQUIRE_WASM_FIXTURE is set, as on CI), and fails it when the
// module is older than its sources.
func Require(t testing.TB, wasm string) {
	t.Helper()
	if _, err := os.Stat(wasm); err != nil {
		if os.Getenv("FILEX_REQUIRE_WASM_FIXTURE") != "" {
			t.Fatalf("%s is missing on CI: run %s before go test", wasm, rebuild)
		}
		t.Skipf("%s not built (%s)", wasm, rebuild)
	}
	newer, err := Stale(wasm)
	if err != nil {
		t.Fatalf("%s: %v", wasm, err)
	}
	if newer != "" {
		t.Fatalf("%s is older than %s beside it — the tests would run the module built before that change; rebuild it: %s", filepath.Base(wasm), newer, rebuild)
	}
}
