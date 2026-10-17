package sharezip

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// A folder link's archive holds only what its rule keeps (handlers.LinkMembers:
// what the link's creator may download). The cached archive is keyed by the
// list it holds, so an archive built with every member is never served for a
// link whose rule keeps fewer, and the kept list is what gets built.
func TestPlan_KeepLeavesMembersOutAndKeysTheArchiveByWhatIsKept(t *testing.T) {
	dir := t.TempDir()
	for rel, body := range map[string]string{
		"shared/open.txt":       "open",
		"shared/kept-back.txt":  "kept back",
		"shared/sub/deeper.txt": "deeper",
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
	c := New(t.TempDir())
	ctx := context.Background()

	allPath, all, err := c.Plan(ctx, drv, "shared", 7, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("a nil keep keeps every member, got %d: %+v", len(all), all)
	}

	keep := func(p string) bool { return filepath.Base(p) != "kept-back.txt" }
	keptPath, kept, err := c.Plan(ctx, drv, "shared", 7, keep)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 2 {
		t.Fatalf("the refused member must be left out, got %+v", kept)
	}
	for _, f := range kept {
		if f.Rel == "kept-back.txt" {
			t.Fatalf("the refused member is in the plan: %+v", kept)
		}
	}
	if keptPath == allPath {
		t.Fatalf("the archive of the kept list must not share a cache file with the full one (%s)", keptPath)
	}

	// Warm builds the kept list, never the full one.
	if _, err := c.Warm(ctx, drv, "shared", 7, keep); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Cached(keptPath); !ok {
		t.Fatalf("Warm with a keep must build the kept archive at %s", keptPath)
	}
	if _, ok := c.Cached(allPath); ok {
		t.Fatalf("Warm with a keep built the full archive at %s", allPath)
	}
}

// Kept is the one filter both archives use: nil keeps the list as it is.
func TestKept_NilKeepsEverything(t *testing.T) {
	files := []File{{Path: "a"}, {Path: "b"}}
	if got := Kept(files, nil); len(got) != 2 {
		t.Fatalf("nil keep: %+v", got)
	}
	if got := Kept(files, func(p string) bool { return p == "b" }); len(got) != 1 || got[0].Path != "b" {
		t.Fatalf("keep b only: %+v", got)
	}
}
