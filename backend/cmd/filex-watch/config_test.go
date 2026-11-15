package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testKey = "fxw_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// The three sources stack: the file, then the environment, then the flags.
func TestLoadConfig_FileThenEnvThenFlags(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	keyFile := writeFile(t, dir, "key", testKey+"\n")
	cfgFile := writeFile(t, dir, "config.json", `{
		"server": "https://file.example.com/", "storage": 3, "root": "`+filepath.ToSlash(root)+`",
		"key_file": "`+filepath.ToSlash(keyFile)+`", "quiet": "2s", "heartbeat": 120
	}`)
	cfg, err := loadConfig([]string{"--config", cfgFile, "--storage", "9"},
		envOf(map[string]string{"FILEX_WATCH_STORAGE": "5", "FILEX_WATCH_SERVER": "https://env.example.com"}), io.Discard)
	require.NoError(t, err)
	assert.Equal(t, "https://env.example.com", cfg.Server, "the environment over the file")
	assert.EqualValues(t, 9, cfg.Storage, "a flag over the environment")
	assert.Equal(t, 2*time.Second, time.Duration(cfg.Quiet))
	assert.Equal(t, 2*time.Minute, time.Duration(cfg.Heartbeat), "a bare number is seconds")
	assert.Equal(t, 10*time.Second, time.Duration(cfg.MaxWait), "the default where nothing names it")
	assert.Equal(t, testKey, cfg.key, "read from the key file, trimmed")
	abs, _ := filepath.Abs(root)
	assert.Equal(t, abs, cfg.Root)
}

// FILEX_WATCH_KEY wins over the key file, and the config file may be named by
// the environment.
func TestLoadConfig_KeyFromTheEnvironment(t *testing.T) {
	root := t.TempDir()
	cfgFile := writeFile(t, t.TempDir(), "c.json", `{"server": "http://127.0.0.1:5212", "storage": 1, "root": "`+filepath.ToSlash(root)+`", "key_file": "/nowhere"}`)
	cfg, err := loadConfig(nil, envOf(map[string]string{"FILEX_WATCH_CONFIG": cfgFile, "FILEX_WATCH_KEY": testKey}), io.Discard)
	require.NoError(t, err)
	assert.Equal(t, testKey, cfg.key)
}

// The certificate switch has the names filex uses for it elsewhere.
func TestLoadConfig_TLSSwitchNames(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"FILEX_WATCH_SERVER": "https://nas.lan:8443", "FILEX_WATCH_STORAGE": "1", "FILEX_WATCH_ROOT": root, "FILEX_WATCH_KEY": testKey}
	cfg, err := loadConfig(nil, envOf(env), io.Discard)
	require.NoError(t, err)
	assert.False(t, cfg.InsecureSkipTLSVerify, "the check is on unless turned off")

	env["FILEX_WATCH_TLS_SKIP_VERIFY"] = "true"
	cfg, err = loadConfig(nil, envOf(env), io.Discard)
	require.NoError(t, err)
	assert.True(t, cfg.InsecureSkipTLSVerify)

	cfg, err = loadConfig([]string{"--insecure-skip-tls-verify=false"}, envOf(env), io.Discard)
	require.NoError(t, err)
	assert.False(t, cfg.InsecureSkipTLSVerify, "the flag over the environment")
}

// What cannot work is refused at start, each with what to set.
func TestLoadConfig_Refusals(t *testing.T) {
	root := t.TempDir()
	file := writeFile(t, t.TempDir(), "plain.txt", "x")
	good := map[string]string{"FILEX_WATCH_SERVER": "https://f.example.com", "FILEX_WATCH_STORAGE": "1", "FILEX_WATCH_ROOT": root, "FILEX_WATCH_KEY": testKey}
	for name, tc := range map[string]struct {
		change map[string]string
		args   []string
		says   string
	}{
		"no key":               {change: map[string]string{"FILEX_WATCH_KEY": ""}, says: "no key"},
		"not a watch key":      {change: map[string]string{"FILEX_WATCH_KEY": "0123456789abcdef"}, says: "does not look like a filex-watch key"},
		"no server":            {change: map[string]string{"FILEX_WATCH_SERVER": ""}, says: "server"},
		"a server not http":    {change: map[string]string{"FILEX_WATCH_SERVER": "ftp://f.example.com"}, says: "server"},
		"no storage":           {change: map[string]string{"FILEX_WATCH_STORAGE": ""}, says: "storage"},
		"a root that is not":   {change: map[string]string{"FILEX_WATCH_ROOT": filepath.Join(root, "nope")}, says: "root"},
		"a root that's a file": {change: map[string]string{"FILEX_WATCH_ROOT": file}, says: "not a folder"},
		"a quiet too short":    {change: map[string]string{"FILEX_WATCH_QUIET": "10ms"}, says: "quiet"},
		"a heartbeat too long": {change: map[string]string{"FILEX_WATCH_HEARTBEAT": "2h"}, says: "heartbeat"},
		"a stray argument":     {args: []string{"/srv"}, says: "unexpected argument"},
		"a bad number":         {change: map[string]string{"FILEX_WATCH_STORAGE": "seven"}, says: "FILEX_WATCH_STORAGE"},
	} {
		env := map[string]string{}
		for k, v := range good {
			env[k] = v
		}
		for k, v := range tc.change {
			env[k] = v
		}
		_, err := loadConfig(tc.args, envOf(env), io.Discard)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.says, name)
	}
}

// A misspelt field in the file is refused, not silently a setting that is
// not there.
func TestLoadConfig_UnknownFieldInTheFile(t *testing.T) {
	cfgFile := writeFile(t, t.TempDir(), "c.json", `{"server": "https://f.example.com", "key_flie": "/x"}`)
	_, err := loadConfig([]string{"--config", cfgFile}, envOf(nil), io.Discard)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "key_flie"), err.Error())
}

// The key is never a flag: a flag is on the process list.
func TestLoadConfig_NoKeyFlag(t *testing.T) {
	_, err := loadConfig([]string{"--key", testKey}, envOf(nil), io.Discard)
	require.Error(t, err)
}
