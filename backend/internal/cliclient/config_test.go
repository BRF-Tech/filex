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

	// Mixed: URL from flag, token from file - only when the flag names the
	// server the token was saved for.
	conn, err = Resolve("https://file.example.com/", "", cfgPath, func(string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, "https://file.example.com", conn.URL)
	assert.Equal(t, "file-token", conn.Token)

	// ⚠ Another server: the saved token is not its to have.
	conn, err = Resolve("https://flag.example.com", "", cfgPath, func(string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, "https://flag.example.com", conn.URL)
	assert.Empty(t, conn.Token, "a token saved for one server never goes to another")
	assert.Equal(t, "https://file.example.com", conn.SavedURL, "and the caller can say whose it is")

	// Nothing anywhere → empty conn, no error (commands decide what's fatal).
	conn, err = Resolve("", "", filepath.Join(t.TempDir(), "missing.yaml"), func(string) string { return "" })
	require.NoError(t, err)
	assert.Empty(t, conn.URL)
	assert.Empty(t, conn.Token)
}

// TestResolve_TokenBelongsToItsAddress: the token in cli.yaml is sent only to
// the address it was saved with - the same scheme, host (any case), port (the
// default one written or not) and base path, a trailing slash aside. Another
// name for the same machine, an http/https change, another port or another
// base path is another address. A token given on purpose (--token,
// FILEX_TOKEN) goes wherever it is sent.
func TestResolve_TokenBelongsToItsAddress(t *testing.T) {
	none := func(string) string { return "" }
	saved := func(t *testing.T, url string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "cli.yaml")
		require.NoError(t, SaveFileConfig(p, FileConfig{URL: url, Token: "saved-token"}))
		return p
	}

	cfgPath := saved(t, "https://files.example.com/filex")
	for _, same := range []string{
		"", // the saved address itself
		"https://files.example.com/filex",
		"https://files.example.com/filex/",
		"https://FILES.Example.com/filex",
		"HTTPS://files.example.com/filex",
		"https://files.example.com:443/filex",
		"https://files.example.com./filex", // the fully qualified name
		" https://files.example.com/filex ",
	} {
		conn, err := Resolve(same, "", cfgPath, none)
		require.NoError(t, err)
		assert.Equal(t, "saved-token", conn.Token, "url %q", same)
		assert.Empty(t, conn.SavedURL)
	}
	for _, other := range []string{
		"https://files.example.org/filex", // another server
		"https://192.0.2.10/filex",        // the same machine by its IP: not provably the same server
		"https://files/filex",             // a short name for it: likewise
		"http://files.example.com/filex",  // the unencrypted address
		"https://files.example.com:8443/filex",
		"https://files.example.com",                    // no base path
		"https://files.example.com/other",              // another base path
		"https://files.example.com/filex/sub",          // a longer one
		"https://files.example.com@evil.example/filex", // the host is evil.example
	} {
		conn, err := Resolve(other, "", cfgPath, none)
		require.NoError(t, err)
		assert.Empty(t, conn.Token, "url %q", other)
		assert.Equal(t, "https://files.example.com/filex", conn.SavedURL, "url %q", other)
	}

	// FILEX_URL naming another server is the same as --url.
	conn, err := Resolve("", "", cfgPath, func(k string) string {
		if k == "FILEX_URL" {
			return "https://elsewhere.example.com"
		}
		return ""
	})
	require.NoError(t, err)
	assert.Empty(t, conn.Token)

	// A token given on purpose goes to the address given with it.
	conn, err = Resolve("https://elsewhere.example.com", "flag-token", cfgPath, none)
	require.NoError(t, err)
	assert.Equal(t, "flag-token", conn.Token)
	conn, err = Resolve("https://elsewhere.example.com", "", cfgPath, func(k string) string {
		if k == "FILEX_TOKEN" {
			return "env-token"
		}
		return ""
	})
	require.NoError(t, err)
	assert.Equal(t, "env-token", conn.Token)

	// http -> https is another address: signing in over https is the way up.
	cfgPath = saved(t, "http://files.example.com")
	conn, err = Resolve("https://files.example.com", "", cfgPath, none)
	require.NoError(t, err)
	assert.Empty(t, conn.Token, "http -> https")
	conn, err = Resolve("http://files.example.com:80/", "", cfgPath, none)
	require.NoError(t, err)
	assert.Equal(t, "saved-token", conn.Token, "port 80 is http's own")

	// The tenant address a handoff saved is bound the same way.
	cfgPath = saved(t, "https://files.acme.example")
	conn, err = Resolve("https://files.example.com", "", cfgPath, none)
	require.NoError(t, err)
	assert.Empty(t, conn.Token, "the platform's address does not get the tenant address's session")

	// A saved token with no address has nothing to be bound to: never sent.
	p := filepath.Join(t.TempDir(), "cli.yaml")
	require.NoError(t, os.WriteFile(p, []byte("token: orphan\n"), 0o600))
	conn, err = Resolve("https://files.example.com", "", p, none)
	require.NoError(t, err)
	assert.Empty(t, conn.Token)
}

// TestConfig_RealmRoundtrip: the realm is saved with the address; a config
// without one writes no realm key (an older CLI reads it unchanged).
func TestConfig_RealmRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yaml")
	in := FileConfig{URL: "https://files.example.com", Token: "tok", Realm: "acme"}
	require.NoError(t, SaveFileConfig(path, in))
	out, err := LoadFileConfig(path)
	require.NoError(t, err)
	assert.Equal(t, in, out)

	require.NoError(t, SaveFileConfig(path, FileConfig{URL: "https://files.example.com", Token: "tok"}))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "realm")
}

// TestResolve_RealmBelongsToItsAddress: the saved realm comes back only for
// the address it was saved with - never for another server.
func TestResolve_RealmBelongsToItsAddress(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cli.yaml")
	require.NoError(t, SaveFileConfig(cfgPath, FileConfig{URL: "https://files.example.com/filex", Token: "tok", Realm: "acme"}))
	none := func(string) string { return "" }

	for _, same := range []string{"", "https://files.example.com/filex", "https://files.example.com/filex/", " https://FILES.example.com/filex "} {
		conn, err := Resolve(same, "", cfgPath, none)
		require.NoError(t, err)
		assert.Equal(t, "acme", conn.Realm, "url %q", same)
	}
	for _, other := range []string{"https://files.example.org/filex", "http://files.example.com/filex", "https://files.example.com", "https://files.example.com/other", "https://files.example.com:8443/filex"} {
		conn, err := Resolve(other, "", cfgPath, none)
		require.NoError(t, err)
		assert.Empty(t, conn.Realm, "url %q", other)
	}
	conn, err := Resolve("", "", cfgPath, func(k string) string {
		if k == "FILEX_URL" {
			return "https://elsewhere.example.com"
		}
		return ""
	})
	require.NoError(t, err)
	assert.Empty(t, conn.Realm, "FILEX_URL naming another server")
}
