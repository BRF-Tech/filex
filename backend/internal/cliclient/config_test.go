package cliclient

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfig_SaveLoadRoundtrip writes then re-reads the CLI config and
// asserts owner-only permissions on the file.
func TestConfig_SaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "cli.yaml")
	in := FileConfig{URL: "https://fm.example.com", Token: "tok-123"}
	require.NoError(t, SaveFileConfig(path, in))

	out, err := LoadFileConfig(path)
	require.NoError(t, err)
	assert.Equal(t, in, out)

	if runtime.GOOS != "windows" { // Windows has no POSIX modes
		fi, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "token file must be owner-only")
	}
}

// TestConfig_SaveTightensExistingMode ensures a re-login chmods a
// pre-existing looser file down to 0600.
func TestConfig_SaveTightensExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not applicable on windows")
	}
	path := filepath.Join(t.TempDir(), "cli.yaml")
	require.NoError(t, os.WriteFile(path, []byte("url: x\n"), 0o644))

	require.NoError(t, SaveFileConfig(path, FileConfig{URL: "u", Token: "t"}))
	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

// TestConfig_LoadMissingIsZero: a missing file is not an error.
func TestConfig_LoadMissingIsZero(t *testing.T) {
	cfg, err := LoadFileConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	require.NoError(t, err)
	assert.Equal(t, FileConfig{}, cfg)
}

// TestResolve_Precedence: flags > env > config file, per field.
func TestResolve_Precedence(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cli.yaml")
	require.NoError(t, SaveFileConfig(cfgPath, FileConfig{URL: "https://file.example.com", Token: "file-token"}))

	env := map[string]string{"FILEX_URL": "https://env.example.com", "FILEX_TOKEN": "env-token"}
	getenv := func(k string) string { return env[k] }

	// Flags beat everything.
	conn, err := Resolve("https://flag.example.com/", "flag-token", cfgPath, getenv)
	require.NoError(t, err)
	assert.Equal(t, "https://flag.example.com", conn.URL, "trailing slash trimmed")
	assert.Equal(t, "flag-token", conn.Token)

	// Env beats the file.
	conn, err = Resolve("", "", cfgPath, getenv)
	require.NoError(t, err)
	assert.Equal(t, "https://env.example.com", conn.URL)
	assert.Equal(t, "env-token", conn.Token)

	// File is the fallback.
	conn, err = Resolve("", "", cfgPath, func(string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, "https://file.example.com", conn.URL)
	assert.Equal(t, "file-token", conn.Token)

	// Mixed: URL from flag, token from file.
	conn, err = Resolve("https://flag.example.com", "", cfgPath, func(string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, "https://flag.example.com", conn.URL)
	assert.Equal(t, "file-token", conn.Token)

	// Nothing anywhere → empty conn, no error (commands decide what's fatal).
	conn, err = Resolve("", "", filepath.Join(t.TempDir(), "missing.yaml"), func(string) string { return "" })
	require.NoError(t, err)
	assert.Empty(t, conn.URL)
	assert.Empty(t, conn.Token)
}
