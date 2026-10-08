package e2e

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The layout half of docs/E2E-VAULT-FORMAT.md's test vectors (item 7,
// padme_index_size) and the plaintext headers of the fixture vault, as
// gen_vault_vectors.mjs (node:crypto, neither implementation) wrote them. The
// server does no cryptography: what it checks of a vault is exactly what is
// tested here - names, sizes and headers.

const vaultTestdata = "../e2edecrypt/testdata"

type vaultVectors struct {
	FixtureDir string `json:"fixture_dir"`
	Secrets    struct {
		VaultID string `json:"vault_id"`
	} `json:"secrets"`
	PackLog2    int `json:"pack_log2"`
	Latest      int `json:"latest_generation"`
	Generations []struct {
		Generation   uint64 `json:"generation"`
		IndexPath    string `json:"index_path"`
		IndexSize    int64  `json:"index_size"`
		BodyLen      int64  `json:"body_len"`
		PacksWritten []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"packs_written"`
	} `json:"generations"`
	Layers struct {
		Padme []struct {
			BodyLen  int64 `json:"body_len"`
			MinLen   int64 `json:"min_len"`
			FileSize int64 `json:"file_size"`
		} `json:"padme_index_size"`
	} `json:"layers"`
}

func loadVaultVectors(t *testing.T) vaultVectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(vaultTestdata, "vault-vectors.json"))
	if err != nil {
		t.Fatalf("vectors: %v", err)
	}
	var v vaultVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("vectors: %v", err)
	}
	if len(v.Generations) != 3 || len(v.Layers.Padme) == 0 || v.FixtureDir == "" {
		t.Fatalf("the vectors read as empty: %d generations, %d padme cases", len(v.Generations), len(v.Layers.Padme))
	}
	return v
}

func fixturePath(v vaultVectors, rel string) string {
	return filepath.Join(vaultTestdata, filepath.FromSlash(v.FixtureDir), filepath.FromSlash(rel))
}

func mustID(t *testing.T, s string) [16]byte {
	t.Helper()
	id, ok := ParseVaultID(s)
	if !ok {
		t.Fatalf("not an id: %q", s)
	}
	return id
}

func TestVaultPadmeVectors(t *testing.T) {
	v := loadVaultVectors(t)
	for _, c := range v.Layers.Padme {
		if got := VaultIndexHeaderLen + c.BodyLen + 16; got != c.MinLen {
			t.Fatalf("body %d: minimum %d, vectors say %d", c.BodyLen, got, c.MinLen)
		}
		if got := VaultIndexFileSize(c.BodyLen); got != c.FileSize {
			t.Errorf("VaultIndexFileSize(%d) = %d, want %d", c.BodyLen, got, c.FileSize)
		}
		if c.FileSize > VaultIndexMinSize {
			if got := VaultPadme(c.MinLen); got != c.FileSize {
				t.Errorf("VaultPadme(%d) = %d, want %d", c.MinLen, got, c.FileSize)
			}
		}
		if c.FileSize <= VaultIndexReadMax && !ValidVaultIndexSize(c.FileSize) {
			t.Errorf("%d is a padded size and must be accepted", c.FileSize)
		}
		if ValidVaultIndexSize(c.FileSize + 1) {
			t.Errorf("%d is not a Padmé size and must be refused", c.FileSize+1)
		}
	}
	// A Padmé size is its own Padmé size; the bounds are 64 KiB and 64 MiB.
	for _, n := range []int64{65536, 67584, 1081344, 64 << 20} {
		if VaultPadme(n) != n || !ValidVaultIndexSize(n) {
			t.Errorf("%d must be a valid index size", n)
		}
	}
	for _, n := range []int64{0, 65535, 65537, 32768, 128 << 20} {
		if ValidVaultIndexSize(n) {
			t.Errorf("%d must not be a valid index size", n)
		}
	}
	if VaultPadme(0) != 0 || VaultPadme(1) != 1 || VaultPadme(2) != 2 || VaultPadme(3) != 3 {
		t.Error("the smallest lengths are their own Padmé size")
	}
}

func TestVaultFixtureHeadersAndNames(t *testing.T) {
	v := loadVaultVectors(t)
	packs := 0
	for _, g := range v.Generations {
		b, err := os.ReadFile(fixturePath(v, g.IndexPath))
		if err != nil {
			t.Fatalf("generation %d: %v", g.Generation, err)
		}
		if int64(len(b)) != g.IndexSize || !ValidVaultIndexSize(int64(len(b))) {
			t.Errorf("generation %d: %d bytes, vectors say %d", g.Generation, len(b), g.IndexSize)
		}
		if VaultIndexFileSize(g.BodyLen) != g.IndexSize {
			t.Errorf("generation %d: body of %d bytes gives %d, the file is %d", g.Generation, g.BodyLen, VaultIndexFileSize(g.BodyLen), g.IndexSize)
		}
		if err := CheckVaultIndexHeader(b, g.Generation); err != nil {
			t.Errorf("generation %d: %v", g.Generation, err)
		}
		if err := CheckVaultIndexHeader(b, g.Generation+1); !errors.Is(err, ErrVaultHeader) {
			t.Errorf("generation %d accepted under the name of %d", g.Generation, g.Generation+1)
		}
		kind, _, gen := ParseVaultPath(g.IndexPath)
		if kind != VaultPathIndex || gen != g.Generation {
			t.Errorf("ParseVaultPath(%q) = %v, %d", g.IndexPath, kind, gen)
		}
		if VaultIndexPath(g.Generation) != g.IndexPath {
			t.Errorf("VaultIndexPath(%d) = %q, want %q", g.Generation, VaultIndexPath(g.Generation), g.IndexPath)
		}
		for _, p := range g.PacksWritten {
			packs++
			id := mustID(t, p.ID)
			if VaultPackPath(id) != p.Path {
				t.Errorf("VaultPackPath(%s) = %q, want %q", p.ID, VaultPackPath(id), p.Path)
			}
			kind, got, _ := ParseVaultPath(p.Path)
			if kind != VaultPathPack || got != id {
				t.Errorf("ParseVaultPath(%q) = %v, %x", p.Path, kind, got)
			}
			b, err := os.ReadFile(fixturePath(v, p.Path))
			if err != nil {
				t.Fatalf("pack %s: %v", p.ID, err)
			}
			if len(b) != 1<<v.PackLog2 {
				t.Errorf("pack %s: %d bytes, want 2^%d", p.ID, len(b), v.PackLog2)
			}
			if err := CheckVaultPackHeader(b, id, v.PackLog2); err != nil {
				t.Errorf("pack %s: %v", p.ID, err)
			}
			if err := CheckVaultPackHeader(b, id, VaultDefaultPackLog2); !errors.Is(err, ErrVaultHeader) {
				t.Errorf("pack %s accepted as a 4 MiB pack", p.ID)
			}
			other := id
			other[15] ^= 1
			if err := CheckVaultPackHeader(b, other, v.PackLog2); !errors.Is(err, ErrVaultHeader) {
				t.Errorf("pack %s accepted under another id", p.ID)
			}
			if !HasEncryptedPrefix(b) {
				t.Errorf("pack %s does not sniff as ciphertext", p.ID)
			}
			// A pack is not an index file, and the reverse.
			if err := CheckVaultIndexHeader(b, 1); !errors.Is(err, ErrVaultHeader) {
				t.Errorf("pack %s passed as an index file", p.ID)
			}
		}
	}
	if packs != 18 {
		t.Fatalf("the fixture's generations wrote %d packs, the format page says 18", packs)
	}

	// Header damage the server can see, one byte at a time.
	b, err := os.ReadFile(fixturePath(v, v.Generations[2].IndexPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []int{0, 8, 9, 10, 15, 23} {
		bad := append([]byte(nil), b[:VaultIndexHeaderLen]...)
		bad[at] ^= 0x01
		if err := CheckVaultIndexHeader(bad, v.Generations[2].Generation); !errors.Is(err, ErrVaultHeader) {
			t.Errorf("index header byte %d changed and still accepted", at)
		}
	}
	if err := CheckVaultIndexHeader(b[:VaultIndexHeaderLen-1], v.Generations[2].Generation); err == nil {
		t.Error("a short index header was accepted")
	}
	// The seal id (bytes 24 to 39) is random: the server does not judge it.
	sealed := append([]byte(nil), b[:VaultIndexHeaderLen]...)
	sealed[30] ^= 0xff
	if err := CheckVaultIndexHeader(sealed, v.Generations[2].Generation); err != nil {
		t.Errorf("a different seal id was refused: %v", err)
	}
}

func TestParseVaultKeyFile(t *testing.T) {
	v := loadVaultVectors(t)
	raw, err := os.ReadFile(fixturePath(v, MarkerName))
	if err != nil {
		t.Fatal(err)
	}
	info, isVault, err := ParseVaultKeyFile(raw)
	if err != nil || !isVault {
		t.Fatalf("the fixture's key file: vault=%v err=%v", isVault, err)
	}
	if info.V != 1 || info.PackLog2 != v.PackLog2 || info.IDHex() != v.Secrets.VaultID {
		t.Fatalf("parsed %+v (id %s), want v1, pack %d, id %s", info, info.IDHex(), v.PackLog2, v.Secrets.VaultID)
	}

	mutate := func(fn func(m map[string]any)) []byte {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		fn(m)
		out, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	vaultBlock := func(m map[string]any) map[string]any { return m["vault"].(map[string]any) }

	malformed := map[string][]byte{
		// The negative vectors marker_req_extra and marker_kek.
		"req names more":    mutate(func(m map[string]any) { m["req"] = []any{"vault", "names"} }),
		"fmk kek":           mutate(func(m map[string]any) { m["fmk"] = "kek"; delete(m, "fmk_pw") }),
		"no fmk_pw":         mutate(func(m map[string]any) { delete(m, "fmk_pw") }),
		"v 2":               mutate(func(m map[string]any) { m["v"] = 2 }),
		"no vault block":    mutate(func(m map[string]any) { delete(m, "vault") }),
		"vault.v 0":         mutate(func(m map[string]any) { vaultBlock(m)["v"] = 0 }),
		"vault.v a string":  mutate(func(m map[string]any) { vaultBlock(m)["v"] = "1" }),
		"id too short":      mutate(func(m map[string]any) { vaultBlock(m)["id"] = "wpU155hSR3hD1u8bSnGv7" }),
		"id padded":         mutate(func(m map[string]any) { vaultBlock(m)["id"] = "wpU155hSR3hD1u8bSnGv7w==" }),
		"id not base64url":  mutate(func(m map[string]any) { vaultBlock(m)["id"] = "wpU155hSR3hD1u8bSnGv/w" }),
		"pack 15":           mutate(func(m map[string]any) { vaultBlock(m)["pack"] = 15 }),
		"pack 25":           mutate(func(m map[string]any) { vaultBlock(m)["pack"] = 25 }),
		"pack not integral": mutate(func(m map[string]any) { vaultBlock(m)["pack"] = 22.5 }),
	}
	for name, b := range malformed {
		_, isVault, err := ParseVaultKeyFile(b)
		if !isVault || !errors.Is(err, ErrVaultMalformed) {
			t.Errorf("%s: vault=%v err=%v, want a malformed vault", name, isVault, err)
		}
	}
	// The negative vector marker_vault_v2: a newer format, not damage.
	_, isVault, err = ParseVaultKeyFile(mutate(func(m map[string]any) { vaultBlock(m)["v"] = 2 }))
	if !isVault || !errors.Is(err, ErrVaultNewer) || errors.Is(err, ErrVaultMalformed) {
		t.Errorf("vault.v 2: vault=%v err=%v, want ErrVaultNewer", isVault, err)
	}
	// Both pack sizes a writer makes are read.
	for _, p := range []int{VaultMinPackLog2, VaultDefaultPackLog2, VaultLargePackLog2} {
		info, isVault, err := ParseVaultKeyFile(mutate(func(m map[string]any) { vaultBlock(m)["pack"] = p }))
		if !isVault || err != nil || info.PackLog2 != p {
			t.Errorf("pack %d: %+v %v %v", p, info, isVault, err)
		}
	}

	// Levels 1 and 2 are not vaults, and are not refused here.
	for name, b := range map[string]string{
		"level 1":            kfBase,
		"level 2":            `{"v":3,"req":["names"],"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","names":{"alg":"AES-SIV-512"}}`,
		"a vault block only": `{"v":3,"req":["names"],"salt":"c2FsdA==","vault":{"v":1,"id":"wpU155hSR3hD1u8bSnGv7w","pack":22}}`,
	} {
		_, isVault, err := ParseVaultKeyFile([]byte(b))
		if isVault || err != nil {
			t.Errorf("%s: vault=%v err=%v, want not a vault", name, isVault, err)
		}
	}
	if _, isVault, err := ParseVaultKeyFile([]byte("not json")); isVault || err == nil {
		t.Errorf("bytes that are not JSON: vault=%v err=%v", isVault, err)
	}
}

func TestSameVaultBlock(t *testing.T) {
	v := loadVaultVectors(t)
	raw, err := os.ReadFile(fixturePath(v, MarkerName))
	if err != nil {
		t.Fatal(err)
	}
	with := func(fn func(m map[string]any)) []byte {
		var c map[string]any
		_ = json.Unmarshal(raw, &c)
		fn(c)
		out, _ := json.Marshal(c)
		return out
	}

	// What a password change, a recovery reset or an escrow slot does: the
	// slots change, the block does not.
	kept := map[string][]byte{
		"a new password": with(func(c map[string]any) { c["salt"] = "bmV3"; c["verify"] = "bmV3dg=="; c["fmk_pw"] = "bmV3cA==" }),
		"an escrow slot": with(func(c map[string]any) {
			c["esc"] = map[string]any{"kid": "k", "alg": "RSA-OAEP-256", "blob": "YmxvYg=="}
		}),
		"an unknown field":     with(func(c map[string]any) { c["later"] = true }),
		"re-spaced, reordered": []byte(strings.Replace(string(raw), `"v": 1,`, ` "v" :1 ,`, 1)),
		"keys of the block reordered": []byte(`{"vault":{"pack":16,"id":"wpU155hSR3hD1u8bSnGv7w","v":1},"req":["vault"],"v":3,` +
			strings.TrimPrefix(string(with(func(c map[string]any) { delete(c, "vault"); delete(c, "req"); delete(c, "v") })), "{")),
	}
	for name, after := range kept {
		if !SameVaultBlock(raw, after) {
			t.Errorf("%s: the block was kept, SameVaultBlock says it changed", name)
		}
	}
	changed := map[string][]byte{
		"another pack size": with(func(c map[string]any) { c["vault"].(map[string]any)["pack"] = 22 }),
		"another id":        with(func(c map[string]any) { c["vault"].(map[string]any)["id"] = "AAAAAAAAAAAAAAAAAAAAAA" }),
		"no vault":          with(func(c map[string]any) { delete(c, "vault") }),
		"req dropped":       with(func(c map[string]any) { delete(c, "req") }),
		"req of level 2":    with(func(c map[string]any) { c["req"] = []any{"names"} }),
		"version 2":         with(func(c map[string]any) { c["v"] = 2 }),
		"not JSON":          []byte("{"),
	}
	for name, after := range changed {
		if SameVaultBlock(raw, after) {
			t.Errorf("%s: SameVaultBlock says nothing changed", name)
		}
	}
	// A level-2 key file rewritten without a vault block keeps its (absent)
	// block: the rule is about the vault, and changes nothing for others.
	if !SameVaultBlock([]byte(kfBase), []byte(strings.Replace(kfBase, `"salt":"c2FsdA=="`, `"salt":"bmV3"`, 1))) {
		t.Error("a level-1 password change reads as a vault change")
	}
}

func TestParseVaultPath(t *testing.T) {
	id := mustID(t, "b067d7bcd62c9f817216a5ef1b1b653b")
	cases := []struct {
		rel  string
		kind VaultPathKind
		gen  uint64
	}{
		{".filex-e2e.json", VaultPathKeyFile, 0},
		{"/.filex-e2e.json", VaultPathKeyFile, 0},
		{"v/idx/0000000000000003.fxi", VaultPathIndex, 3},
		{"v/idx/00000000000000ff.fxi", VaultPathIndex, 255},
		{"v/idx/0000000000000000.fxi", VaultPathOther, 0},
		{"v/idx/00000000000000FF.fxi", VaultPathOther, 0},
		{"v/idx/000000000000003.fxi", VaultPathOther, 0},
		{"v/idx/0000000000000003.fxp", VaultPathOther, 0},
		{"v/idx/.tmp-8f2c", VaultPathTemp, 0},
		{"v/idx/.tmp-", VaultPathOther, 0},
		{"v/p/b0/b067d7bcd62c9f817216a5ef1b1b653b.fxp", VaultPathPack, 0},
		{"v/p/b1/b067d7bcd62c9f817216a5ef1b1b653b.fxp", VaultPathOther, 0},
		{"v/p/B0/B067D7BCD62C9F817216A5EF1B1B653B.fxp", VaultPathOther, 0},
		{"v/p/b0/b067d7bcd62c9f817216a5ef1b1b653.fxp", VaultPathOther, 0},
		{"v/p/b067d7bcd62c9f817216a5ef1b1b653b.fxp", VaultPathOther, 0},
		{"v/p/b0/x/b067d7bcd62c9f817216a5ef1b1b653b.fxp", VaultPathOther, 0},
		{"v", VaultPathOther, 0},
		{"v/idx", VaultPathOther, 0},
		{"notes.txt", VaultPathOther, 0},
		{"v/idx/0000000000000003.fxi/x", VaultPathOther, 0},
	}
	for _, c := range cases {
		kind, gotID, gen := ParseVaultPath(c.rel)
		if kind != c.kind || gen != c.gen {
			t.Errorf("ParseVaultPath(%q) = %v, %d; want %v, %d", c.rel, kind, gen, c.kind, c.gen)
		}
		if kind == VaultPathPack && gotID != id {
			t.Errorf("ParseVaultPath(%q) id %x", c.rel, gotID)
		}
		if kind != VaultPathPack && gotID != ([16]byte{}) {
			t.Errorf("ParseVaultPath(%q) names an id for a %v", c.rel, kind)
		}
	}
	if got := VaultPackPath(id); got != "v/p/b0/b067d7bcd62c9f817216a5ef1b1b653b.fxp" {
		t.Errorf("VaultPackPath = %q", got)
	}
	if got := VaultIndexPath(3); got != "v/idx/0000000000000003.fxi" {
		t.Errorf("VaultIndexPath = %q", got)
	}
	if _, ok := ParseVaultID(hex.EncodeToString(id[:])); !ok {
		t.Error("ParseVaultID refuses its own spelling")
	}
	for _, s := range []string{"", "b067", strings.ToUpper("b067d7bcd62c9f817216a5ef1b1b653b"), "g067d7bcd62c9f817216a5ef1b1b653b"} {
		if _, ok := ParseVaultID(s); ok {
			t.Errorf("ParseVaultID(%q) accepted", s)
		}
	}
}
