package e2edecrypt

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The server must never decrypt anything. docs/E2E-ENCRYPTION.md promises
// "no encryption or decryption anywhere in the server", and this package is
// the one place in the repository that can decrypt: it exists for
// `filex decrypt`, which runs on a user's machine, offline.
//
// The CLI and the server are the same binary, so the promise cannot be kept
// at the binary level; it is kept at the import level instead. Only the CLI
// command (backend/cmd/filex) may import this package — not a handler, not a
// queue worker, not a plugin. A future change that reaches for it from the
// server side fails here, before review has to notice.
func TestNothingButTheCLIImportsTheDecryptor(t *testing.T) {
	backend, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(backend, "go.mod"))

	var offenders []string
	scanned := 0
	fset := token.NewFileSet()
	err = filepath.WalkDir(backend, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(backend, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch rel {
			case "cmd/filex", "internal/e2edecrypt", "embed":
				return filepath.SkipDir
			}
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, p, src, parser.ImportsOnly)
		if err != nil {
			return err
		}
		scanned++
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); importsDecryptor(path) {
				offenders = append(offenders, rel)
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, scanned, 200, "the scan must actually cover the server code")
	require.Empty(t, offenders, "server code imports the offline decryptor")
}

// decryptorPkg is this package's import path.
const decryptorPkg = "github.com/brf-tech/filex/backend/internal/e2edecrypt"

// importsDecryptor reports whether an import path is the decryptor or one of
// its subpackages. An exact match alone let a subpackage (a vault WebDAV
// server under e2edecrypt/, say) carry decryption into the server unnoticed.
func importsDecryptor(path string) bool {
	return path == decryptorPkg || strings.HasPrefix(path, decryptorPkg+"/")
}

func TestImportsDecryptor_CountsSubpackages(t *testing.T) {
	require.True(t, importsDecryptor(decryptorPkg))
	require.True(t, importsDecryptor(decryptorPkg+"/vaultdav"), "a subpackage decrypts too")
	require.False(t, importsDecryptor(decryptorPkg+"x"), "a sibling with a longer name is not the decryptor")
	require.False(t, importsDecryptor("github.com/brf-tech/filex/backend/internal/e2e"))
}
