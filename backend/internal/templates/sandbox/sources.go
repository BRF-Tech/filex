package sandbox

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SourcesFile names, next to engine.wasm.gz, the digest of what the module was
// built from (build.sh writes it). TestEngineIsBuiltFromTheseSources reads it
// back: a change to the dialect (internal/templates/engine), to the guest, to
// build.sh or to the version of the Liquid library that is not followed by a
// rebuild is a red test, not a server whose edited templates quietly speak an
// older dialect than the ones it ships.
const SourcesFile = "engine.wasm.sources"

// guestModules are the modules compiled into the guest besides filex's own
// packages; their go.sum hashes are part of the digest.
var guestModules = []string{"github.com/osteele/liquid", "github.com/osteele/tuesday", "gopkg.in/yaml.v2"}

// SourceDigest is the digest of the guest's sources under backend (the module
// root): every non-test Go file of internal/templates/engine and of the guest,
// build.sh, and the go.sum lines of guestModules.
func SourceDigest(backend string) (string, error) {
	var files []string
	for _, dir := range []string{"internal/templates/engine", "internal/templates/sandbox/guest"} {
		matches, err := filepath.Glob(filepath.Join(backend, dir, "*.go"))
		if err != nil {
			return "", err
		}
		for _, m := range matches {
			if !strings.HasSuffix(m, "_test.go") {
				files = append(files, m)
			}
		}
	}
	files = append(files, filepath.Join(backend, "internal/templates/sandbox/build.sh"))
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		rel, _ := filepath.Rel(backend, f)
		// Line ends as committed: a Windows checkout's CRLF is not a change.
		b = []byte(strings.ReplaceAll(string(b), "\r\n", "\n"))
		sum := sha256.Sum256(b)
		fmt.Fprintf(h, "%s %s\n", filepath.ToSlash(rel), hex.EncodeToString(sum[:]))
	}
	sum, err := os.Open(filepath.Join(backend, "go.sum"))
	if err != nil {
		return "", err
	}
	defer sum.Close()
	var lines []string
	sc := bufio.NewScanner(sum)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 3 || strings.HasSuffix(fields[1], "/go.mod") {
			continue
		}
		for _, m := range guestModules {
			if fields[0] == m {
				lines = append(lines, sc.Text())
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	sort.Strings(lines)
	for _, l := range lines {
		fmt.Fprintln(h, l)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
