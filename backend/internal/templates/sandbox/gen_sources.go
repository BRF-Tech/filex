//go:build ignore

// gen_sources writes engine.wasm.sources: build.sh runs it after the module is
// built (go run ./internal/templates/sandbox/gen_sources.go, from backend/).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/brf-tech/filex/backend/internal/templates/sandbox"
)

func main() {
	d, err := sandbox.SourceDigest(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out := filepath.Join("internal", "templates", "sandbox", sandbox.SourcesFile)
	if err := os.WriteFile(out, []byte(d+"\n"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(d)
}
