package e2edecrypt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// `filex encrypt <local folder>` (encrypttree.go): what it writes, `filex
// decrypt` (this package's other half) opens - by password and by recovery
// key, at both levels - and a run stopped half-way continues.

const treePW = "a tree password, long enough"

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func plainTree(t *testing.T) (string, map[string]string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Kasa")
	files := map[string]string{
		"notlar.txt":                            "merhaba",
		"boş.txt":                               "",
		"Faturalar/2024.pdf":                    "%PDF 2024",
		"Faturalar/Eski yıllar/2019.md":         strings.Repeat("satır\n", 900),
		strings.Repeat("uzun ad ", 30) + ".txt": "long name",
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "boş klasör"), 0o755))
	want := map[string]string{"boş klasör/": "dir", "Faturalar/": "dir", "Faturalar/Eski yıllar/": "dir"}
	for rel, content := range files {
		want[rel] = sha256Hex([]byte(content))
	}
	return root, want
}

func encryptLocal(t *testing.T, in string, names bool) (*EncryptTreeResult, string) {
	t.Helper()
	job, err := OpenEncrypt(in, EncryptTreeOptions{Names: names})
	require.NoError(t, err)
	require.False(t, job.Resuming())
	key, err := job.Create(treePW)
	require.NoError(t, err)
	res, err := job.Run(context.Background(), nil)
	require.NoError(t, err)
	return res, key
}

func TestEncryptTree_Level1_DecryptsByPasswordAndByRecoveryKey(t *testing.T) {
	in, want := plainTree(t)
	res, key := encryptLocal(t, in, false)
	require.Equal(t, in+"-encrypted", res.Out)
	require.Equal(t, 5, res.Files)
	require.Equal(t, 3, res.Dirs)
	_, err := os.Lstat(res.Out + ".partial")
	require.True(t, os.IsNotExist(err), "the partial output became the output")

	m, err := os.ReadFile(filepath.Join(res.Out, MarkerName))
	require.NoError(t, err)
	mk, err := ParseMarker(m)
	require.NoError(t, err)
	require.Equal(t, 2, mk.V)
	// Level 1: the names are as they were, the contents are not.
	head, err := readHead(filepath.Join(res.Out, "notlar.txt"), 9)
	require.NoError(t, err)
	require.True(t, HasMagic(head))
	require.EqualValues(t, 1, head[8])

	for _, byRecovery := range []bool{false, true} {
		secret := treePW
		if byRecovery {
			secret = key
		}
		out := filepath.Join(t.TempDir(), "plain")
		got, err := DecryptTree(res.Out, OpenOptions{Out: out}, secret, byRecovery)
		require.NoError(t, err)
		require.Empty(t, got.Warnings)
		require.Equal(t, want, treeOf(t, out))
	}
	// The input is untouched.
	b, err := os.ReadFile(filepath.Join(in, "notlar.txt"))
	require.NoError(t, err)
	require.Equal(t, "merhaba", string(b))
}

func TestEncryptTree_Level2_NoPlaintextNameAndItDecrypts(t *testing.T) {
	in, want := plainTree(t)
	res, _ := encryptLocal(t, in, true)
	mk, err := os.ReadFile(filepath.Join(res.Out, MarkerName))
	require.NoError(t, err)
	m, err := ParseMarker(mk)
	require.NoError(t, err)
	require.True(t, m.HasNames())
	require.False(t, m.Names.Pending)
	err = filepath.WalkDir(res.Out, func(p string, d os.DirEntry, err error) error {
		require.NoError(t, err)
		if p == res.Out || d.Name() == MarkerName {
			return nil
		}
		require.NotEqual(t, KindPlain, ClassifyStoredName(d.Name()).Kind, "a plaintext name in the output: %s", p)
		for _, word := range []string{"Fatura", "notlar", "Eski", "uzun", "klasör"} {
			require.NotContains(t, d.Name(), word)
		}
		return nil
	})
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "plain")
	_, err = DecryptTree(res.Out, OpenOptions{Out: out}, treePW, false)
	require.NoError(t, err)
	require.Equal(t, want, treeOf(t, out))
}

func TestEncryptTree_AStoppedRunContinuesAndWritesOnlyWhatIsMissing(t *testing.T) {
	in, want := plainTree(t)
	for _, names := range []bool{false, true} {
		job, err := OpenEncrypt(in, EncryptTreeOptions{Names: names})
		require.NoError(t, err)
		_, err = job.Create(treePW)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		n := 0
		_, err = job.Run(ctx, func(string, int64) {
			n++
			if n == 3 {
				cancel()
			}
		})
		require.ErrorIs(t, err, ErrStopped)
		cancel()
		_, err = os.Stat(job.Out())
		require.True(t, os.IsNotExist(err), "nothing is published until the run is whole")
		parts, _ := filepath.Glob(filepath.Join(job.Out()+".partial", "*"+partSuffix))
		require.Empty(t, parts, "the file in flight is discarded")

		again, err := OpenEncrypt(in, EncryptTreeOptions{})
		require.NoError(t, err)
		require.True(t, again.Resuming())
		require.Equal(t, names, again.Names(), "the level is the key file's")
		require.ErrorIs(t, again.Unlock(treePW+"x", false), ErrWrongPassword)
		require.NoError(t, again.Unlock(treePW, false))
		var seen []string
		res, err := again.Run(context.Background(), func(rel string, _ int64) { seen = append(seen, rel) })
		require.NoError(t, err)
		require.Equal(t, 5, res.Files)
		require.Equal(t, 2, res.Kept, "the two files written before the stop are kept")
		require.Len(t, seen, 3)

		out := filepath.Join(t.TempDir(), "plain")
		_, err = DecryptTree(res.Out, OpenOptions{Out: out}, treePW, false)
		require.NoError(t, err)
		require.Equal(t, want, treeOf(t, out))
		require.NoError(t, os.RemoveAll(res.Out))
	}
}

func TestEncryptTree_RefusesWhatTheBrowserRefuses(t *testing.T) {
	in, _ := plainTree(t)
	res, _ := encryptLocal(t, in, false)

	_, err := OpenEncrypt(res.Out, EncryptTreeOptions{Out: filepath.Join(t.TempDir(), "x")})
	require.ErrorIs(t, err, ErrAlreadyEncrypted)

	holder := filepath.Join(t.TempDir(), "Holder")
	require.NoError(t, os.MkdirAll(filepath.Join(holder, "inner"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(holder, "inner", MarkerName), []byte(`{}`), 0o644))
	_, err = OpenEncrypt(holder, EncryptTreeOptions{})
	require.ErrorIs(t, err, ErrNestedEncrypted)

	_, err = OpenEncrypt(in, EncryptTreeOptions{Out: filepath.Join(in, "inside")})
	require.Error(t, err, "an output inside the input")
	_, err = OpenEncrypt(in, EncryptTreeOptions{Out: res.Out})
	require.ErrorIs(t, err, ErrOutputExists)

	job, err := OpenEncrypt(in, EncryptTreeOptions{Out: filepath.Join(t.TempDir(), "o")})
	require.NoError(t, err)
	_, err = job.Create("short")
	require.Error(t, err, "the dialog's password rule")
}

func TestEncryptTree_ASymlinkIsNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows")
	}
	in, _ := plainTree(t)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("outside the folder"), 0o644))
	require.NoError(t, os.Symlink(secret, filepath.Join(in, "link.txt")))
	res, _ := encryptLocal(t, in, false)
	require.Len(t, res.Warnings, 1)
	require.Contains(t, res.Warnings[0], "link.txt")
	_, err := os.Lstat(filepath.Join(res.Out, "link.txt"))
	require.True(t, os.IsNotExist(err))
}

func TestEncryptTree_EscrowSlotWhenGivenAKey(t *testing.T) {
	in, _ := plainTree(t)
	job, err := OpenEncrypt(in, EncryptTreeOptions{EscrowPublicKey: "not a key"})
	require.NoError(t, err)
	_, err = job.Create(treePW)
	require.Error(t, err, "an escrow key that does not parse is refused, not silently dropped")
}
