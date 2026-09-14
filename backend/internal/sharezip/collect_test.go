package sharezip

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// filex's own bookkeeping must never end up inside an archive of the user's
// files — not the trash, not thumbnails, and above all not `.versions`.
//
// Red proof (measured 2026-09-13 on a live local storage, before internalName
// existed): `POST /api/files/archive/download {"paths":["depoA://"]}` produced
// an archive whose first member was `.versions/7/1` — a PREVIOUS version of a
// file, which no listing in filex will show you. `.versions` lives at the
// storage root, so the omission was invisible for as long as this walk was only
// ever pointed at a folder, and became visible the first time somebody archived
// a root. All three archives share this walk (the folder-share ZIP, its on-disk
// cache, the selection download), so all three carried it.
func TestCollectFilesSkipsFilexsOwnDirectories(t *testing.T) {
	// ⚠ A REAL driver, not the flat fake the other tests here use. The bug is
	// about a DIRECTORY being descended into, and a fake whose List returns
	// every path as a top-level file cannot express that: it would report the
	// basename `1` for `.versions/7/1` and the guard would look like it works
	// no matter what it did.
	dir := t.TempDir()
	for rel, body := range map[string]string{
		"notes.md":              "mine",
		"docs/report.txt":       "also mine",
		".versions/7/1":         "an older version of somebody's file",
		".versions/7/2":         "and another",
		".filex-trash/gone.txt": "deleted",
		".thumbs/7.webp":        "thumbnail",
	} {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	drv := &local.Driver{}
	if err := drv.Init(context.Background(), map[string]any{"root": dir}); err != nil {
		t.Fatal(err)
	}

	got, err := CollectFiles(context.Background(), drv, "/")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range got {
		names[f.Rel] = true
	}
	for _, want := range []string{"notes.md", "docs/report.txt"} {
		if !names[want] {
			t.Errorf("the user's own file %q is missing from the walk: %v", want, names)
		}
	}
	for _, forbidden := range []string{
		".versions/7/1", ".versions/7/2", ".filex-trash/gone.txt", ".thumbs/7.webp",
	} {
		if names[forbidden] {
			t.Errorf("%q is filex's own bookkeeping and must not be archived (walk returned %v)", forbidden, names)
		}
	}
	if len(got) != 2 {
		t.Errorf("walk returned %d files, want exactly the 2 real ones: %v", len(got), names)
	}
}
