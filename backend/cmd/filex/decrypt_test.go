package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// `filex decrypt` end to end, on the folders the browser code wrote
// (internal/e2edecrypt/testdata/folders — see web/tests/lib/e2eFixtureFolders.test.ts).

var decryptFixtures = filepath.Join("..", "..", "internal", "e2edecrypt", "testdata", "folders")

type decryptCase struct {
	Password    string  `json:"password"`
	RecoveryKey *string `json:"recovery_key"`
}

func decryptSecrets(t *testing.T) map[string]decryptCase {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(decryptFixtures, "secrets.json"))
	require.NoError(t, err)
	var s struct {
		Cases map[string]decryptCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(b, &s))
	return s.Cases
}

func runDecrypt(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := decryptCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return out.String(), errb.String(), err
}

func TestDecryptCmd_PasswordFromStdin(t *testing.T) {
	c := decryptSecrets(t)["v3-names"]
	out := filepath.Join(t.TempDir(), "plain")
	stdout, stderr, err := runDecrypt(t, c.Password+"\n", filepath.Join(decryptFixtures, "v3-names"), "-o", out, "--password-stdin")
	require.NoError(t, err)
	require.Equal(t, 0, exitCode(err))
	require.Contains(t, stdout, "Decrypted ")
	require.Contains(t, stdout, "(2 warning(s))")
	require.Contains(t, stderr, "warning: stray.txt")
	b, err := os.ReadFile(filepath.Join(out, "Sözleşmeler", "Eski", "2019.txt"))
	require.NoError(t, err)
	require.Equal(t, "eski sözleşme\n", string(b))
	// The secret appears nowhere in what the command printed.
	require.NotContains(t, stdout+stderr, c.Password)
}

func TestDecryptCmd_RecoveryKeyFromAPipe(t *testing.T) {
	c := decryptSecrets(t)["v2-0.47.0"]
	require.NotNil(t, c.RecoveryKey)
	out := filepath.Join(t.TempDir(), "plain")
	// Not a terminal and no --password-stdin: a pipe is read all the same.
	_, _, err := runDecrypt(t, *c.RecoveryKey+"\r\n", filepath.Join(decryptFixtures, "v2-0.47.0"), "-o", out, "--recovery-key", "-q")
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(out, "report.txt"))
	require.NoError(t, err)
}

func TestDecryptCmd_WrongPasswordExitsFiveAndWritesNothing(t *testing.T) {
	out := filepath.Join(t.TempDir(), "plain")
	_, _, err := runDecrypt(t, "not the password\n", filepath.Join(decryptFixtures, "v3-names"), "-o", out, "--password-stdin")
	require.Error(t, err)
	require.Equal(t, exitDecryptWrongSecret, exitCode(err))
	require.Contains(t, err.Error(), "nothing was written")
	_, statErr := os.Stat(out)
	require.True(t, os.IsNotExist(statErr))
	left, _ := filepath.Glob(out + ".partial-*")
	require.Empty(t, left)
}

func TestDecryptCmd_WrongRecoveryKeyExitsFive(t *testing.T) {
	out := filepath.Join(t.TempDir(), "plain")
	_, _, err := runDecrypt(t, "0000-0000-0000-0000-0000-0000-0000-0000\n", filepath.Join(decryptFixtures, "v3-names"), "-o", out, "--recovery-key")
	require.Equal(t, exitDecryptWrongSecret, exitCode(err))
}

func TestDecryptCmd_DamagedFileExitsSix(t *testing.T) {
	c := decryptSecrets(t)["v2-0.47.0"]
	in := filepath.Join(t.TempDir(), "in")
	require.NoError(t, os.CopyFS(in, os.DirFS(filepath.Join(decryptFixtures, "v2-0.47.0"))))
	p := filepath.Join(in, "report.txt")
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	b[len(b)-1] ^= 0xff
	require.NoError(t, os.WriteFile(p, b, 0o600))
	out := filepath.Join(t.TempDir(), "plain")
	_, _, err = runDecrypt(t, c.Password+"\n", in, "-o", out, "--password-stdin")
	require.Equal(t, exitDecryptCorrupt, exitCode(err))
	require.Contains(t, err.Error(), "report.txt")
	_, statErr := os.Stat(out)
	require.True(t, os.IsNotExist(statErr))
}

func TestDecryptCmd_UnsupportedFeatureExitsSevenBeforeAskingForAnything(t *testing.T) {
	in := filepath.Join(t.TempDir(), "in")
	require.NoError(t, os.CopyFS(in, os.DirFS(filepath.Join(decryptFixtures, "v3-names"))))
	mp := filepath.Join(in, ".filex-e2e.json")
	b, err := os.ReadFile(mp)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	m["req"] = []string{"names", "x-future"}
	b, _ = json.Marshal(m)
	require.NoError(t, os.WriteFile(mp, b, 0o600))
	// Empty stdin: if it asked for the password it would fail differently.
	_, _, err = runDecrypt(t, "", in, "-o", filepath.Join(t.TempDir(), "plain"), "--password-stdin")
	require.Equal(t, exitDecryptUnsupported, exitCode(err))
	require.Contains(t, err.Error(), "x-future")
}

func TestDecryptCmd_NoSecretIsNotAnAttempt(t *testing.T) {
	out := filepath.Join(t.TempDir(), "plain")
	_, _, err := runDecrypt(t, "", filepath.Join(decryptFixtures, "v1-0.30.1"), "-o", out, "--password-stdin")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no folder password given")
}

func TestDecryptCmd_TakesNoSecretFlag(t *testing.T) {
	// The secret has no flag and no environment variable. If one is ever
	// added, this is where somebody has to decide it on purpose.
	c := decryptCmd()
	for _, name := range []string{"password", "pass", "secret", "key", "recovery"} {
		require.Nil(t, c.Flags().Lookup(name), "--%s must not exist", name)
	}
}
