package e2edecrypt

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/text/unicode/norm"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

// memVaultSource is a vault held in memory: what a writer stored, read back.
type memVaultSource struct {
	packs   map[[16]byte][]byte
	indexes map[uint64][]byte
}

func newMemVaultSource() *memVaultSource {
	return &memVaultSource{packs: map[[16]byte][]byte{}, indexes: map[uint64][]byte{}}
}

func (m *memVaultSource) sink(_ context.Context, id [16]byte, b []byte) error {
	m.packs[id] = bytes.Clone(b)
	return nil
}

func (m *memVaultSource) ListIndexes(context.Context) ([]VaultObject, error) {
	var out []VaultObject
	for g, b := range m.indexes {
		out = append(out, VaultObject{Generation: g, Size: int64(len(b))})
	}
	return out, nil
}

func (m *memVaultSource) ListPacks(context.Context) ([]VaultObject, error) {
	var out []VaultObject
	for id, b := range m.packs {
		out = append(out, VaultObject{Pack: id, Size: int64(len(b))})
	}
	return out, nil
}

func (m *memVaultSource) ReadIndex(_ context.Context, g uint64) ([]byte, error) {
	b, ok := m.indexes[g]
	if !ok {
		return nil, fmt.Errorf("%d: %w", g, ErrVaultObjectMissing)
	}
	return b, nil
}

func (m *memVaultSource) ReadPack(_ context.Context, id [16]byte, off, n int64) ([]byte, error) {
	b, ok := m.packs[id]
	if !ok {
		return nil, fmt.Errorf("%x: %w", id, ErrVaultObjectMissing)
	}
	return bytes.Clone(b[off : off+n]), nil
}

func testVaultKeys(t *testing.T) *VaultKeys {
	t.Helper()
	fmk := make([]byte, 32)
	_, _ = rand.Read(fmk)
	k, err := NewVaultKeys(fmk, [16]byte{7})
	require.NoError(t, err)
	return k
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// commitTo writes one generation into m and returns it, read back.
func commitTo(t *testing.T, m *memVaultSource, k *VaultKeys, base *VaultIndex, ops func(w *VaultWriter)) *VaultIndex {
	t.Helper()
	w, err := NewVaultWriter(VaultWriterConfig{Keys: k, PackLog2: 16, Base: base, Sink: m.sink})
	require.NoError(t, err)
	ops(w)
	c, err := w.Commit(context.Background())
	require.NoError(t, err)
	m.indexes[c.Index.Generation] = c.File
	idx, err := OpenVaultIndex(k, 16, c.Index.Generation, c.File)
	require.NoError(t, err)
	return idx
}

func writeFile(t *testing.T, w *VaultWriter, p string, data []byte) {
	t.Helper()
	_, err := w.Write(context.Background(), p, 1000, bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err, p)
}

func vaultReadBack(t *testing.T, m *memVaultSource, k *VaultKeys, idx *VaultIndex, p string) []byte {
	t.Helper()
	n := idx.Tree.Lookup(p)
	require.NotNil(t, n, p)
	var buf bytes.Buffer
	_, err := NewVaultReader(m, k).WriteTo(context.Background(), n, &buf)
	require.NoError(t, err, p)
	return buf.Bytes()
}

func TestVaultWriter_RoundTrip(t *testing.T) {
	k := testVaultKeys(t)
	m := newMemVaultSource()
	a, b := randomBytes(70000), randomBytes(5)
	g1 := commitTo(t, m, k, nil, func(w *VaultWriter) {
		require.NoError(t, w.Mkdir("Belgeler", 1))
		writeFile(t, w, "Belgeler/a.bin", a)
		writeFile(t, w, "b.txt", b)
		writeFile(t, w, "boş", nil)
	})
	require.EqualValues(t, 1, g1.Generation)
	require.Equal(t, a, vaultReadBack(t, m, k, g1, "Belgeler/a.bin"))
	require.Equal(t, b, vaultReadBack(t, m, k, g1, "b.txt"))
	require.Len(t, g1.Packs, 2, "70 000 + 5 bytes of body spread over two 64 KiB packs")

	// A new version always gets a new content id, even for the same bytes.
	g2 := commitTo(t, m, k, g1, func(w *VaultWriter) {
		writeFile(t, w, "b.txt", b)
		require.NoError(t, w.Move("Belgeler", "Arşiv"))
	})
	require.NotEqual(t, g1.Tree.Lookup("b.txt").Content.ID, g2.Tree.Lookup("b.txt").Content.ID)
	require.Equal(t, b, vaultReadBack(t, m, k, g2, "b.txt"))
	require.Equal(t, a, vaultReadBack(t, m, k, g2, "Arşiv/a.bin"))
	require.Equal(t, g1.Tree.Lookup("Belgeler/a.bin").MTime, g2.Tree.Lookup("Arşiv/a.bin").MTime, "a move keeps mtime")
}

// A pack whose upload outcome is unknown goes again under a new id, and the
// extents follow it.
func TestVaultWriter_UnknownPackOutcomeSendsANewID(t *testing.T) {
	k := testVaultKeys(t)
	m := newMemVaultSource()
	var tries [][16]byte
	sink := func(ctx context.Context, id [16]byte, b []byte) error {
		tries = append(tries, id)
		if len(tries) == 1 {
			return fmt.Errorf("time-out: %w", ErrVaultPackUnknown)
		}
		require.Equal(t, id[:], b[16:32], "the header carries the new id")
		return m.sink(ctx, id, b)
	}
	data := randomBytes(1000)
	w, err := NewVaultWriter(VaultWriterConfig{Keys: k, PackLog2: 16, Sink: sink})
	require.NoError(t, err)
	writeFile(t, w, "a", data)
	c, err := w.Commit(context.Background())
	require.NoError(t, err)
	require.Len(t, tries, 2)
	require.NotEqual(t, tries[0], tries[1])
	require.Equal(t, [][16]byte{tries[1]}, c.Index.Packs)
	require.Equal(t, tries[1], c.Index.Tree.Lookup("a").Content.Extents[0].Pack)
	require.Equal(t, [][16]byte{tries[1]}, c.Written)
	m.indexes[1] = c.File
	idx, err := OpenVaultIndex(k, 16, 1, c.File)
	require.NoError(t, err)
	require.Equal(t, data, vaultReadBack(t, m, k, idx, "a"))
}

func TestVaultWriter_RefusesWhatTheTreeRefuses(t *testing.T) {
	k := testVaultKeys(t)
	w, err := NewVaultWriter(VaultWriterConfig{Keys: k, PackLog2: 16, Sink: discardPacks})
	require.NoError(t, err)
	require.NoError(t, w.Mkdir("a", 0))
	require.ErrorIs(t, w.Mkdir("a", 0), ErrVaultExists)
	require.ErrorIs(t, w.Mkdir("missing/b", 0), ErrVaultNotFound)
	require.ErrorIs(t, w.Mkdir("a/..", 0), ErrVaultBadName)
	require.NoError(t, w.Mkdir("a/b", 0))
	require.ErrorIs(t, w.Move("a", "a/b/c"), ErrVaultLoop)
	_, err = w.Write(context.Background(), "a", 0, bytes.NewReader([]byte("x")), 1)
	require.ErrorIs(t, err, ErrVaultIsDir)
	_, err = w.Write(context.Background(), "short", 0, bytes.NewReader([]byte("x")), 2)
	require.Error(t, err, "fewer bytes than the size says")
	_, err = w.Write(context.Background(), "long", 0, bytes.NewReader([]byte("xyz")), 2)
	require.Error(t, err, "more bytes than the size says")
	require.NoError(t, w.Delete("a"))
	require.Zero(t, w.Tree().Len())
}

func TestVaultTree_NamesAndDepth(t *testing.T) {
	tr := NewVaultTree()
	nfd := norm.NFD.String("Arşiv")
	n, err := tr.Mkdir(nfd, 5)
	require.NoError(t, err)
	require.Equal(t, "Arşiv", n.Name, "stored in NFC")
	require.Same(t, n, tr.Lookup(nfd), "found by its NFD spelling too")
	require.Same(t, n, tr.Lookup("/Arşiv/"))

	p := ""
	for i := 0; i < e2e.VaultMaxDepth; i++ {
		p += "/d"
		_, err := tr.Mkdir(p, 0)
		require.NoError(t, err, "depth %d", i+1)
	}
	_, err = tr.Mkdir(p+"/d", 0)
	require.ErrorIs(t, err, ErrVaultTooDeep)
	_, err = tr.PutFile(p+"/f", 0, 0, nil)
	require.ErrorIs(t, err, ErrVaultTooDeep)
	require.ErrorIs(t, tr.CheckMove("d", "Arşiv/d"), ErrVaultTooDeep, "a move may not push a subtree past the limit")
	removed, err := tr.Remove("d")
	require.NoError(t, err)
	require.Equal(t, "d", removed.Name)
	require.Equal(t, 1, tr.Len())
}

func TestPlanVaultGC(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	old := now.Add(-time.Hour)
	A, B, C, D, E, F := [16]byte{0xa}, [16]byte{0xb}, [16]byte{0xc}, [16]byte{0xd}, [16]byte{0xe}, [16]byte{0xf}
	tr := NewVaultTree()
	_, err := tr.PutFile("x", 0, 5, &VaultContent{ChunkLog2: 20, Extents: []VaultExtent{{Pack: A, Offset: 32, Length: 21}}})
	require.NoError(t, err)
	latest := &VaultIndex{Generation: 6, Tree: tr, Packs: [][16]byte{A}, Grave: []VaultGrave{{Pack: B, Died: 4}, {Pack: C, Died: 6}, {Pack: F, Died: 3}}}
	indexes := []VaultObject{
		{Generation: 1, ModTime: old}, {Generation: 2, ModTime: old}, {Generation: 3, ModTime: old},
		{Generation: 4, ModTime: old}, {Generation: 5, ModTime: now.Add(-time.Minute)}, {Generation: 6, ModTime: now},
	}
	packs := []VaultObject{{Pack: A}, {Pack: B}, {Pack: C}, {Pack: D}, {Pack: E}}
	mine := map[[16]byte]bool{E: true}

	plan := PlanVaultGC(latest, indexes, packs, mine, now, VaultGCPassMax)
	require.Equal(t, []uint64{1, 2, 3}, plan.Indexes, "the three newest stay; 1 to 3 were replaced long ago")
	require.ElementsMatch(t, [][16]byte{B, D}, plan.Packs, "B's last user (3) expired; D is an orphan; E is this writer's; C's last user (5) is kept; A is live")
	require.ElementsMatch(t, [][16]byte{B, F}, plan.Forget, "F is gone already: it leaves the graveyard too")

	// Replaced less than 15 minutes ago: kept for its readers.
	indexes[3].ModTime = now.Add(-5 * time.Minute)
	plan = PlanVaultGC(latest, indexes, packs, mine, now, VaultGCPassMax)
	require.Equal(t, []uint64{1, 2}, plan.Indexes)
	require.ElementsMatch(t, [][16]byte{D}, plan.Packs, "B's last user, 3, was replaced 5 minutes ago")

	// One pass is bounded.
	plan = PlanVaultGC(latest, indexes, packs, mine, now, 1)
	require.Equal(t, 1, len(plan.Indexes)+len(plan.Packs))

	// Nothing while the latest must not be written.
	latest.ReadOnly = "a newer filex wrote this vault"
	require.True(t, PlanVaultGC(latest, indexes, packs, mine, now, VaultGCPassMax).Empty())
}

// Repacking copies the live extents into new packs, nothing encrypted again,
// and the old packs go to the graveyard.
func TestVaultRepack_RoundTrip(t *testing.T) {
	k := testVaultKeys(t)
	m := newMemVaultSource()
	files := map[string][]byte{}
	var idx *VaultIndex
	// Four generations, each with one small file in a pack of its own, and
	// a big one deleted again: four packs, each mostly dead.
	for i := range 4 {
		name := fmt.Sprintf("f%d", i)
		files[name] = randomBytes(3000)
		idx = commitTo(t, m, k, idx, func(w *VaultWriter) {
			writeFile(t, w, name, files[name])
			writeFile(t, w, "big", randomBytes(30000))
		})
	}
	idx = commitTo(t, m, k, idx, func(w *VaultWriter) { require.NoError(t, w.Delete("big")) })
	s := PlanVaultRepack(idx, 16)
	require.Len(t, s, 4)

	ids := map[string][16]byte{}
	for name := range files {
		ids[name] = idx.Tree.Lookup(name).Content.ID
	}
	before := idx.Generation
	idx = commitTo(t, m, k, idx, func(w *VaultWriter) {
		require.NoError(t, w.Repack(context.Background(), s, m.ReadPack))
	})
	require.Len(t, idx.Packs, 1, "12 000 live bytes fit one pack")
	for name, data := range files {
		require.Equal(t, data, vaultReadBack(t, m, k, idx, name))
		require.Equal(t, ids[name], idx.Tree.Lookup(name).Content.ID, "nothing was encrypted again")
	}
	for _, p := range s {
		i, ok := slices.BinarySearchFunc(idx.Grave, p, func(g VaultGrave, id [16]byte) int { return compareVaultIDs(g.Pack, id) })
		require.True(t, ok)
		require.Equal(t, before+1, idx.Grave[i].Died)
	}
	require.Nil(t, PlanVaultRepack(idx, 16))
}

// A file whose two extents land side by side in one new pack keeps them as
// two extents: a repack merges nothing (docs/E2E-VAULT-FORMAT.md → "Details
// the implementations settled"), so two writers lay a repack out alike.
// web/tests/lib/e2evaultWriter.test.ts holds the browser to the same.
func TestVaultRepack_KeepsExtentsApart(t *testing.T) {
	k := testVaultKeys(t)
	m := newMemVaultSource()
	area := VaultPackDataArea(16)
	// The filler leaves 488 bytes of the first pack: the file's body (3 016
	// bytes) is 488 there and 2 528 at the start of the second.
	filler := randomBytes(int(area) - 488 - 16)
	data := randomBytes(3000)
	idx := commitTo(t, m, k, nil, func(w *VaultWriter) {
		writeFile(t, w, "filler", filler)
		writeFile(t, w, "x", data)
	})
	require.Len(t, idx.Tree.Lookup("x").Content.Extents, 2)
	idx = commitTo(t, m, k, idx, func(w *VaultWriter) { require.NoError(t, w.Delete("filler")) })
	s := PlanVaultRepack(idx, 16)
	require.Len(t, s, 2)
	idx = commitTo(t, m, k, idx, func(w *VaultWriter) {
		require.NoError(t, w.Repack(context.Background(), s, m.ReadPack))
	})
	require.Len(t, idx.Packs, 1)
	ext := idx.Tree.Lookup("x").Content.Extents
	require.Len(t, ext, 2, "side by side in one pack, still two extents")
	require.Equal(t, ext[0].Pack, ext[1].Pack)
	require.EqualValues(t, e2e.VaultPackHeaderLen, ext[0].Offset)
	require.EqualValues(t, 488, ext[0].Length)
	require.Equal(t, ext[0].Offset+ext[0].Length, ext[1].Offset)
	require.EqualValues(t, 2528, ext[1].Length)
	require.Equal(t, data, vaultReadBack(t, m, k, idx, "x"))
}
