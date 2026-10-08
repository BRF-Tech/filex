package e2edecrypt

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

// Vaults one implementation wrote, frozen on disk for the other - the vault
// level's twin of testdata/folders (the browser's folders, read here) and
// testdata/folders-go (Go's folders, read by the browser):
//
//	testdata/vault-go/go-vault    written by THIS package's writer
//	                              (TestVaultGoFixture_Write); the browser opens it
//	                              in web/tests/lib/e2eVaultCrossFixtures.test.ts
//	testdata/vault-web/web-vault  written by packages/core's writer (that
//	                              vitest file, under FILEX_WRITE_E2E_FIXTURES=1);
//	                              opened here by TestVaultWebFixture_OpenWithGo
//
// The vectors already hold both writers to one reference byte for byte; these
// hold each reader to what the OTHER writer really wrote - its random ids, its
// key file, its padding, a repack - with the same operations on both sides
// (vaultCrossOps below, crossOps in the vitest file): four generations, Turkish
// names in byte order (`Zebra` < `alfa` < `cay.txt` < `Çay.txt`; no two names
// that differ only in case, so the copy decrypts on Windows too), an empty
// file, a file over three packs, a folder moved with what is in it, and a
// repack of the half-empty packs.
//
// Regenerating is deliberate, never incidental:
//
//	FILEX_WRITE_E2E_FIXTURES=1 go test -run TestVaultGoFixture_Write ./internal/e2edecrypt/
//
// The reading tests run every time.

const vaultCrossPackLog2 = 16

var (
	vaultGoFixtureRoot  = filepath.Join("testdata", "vault-go")
	vaultWebFixtureRoot = filepath.Join("testdata", "vault-web")
)

// vaultCrossEntry is one entry of a generation's tree in secrets.json: a
// folder ("dir": true) or a file (its size and SHA-256).
type vaultCrossEntry struct {
	Dir    bool   `json:"dir,omitempty"`
	MTime  int64  `json:"mtime"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

type vaultCrossCase struct {
	Password    string `json:"password"`
	RecoveryKey string `json:"recovery_key"`
	PackLog2    int    `json:"pack_log2"`
	Latest      uint64 `json:"latest"`
	// Generations: "2", "3", "4" → path → entry. Generation 1 is empty.
	Generations map[string]map[string]vaultCrossEntry `json:"generations"`
	// Repacked: the packs generation 4 moved into new ones.
	Repacked int `json:"repacked"`
}

type vaultCrossSecrets struct {
	Comment string                    `json:"comment"`
	Cases   map[string]vaultCrossCase `json:"cases"`
}

// The operations of generations 2 and 3, the same on both sides; generation 4
// is the repack the collector asks for after 3.
const vaultCrossT0 = int64(1791277200000)

type vaultCrossOp struct {
	op, path, to string
	mtime        int64
	data         []byte
}

func vaultCrossOps(gen int, who string) []vaultCrossOp {
	switch gen {
	case 2:
		return []vaultCrossOp{
			{op: "mkdir", path: "Belgeler", mtime: vaultCrossT0},
			{op: "mkdir", path: "Belgeler/Arşiv", mtime: vaultCrossT0 + 1000},
			{op: "mkdir", path: "Zebra", mtime: vaultCrossT0 + 2000},
			{op: "mkdir", path: "alfa", mtime: vaultCrossT0 + 3000},
			{op: "write", path: "not.txt", mtime: vaultCrossT0 + 4000, data: []byte(who + " yazdı, öbürü okur.\n")},
			{op: "write", path: "Belgeler/boş.txt", mtime: vaultCrossT0 + 5000},
			{op: "write", path: "Belgeler/Arşiv/rapor.bin", mtime: vaultCrossT0 + 6000, data: pattern(150_000, 5)},
			{op: "write", path: "cay.txt", mtime: vaultCrossT0 + 7000, data: []byte("c\n")},
			{op: "write", path: "Çay.txt", mtime: vaultCrossT0 + 8000, data: []byte("Ç\n")},
			{op: "write", path: "Zebra/ü.md", mtime: vaultCrossT0 + 10000, data: pattern(700, 9)},
		}
	case 3:
		return []vaultCrossOp{
			{op: "delete", path: "Belgeler/Arşiv/rapor.bin"},
			{op: "move", path: "Belgeler/boş.txt", to: "boş.txt"},
			{op: "write", path: "not.txt", mtime: vaultCrossT0 + 60000, data: []byte(who + " yazdı; bu ikinci sürüm.\n")},
			{op: "mkdir", path: "Yeni", mtime: vaultCrossT0 + 61000},
			{op: "move", path: "Zebra", to: "Yeni/Zebra"},
		}
	}
	return nil
}

// vaultCrossTree is a generation's tree in secrets.json's shape, read back
// through the reader (so a file's SHA-256 is of what decrypts).
func vaultCrossTree(t *testing.T, src VaultSource, k *VaultKeys, idx *VaultIndex) map[string]vaultCrossEntry {
	t.Helper()
	out := map[string]vaultCrossEntry{}
	r := NewVaultReader(src, k)
	require.NoError(t, idx.Tree.Walk(func(n *VaultNode, _ int) error {
		if n.Dir {
			out[n.Path()] = vaultCrossEntry{Dir: true, MTime: n.MTime}
			return nil
		}
		var buf bytes.Buffer
		if _, err := r.WriteTo(context.Background(), n, &buf); err != nil {
			return err
		}
		out[n.Path()] = vaultCrossEntry{MTime: n.MTime, Size: n.Size, SHA256: sha256Hex(buf.Bytes())}
		return nil
	}))
	return out
}

func TestVaultGoFixture_Write(t *testing.T) {
	if os.Getenv("FILEX_WRITE_E2E_FIXTURES") == "" {
		t.Skip("set FILEX_WRITE_E2E_FIXTURES=1 to regenerate testdata/vault-go")
	}
	ctx := context.Background()
	const password = "fixture-go-vault-password"
	require.NoError(t, os.RemoveAll(vaultGoFixtureRoot))
	dir := filepath.Join(vaultGoFixtureRoot, "go-vault")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	// The key file: a level-1 key file's slots, made a vault's (v3, req
	// ["vault"], the vault block). Go has no "new vault" of its own - the
	// browser and the server's create make them - so the fixture builds it
	// from the same slots.
	made, err := CreateFolder(password, CreateOptions{})
	require.NoError(t, err)
	var kf map[string]any
	require.NoError(t, json.Unmarshal(made.Marker, &kf))
	var id [16]byte
	_, err = rand.Read(id[:])
	require.NoError(t, err)
	kf["v"] = 3
	kf["req"] = []string{e2e.VaultFeature}
	kf["vault"] = map[string]any{"v": e2e.VaultFormat, "id": base64.RawURLEncoding.EncodeToString(id[:]), "pack": vaultCrossPackLog2}
	marker, err := json.Marshal(kf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, MarkerName), marker, 0o644))
	m, err := ParseMarker(marker)
	require.NoError(t, err)
	require.True(t, m.IsVault())
	keys, err := m.UnlockPassword(password)
	require.NoError(t, err)
	k, err := NewVaultKeys(keys.FMK, m.Vault.ID)
	require.NoError(t, err)

	sink := func(_ context.Context, id [16]byte, b []byte) error {
		p := filepath.Join(dir, filepath.FromSlash(e2e.VaultPackPath(id)))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, b, 0o644)
	}
	src := NewDirVaultSource(os.DirFS(dir), vaultCrossPackLog2)
	commit := func(base *VaultIndex, apply func(w *VaultWriter)) *VaultIndex {
		w, err := NewVaultWriter(VaultWriterConfig{Keys: k, PackLog2: vaultCrossPackLog2, Base: base, Sink: sink})
		require.NoError(t, err)
		apply(w)
		c, err := w.Commit(ctx)
		require.NoError(t, err)
		p := filepath.Join(dir, filepath.FromSlash(e2e.VaultIndexPath(c.Index.Generation)))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, c.File, 0o644))
		idx, err := OpenVaultIndex(k, vaultCrossPackLog2, c.Index.Generation, c.File)
		require.NoError(t, err)
		return idx
	}
	run := func(w *VaultWriter, ops []vaultCrossOp) {
		for _, o := range ops {
			switch o.op {
			case "mkdir":
				require.NoError(t, w.Mkdir(o.path, o.mtime), o.path)
			case "write":
				_, err := w.Write(ctx, o.path, o.mtime, bytes.NewReader(o.data), int64(len(o.data)))
				require.NoError(t, err, o.path)
			case "delete":
				require.NoError(t, w.Delete(o.path), o.path)
			case "move":
				require.NoError(t, w.Move(o.path, o.to), o.path)
			}
		}
	}

	c := vaultCrossCase{Password: password, RecoveryKey: made.RecoveryKey, PackLog2: vaultCrossPackLog2, Generations: map[string]map[string]vaultCrossEntry{}}
	idx := commit(nil, func(*VaultWriter) {})
	require.EqualValues(t, 1, idx.Generation)
	for _, gen := range []int{2, 3} {
		idx = commit(idx, func(w *VaultWriter) { run(w, vaultCrossOps(gen, "Go")) })
		c.Generations[strconv.Itoa(gen)] = vaultCrossTree(t, src, k, idx)
	}
	s := PlanVaultRepack(idx, vaultCrossPackLog2)
	require.NotEmpty(t, s, "generation 3 leaves half-empty packs to repack")
	idx = commit(idx, func(w *VaultWriter) { require.NoError(t, w.Repack(ctx, s, src.ReadPack)) })
	c.Generations["4"] = vaultCrossTree(t, src, k, idx)
	c.Latest, c.Repacked = idx.Generation, len(s)

	out, err := json.MarshalIndent(vaultCrossSecrets{
		Comment: "Test fixtures only. Written by backend/internal/e2edecrypt/vault_crossfixture_test.go (FILEX_WRITE_E2E_FIXTURES=1). " +
			"The password and recovery key here open nothing but this vault.",
		Cases: map[string]vaultCrossCase{"go-vault": c},
	}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(vaultGoFixtureRoot, "secrets.json"), append(out, '\n'), 0o644))
}

func loadVaultCross(t *testing.T, root, regen string) vaultCrossSecrets {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "secrets.json"))
	require.NoError(t, err, "%s is not generated: %s", root, regen)
	var s vaultCrossSecrets
	require.NoError(t, json.Unmarshal(b, &s))
	return s
}

// checkVaultCross opens one cross fixture with this package: every kept
// generation's tree, by password and by recovery key, the latest through
// `filex decrypt`'s DecryptTree, ranges over a pack boundary, and every pack's
// header.
func checkVaultCross(t *testing.T, root string, s vaultCrossSecrets) {
	t.Helper()
	require.NotEmpty(t, s.Cases)
	names := make([]string, 0, len(s.Cases))
	for n := range s.Cases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		c := s.Cases[name]
		dir := filepath.Join(root, name)
		mk, err := os.ReadFile(filepath.Join(dir, MarkerName))
		require.NoError(t, err)
		m, err := ParseMarker(mk)
		require.NoError(t, err, name)
		require.True(t, m.IsVault(), name)
		require.Equal(t, c.PackLog2, m.Vault.PackLog2, name)

		for _, byRecovery := range []bool{false, true} {
			var keys *Keys
			if byRecovery {
				keys, err = m.UnlockRecoveryKey(c.RecoveryKey)
			} else {
				keys, err = m.UnlockPassword(c.Password)
			}
			require.NoError(t, err, name)
			k, err := NewVaultKeys(keys.FMK, m.Vault.ID)
			require.NoError(t, err)
			src := NewDirVaultSource(os.DirFS(dir), c.PackLog2)

			st, err := LoadVault(context.Background(), src, k, c.PackLog2, VaultLoadOptions{})
			require.NoError(t, err, name)
			require.Equal(t, c.Latest, st.Index.Generation, name)
			require.NoError(t, st.Writable(), "%s: a vault the other side wrote is writable here", name)

			one, err := src.ReadIndex(context.Background(), 1)
			require.NoError(t, err)
			g1, err := OpenVaultIndex(k, c.PackLog2, 1, one)
			require.NoError(t, err, name)
			require.Zero(t, g1.Tree.Len(), "%s: generation 1 is the empty tree", name)
			require.Len(t, one, e2e.VaultIndexMinSize)

			for g, want := range c.Generations {
				gen, err := strconv.ParseUint(g, 10, 64)
				require.NoError(t, err)
				b, err := src.ReadIndex(context.Background(), gen)
				require.NoError(t, err, "%s generation %d", name, gen)
				idx, err := OpenVaultIndex(k, c.PackLog2, gen, b)
				require.NoError(t, err, "%s generation %d", name, gen)
				require.Equal(t, want, vaultCrossTree(t, src, k, idx), "%s generation %d", name, gen)
				if gen == 2 {
					// Bytes 65 400 to 65 600 of the file over three packs:
					// across the end of the first.
					n := idx.Tree.Lookup("Belgeler/Arşiv/rapor.bin")
					require.NotNil(t, n)
					got, err := NewVaultReader(src, k).ReadRange(context.Background(), n, 65_400, 65_600)
					require.NoError(t, err)
					require.Equal(t, pattern(150_000, 5)[65_400:65_600], got)
				}
			}

			if !byRecovery {
				out := filepath.Join(t.TempDir(), "plain")
				_, err := DecryptTree(dir, OpenOptions{Out: out}, c.Password, false)
				require.NoError(t, err, name)
				tree := treeOf(t, out)
				latest := c.Generations[strconv.FormatUint(c.Latest, 10)]
				require.Len(t, tree, len(latest), name)
				for p, e := range latest {
					if e.Dir {
						require.Equal(t, "dir", tree[p+"/"], "%s: %s", name, p)
					} else {
						require.Equal(t, e.SHA256, tree[p], "%s: %s", name, p)
					}
				}
			}

			packs, err := src.ListPacks(context.Background())
			require.NoError(t, err)
			require.NotEmpty(t, packs)
			for _, p := range packs {
				_, err := src.ReadPack(context.Background(), p.Pack, int64(e2e.VaultPackHeaderLen), 1)
				require.NoError(t, err, "%s: pack %x has a good header and size", name, p.Pack)
			}
		}
	}
}

// What this package wrote, this package still reads.
func TestVaultGoFixture_OpenWithThisBuild(t *testing.T) {
	s := loadVaultCross(t, vaultGoFixtureRoot, "FILEX_WRITE_E2E_FIXTURES=1 go test -run TestVaultGoFixture_Write ./internal/e2edecrypt/")
	require.Contains(t, s.Cases, "go-vault")
	checkVaultCross(t, vaultGoFixtureRoot, s)
}

// What the browser wrote, `filex decrypt` reads.
func TestVaultWebFixture_OpenWithGo(t *testing.T) {
	s := loadVaultCross(t, vaultWebFixtureRoot, "cd web && FILEX_WRITE_E2E_FIXTURES=1 npx vitest run tests/lib/e2eVaultCrossFixtures.test.ts")
	require.Contains(t, s.Cases, "web-vault")
	checkVaultCross(t, vaultWebFixtureRoot, s)
}
