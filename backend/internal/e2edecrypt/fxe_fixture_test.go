package e2edecrypt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Single encrypted files (.fxe) the BROWSER wrote
// (web/tests/lib/e2eFixtureFolders.test.ts → testdata/folders/fxe), opened
// by the CLI's decryptor: by password, by recovery key, to their original
// name — and a wrong key or a damaged file leaves nothing at all.

type fxeFixture struct {
	Password    string `json:"password"`
	RecoveryKey string `json:"recovery_key"`
	Name        string `json:"name"`
	Sha256      string `json:"sha256"`
	Size        int64  `json:"size"`
}

func loadFxeFixtures(t *testing.T) map[string]fxeFixture {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "folders", "secrets.json"))
	require.NoError(t, err)
	var s struct {
		Fxe map[string]fxeFixture `json:"fxe"`
	}
	require.NoError(t, json.Unmarshal(b, &s))
	require.Len(t, s.Fxe, 2)
	return s.Fxe
}

// copyFxe puts a fixture into a scratch directory of its own: without -o the
// plaintext lands next to the input.
func copyFxe(t *testing.T, stored string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	b, err := os.ReadFile(filepath.Join("testdata", "folders", "fxe", stored))
	require.NoError(t, err)
	path = filepath.Join(dir, stored)
	require.NoError(t, os.WriteFile(path, b, 0o600))
	return dir, path
}

func sha256File(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// onlyThis asserts dir holds exactly these names — no partial file, nothing
// else.
func onlyThis(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	require.ElementsMatch(t, names, got)
}

func TestFxeFixture_ToItsOriginalName(t *testing.T) {
	hidden := 0
	for stored, c := range loadFxeFixtures(t) {
		if strings.HasPrefix(stored, "encrypted-") {
			hidden++
		}
		t.Run(stored, func(t *testing.T) {
			for _, byRecovery := range []bool{false, true} {
				dir, in := copyFxe(t, stored)
				secret := c.Password
				if byRecovery {
					secret = c.RecoveryKey
				}
				res, err := DecryptTree(in, OpenOptions{GOOS: "linux"}, secret, byRecovery)
				require.NoError(t, err)
				require.Equal(t, filepath.Join(dir, c.Name), res.Out)
				require.Equal(t, 1, res.Files)
				require.Equal(t, c.Sha256, sha256File(t, res.Out))
				onlyThis(t, dir, stored, c.Name)
			}
		})
	}
	require.Equal(t, 1, hidden, "one fixture hides its name")
}

func TestFxeFixture_ExplicitOutput(t *testing.T) {
	for stored, c := range loadFxeFixtures(t) {
		_, in := copyFxe(t, stored)
		out := filepath.Join(t.TempDir(), "chosen.bin")
		res, err := DecryptTree(in, OpenOptions{Out: out}, c.Password, false)
		require.NoError(t, err)
		require.Equal(t, out, res.Out)
		require.Equal(t, c.Sha256, sha256File(t, out))
		// -o that exists is refused before a password is asked for
		_, err = Open(in, OpenOptions{Out: out})
		require.ErrorIs(t, err, ErrFileExists)
	}
}

func TestFxeFixture_WrongSecretOrDamageLeavesNothing(t *testing.T) {
	for stored, c := range loadFxeFixtures(t) {
		t.Run(stored, func(t *testing.T) {
			dir, in := copyFxe(t, stored)
			_, err := DecryptTree(in, OpenOptions{}, c.Password+"x", false)
			require.ErrorIs(t, err, ErrWrongPassword)
			_, err = DecryptTree(in, OpenOptions{}, "0000-0000-0000-0000-0000-0000-0000-0000", true)
			require.ErrorIs(t, err, ErrWrongRecoveryKey)
			onlyThis(t, dir, stored)

			b, err := os.ReadFile(in)
			require.NoError(t, err)
			for _, damage := range []struct {
				what string
				b    []byte
			}{
				{"a flipped bit in the last chunk", func() []byte { x := append([]byte{}, b...); x[len(x)-20] ^= 1; return x }()},
				{"cut at a chunk boundary", b[:len(b)-(len(b)-bodyStart(t, b))%(1024+16)-(1024+16)]},
				{"one byte short", b[:len(b)-1]},
				{"a chunk appended", append(append([]byte{}, b...), b[len(b)-40:]...)},
			} {
				require.NoError(t, os.WriteFile(in, damage.b, 0o600))
				_, err = DecryptTree(in, OpenOptions{}, c.Password, false)
				var ce *CorruptError
				require.ErrorAs(t, err, &ce, damage.what)
				require.ErrorIs(t, err, ErrContent, damage.what)
				onlyThis(t, dir, stored)
			}
		})
	}
}

// bodyStart is where a .fxe's STREAM body begins.
func bodyStart(t *testing.T, b []byte) int {
	t.Helper()
	require.True(t, HasFxeMagic(b))
	n := int(b[9])<<24 | int(b[10])<<16 | int(b[11])<<8 | int(b[12])
	return 13 + n
}

// A folder that holds .fxe files but no marker is not an encrypted folder:
// say what to do rather than "no marker".
func TestFxeFixture_FolderOfFxeSaysWhatToDo(t *testing.T) {
	_, err := Open(filepath.Join("testdata", "folders", "fxe"), OpenOptions{Out: filepath.Join(t.TempDir(), "x")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "one by one")
}

// A .fxe inside an encrypted folder is not the folder's to open: copied as
// it is, with a warning that names the tool that opens it.
func TestFxeFixture_InsideAnEncryptedFolderIsCopied(t *testing.T) {
	c := loadFixtures(t)["v2-0.47.0"]
	in := copyTree(t, fixtureDir("v2-0.47.0"))
	for stored := range loadFxeFixtures(t) {
		b, err := os.ReadFile(filepath.Join("testdata", "folders", "fxe", stored))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(in, stored), b, 0o600))
	}
	out := filepath.Join(t.TempDir(), "plain")
	res, err := DecryptTree(in, OpenOptions{Out: out}, c.Password, false)
	require.NoError(t, err)
	require.Contains(t, strings.Join(res.Warnings, "\n"), "single encrypted file (.fxe)")
}
