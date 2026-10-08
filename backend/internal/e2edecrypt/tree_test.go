package e2edecrypt

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The folders under testdata/folders were written by the BROWSER code
// (web/tests/lib/e2eFixtureFolders.test.ts): v0.30.1 and v0.47.0 folders by
// the frozen copies of those releases, and the encrypted-names folders by
// the current packages/core. Nothing here encrypts them — the claim under
// test is "what the browser wrote, the CLI reads".

type fixtureCase struct {
	Password    string            `json:"password"`
	OldPassword string            `json:"old_password"`
	RecoveryKey *string           `json:"recovery_key"`
	MarkerV     int               `json:"marker_v"`
	Names       bool              `json:"names"`
	Warnings    int               `json:"warnings"`
	Tree        map[string]string `json:"tree"`
}

func loadFixtures(t *testing.T) map[string]fixtureCase {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "folders", "secrets.json"))
	require.NoError(t, err)
	var s struct {
		Cases map[string]fixtureCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(b, &s))
	require.Len(t, s.Cases, 7)
	return s.Cases
}

func fixtureDir(name string) string { return filepath.Join("testdata", "folders", name) }

// treeOf is the plaintext tree of an output directory, in secrets.json's shape.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	require.NoError(t, err)
	return out
}

// noPartials asserts nothing was left next to out: no out, no out.partial-*.
func noPartials(t *testing.T, out string) {
	t.Helper()
	_, err := os.Lstat(out)
	require.True(t, os.IsNotExist(err), "output %s must not exist", out)
	left, _ := filepath.Glob(out + ".partial-*")
	require.Empty(t, left, "partial output left behind")
}

func TestDecryptTree_EveryFixtureByPassword(t *testing.T) {
	for name, c := range loadFixtures(t) {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "plain")
			res, err := DecryptTree(fixtureDir(name), OpenOptions{Out: out, GOOS: "linux"}, c.Password, false)
			require.NoError(t, err)
			require.Equal(t, c.Tree, treeOf(t, out))
			require.Len(t, res.Warnings, c.Warnings, "%v", res.Warnings)
			files, dirs := 0, 0
			for k := range c.Tree {
				if strings.HasSuffix(k, "/") {
					dirs++
				} else {
					files++
				}
			}
			require.Equal(t, files, res.Files)
			require.Equal(t, dirs, res.Dirs)
		})
	}
}

func TestDecryptTree_EveryFixtureByRecoveryKey(t *testing.T) {
	n := 0
	for name, c := range loadFixtures(t) {
		if c.RecoveryKey == nil {
			continue
		}
		n++
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "plain")
			// Typed the way a person types it: lower case, spaces for dashes.
			typed := strings.ToLower(strings.ReplaceAll(*c.RecoveryKey, "-", " "))
			_, err := DecryptTree(fixtureDir(name), OpenOptions{Out: out}, typed, true)
			require.NoError(t, err)
			require.Equal(t, c.Tree, treeOf(t, out))
		})
	}
	require.Equal(t, 6, n)
}

// A folder whose password was changed in the browser: the CLI opens it with
// the new password, and the old one is a wrong password — nothing written.
// v3-rekey-pending also proves the CLI reads a folder mid re-key: one file
// under the new folder key, one still under the previous key.
func TestDecryptTree_OldPasswordOpensNothing(t *testing.T) {
	n := 0
	for name, c := range loadFixtures(t) {
		if c.OldPassword == "" {
			continue
		}
		n++
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "plain")
			_, err := DecryptTree(fixtureDir(name), OpenOptions{Out: out}, c.OldPassword, false)
			require.ErrorIs(t, err, ErrWrongPassword)
			noPartials(t, out)
			_, err = DecryptTree(fixtureDir(name), OpenOptions{Out: out}, c.Password, false)
			require.NoError(t, err)
			require.Equal(t, c.Tree, treeOf(t, out))
		})
	}
	require.Equal(t, 2, n)
}

func TestDecryptTree_MarkerVersionsAndNames(t *testing.T) {
	for name, c := range loadFixtures(t) {
		b, err := os.ReadFile(filepath.Join(fixtureDir(name), MarkerName))
		require.NoError(t, err)
		m, err := ParseMarker(b)
		require.NoError(t, err, name)
		require.Equal(t, c.MarkerV, m.V, name)
		require.Equal(t, c.Names, m.HasNames(), name)
	}
}

func TestDecryptTree_WrongSecretsLeaveNothing(t *testing.T) {
	c := loadFixtures(t)["v3-names"]
	out := filepath.Join(t.TempDir(), "plain")
	_, err := DecryptTree(fixtureDir("v3-names"), OpenOptions{Out: out}, c.Password+"x", false)
	require.ErrorIs(t, err, ErrWrongPassword)
	noPartials(t, out)

	_, err = DecryptTree(fixtureDir("v3-names"), OpenOptions{Out: out}, "0000-0000-0000-0000-0000-0000-0000-0000", true)
	require.ErrorIs(t, err, ErrWrongRecoveryKey)
	noPartials(t, out)

	_, err = DecryptTree(fixtureDir("v3-names"), OpenOptions{Out: out}, "not a key", true)
	require.ErrorIs(t, err, ErrWrongRecoveryKey)

	// A v1 folder has no recovery key at all.
	_, err = DecryptTree(fixtureDir("v1-0.30.1"), OpenOptions{Out: out}, "0000-0000-0000-0000-0000-0000-0000-0000", true)
	require.ErrorIs(t, err, ErrWrongRecoveryKey)
	noPartials(t, out)
}

// copyTree copies a fixture into a scratch directory so a test can damage it.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "in")
	require.NoError(t, os.CopyFS(dst, os.DirFS(src)))
	return dst
}

// storedFiles lists the regular files of a fixture, marker and sidecars excluded.
func storedFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && d.Name() != MarkerName && !strings.HasSuffix(d.Name(), SidecarSuffix) {
			out = append(out, p)
		}
		return nil
	}))
	sort.Strings(out)
	return out
}

func TestDecryptTree_OneDamagedFileStopsEverything(t *testing.T) {
	c := loadFixtures(t)["v3-names"]
	in := copyTree(t, fixtureDir("v3-names"))
	var victim string
	for _, p := range storedFiles(t, in) {
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		if HasMagic(b) && len(b) > 200 {
			b[150] ^= 0x01
			require.NoError(t, os.WriteFile(p, b, 0o600))
			victim = p
			break
		}
	}
	require.NotEmpty(t, victim)
	out := filepath.Join(t.TempDir(), "plain")
	_, err := DecryptTree(in, OpenOptions{Out: out}, c.Password, false)
	var ce *CorruptError
	require.ErrorAs(t, err, &ce)
	require.ErrorIs(t, err, ErrContent)
	// The message names the file: its plaintext path and its stored name.
	require.Contains(t, ce.Path, filepath.Base(victim))
	noPartials(t, out)
}

func TestDecryptTree_MissingSidecarIsAnError(t *testing.T) {
	c := loadFixtures(t)["v3-names"]
	in := copyTree(t, fixtureDir("v3-names"))
	sidecars, err := filepath.Glob(filepath.Join(in, "*"+SidecarSuffix))
	require.NoError(t, err)
	require.NotEmpty(t, sidecars)
	require.NoError(t, os.Remove(sidecars[0]))
	out := filepath.Join(t.TempDir(), "plain")
	_, err = DecryptTree(in, OpenOptions{Out: out}, c.Password, false)
	var ce *CorruptError
	require.ErrorAs(t, err, &ce)
	require.Contains(t, err.Error(), "sidecar")
	noPartials(t, out)
}

func TestDecryptTree_StrayIsCopiedWithWarnings(t *testing.T) {
	c := loadFixtures(t)["v3-names"]
	out := filepath.Join(t.TempDir(), "plain")
	res, err := DecryptTree(fixtureDir("v3-names"), OpenOptions{Out: out}, c.Password, false)
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(out, "stray.txt"))
	require.NoError(t, err)
	require.Equal(t, "written over WebDAV\n", string(b))
	joined := strings.Join(res.Warnings, "\n")
	require.Contains(t, joined, "stray.txt: this name was never encrypted")
	require.Contains(t, joined, "stray.txt: this file was never encrypted")
}

func TestDecryptTree_UnsupportedFeatureIsRefusedBeforeThePassword(t *testing.T) {
	in := copyTree(t, fixtureDir("v3-names"))
	mp := filepath.Join(in, MarkerName)
	b, err := os.ReadFile(mp)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	// "vault" was the stand-in for an unknown feature until level 3 made it
	// a known one (#94).
	m["req"] = []string{"names", "x-future"}
	b, err = json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(mp, b, 0o600))

	_, err = Open(in, OpenOptions{Out: filepath.Join(t.TempDir(), "plain")})
	var ue *UnsupportedError
	require.ErrorAs(t, err, &ue)
	require.Equal(t, []string{"x-future"}, ue.Features)
	require.Contains(t, err.Error(), "newer filex")
}

func TestDecryptTree_RefusesAnExistingOutput(t *testing.T) {
	out := t.TempDir()
	_, err := Open(fixtureDir("v2-0.47.0"), OpenOptions{Out: out})
	require.ErrorIs(t, err, ErrOutputExists)
}

// zipDir zips root, optionally under a top-level folder, the way a browser
// download of the folder looks.
func zipDir(t *testing.T, root, prefix string) string {
	t.Helper()
	zp := filepath.Join(t.TempDir(), "download.zip")
	f, err := os.Create(zp)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	require.NoError(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		name := prefix + filepath.ToSlash(rel)
		if d.IsDir() {
			_, err := zw.Create(name + "/")
			return err
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(w, src)
		return err
	}))
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	return zp
}

func TestDecryptTree_ZipInput(t *testing.T) {
	c := loadFixtures(t)["v3-names"]
	for _, prefix := range []string{"", "Kasa/"} {
		zp := zipDir(t, fixtureDir("v3-names"), prefix)
		res, err := DecryptTree(zp, OpenOptions{}, c.Password, false)
		require.NoError(t, err, "prefix %q", prefix)
		require.Equal(t, strings.TrimSuffix(zp, ".zip")+"-decrypted", res.Out)
		require.Equal(t, c.Tree, treeOf(t, res.Out), "prefix %q", prefix)
		require.NoError(t, os.RemoveAll(res.Out))
	}
}

func TestDecryptTree_ZipSlipIsRefused(t *testing.T) {
	zp := filepath.Join(t.TempDir(), "evil.zip")
	f, err := os.Create(zp)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	w, err := zw.Create("../escape.txt")
	require.NoError(t, err)
	_, _ = w.Write([]byte("x"))
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	_, err = Open(zp, OpenOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsafe entry")
	_, statErr := os.Stat(filepath.Join(filepath.Dir(zp), "..", "escape.txt"))
	require.True(t, errors.Is(statErr, fs.ErrNotExist))
}

func TestDecryptTree_SubfolderWithExplicitMarker(t *testing.T) {
	// A subfolder downloaded on its own carries no marker; its entries'
	// names are still ciphertext and decrypt with the root's marker.
	c := loadFixtures(t)["v3-names"]
	root := fixtureDir("v3-names")
	var sub string
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	k, err := func() (*Keys, error) {
		b, _ := os.ReadFile(filepath.Join(root, MarkerName))
		m, _ := ParseMarker(b)
		return m.UnlockPassword(c.Password)
	}()
	require.NoError(t, err)
	for _, e := range entries {
		if p, st := k.Names.DecryptStoredName(e.Name(), k.Names.RootID, nil); e.IsDir() && st == NameDecrypted && p == "Sözleşmeler" {
			sub = filepath.Join(root, e.Name())
		}
	}
	require.NotEmpty(t, sub)

	out := filepath.Join(t.TempDir(), "plain")
	_, err = Open(sub, OpenOptions{Out: out})
	require.ErrorIs(t, err, ErrNoMarker)

	_, err = DecryptTree(sub, OpenOptions{Out: out, MarkerPath: filepath.Join(root, MarkerName)}, c.Password, false)
	require.NoError(t, err)
	got := treeOf(t, out)
	want := map[string]string{}
	for k, v := range c.Tree {
		if rest, ok := strings.CutPrefix(k, "Sözleşmeler/"); ok && rest != "" {
			want[rest] = v
		}
	}
	require.Equal(t, want, got)
}

func TestDecryptTree_SingleFile(t *testing.T) {
	c := loadFixtures(t)["v3-names"]
	root := fixtureDir("v3-names")
	b, _ := os.ReadFile(filepath.Join(root, MarkerName))
	m, err := ParseMarker(b)
	require.NoError(t, err)
	k, err := m.UnlockPassword(c.Password)
	require.NoError(t, err)
	enc, err := k.Names.EncryptName("Bütçe 2027 — İzmir.xlsx", k.Names.RootID, nil)
	require.NoError(t, err)

	// Next to its marker: found without --marker.
	out := filepath.Join(t.TempDir(), "one")
	res, err := DecryptTree(filepath.Join(root, enc.Stored), OpenOptions{Out: out}, c.Password, false)
	require.NoError(t, err)
	require.Equal(t, 1, res.Files)
	require.Equal(t, map[string]string{"Bütçe 2027 — İzmir.xlsx": c.Tree["Bütçe 2027 — İzmir.xlsx"]}, treeOf(t, out))

	// Copied somewhere else: needs --marker.
	lone := filepath.Join(t.TempDir(), enc.Stored)
	data, _ := os.ReadFile(filepath.Join(root, enc.Stored))
	require.NoError(t, os.WriteFile(lone, data, 0o600))
	_, err = Open(lone, OpenOptions{Out: filepath.Join(t.TempDir(), "x")})
	require.ErrorIs(t, err, ErrNoMarker)
	_, err = DecryptTree(lone, OpenOptions{Out: filepath.Join(t.TempDir(), "x"), MarkerPath: filepath.Join(root, MarkerName)}, c.Password, false)
	require.NoError(t, err)
}

func TestDecryptTree_UnsafeDecryptedNameIsAnError(t *testing.T) {
	// A name the browser would never encrypt ("../x"), sealed by hand with
	// the folder's real name key: the key holder is the only one who can
	// make it, and the CLI still refuses to write it.
	c := loadFixtures(t)["v3-names"]
	in := copyTree(t, fixtureDir("v3-names"))
	b, _ := os.ReadFile(filepath.Join(in, MarkerName))
	m, _ := ParseMarker(b)
	k, err := m.UnlockPassword(c.Password)
	require.NoError(t, err)
	evil := b64urlEncode(k.Names.siv.seal([]byte("../escape.txt"), k.Names.RootID))
	require.NoError(t, os.WriteFile(filepath.Join(in, evil), []byte("x"), 0o600))
	out := filepath.Join(t.TempDir(), "plain")
	_, err = DecryptTree(in, OpenOptions{Out: out}, c.Password, false)
	var ce *CorruptError
	require.ErrorAs(t, err, &ce)
	require.Contains(t, err.Error(), "not safe")
	noPartials(t, out)
}

func TestSafeOutputName_Windows(t *testing.T) {
	cases := map[string]string{
		"rapor.txt":   "rapor.txt",
		"a:b.txt":     "a_b.txt",
		"soru?.md":    "soru_.md",
		"sonnokta.":   "sonnokta_",
		"boşluk ":     "boşluk_",
		"CON":         "_CON",
		"con.txt":     "_con.txt",
		"COM1.log":    "_COM1.log",
		"console.txt": "console.txt",
		`a\b`:         "a_b",
	}
	for in, want := range cases {
		require.Equal(t, want, safeOutputName(in, "windows"), in)
		require.Equal(t, in, safeOutputName(in, "linux"), in)
	}
}

func TestOutputName_CollisionsIgnoringCase(t *testing.T) {
	j := &Job{goos: "linux", res: &Result{}}
	taken := map[string]bool{}
	require.Equal(t, "Rapor.txt", j.outputName("Rapor.txt", taken, "Rapor.txt"))
	require.Equal(t, "rapor (2).txt", j.outputName("rapor.txt", taken, "rapor.txt"))
	require.Equal(t, "RAPOR (3).txt", j.outputName("RAPOR.txt", taken, "RAPOR.txt"))
	require.Equal(t, ".gizli", j.outputName(".gizli", taken, ".gizli"))
	require.Equal(t, ".GIZLI (2)", j.outputName(".GIZLI", taken, ".GIZLI"))
	require.Len(t, j.res.Warnings, 3)
}

// storedPathOf finds the stored (on-disk) relative path of a plaintext path
// inside a names-encrypted folder, the way the explorer would.
func storedPathOf(t *testing.T, k *NameKey, root, plainRel string) string {
	t.Helper()
	dir, id, rel := root, k.RootID, ""
	for _, seg := range strings.Split(plainRel, "/") {
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		found := false
		for _, e := range entries {
			d := dir
			p, st := k.DecryptStoredName(e.Name(), id, func(name string) ([]byte, bool) {
				b, err := os.ReadFile(filepath.Join(d, name))
				return b, err == nil
			})
			if st == NameDecrypted && p == seg {
				id = k.EffectiveDirID(id, e.Name())
				dir = filepath.Join(dir, e.Name())
				rel = joinRel(rel, e.Name())
				found = true
				break
			}
		}
		require.True(t, found, "%s: no %q", plainRel, seg)
	}
	return rel
}

func TestDecryptTree_RecoversNamesMovedOutsideFilex(t *testing.T) {
	// A WebDAV client moved a file and a folder to other folders of the same
	// encrypted folder, names untouched. Each name is sealed for the folder it
	// came from; the CLI knows every folder id and recovers both.
	c := loadFixtures(t)["v3-names"]
	in := copyTree(t, fixtureDir("v3-names"))
	b, _ := os.ReadFile(filepath.Join(in, MarkerName))
	m, err := ParseMarker(b)
	require.NoError(t, err)
	k, err := m.UnlockPassword(c.Password)
	require.NoError(t, err)

	fatura := storedPathOf(t, k.Names, in, "Sözleşmeler/2024/fatura.pdf")
	eski := storedPathOf(t, k.Names, in, "Sözleşmeler/Eski")
	require.NoError(t, os.Rename(filepath.Join(in, filepath.FromSlash(fatura)), filepath.Join(in, filepath.Base(fatura))))
	require.NoError(t, os.Rename(filepath.Join(in, filepath.FromSlash(eski)), filepath.Join(in, filepath.Base(eski))))

	out := filepath.Join(t.TempDir(), "plain")
	res, err := DecryptTree(in, OpenOptions{Out: out, GOOS: "linux"}, c.Password, false)
	require.NoError(t, err)
	want := map[string]string{}
	for p, v := range c.Tree {
		switch {
		case p == "Sözleşmeler/2024/fatura.pdf":
			want["fatura.pdf"] = v
		case strings.HasPrefix(p, "Sözleşmeler/Eski/"):
			want[strings.TrimPrefix(p, "Sözleşmeler/")] = v
		default:
			want[p] = v
		}
	}
	require.Equal(t, want, treeOf(t, out))
	moved := 0
	for _, w := range res.Warnings {
		if strings.Contains(w, "moved outside filex; its name was recovered") {
			moved++
		}
	}
	require.Equal(t, 2, moved, "%v", res.Warnings)
}

func TestDecryptTree_PlaintextNamedSubfolderWithExplicitMarker(t *testing.T) {
	// v3-pending: "dir-plain" was not renamed yet, and what is inside it is
	// already sealed under the id it will carry — derived from its parent's
	// id and its name, which the CLI works out from where the marker is.
	c := loadFixtures(t)["v3-pending"]
	root := fixtureDir("v3-pending")
	out := filepath.Join(t.TempDir(), "plain")
	_, err := DecryptTree(filepath.Join(root, "dir-plain"), OpenOptions{Out: out, MarkerPath: filepath.Join(root, MarkerName)}, c.Password, false)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"renamed.txt": c.Tree["dir-plain/renamed.txt"]}, treeOf(t, out))
}
