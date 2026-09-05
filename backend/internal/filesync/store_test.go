package filesync

import (
	"path/filepath"
	"strings"
	"testing"
)

// The portable desktop build depends on this override: its whole promise is
// that deleting one folder leaves nothing behind, and the sync store holds the
// local trash — real copies of files the engine deleted. Without the override
// those land in the home directory of a machine that is not the user's.
func TestDefaultStoreDirHonoursEnvOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "portable", "sync")
	t.Setenv("FILEX_SYNC_DIR", want)

	got, err := DefaultStoreDir()
	if err != nil {
		t.Fatalf("DefaultStoreDir: %v", err)
	}
	if got != want {
		t.Fatalf("store dir = %q, want %q", got, want)
	}
}

// An empty variable is not an instruction. Treating "" as a path would put the
// pairs file in the process's working directory — wherever that happens to be.
func TestDefaultStoreDirIgnoresEmptyOverride(t *testing.T) {
	t.Setenv("FILEX_SYNC_DIR", "")

	got, err := DefaultStoreDir()
	if err != nil {
		t.Fatalf("DefaultStoreDir: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(got), "/.filex/sync") {
		t.Fatalf("store dir = %q, want it to fall back to ~/.filex/sync", got)
	}
}
