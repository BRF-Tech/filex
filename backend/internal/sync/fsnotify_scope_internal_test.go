package sync

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/fsnotify/fsnotify"

	"github.com/brf-tech/filex/backend/internal/scanrule"
)

// The fsnotify watcher watches exactly what the scan walks (issue #44): the
// same scanrule decides both, so a folder the storage excludes is neither
// walked nor watched — and a change inside it does not start a scan.
//
// It used to skip only folders whose own name began with a dot, and still
// descend into them: `.git` went unwatched while every folder INSIDE it was
// watched, and so was filex's own version history (`.versions/<id>/`), which
// raised a full scan on every snapshot.
func TestAddRecursive_WatchesWhatTheScanWalks(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{
		"docs/2026", ".config/app", // a hidden folder the storage does not exclude
		"proj/.git/objects/aa", "downloads/incomplete/deep", // excluded
		".versions/12", ".filex-trash/x", // filex's own
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rule, err := scanrule.Parse(".git\ndownloads/incomplete/**")
	if err != nil {
		t.Fatal(err)
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := addRecursive(w, root, rule); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range w.WatchList() {
		rel, _ := filepath.Rel(root, p)
		got = append(got, filepath.ToSlash(rel))
	}
	sort.Strings(got)
	want := []string{".", ".config", ".config/app", "docs", "docs/2026", "downloads", "proj"}
	if len(got) != len(want) {
		t.Fatalf("watched %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("watched %v, want %v", got, want)
		}
	}

	// An event is judged by the same rule before it may start a scan.
	for rel, want := range map[string]bool{
		"docs/2026/rapor.txt":                true,
		".config/app/settings.json":          true,
		"downloads/incomplete/film.part":     false,
		"proj/.git/index":                    false,
		".versions/12/3":                     false,
		".filex-trash/1700000000-x__old.txt": false,
	} {
		if got := watched(root, filepath.Join(root, filepath.FromSlash(rel)), rule); got != want {
			t.Errorf("watched(%s) = %v, want %v", rel, got, want)
		}
	}
}
