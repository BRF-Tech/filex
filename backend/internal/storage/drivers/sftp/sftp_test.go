package sftp

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// The admin form sent "base_path" and the replication dialog "username";
// the driver read "root" and "user" and quietly ignored both. Those rows
// exist, so the aliases the descriptor declares must actually be read.
func TestInit_LegacyKeySpellings(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{"canonical", map[string]any{"host": "h", "user": "u", "password": "p", "root": "/srv/files"}},
		{"admin form", map[string]any{"host": "h", "user": "u", "password": "p", "base_path": "/srv/files"}},
		{"replication dialog", map[string]any{"host": "h", "username": "u", "password": "p", "remote_path": "/srv/files"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &Driver{}
			if err := d.Init(context.Background(), tc.cfg); err != nil {
				t.Fatalf("init: %v", err)
			}
			if d.user != "u" {
				t.Errorf("user = %q, want u", d.user)
			}
			if d.root != "/srv/files" {
				t.Errorf("root = %q, want /srv/files", d.root)
			}
		})
	}
}

// key_path names a key file on the server — the form offered it, the
// driver never read it, so an operator who filled it in got
// "either password or private_key required".
func TestInit_KeyPathIsLoaded(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(key, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nzzz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Driver{}
	if err := d.Init(context.Background(), map[string]any{
		"host": "h", "user": "u", "key_path": key, "root": "/srv/files",
	}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if d.keyPEM == "" {
		t.Fatal("key_path was not loaded into the PEM")
	}

	// An inline PEM wins over the file.
	d2 := &Driver{}
	if err := d2.Init(context.Background(), map[string]any{
		"host": "h", "user": "u", "private_key": "INLINE", "key_path": key, "root": "/srv/files",
	}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if d2.keyPEM != "INLINE" {
		t.Errorf("private_key should win over key_path, got %q", d2.keyPEM)
	}

	// A bad path fails at configure time, not on the first listing.
	d3 := &Driver{}
	if err := d3.Init(context.Background(), map[string]any{
		"host": "h", "user": "u", "key_path": filepath.Join(dir, "nope"), "root": "/srv/files",
	}); err == nil {
		t.Error("missing key file should fail Init")
	}
}

func TestInit_Defaults(t *testing.T) {
	d := &Driver{}
	if err := d.Init(context.Background(), map[string]any{
		"host": "h", "user": "u", "password": "p",
	}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if d.port != 22 {
		t.Errorf("port = %d, want 22", d.port)
	}
	// No root configured is still "/" — rows written before the validator
	// demanded a sub-folder keep mounting where they always did.
	if d.root != "/" {
		t.Errorf("root = %q, want /", d.root)
	}
}

// A remote DIRECTORY symlink used to be reported as storage.KindFile, because
// List asked IsDir and called everything else a file. SFTP READDIR hands back
// LSTAT attributes, so the link bit is right there and was simply never read.
//
// ⚠ The consequence was not a cosmetic mislabel: the explorer offered the
// entry as a downloadable file, the download opened a directory and failed,
// and the catalogue walk minted a file row for an object with no bytes.
func TestKindOf_ADirectorySymlinkIsNotAFile(t *testing.T) {
	cases := []struct {
		name string
		mode fs.FileMode
		want storage.ObjectKind
	}{
		{"plain file", 0o644, storage.KindFile},
		{"directory", fs.ModeDir | 0o755, storage.KindDirectory},
		{"symlink to a file", fs.ModeSymlink | 0o777, storage.KindSymlink},
		// ⚠ The reported case. READDIR sets BOTH bits for a link to a
		// directory on some servers, and the symlink bit has to win — asking
		// IsDir first would call it a directory and make the catalogue walk
		// descend through it, off the configured root, with nothing to stop it.
		{"symlink to a directory", fs.ModeSymlink | fs.ModeDir | 0o777, storage.KindSymlink},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := kindOf(c.mode); got != c.want {
				t.Errorf("kindOf(%v) = %q, want %q", c.mode, got, c.want)
			}
		})
	}
}
