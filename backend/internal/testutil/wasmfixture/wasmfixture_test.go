package wasmfixture

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestStale(t *testing.T) {
	dir := t.TempDir()
	wasm := filepath.Join(dir, "echo.wasm")
	base := time.Now().Add(-time.Hour)
	write(t, filepath.Join(dir, "main.go"), base)
	write(t, filepath.Join(dir, "manifest.json"), base)
	write(t, wasm, base.Add(time.Minute))

	if got, err := Stale(wasm); err != nil || got != "" {
		t.Fatalf("a module built after its sources is fresh, got %q, %v", got, err)
	}

	// The #64 case: the fixture's source changed after the module was built.
	write(t, filepath.Join(dir, "main.go"), base.Add(2*time.Minute))
	if got, _ := Stale(wasm); got != "main.go" {
		t.Fatalf("main.go is newer than the module, got %q", got)
	}
	write(t, filepath.Join(dir, "manifest.json"), base.Add(3*time.Minute))
	if got, _ := Stale(wasm); got != "manifest.json" {
		t.Fatalf("the newest source is named, got %q", got)
	}

	// Anything else beside it does not count.
	write(t, wasm, base.Add(4*time.Minute))
	write(t, filepath.Join(dir, "notes.txt"), base.Add(5*time.Minute))
	if got, _ := Stale(wasm); got != "" {
		t.Fatalf("only sources count, got %q", got)
	}

	if _, err := Stale(filepath.Join(dir, "missing.wasm")); err == nil {
		t.Fatal("a module that does not exist is an error")
	}
}
