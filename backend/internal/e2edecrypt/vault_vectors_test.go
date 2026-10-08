package e2edecrypt

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

// The vault (level 3) against the vectors gen_vault_vectors.mjs wrote with
// node:crypto - docs/E2E-VAULT-FORMAT.md → "Test vectors", items 1 to 9, byte
// for byte. The browser's twin is web/tests/lib/e2evault*.test.ts; both are
// held to the same third implementation, so a mistake the two share fails.

var (
	vaultVectorsPath = filepath.Join("testdata", "vault-vectors.json")
	vaultFixture     = filepath.Join("testdata", "vault", "v3-vault")
)

type vaultOpVector struct {
	Op      string `json:"op"`
	Path    string `json:"path"`
	From    string `json:"from"`
	To      string `json:"to"`
	MTime   int64  `json:"mtime"`
	Content *struct {
		Text    *string `json:"text"`
		Empty   bool    `json:"empty"`
		Pattern *struct {
			Len  int `json:"len"`
			Seed int `json:"seed"`
		} `json:"pattern"`
	} `json:"content"`
}

type vaultTreeRow struct {
	Path       string  `json:"path"`
	Kind       string  `json:"kind"`
	MTime      int64   `json:"mtime"`
	Size       int64   `json:"size"`
	SHA256     string  `json:"sha256"`
	Text       *string `json:"text"`
	ContentID  string  `json:"content_id"`
	ContentKey string  `json:"content_key"`
	ChunkLog2  int     `json:"chunk_log2"`
	Extents    []struct {
		Pack   string `json:"pack"`
		Offset int64  `json:"offset"`
		Length int64  `json:"length"`
	} `json:"extents"`
}

type vaultGenVector struct {
	Generation    uint64          `json:"generation"`
	Ops           []vaultOpVector `json:"ops"`
	DRBGSeed      string          `json:"drbg_seed"`
	DRBGBytesUsed int64           `json:"drbg_bytes_used"`
	IndexPath     string          `json:"index_path"`
	IndexSize     int64           `json:"index_size"`
	IndexSHA256   string          `json:"index_sha256"`
	PacksWritten  []struct {
		ID     string `json:"id"`
		Path   string `json:"path"`
		Used   int64  `json:"used"`
		SHA256 string `json:"sha256"`
	} `json:"packs_written"`
	SealID    string   `json:"seal_id"`
	IndexKey  string   `json:"index_key"`
	BodyLen   int      `json:"body_len"`
	BodyHex   string   `json:"body_hex"`
	PackTable []string `json:"pack_table"`
	Graveyard []struct {
		Pack string `json:"pack"`
		Died uint64 `json:"died"`
	} `json:"graveyard"`
	Tree []vaultTreeRow `json:"tree"`

	// Repack is the set S a generation of the repack branch copies, in
	// table order; empty for every other generation.
	Repack []string `json:"repack"`
}

type vaultVectors struct {
	Secrets struct {
		Password    string `json:"password"`
		RecoveryKey string `json:"recovery_key"`
		Iter        int    `json:"iter"`
		FMK         string `json:"fmk"`
		VaultID     string `json:"vault_id"`
		VaultIDB64  string `json:"vault_id_b64url"`
	} `json:"secrets"`
	Marker           json.RawMessage  `json:"marker"`
	PackLog2         int              `json:"pack_log2"`
	LatestGeneration uint64           `json:"latest_generation"`
	Generations      []vaultGenVector `json:"generations"`
	Canonical4MiB    struct {
		Marker      json.RawMessage  `json:"marker"`
		PackLog2    int              `json:"pack_log2"`
		Generations []vaultGenVector `json:"generations"`
	} `json:"canonical_4mib"`
	Repack struct {
		PackLog2       int              `json:"pack_log2"`
		FromGeneration uint64           `json:"from_generation"`
		Generations    []vaultGenVector `json:"generations"`
	} `json:"repack"`
	Layers struct {
		DRBG struct {
			Seed    string `json:"seed"`
			First64 string `json:"first_64_bytes"`
		} `json:"drbg"`
		Uvarint []struct {
			Value uint64 `json:"value"`
			Hex   string `json:"hex"`
		} `json:"uvarint"`
		PadmeIndexSize []struct {
			BodyLen  int64 `json:"body_len"`
			MinLen   int64 `json:"min_len"`
			FileSize int64 `json:"file_size"`
		} `json:"padme_index_size"`
		StreamSize []struct {
			Size       int64 `json:"size"`
			ChunkLog2  int   `json:"chunk_log2"`
			Ciphertext int64 `json:"ciphertext"`
		} `json:"stream_size"`
		NameOrder struct {
			Input  []string `json:"input"`
			Sorted []string `json:"sorted"`
		} `json:"name_order"`
	} `json:"layers"`
	BodyCases []struct {
		Name         string `json:"name"`
		Valid        bool   `json:"valid"`
		Why          string `json:"why"`
		PackLog2     int    `json:"pack_log2"`
		Generation   uint64 `json:"generation"`
		PlaintextHex string `json:"plaintext_hex"`
		Writable     *bool  `json:"writable"`
	} `json:"body_cases"`
	Negative []struct {
		Name   string `json:"name"`
		Do     string `json:"do"`
		Expect string `json:"expect"`
	} `json:"negative"`
}

func loadVaultVectors(t *testing.T) *vaultVectors {
	t.Helper()
	b, err := os.ReadFile(vaultVectorsPath)
	require.NoError(t, err)
	var v vaultVectors
	require.NoError(t, json.Unmarshal(b, &v))
	require.Len(t, v.Generations, 3)
	return &v
}

func id16(t *testing.T, s string) [16]byte {
	t.Helper()
	var id [16]byte
	b := unhex(t, s)
	require.Len(t, b, 16)
	copy(id[:], b)
	return id
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// vaultDRBG is the vectors' random source: the AES-256-CTR keystream under
// SHA-256(seed), counter block 0, the whole block counting up big-endian.
type vaultDRBG struct {
	s    cipher.Stream
	used int64
}

func newVaultDRBG(t *testing.T, seed string) *vaultDRBG {
	t.Helper()
	key := sha256.Sum256([]byte(seed))
	block, err := aes.NewCipher(key[:])
	require.NoError(t, err)
	return &vaultDRBG{s: cipher.NewCTR(block, make([]byte, aes.BlockSize))}
}

func (d *vaultDRBG) Read(p []byte) (int, error) {
	clear(p)
	d.s.XORKeyStream(p, p)
	d.used += int64(len(p))
	return len(p), nil
}

func (v *vaultVectors) keys(t *testing.T) *VaultKeys {
	t.Helper()
	k, err := NewVaultKeys(unhex(t, v.Secrets.FMK), id16(t, v.Secrets.VaultID))
	require.NoError(t, err)
	return k
}

func vectorContent(op vaultOpVector) []byte {
	switch {
	case op.Content == nil || op.Content.Empty:
		return nil
	case op.Content.Text != nil:
		return []byte(*op.Content.Text)
	}
	b := make([]byte, op.Content.Pattern.Len)
	for i := range b {
		b[i] = byte(i*31 + op.Content.Pattern.Seed)
	}
	return b
}

func fixtureSource() *DirVaultSource { return NewDirVaultSource(os.DirFS(vaultFixture), 16) }

// 1. Unlock the key file with the password and with the recovery key.
func TestVaultVectors_1_Unlock(t *testing.T) {
	v := loadVaultVectors(t)
	data, err := os.ReadFile(filepath.Join(vaultFixture, MarkerName))
	require.NoError(t, err)
	m, err := ParseMarker(data)
	require.NoError(t, err)
	require.True(t, m.IsVault())
	require.Equal(t, 16, m.Vault.PackLog2)
	require.Equal(t, 1, m.Vault.V)
	require.Equal(t, v.Secrets.VaultID, hex.EncodeToString(m.Vault.ID[:]))
	require.False(t, m.HasNames())

	k, err := m.UnlockPassword(v.Secrets.Password)
	require.NoError(t, err)
	require.Equal(t, v.Secrets.FMK, hex.EncodeToString(k.FMK))
	k, err = m.UnlockRecoveryKey(v.Secrets.RecoveryKey)
	require.NoError(t, err)
	require.Equal(t, v.Secrets.FMK, hex.EncodeToString(k.FMK))
}

// 2. The index key of every generation and the content key of every file.
func TestVaultVectors_2_Keys(t *testing.T) {
	v := loadVaultVectors(t)
	k := v.keys(t)
	for _, g := range v.Generations {
		ik, err := k.IndexKey(id16(t, g.SealID))
		require.NoError(t, err)
		require.Equal(t, g.IndexKey, hex.EncodeToString(ik), "generation %d", g.Generation)
		for _, row := range g.Tree {
			if row.ContentID == "" {
				continue
			}
			ck, err := k.ContentKey(id16(t, row.ContentID))
			require.NoError(t, err)
			require.Equal(t, row.ContentKey, hex.EncodeToString(ck), "%s in generation %d", row.Path, g.Generation)
		}
	}
}

// 3. Every index file decrypts to body_hex followed by zeros, and decodes to
// tree, pack_table and graveyard.
func TestVaultVectors_3_Index(t *testing.T) {
	v := loadVaultVectors(t)
	k := v.keys(t)
	for _, g := range v.Generations {
		file, err := os.ReadFile(filepath.Join(vaultFixture, filepath.FromSlash(g.IndexPath)))
		require.NoError(t, err)
		require.Equal(t, g.IndexSize, int64(len(file)))
		require.Equal(t, g.IndexSHA256, sha256hex(file))

		aead, err := vaultAEAD(unhex(t, g.IndexKey))
		require.NoError(t, err)
		plain, err := aead.Open(nil, make([]byte, 12), file[40:], file[:40])
		require.NoError(t, err)
		require.Equal(t, g.BodyHex, hex.EncodeToString(plain[:g.BodyLen]))
		require.Equal(t, make([]byte, len(plain)-g.BodyLen), plain[g.BodyLen:], "zeros after the body")

		idx, err := OpenVaultIndex(k, v.PackLog2, g.Generation, file)
		require.NoError(t, err)
		require.Equal(t, g.BodyLen, idx.BodyLen)
		require.Empty(t, idx.ReadOnly)
		var table []string
		for _, p := range idx.Packs {
			table = append(table, hex.EncodeToString(p[:]))
		}
		require.Equal(t, g.PackTable, nilIfEmpty(table), "generation %d", g.Generation)
		var grave []string
		for _, e := range idx.Grave {
			grave = append(grave, fmt.Sprintf("%x/%d", e.Pack[:], e.Died))
		}
		var wantGrave []string
		for _, e := range g.Graveyard {
			wantGrave = append(wantGrave, fmt.Sprintf("%s/%d", e.Pack, e.Died))
		}
		require.Equal(t, wantGrave, grave, "generation %d", g.Generation)
		requireVaultTree(t, g.Tree, idx.Tree)

		// The encoder writes the same bytes back: one encoding per tree.
		body, packs, err := EncodeVaultBody(idx.Tree, idx.Grave)
		require.NoError(t, err)
		require.Equal(t, g.BodyHex, hex.EncodeToString(body))
		require.Equal(t, idx.Packs, nilIfEmptyIDs(packs))
	}
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return []string{}
	}
	return s
}

func nilIfEmptyIDs(s [][16]byte) [][16]byte {
	if len(s) == 0 {
		return [][16]byte{}
	}
	return s
}

// requireVaultTree compares a decoded tree with the vectors' rows, in order.
func requireVaultTree(t *testing.T, want []vaultTreeRow, tree *VaultTree) {
	t.Helper()
	var got []vaultTreeRow
	require.NoError(t, tree.Walk(func(n *VaultNode, _ int) error {
		row := vaultTreeRow{Path: n.Path(), Kind: "folder", MTime: n.MTime}
		if !n.Dir {
			row.Kind, row.Size = "file", n.Size
			if c := n.Content; c != nil {
				row.ContentID = hex.EncodeToString(c.ID[:])
				row.ChunkLog2 = c.ChunkLog2
				for _, x := range c.Extents {
					row.Extents = append(row.Extents, struct {
						Pack   string `json:"pack"`
						Offset int64  `json:"offset"`
						Length int64  `json:"length"`
					}{hex.EncodeToString(x.Pack[:]), x.Offset, x.Length})
				}
			}
		}
		got = append(got, row)
		return nil
	}))
	require.Len(t, got, len(want))
	for i, w := range want {
		g := got[i]
		require.Equal(t, w.Path, g.Path)
		require.Equal(t, w.Kind, g.Kind, w.Path)
		require.Equal(t, w.MTime, g.MTime, w.Path)
		require.Equal(t, w.Size, g.Size, w.Path)
		require.Equal(t, w.ContentID, g.ContentID, w.Path)
		require.Equal(t, w.ChunkLog2, g.ChunkLog2, w.Path)
		require.Equal(t, w.Extents, g.Extents, w.Path)
	}
}

// 4. Every file of generations 2 and 3 reads back, whole and by ranges that
// cross a chunk and a pack boundary.
func TestVaultVectors_4_Contents(t *testing.T) {
	v := loadVaultVectors(t)
	k := v.keys(t)
	ctx := context.Background()
	src := fixtureSource()
	r := NewVaultReader(src, k)
	for _, g := range v.Generations[1:] {
		st, err := LoadVault(ctx, src, k, v.PackLog2, VaultLoadOptions{Generation: g.Generation})
		require.NoError(t, err)
		require.Equal(t, g.Generation, st.Index.Generation)
		for _, row := range g.Tree {
			if row.Kind != "file" {
				continue
			}
			n := st.Index.Tree.Lookup(row.Path)
			require.NotNil(t, n, row.Path)
			var buf bytes.Buffer
			got, err := r.WriteTo(ctx, n, &buf)
			require.NoError(t, err, row.Path)
			require.Equal(t, row.Size, got)
			require.Equal(t, row.SHA256, sha256hex(buf.Bytes()), row.Path)
			if row.Text != nil {
				require.Equal(t, *row.Text, buf.String())
			}
			whole, err := r.ReadRange(ctx, n, 0, n.Size)
			require.NoError(t, err)
			require.Equal(t, buf.String(), string(whole))
		}
	}

	st, err := LoadVault(ctx, src, k, v.PackLog2, VaultLoadOptions{Generation: 2})
	require.NoError(t, err)
	big := st.Index.Tree.Lookup("Belgeler/Arşiv/büyük.bin")
	require.NotNil(t, big)
	want := make([]byte, big.Size)
	for i := range want {
		want[i] = byte(i*31 + 7)
	}
	for _, span := range [][2]int64{
		{65400, 65600},     // inside chunk 0, across the end of the first pack's extent
		{1048570, 1048577}, // across chunks 0 and 1, to the end of the file
		{0, 1},
		{1048576, 1048577}, // the last chunk alone
		{65455, 65456 + 65504},
	} {
		got, err := r.ReadRange(ctx, big, span[0], span[1])
		require.NoError(t, err, "%v", span)
		require.Equal(t, want[span[0]:span[1]], got, "%v", span)
	}
	c0, err := r.ReadChunk(ctx, big, 0)
	require.NoError(t, err)
	require.Equal(t, want[:1<<20], c0)
	c1, err := r.ReadChunk(ctx, big, 1)
	require.NoError(t, err)
	require.Equal(t, want[1<<20:], c1)
}

// 5. Replaying generations 1 to 3 with the DRBG writes every pack and index
// file byte for byte - at 2^16, and at 2^22 against canonical_4mib.
func TestVaultVectors_5_Writing(t *testing.T) {
	v := loadVaultVectors(t)
	replayVaultVectors(t, v, v.PackLog2, v.Generations)
}

func TestVaultVectors_5_Writing4MiB(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 4 MiB packs")
	}
	v := loadVaultVectors(t)
	require.Equal(t, 22, v.Canonical4MiB.PackLog2)
	replayVaultVectors(t, v, v.Canonical4MiB.PackLog2, v.Canonical4MiB.Generations)
}

func replayVaultVectors(t *testing.T, v *vaultVectors, packLog2 int, gens []vaultGenVector) {
	t.Helper()
	replayVaultFrom(t, v, packLog2, nil, gens, nil)
}

// replayVaultFrom replays gens on top of base (nil: a new vault) and returns
// the last generation, read back. Every pack it writes goes into store, when
// there is one: a generation of the repack branch copies its bytes from there.
func replayVaultFrom(t *testing.T, v *vaultVectors, packLog2 int, base *VaultIndex, gens []vaultGenVector, store map[[16]byte][]byte) *VaultIndex {
	t.Helper()
	ctx := context.Background()
	k := v.keys(t)
	for _, g := range gens {
		drbg := newVaultDRBG(t, g.DRBGSeed)
		type stored struct {
			id    string
			bytes []byte
		}
		var packs []stored
		w, err := NewVaultWriter(VaultWriterConfig{
			Keys: k, PackLog2: packLog2, Base: base, Rand: drbg,
			Sink: func(_ context.Context, id [16]byte, b []byte) error {
				packs = append(packs, stored{hex.EncodeToString(id[:]), bytes.Clone(b)})
				if store != nil {
					store[id] = bytes.Clone(b)
				}
				return nil
			},
		})
		require.NoError(t, err)
		require.Equal(t, g.Generation, w.Generation())
		for _, op := range g.Ops {
			switch op.Op {
			case "mkdir":
				require.NoError(t, w.Mkdir(op.Path, op.MTime))
			case "write":
				data := vectorContent(op)
				_, err := w.Write(ctx, op.Path, op.MTime, bytes.NewReader(data), int64(len(data)))
				require.NoError(t, err, op.Path)
			case "delete":
				require.NoError(t, w.Delete(op.Path))
			case "move":
				require.NoError(t, w.Move(op.From, op.To))
			default:
				t.Fatalf("unknown op %q", op.Op)
			}
		}
		if len(g.Repack) > 0 {
			// The set the planner picks after the previous commit is the
			// vectors' S, and the repack copies its live extents.
			want := make([][16]byte, len(g.Repack))
			for i, s := range g.Repack {
				want[i] = id16(t, s)
			}
			require.Equal(t, want, PlanVaultRepack(base, packLog2), "the packs to repack after generation %d", g.Generation-1)
			require.NotNil(t, store, "a repack reads the packs it copies")
			require.NoError(t, w.Repack(ctx, want, func(_ context.Context, id [16]byte, off, n int64) ([]byte, error) {
				b, ok := store[id]
				if !ok {
					return nil, fmt.Errorf("pack %x was never written", id)
				}
				return bytes.Clone(b[off : off+n]), nil
			}))
		}
		c, err := w.Commit(ctx)
		require.NoError(t, err)
		require.Equal(t, g.Generation, c.Index.Generation)
		if store != nil && len(g.Tree) > 0 {
			// Where every extent went, before the hashes say whether it did.
			requireVaultTree(t, g.Tree, c.Index.Tree)
		}
		require.Equal(t, g.IndexSize, int64(len(c.File)), "generation %d", g.Generation)
		require.Equal(t, g.IndexSHA256, sha256hex(c.File), "the index of generation %d", g.Generation)
		require.Len(t, packs, len(g.PacksWritten), "generation %d", g.Generation)
		for i, p := range g.PacksWritten {
			require.Equal(t, p.ID, packs[i].id, "pack %d of generation %d", i, g.Generation)
			require.Len(t, packs[i].bytes, 1<<packLog2)
			require.Equal(t, p.SHA256, sha256hex(packs[i].bytes), "pack %s", p.ID)
		}
		require.Equal(t, g.DRBGBytesUsed, drbg.used, "random bytes drawn in generation %d", g.Generation)
		// The next generation follows what this one wrote, read back.
		base, err = OpenVaultIndex(k, packLog2, g.Generation, c.File)
		require.NoError(t, err)
	}
	return base
}

// 10. The repack branch: generations 1 to 3, then 4 to 6 of `repack`, where 6
// is the repack the planner calls for after 5 - the live extents of every pack
// of the table copied, bytes as they are, into one new pack, byte for byte.
// Generation 6's files still read back.
func TestVaultVectors_10_Repack(t *testing.T) {
	v := loadVaultVectors(t)
	require.NotEmpty(t, v.Repack.Generations, "vault-vectors.json has no repack branch: run gen_vault_vectors.mjs")
	require.Equal(t, v.PackLog2, v.Repack.PackLog2)
	require.Equal(t, v.LatestGeneration, v.Repack.FromGeneration)
	last := v.Repack.Generations[len(v.Repack.Generations)-1]
	require.NotEmpty(t, last.Repack, "the branch ends in a repack")

	store := map[[16]byte][]byte{}
	base := replayVaultFrom(t, v, v.PackLog2, nil, v.Generations, store)
	idx := replayVaultFrom(t, v, v.PackLog2, base, v.Repack.Generations, store)
	require.Nil(t, PlanVaultRepack(idx, v.PackLog2), "a repacked vault calls for no other repack")
	for _, s := range last.Repack {
		require.Contains(t, idx.Grave, VaultGrave{Pack: id16(t, s), Died: last.Generation}, "a repacked pack goes to the graveyard")
	}

	src := &memVaultSource{packs: store, indexes: map[uint64][]byte{}}
	r := NewVaultReader(src, v.keys(t))
	ctx := context.Background()
	for _, row := range last.Tree {
		if row.Kind != "file" {
			continue
		}
		n := idx.Tree.Lookup(row.Path)
		require.NotNil(t, n, row.Path)
		var buf bytes.Buffer
		_, err := r.WriteTo(ctx, n, &buf)
		require.NoError(t, err, row.Path)
		require.Equal(t, row.SHA256, sha256hex(buf.Bytes()), row.Path)
	}
}

// 6. Each body case parses as `valid` says; ext_is_skipped leaves the vault
// read-only for a writer.
func TestVaultVectors_6_BodyCases(t *testing.T) {
	v := loadVaultVectors(t)
	require.NotEmpty(t, v.BodyCases)
	for _, c := range v.BodyCases {
		t.Run(c.Name, func(t *testing.T) {
			idx, err := DecodeVaultBody(unhex(t, c.PlaintextHex), c.PackLog2, c.Generation)
			if !c.Valid {
				require.Error(t, err, c.Why)
				return
			}
			require.NoError(t, err, c.Why)
			if c.Writable != nil && !*c.Writable {
				require.NotEmpty(t, idx.ReadOnly)
				_, werr := NewVaultWriter(VaultWriterConfig{Keys: v.keys(t), PackLog2: c.PackLog2, Base: idx, Sink: discardPacks})
				require.ErrorIs(t, werr, ErrVaultReadOnly)
				return
			}
			require.Empty(t, idx.ReadOnly)
		})
	}
}

func discardPacks(context.Context, [16]byte, []byte) error { return nil }

// 7. The layers: uvarint, padme_index_size, stream_size, name_order, drbg.
func TestVaultVectors_7_Layers(t *testing.T) {
	v := loadVaultVectors(t)
	for _, u := range v.Layers.Uvarint {
		enc := appendVaultUvarint(nil, u.Value)
		require.Equal(t, u.Hex, hex.EncodeToString(enc), "%d", u.Value)
		d := &vaultDecoder{b: enc}
		require.Equal(t, u.Value, d.uvarint())
		require.NoError(t, d.err)
		require.Equal(t, len(enc), d.off)
	}
	for _, p := range v.Layers.PadmeIndexSize {
		require.Equal(t, p.FileSize, e2e.VaultIndexFileSize(p.BodyLen), "body of %d bytes", p.BodyLen)
		require.True(t, e2e.ValidVaultIndexSize(p.FileSize) || p.FileSize > e2e.VaultIndexReadMax)
	}
	for _, s := range v.Layers.StreamSize {
		require.Equal(t, s.Ciphertext, StreamCiphertextSize(s.Size, s.ChunkLog2))
	}
	tree := NewVaultTree()
	for _, name := range v.Layers.NameOrder.Input {
		_, err := tree.Mkdir(name, 0)
		require.NoError(t, err, name)
	}
	var order []string
	for _, n := range tree.Root().Children() {
		order = append(order, n.Name)
	}
	require.Equal(t, v.Layers.NameOrder.Sorted, order)

	d := newVaultDRBG(t, v.Layers.DRBG.Seed)
	first := make([]byte, 64)
	_, _ = d.Read(first)
	require.Equal(t, v.Layers.DRBG.First64, hex.EncodeToString(first))

	// Non-minimal and too-large numbers are refused.
	for _, bad := range []string{"8000", "ff00", "ffffffffffffff10", "808080808080808001"} {
		d := &vaultDecoder{b: unhex(t, bad)}
		d.uvarint()
		require.Error(t, d.err, bad)
	}
}

// copyVaultFixture copies the fixture into a temporary folder to damage it.
func copyVaultFixture(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "Kasa")
	require.NoError(t, filepath.WalkDir(vaultFixture, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(vaultFixture, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	}))
	return dst
}

func flipBit(t *testing.T, p string, off int64) {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	b[off] ^= 1
	require.NoError(t, os.WriteFile(p, b, 0o644))
}

func editMarker(t *testing.T, v *vaultVectors, edit func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(v.Marker, &m))
	edit(m)
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

// 8. Each mutation of a copy of the fixture gives the stated result.
func TestVaultVectors_8_Negative(t *testing.T) {
	v := loadVaultVectors(t)
	k := v.keys(t)
	ctx := context.Background()
	named := map[string]bool{}
	for _, n := range v.Negative {
		named[n.Name] = true
	}

	t.Run("wrong_password", func(t *testing.T) {
		require.True(t, named["wrong_password"])
		m, err := ParseMarker(v.Marker)
		require.NoError(t, err)
		_, err = m.UnlockPassword("vault vector password, not the real one")
		require.ErrorIs(t, err, ErrWrongPassword)
	})

	damagedLatest := func(t *testing.T, dir string, latest, shown uint64) {
		t.Helper()
		var slept []time.Duration
		st, err := LoadVault(ctx, NewDirVaultSource(os.DirFS(dir), v.PackLog2), k, v.PackLog2, VaultLoadOptions{
			Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil },
		})
		require.NoError(t, err)
		require.True(t, st.DamagedLatest)
		require.Equal(t, latest, st.Latest)
		require.Equal(t, shown, st.Index.Generation)
		require.Equal(t, []time.Duration{2 * time.Second}, slept, "the damaged latest is fetched once more, 2 seconds later")
		require.Contains(t, st.ReadOnly(), "damaged")
		require.ErrorIs(t, st.Writable(), ErrVaultReadOnly, "writers refuse to write")
	}

	t.Run("index_renamed", func(t *testing.T) {
		require.True(t, named["index_renamed"])
		dir := copyVaultFixture(t)
		b, err := os.ReadFile(filepath.Join(dir, "v", "idx", "0000000000000003.fxi"))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "v", "idx", "0000000000000004.fxi"), b, 0o644))
		damagedLatest(t, dir, 4, 3)
	})
	t.Run("index_bitflip", func(t *testing.T) {
		require.True(t, named["index_bitflip"])
		dir := copyVaultFixture(t)
		flipBit(t, filepath.Join(dir, "v", "idx", "0000000000000003.fxi"), 1000)
		damagedLatest(t, dir, 3, 2)
	})
	t.Run("index_wrong_size", func(t *testing.T) {
		require.True(t, named["index_wrong_size"])
		dir := copyVaultFixture(t)
		p := filepath.Join(dir, "v", "idx", "0000000000000003.fxi")
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(p, append(b, 0), 0o644))
		_, err = OpenVaultIndex(k, v.PackLog2, 3, append(b, 0))
		require.ErrorIs(t, err, ErrVaultDamaged)
		damagedLatest(t, dir, 3, 2)
	})
	t.Run("pack_bitflip", func(t *testing.T) {
		require.True(t, named["pack_bitflip"])
		dir := copyVaultFixture(t)
		var big vaultTreeRow
		for _, row := range v.Generations[1].Tree {
			if row.Path == "Belgeler/Arşiv/büyük.bin" {
				big = row
			}
		}
		x := big.Extents[2]
		flipBit(t, filepath.Join(dir, filepath.FromSlash(e2e.VaultPackPath(id16(t, x.Pack)))), x.Offset+1000)
		src := NewDirVaultSource(os.DirFS(dir), v.PackLog2)
		st, err := LoadVault(ctx, src, k, v.PackLog2, VaultLoadOptions{Generation: 2})
		require.NoError(t, err)
		r := NewVaultReader(src, k)
		_, err = r.WriteTo(ctx, st.Index.Tree.Lookup(big.Path), &bytes.Buffer{})
		require.ErrorIs(t, err, ErrContent)
		var ce *CorruptError
		require.ErrorAs(t, err, &ce)
		require.Equal(t, big.Path, ce.Path)
		var note bytes.Buffer
		_, err = r.WriteTo(ctx, st.Index.Tree.Lookup("not.txt"), &note)
		require.NoError(t, err)
		require.Equal(t, "Kasadaki ilk not: çay demlendi.\n", note.String())

		// filex decrypt is all or nothing: generation 2 of this copy writes
		// no output at all.
		out := filepath.Join(t.TempDir(), "out")
		_, err = DecryptVault(ctx, src, k, v.PackLog2, VaultDecryptOptions{Out: out, Generation: 2})
		require.ErrorIs(t, err, ErrContent)
		require.NoDirExists(t, out)
	})
	t.Run("marker_req_extra", func(t *testing.T) {
		require.True(t, named["marker_req_extra"])
		_, err := ParseMarker(editMarker(t, v, func(m map[string]any) { m["req"] = []string{"vault", "names"} }))
		require.ErrorIs(t, err, ErrNotMarker)
	})
	t.Run("marker_vault_v2", func(t *testing.T) {
		require.True(t, named["marker_vault_v2"])
		_, err := ParseMarker(editMarker(t, v, func(m map[string]any) { m["vault"].(map[string]any)["v"] = 2 }))
		var ue *UnsupportedError
		require.ErrorAs(t, err, &ue)
		require.Contains(t, ue.Error(), "newer filex")
	})
	t.Run("marker_kek", func(t *testing.T) {
		require.True(t, named["marker_kek"])
		_, err := ParseMarker(editMarker(t, v, func(m map[string]any) {
			m["fmk"] = "kek"
			delete(m, "fmk_pw")
		}))
		require.ErrorIs(t, err, ErrNotMarker)
	})
	require.Len(t, named, 8, "a new negative vector needs a case here")
}

// 9. A writer refuses the 250 001st entry and warns from the 200 000th; a
// reader refuses an index file over 64 MiB before decrypting it.
func TestVaultVectors_9_Limits(t *testing.T) {
	v := loadVaultVectors(t)
	w, err := NewVaultWriter(VaultWriterConfig{Keys: v.keys(t), PackLog2: 16, Sink: discardPacks})
	require.NoError(t, err)
	for i := 0; i < e2e.VaultEntriesWriteMax; i++ {
		if i == e2e.VaultEntriesWarn-1 {
			require.Empty(t, w.Warning(), "no warning below 200 000 entries")
		}
		require.NoError(t, w.Mkdir(fmt.Sprintf("d%06d", i), 0))
		if i == e2e.VaultEntriesWarn-1 {
			require.Contains(t, w.Warning(), "200 000 of at most 250 000")
		}
	}
	require.ErrorIs(t, w.Mkdir("one-too-many", 0), ErrVaultFull)
	_, err = w.Write(context.Background(), "one-too-many.txt", 0, bytes.NewReader(nil), 0)
	require.ErrorIs(t, err, ErrVaultFull)

	// 64 MiB + 2 MiB is a Padmé size: only the reader's ceiling refuses it.
	// The keys are wiped, so a reader that tried to decrypt would fail on
	// them instead of on the size.
	const big = 64<<20 + 2<<20
	require.Equal(t, int64(big), e2e.VaultPadme(big))
	k := v.keys(t)
	k.Wipe()
	_, err = OpenVaultIndex(k, 16, 1, make([]byte, big))
	require.ErrorIs(t, err, ErrVaultDamaged)

	dir := filepath.Join(t.TempDir(), "v", "idx")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	f, err := os.Create(filepath.Join(dir, "0000000000000001.fxi"))
	require.NoError(t, err)
	require.NoError(t, f.Truncate(big))
	require.NoError(t, f.Close())
	_, err = NewDirVaultSource(os.DirFS(filepath.Dir(filepath.Dir(dir))), 16).ReadIndex(context.Background(), 1)
	require.ErrorIs(t, err, ErrVaultDamaged, "refused from its size, before it is read")
}

// filex decrypt on a copy of the vault: the latest generation, or an older one.
func TestVaultDecryptTree_Copy(t *testing.T) {
	v := loadVaultVectors(t)
	out := filepath.Join(t.TempDir(), "plain")
	res, err := DecryptTree(vaultFixture, OpenOptions{Out: out}, v.Secrets.Password, false)
	require.NoError(t, err)
	require.Equal(t, 2, res.Files)
	require.Equal(t, 2, res.Dirs)
	b, err := os.ReadFile(filepath.Join(out, "not.txt"))
	require.NoError(t, err)
	require.Equal(t, "Kasadaki ikinci not: şeker yok.\n", string(b))
	fi, err := os.Stat(filepath.Join(out, "boş.txt"))
	require.NoError(t, err)
	require.Zero(t, fi.Size())
	require.DirExists(t, filepath.Join(out, "Belgeler", "Arşiv"))
	require.Equal(t, time.UnixMilli(1791277206000).Unix(), mustStat(t, filepath.Join(out, "not.txt")).ModTime().Unix())

	out2 := filepath.Join(t.TempDir(), "gen2")
	res, err = DecryptTree(vaultFixture, OpenOptions{Out: out2, Generation: 2}, v.Secrets.RecoveryKey, true)
	require.NoError(t, err)
	require.Equal(t, 3, res.Files)
	big, err := os.ReadFile(filepath.Join(out2, "Belgeler", "Arşiv", "büyük.bin"))
	require.NoError(t, err)
	require.Equal(t, "0d09f3eabb5c78e5d435c41e3b3755f1c4d038657f739d0be0083c24e71235df", sha256hex(big))

	_, err = DecryptTree(vaultFixture, OpenOptions{Out: filepath.Join(t.TempDir(), "x"), Generation: 9}, v.Secrets.Password, false)
	require.Error(t, err)

	// A vault opens whole: a folder inside it is not a vault copy.
	_, err = Open(filepath.Join(vaultFixture, "v"), OpenOptions{Out: filepath.Join(t.TempDir(), "y"), MarkerPath: filepath.Join(vaultFixture, MarkerName)})
	require.ErrorContains(t, err, "decrypted whole")
	// --generation is for a vault only.
	_, err = Open(filepath.Join("testdata", "folders", "v2-stream"), OpenOptions{Out: filepath.Join(t.TempDir(), "z"), Generation: 2})
	require.ErrorContains(t, err, "--generation")
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	require.NoError(t, err)
	return fi
}

// A vault with no index file and no pack is empty at generation 0; packs
// without an index are damage.
func TestLoadVault_NoIndex(t *testing.T) {
	v := loadVaultVectors(t)
	k := v.keys(t)
	dir := t.TempDir()
	st, err := LoadVault(context.Background(), NewDirVaultSource(os.DirFS(dir), 16), k, 16, VaultLoadOptions{})
	require.NoError(t, err)
	require.Zero(t, st.Index.Generation)
	require.Zero(t, st.Index.Tree.Len())
	require.NoError(t, st.Writable())

	p := filepath.Join(dir, filepath.FromSlash(e2e.VaultPackPath([16]byte{1})))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, make([]byte, 1<<16), 0o644))
	_, err = LoadVault(context.Background(), NewDirVaultSource(os.DirFS(dir), 16), k, 16, VaultLoadOptions{})
	require.ErrorIs(t, err, ErrVaultDamaged)
}

// A rollback within a session is read-only.
func TestLoadVault_RollbackIsReadOnly(t *testing.T) {
	v := loadVaultVectors(t)
	st, err := LoadVault(context.Background(), fixtureSource(), v.keys(t), 16, VaultLoadOptions{Seen: 5})
	require.NoError(t, err)
	require.True(t, st.RolledBack)
	require.ErrorIs(t, st.Writable(), ErrVaultReadOnly)
	st, err = LoadVault(context.Background(), fixtureSource(), v.keys(t), 16, VaultLoadOptions{Seen: 3})
	require.NoError(t, err)
	require.False(t, st.RolledBack)
	require.NoError(t, st.Writable())
}

// A vault's key file never carries conv or names: a rewrite that would add
// one is refused, so `filex encrypt` cannot turn a vault into a level-1/2
// folder half way.
func TestVaultKeyFile_IsNeverConvertedOrGivenNames(t *testing.T) {
	v := loadVaultVectors(t)
	_, err := StartConversion(v.Marker, time.Now(), nil)
	require.Error(t, err)
	_, _, err = EnableNames(v.Marker, unhex(t, v.Secrets.FMK), nil)
	require.Error(t, err)
	m, err := ParseMarker(v.Marker)
	require.NoError(t, err)
	require.False(t, m.ConvPending)
	require.False(t, m.HasNames())
}
