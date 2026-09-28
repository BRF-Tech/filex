package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// `filex decrypt <file>.fxe` end to end on the single encrypted files the
// browser wrote (internal/e2edecrypt/testdata/folders/fxe): the original
// name next to the input, exit 5 for a wrong password, 6 for a damaged file,
// and nothing written in either case.

type fxeSecret struct {
	Password string `json:"password"`
	Name     string `json:"name"`
}

func fxeSecrets(t *testing.T) map[string]fxeSecret {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(decryptFixtures, "secrets.json"))
	require.NoError(t, err)
	var s struct {
		Fxe map[string]fxeSecret `json:"fxe"`
	}
	require.NoError(t, json.Unmarshal(b, &s))
	require.NotEmpty(t, s.Fxe)
	return s.Fxe
}

func fxeCopy(t *testing.T, stored string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	b, err := os.ReadFile(filepath.Join(decryptFixtures, "fxe", stored))
	require.NoError(t, err)
	in := filepath.Join(dir, stored)
	require.NoError(t, os.WriteFile(in, b, 0o600))
	return dir, in
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestDecryptCmd_FxeToItsOriginalName(t *testing.T) {
	for stored, c := range fxeSecrets(t) {
		dir, in := fxeCopy(t, stored)
		stdout, stderr, err := runDecrypt(t, c.Password+"\n", in, "--password-stdin")
		require.NoError(t, err, stderr)
		require.Contains(t, stdout, "Decrypted into "+filepath.Join(dir, c.Name))
		require.ElementsMatch(t, []string{stored, c.Name}, dirNames(t, dir))
		require.NotContains(t, stdout+stderr, c.Password)

		// Again: the original name is taken now — refused, nothing overwritten.
		_, _, err = runDecrypt(t, c.Password+"\n", in, "--password-stdin")
		require.Error(t, err)
		require.ElementsMatch(t, []string{stored, c.Name}, dirNames(t, dir))
	}
}

func TestDecryptCmd_FxeWrongPasswordExitsFive(t *testing.T) {
	for stored := range fxeSecrets(t) {
		dir, in := fxeCopy(t, stored)
		_, _, err := runDecrypt(t, "not the password\n", in, "--password-stdin")
		require.Error(t, err)
		require.Equal(t, exitDecryptWrongSecret, exitCode(err))
		require.Equal(t, []string{stored}, dirNames(t, dir))
	}
}

func TestDecryptCmd_FxeDamagedExitsSix(t *testing.T) {
	for stored, c := range fxeSecrets(t) {
		dir, in := fxeCopy(t, stored)
		b, err := os.ReadFile(in)
		require.NoError(t, err)
		b[len(b)-10] ^= 0x40
		require.NoError(t, os.WriteFile(in, b, 0o600))
		_, _, err = runDecrypt(t, c.Password+"\n", in, "--password-stdin")
		require.Error(t, err)
		require.Equal(t, exitDecryptCorrupt, exitCode(err))
		require.Equal(t, []string{stored}, dirNames(t, dir), "no partial output")
	}
}

func TestDecryptCmd_StreamFolder(t *testing.T) {
	c := decryptSecrets(t)["v2-stream"]
	out := filepath.Join(t.TempDir(), "plain")
	_, stderr, err := runDecrypt(t, c.Password+"\n", filepath.Join(decryptFixtures, "v2-stream"), "-o", out, "--password-stdin")
	require.NoError(t, err, stderr)
	st, err := os.Stat(filepath.Join(out, "Büyük video.mp4"))
	require.NoError(t, err)
	require.EqualValues(t, 9000, st.Size())
	st, err = os.Stat(filepath.Join(out, "alt", "boş akış.bin"))
	require.NoError(t, err)
	require.EqualValues(t, 0, st.Size())
}
