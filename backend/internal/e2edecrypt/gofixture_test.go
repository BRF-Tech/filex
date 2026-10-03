package e2edecrypt

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Encrypted folders the GO writer made, frozen on disk for the BROWSER code:
// the mirror of testdata/folders (which the browser wrote for `filex
// decrypt`). web/tests/lib/e2eGoFixtureFolders.test.ts opens every one of
// them with packages/core - by password and by recovery key - and checks the
// plaintext tree against secrets.json. So "what `filex encrypt` writes, the
// browser opens" is a test on committed bytes, not a promise.
//
//	go-v2         level 1, from a folder on disk (EncryptTreeJob), plus a
//	              STREAM (0x02) file at a chunk size of 2^10 the header records
//	go-v3-names   level 2: names sealed per folder, derived folder ids, a long
//	              name with its sidecar, an empty file, a dotfile
//	go-v3-conv    a key file mid-conversion (StartConversion): one file
//	              encrypted, one still plaintext
//
// Regenerating is deliberate, never incidental:
//
//	FILEX_WRITE_E2E_FIXTURES=1 go test -run TestGoFixtures_Write ./internal/e2edecrypt/
//
// The second test runs every time: the committed folders still open with
// THIS package's reader.

var goFixtureRoot = filepath.Join("testdata", "folders-go")

type goFixtureCase struct {
	Password    string            `json:"password"`
	RecoveryKey string            `json:"recovery_key"`
	MarkerV     int               `json:"marker_v"`
	Names       bool              `json:"names"`
	Conv        bool              `json:"conv"`
	Tree        map[string]string `json:"tree"`
}

type goFixtureSecrets struct {
	Comment string                   `json:"comment"`
	Cases   map[string]goFixtureCase `json:"cases"`
}

func goFixtureSource(t *testing.T, files map[string][]byte, dirs ...string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source")
	require.NoError(t, os.MkdirAll(root, 0o755))
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755))
	}
	for rel, b := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, b, 0o644))
	}
	return root
}

func treeWant(files map[string][]byte, dirs ...string) map[string]string {
	out := map[string]string{}
	for rel, b := range files {
		out[rel] = sha256Hex(b)
		for d := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel))); d != "."; d = filepath.ToSlash(filepath.Dir(filepath.FromSlash(d))) {
			out[d+"/"] = "dir"
		}
	}
	for _, d := range dirs {
		out[d+"/"] = "dir"
	}
	return out
}

func TestGoFixtures_Write(t *testing.T) {
	if os.Getenv("FILEX_WRITE_E2E_FIXTURES") == "" {
		t.Skip("set FILEX_WRITE_E2E_FIXTURES=1 to regenerate testdata/folders-go")
	}
	require.NoError(t, os.RemoveAll(goFixtureRoot))
	require.NoError(t, os.MkdirAll(goFixtureRoot, 0o755))
	cases := map[string]goFixtureCase{}

	encryptInto := func(name, pw string, names bool, files map[string][]byte, dirs ...string) (*EncryptTreeJob, string) {
		src := goFixtureSource(t, files, dirs...)
		job, err := OpenEncrypt(src, EncryptTreeOptions{Out: filepath.Join(goFixtureRoot, name), Names: names})
		require.NoError(t, err)
		key, err := job.Create(pw)
		require.NoError(t, err)
		_, err = job.Run(context.Background(), nil)
		require.NoError(t, err)
		return job, key
	}

	// go-v2: level 1, and a STREAM file next to the one-shot ones.
	v2 := map[string][]byte{
		"notlar.txt":             []byte("Go wrote this, the browser reads it\n"),
		"boş.txt":                {},
		"Faturalar/2024.pdf":     pattern(70_000, 2),
		"Faturalar/İzmir ödeme":  []byte("ödendi\n"),
		"Faturalar/Eski/2019.md": bytes.Repeat([]byte("satır\n"), 300),
	}
	// No empty folder: git cannot hold one, and the committed copy would not
	// be the tree secrets.json describes.
	job, key := encryptInto("go-v2", "fixture-go-v2-password", false, v2)
	stream := pattern(9000, 21)
	f, err := os.Create(filepath.Join(goFixtureRoot, "go-v2", "Büyük video.mp4"))
	require.NoError(t, err)
	require.NoError(t, encryptStreamFile(f, bytes.NewReader(stream), int64(len(stream)), job.keys.FMK, 10, nil))
	require.NoError(t, f.Close())
	want := treeWant(v2)
	want["Büyük video.mp4"] = sha256Hex(stream)
	cases["go-v2"] = goFixtureCase{Password: "fixture-go-v2-password", RecoveryKey: key, MarkerV: 2, Tree: want}

	// go-v3-names: level 2.
	long := strings.Repeat("ş", 100) + ".txt"
	v3 := map[string][]byte{
		"Bütçe 2027 - İzmir.xlsx":         pattern(5000, 3),
		"Sözleşmeler/Kira sözleşmesi.pdf": pattern(12_345, 4),
		"Sözleşmeler/2024/fatura.pdf":     pattern(900, 6),
		"Sözleşmeler/2025/fatura.pdf":     pattern(901, 7),
		"boş.txt":                         {},
		".gizli":                          []byte("dotfile\n"),
		long:                              []byte("uzun adlı dosya\n"),
	}
	_, key = encryptInto("go-v3-names", "fixture-go-v3-password", true, v3)
	cases["go-v3-names"] = goFixtureCase{Password: "fixture-go-v3-password", RecoveryKey: key, MarkerV: 3, Names: true, Tree: treeWant(v3)}

	// go-v3-conv: mid-conversion. One file encrypted, one not reached yet.
	made, err := CreateFolder("fixture-go-conv-password", CreateOptions{})
	require.NoError(t, err)
	marker, err := StartConversion(made.Marker, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), &Cleanup{Versions: true, Trash: true})
	require.NoError(t, err)
	dir := filepath.Join(goFixtureRoot, "go-v3-conv")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, MarkerName), marker, 0o644))
	done := []byte("encrypted by the conversion\n")
	ct, err := EncryptContent(made.Keys.FMK, done, nil)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "done.txt"), ct, 0o644))
	notYet := []byte("not reached yet, still plaintext\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "not-yet.txt"), notYet, 0o644))
	cases["go-v3-conv"] = goFixtureCase{
		Password: "fixture-go-conv-password", RecoveryKey: made.RecoveryKey, MarkerV: 3, Conv: true,
		Tree: map[string]string{"done.txt": sha256Hex(done), "not-yet.txt": sha256Hex(notYet)},
	}

	out, err := json.MarshalIndent(goFixtureSecrets{
		Comment: "Test fixtures only. Written by backend/internal/e2edecrypt/gofixture_test.go (FILEX_WRITE_E2E_FIXTURES=1). " +
			"Passwords and recovery keys here open nothing but these folders.",
		Cases: cases,
	}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(goFixtureRoot, "secrets.json"), append(out, '\n'), 0o644))
}

func loadGoFixtures(t *testing.T) goFixtureSecrets {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goFixtureRoot, "secrets.json"))
	require.NoError(t, err, "testdata/folders-go is not generated: FILEX_WRITE_E2E_FIXTURES=1 go test -run TestGoFixtures_Write ./internal/e2edecrypt/")
	var s goFixtureSecrets
	require.NoError(t, json.Unmarshal(b, &s))
	return s
}

func TestGoFixtures_OpenWithThisBuild(t *testing.T) {
	s := loadGoFixtures(t)
	names := make([]string, 0, len(s.Cases))
	for n := range s.Cases {
		names = append(names, n)
	}
	sort.Strings(names)
	require.Equal(t, []string{"go-v2", "go-v3-conv", "go-v3-names"}, names)
	for name, c := range s.Cases {
		mk, err := os.ReadFile(filepath.Join(goFixtureRoot, name, MarkerName))
		require.NoError(t, err)
		m, err := ParseMarker(mk)
		require.NoError(t, err, name)
		require.Equal(t, c.MarkerV, m.V, name)
		require.Equal(t, c.Names, m.HasNames(), name)
		require.Equal(t, c.Conv, m.ConvPending, name)
		for _, byRecovery := range []bool{false, true} {
			secret := c.Password
			if byRecovery {
				secret = c.RecoveryKey
			}
			out := filepath.Join(t.TempDir(), "plain")
			res, err := DecryptTree(filepath.Join(goFixtureRoot, name), OpenOptions{Out: out}, secret, byRecovery)
			require.NoError(t, err, name)
			got := treeOf(t, out)
			require.Equal(t, c.Tree, got, name)
			if c.Conv {
				require.Len(t, res.Warnings, 1, "the plaintext file not reached yet is said")
			}
		}
	}
}
