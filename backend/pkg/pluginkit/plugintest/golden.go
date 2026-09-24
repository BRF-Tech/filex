package plugintest

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ── Golden screens ─────────────────────────────────────────────────────
//
// A surface is data, so its shape can be stored and compared. A golden
// screen catches the change nobody meant to make: a button that lost its
// `primary`, a field that quietly changed type, a section that vanished
// when a branch was refactored. The diff is the review.
//
//	plugintest.Golden(t, "options", s)
//
//	go test ./...            compares against testdata/golden/options.json
//	go test ./... -update    rewrites them; `git diff` is what you review

// GoldenDir is where the stored screens live, relative to the test's
// package directory.
var GoldenDir = filepath.Join("testdata", "golden")

var updateFlag *bool

func init() {
	// Register -update unless the test binary already has one of its own.
	// Flags must exist before `go test` parses them, so this cannot be
	// done lazily; PLUGINTEST_UPDATE=1 is the way out when a package owns
	// the name itself.
	if flag.Lookup("update") == nil {
		updateFlag = flag.Bool("update", false, "plugintest: rewrite the golden screens under testdata/golden")
	}
}

// Updating reports whether this run rewrites the golden screens (-update,
// or PLUGINTEST_UPDATE=1).
func Updating() bool {
	if updateFlag != nil && *updateFlag {
		return true
	}
	if f := flag.Lookup("update"); f != nil && f.Value.String() == "true" {
		return true
	}
	switch os.Getenv("PLUGINTEST_UPDATE") {
	case "", "0", "false":
		return false
	}
	return true
}

// Golden compares v (a surface, a manifest, an action's output — anything
// that marshals) against testdata/golden/<name>.json. With -update it
// writes the file instead.
func Golden(t TB, name string, v any) {
	t.Helper()
	GoldenWith(t, name, v, nil)
}

// GoldenWith is Golden with a chance to scrub the volatile parts — a
// token, a timestamp, a temporary path — out of the JSON before it is
// compared, so the stored screen stays stable.
func GoldenWith(t TB, name string, v any, scrub func([]byte) []byte) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("golden %s: the value does not marshal: %v", name, err)
		return
	}
	if scrub != nil {
		got = scrub(got)
	}
	got = append(bytes.TrimRight(got, "\n"), '\n')

	path := filepath.Join(GoldenDir, name+".json")
	if Updating() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("golden %s: %v", name, err)
			return
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("golden %s: %v", name, err)
			return
		}
		t.Logf("golden %s: written (%d bytes) — review it with `git diff %s`", name, len(got), path)
		return
	}
	want, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Errorf("golden %s: %s does not exist yet — run the tests once with -update and review what it wrote", name, path)
		return
	}
	if err != nil {
		t.Fatalf("golden %s: %v", name, err)
		return
	}
	want = append(bytes.TrimRight(want, "\n"), '\n')
	if bytes.Equal(want, got) {
		return
	}
	t.Errorf("golden %s: the screen changed shape.\n%s\nIf the change is intended: go test ./... -update, then review `git diff %s`.",
		name, lineDiff(string(want), string(got)), path)
}

// lineDiff is a plain "first lines that differ" report — enough to see
// what moved without pulling in a diff library.
func lineDiff(want, got string) string {
	w := strings.Split(strings.TrimRight(want, "\n"), "\n")
	g := strings.Split(strings.TrimRight(got, "\n"), "\n")
	var b strings.Builder
	shown := 0
	n := len(w)
	if len(g) > n {
		n = len(g)
	}
	for i := 0; i < n && shown < 30; i++ {
		var lw, lg string
		if i < len(w) {
			lw = w[i]
		}
		if i < len(g) {
			lg = g[i]
		}
		if lw == lg {
			continue
		}
		if i < len(w) {
			fmt.Fprintf(&b, "  -%4d %s\n", i+1, strings.TrimSpace(lw))
		}
		if i < len(g) {
			fmt.Fprintf(&b, "  +%4d %s\n", i+1, strings.TrimSpace(lg))
		}
		shown++
	}
	if shown == 0 {
		return "  (only whitespace differs)"
	}
	if shown >= 30 {
		b.WriteString("  … more\n")
	}
	return b.String()
}
