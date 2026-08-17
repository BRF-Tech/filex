package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The folder-share ZIP cache used to live in the data directory, which is
// exactly where backups, rsync and DR restores pick things up — one 15 GB
// archive of an eleven-minute share rode into three of them. It now lives
// under the cache directory, and an existing install must not be left with a
// second copy that nothing sweeps.
func TestMigrateShareZipDir(t *testing.T) {
	t.Run("moves an existing cache into the cache directory", func(t *testing.T) {
		data := t.TempDir()
		legacy := filepath.Join(data, "sharezips")
		dst := filepath.Join(data, "cache", "sharezips")
		require.NoError(t, os.MkdirAll(legacy, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(legacy, "3748-0123456789abcdef.zip"), []byte("zip"), 0o644))

		migrateShareZipDir(legacy, dst)

		require.NoDirExists(t, legacy, "the legacy directory must not survive: nothing sweeps it")
		require.FileExists(t, filepath.Join(dst, "3748-0123456789abcdef.zip"))
	})

	t.Run("drops the legacy copy when the destination already exists", func(t *testing.T) {
		data := t.TempDir()
		legacy := filepath.Join(data, "sharezips")
		dst := filepath.Join(data, "cache", "sharezips")
		require.NoError(t, os.MkdirAll(legacy, 0o755))
		require.NoError(t, os.MkdirAll(dst, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(legacy, "1-0123456789abcdef.zip"), []byte("old"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dst, "2-0123456789abcdef.zip"), []byte("new"), 0o644))

		migrateShareZipDir(legacy, dst)

		require.NoDirExists(t, legacy)
		require.FileExists(t, filepath.Join(dst, "2-0123456789abcdef.zip"), "the live cache must be untouched")
	})

	t.Run("does nothing without a legacy directory", func(t *testing.T) {
		data := t.TempDir()
		dst := filepath.Join(data, "cache", "sharezips")

		migrateShareZipDir(filepath.Join(data, "sharezips"), dst)

		require.NoDirExists(t, dst, "migration must not create a directory it was never asked for")
	})

	t.Run("does nothing when the two paths are the same", func(t *testing.T) {
		data := t.TempDir()
		same := filepath.Join(data, "sharezips")
		require.NoError(t, os.MkdirAll(same, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(same, "1-0123456789abcdef.zip"), []byte("x"), 0o644))

		migrateShareZipDir(same, same)

		require.FileExists(t, filepath.Join(same, "1-0123456789abcdef.zip"))
	})
}
