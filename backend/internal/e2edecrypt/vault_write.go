package e2edecrypt

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

// Writing a vault - docs/E2E-VAULT-FORMAT.md → "Writing". Everything here
// happens under the write lock; the writer only builds bytes - the caller
// stores the packs (Sink) and commits the index file Commit returns.
//
// The canonical layout: a generation starts with no open pack and never adds
// to a pack of an earlier one. New contents draw a content id, are encrypted,
// and their bytes are appended to the open pack, which is opened (a pack id
// drawn) when there is none and closed, unpadded, when its data area is full.
// After the last operation an open pack is padded with random bytes and
// closed; then the body is built and sealed under a new seal id. All random
// bytes come from one source in exactly this order, so two implementations
// given the same operations and the same random bytes write the same packs
// and the same index.

// ErrVaultPackUnknown is what a Sink wraps when it cannot tell whether a pack
// was stored (a time-out): the writer sends the same data again under a new
// pack id.
var ErrVaultPackUnknown = errors.New("the pack upload's outcome is unknown")

// ErrVaultReadOnly: this state of the vault must not be written.
var ErrVaultReadOnly = errors.New("this vault is read-only here")

// VaultPackSink stores one closed pack: its id and its 2^pack bytes.
type VaultPackSink func(ctx context.Context, id [16]byte, pack []byte) error

// VaultPackReader reads n bytes of a stored pack from off (repacking).
type VaultPackReader func(ctx context.Context, id [16]byte, off, n int64) ([]byte, error)

// VaultWriterConfig configures NewVaultWriter.
type VaultWriterConfig struct {
	Keys     *VaultKeys
	PackLog2 int
	// Base is the latest generation, which the commit follows; nil is an
	// empty vault at generation 0 (the creation commit writes generation 1).
	Base *VaultIndex
	// Rand is where every random byte comes from; nil is crypto/rand.
	Rand io.Reader
	// Sink stores packs as they close.
	Sink VaultPackSink
}

// VaultWriter builds the next generation of a vault.
type VaultWriter struct {
	keys     *VaultKeys
	packLog2 int
	rand     io.Reader
	sink     VaultPackSink
	base     *VaultIndex
	tree     *VaultTree
	gen      uint64
	open     *vaultPack
	written  [][16]byte
	deleted  map[[16]byte]bool
	done     bool
}

// VaultCommit is a sealed generation, ready to store.
type VaultCommit struct {
	// Index is the new generation as readers will decode it.
	Index *VaultIndex
	// File is the index file to store as v/idx/<generation>.fxi.
	File []byte
	// Written are the packs this writer stored (orphans if the index is
	// never stored).
	Written [][16]byte
}

// NewVaultWriter starts the generation after cfg.Base.
func NewVaultWriter(cfg VaultWriterConfig) (*VaultWriter, error) {
	if cfg.Keys == nil {
		return nil, errors.New("e2edecrypt: a vault writer needs the vault's keys")
	}
	if cfg.PackLog2 < e2e.VaultMinPackLog2 || cfg.PackLog2 > e2e.VaultMaxPackLog2 {
		return nil, fmt.Errorf("e2edecrypt: a pack size of 2^%d", cfg.PackLog2)
	}
	if cfg.Sink == nil {
		return nil, errors.New("e2edecrypt: a vault writer needs somewhere to store packs")
	}
	base := cfg.Base
	if base == nil {
		base = emptyVaultIndex()
	}
	if base.ReadOnly != "" {
		return nil, fmt.Errorf("%w: %s", ErrVaultReadOnly, base.ReadOnly)
	}
	rnd := cfg.Rand
	if rnd == nil {
		rnd = rand.Reader
	}
	return &VaultWriter{
		keys:     cfg.Keys,
		packLog2: cfg.PackLog2,
		rand:     rnd,
		sink:     cfg.Sink,
		base:     base,
		tree:     base.Tree.Clone(),
		gen:      base.Generation + 1,
		deleted:  map[[16]byte]bool{},
	}, nil
}

// Generation is the generation this writer commits.
func (w *VaultWriter) Generation() uint64 { return w.gen }

// Tree is the tree as the operations so far left it. Do not change it.
func (w *VaultWriter) Tree() *VaultTree { return w.tree }

// Warning is what to say before a change to this vault, or "".
func (w *VaultWriter) Warning() string { return VaultWarning(w.tree.Len(), w.base.Size) }

func (w *VaultWriter) draw(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(w.rand, b); err != nil {
		return nil, fmt.Errorf("e2edecrypt: reading random bytes: %w", err)
	}
	return b, nil
}

func (w *VaultWriter) drawID() ([16]byte, error) {
	var id [16]byte
	b, err := w.draw(vaultIDLen)
	copy(id[:], b)
	return id, err
}

func (w *VaultWriter) usable() error {
	if w.done {
		return errors.New("e2edecrypt: this generation is already committed")
	}
	return nil
}

// Mkdir makes a new folder.
func (w *VaultWriter) Mkdir(p string, mtime int64) error {
	if err := w.usable(); err != nil {
		return err
	}
	if w.tree.Len() >= e2e.VaultEntriesWriteMax {
		return ErrVaultFull
	}
	_, err := w.tree.Mkdir(p, mtime)
	return err
}

// Write stores new contents for the file at p - a new file, or a new version
// of one: always a new content id, even for the same bytes. r must hold
// exactly size bytes.
func (w *VaultWriter) Write(ctx context.Context, p string, mtime int64, r io.Reader, size int64) (*VaultNode, error) {
	if err := w.usable(); err != nil {
		return nil, err
	}
	if size < 0 {
		return nil, errors.New("e2edecrypt: a negative size")
	}
	adds, err := w.tree.CheckPutFile(p)
	if err != nil {
		return nil, err
	}
	if adds && w.tree.Len() >= e2e.VaultEntriesWriteMax {
		return nil, ErrVaultFull
	}
	var c *VaultContent
	if size > 0 {
		if c, err = w.encrypt(ctx, p, r, size); err != nil {
			return nil, err
		}
	}
	return w.tree.PutFile(p, mtime, size, c)
}

// encrypt draws a content id and appends the STREAM of r to the open pack.
func (w *VaultWriter) encrypt(ctx context.Context, p string, r io.Reader, size int64) (*VaultContent, error) {
	cid, err := w.drawID()
	if err != nil {
		return nil, err
	}
	aead, err := w.keys.contentAEAD(cid)
	if err != nil {
		return nil, err
	}
	c := &VaultContent{ID: cid, ChunkLog2: VaultChunkLog2}
	cs := int64(1) << VaultChunkLog2
	chunks := vaultChunks(size, VaultChunkLog2)
	if chunks > 1<<32 {
		return nil, fmt.Errorf("%s: too large for one vault file", p)
	}
	buf := make([]byte, cs)
	sealed := make([]byte, 0, cs+gcmTagLen)
	for i := int64(0); i < chunks; i++ {
		n := min(cs, size-i*cs)
		if _, err := io.ReadFull(r, buf[:n]); err != nil {
			return nil, fmt.Errorf("%s: reading its contents: %w", p, err)
		}
		sealed = aead.Seal(sealed[:0], vaultChunkNonce(uint32(i), i == chunks-1), buf[:n], nil)
		if err := w.appendBody(ctx, c, sealed, true); err != nil {
			return nil, err
		}
	}
	var one [1]byte
	if k, _ := r.Read(one[:]); k > 0 {
		return nil, fmt.Errorf("%s: its contents are longer than %d bytes", p, size)
	}
	return c, nil
}

// appendBody appends body bytes to the open pack, opening and closing packs
// as it goes, and records them as extents of c. With merge (a body being
// written) the extents are maximal: a run that goes on in the same pack
// lengthens the last extent. Without it (a repack) every piece is an extent
// of its own, as docs/E2E-VAULT-FORMAT.md → "Details the implementations
// settled" asks, so two writers lay a repack out alike.
func (w *VaultWriter) appendBody(ctx context.Context, c *VaultContent, b []byte, merge bool) error {
	for len(b) > 0 {
		if w.open == nil {
			id, err := w.drawID()
			if err != nil {
				return err
			}
			w.open = newVaultPack(id, w.packLog2)
		}
		n := min(len(b), w.open.room())
		off := e2e.VaultPackHeaderLen + w.open.used
		copy(w.open.buf[off:], b[:n])
		if k := len(c.Extents); merge && k > 0 && c.Extents[k-1].Pack == w.open.id && c.Extents[k-1].Offset+c.Extents[k-1].Length == int64(off) {
			c.Extents[k-1].Length += int64(n)
		} else {
			c.Extents = append(c.Extents, VaultExtent{Pack: w.open.id, Offset: int64(off), Length: int64(n)})
		}
		w.open.used += n
		b = b[n:]
		if w.open.room() == 0 {
			if err := w.closePack(ctx, false, c); err != nil {
				return err
			}
		}
	}
	return nil
}

// closePack pads the open pack (pad: the last one of the generation) and
// stores it. When the sink cannot tell whether it was stored, the same data
// goes again under a new id, and every extent - in the tree and in the
// content being built - follows it.
func (w *VaultWriter) closePack(ctx context.Context, pad bool, building *VaultContent) error {
	p := w.open
	if pad && p.room() > 0 {
		fill, err := w.draw(p.room())
		if err != nil {
			return err
		}
		copy(p.buf[e2e.VaultPackHeaderLen+p.used:], fill)
	}
	const attempts = 3
	for try := 1; ; try++ {
		err := w.sink(ctx, p.id, p.buf)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrVaultPackUnknown) || try == attempts {
			return err
		}
		var fresh [16]byte
		if _, rerr := io.ReadFull(rand.Reader, fresh[:]); rerr != nil {
			return rerr
		}
		w.renamePack(p.id, fresh, building)
		p.setID(fresh)
	}
	w.written = append(w.written, p.id)
	w.open = nil
	return nil
}

// renamePack points every extent in pack from at pack to. Contents are never
// changed in place (trees share them): a touched one is copied.
func (w *VaultWriter) renamePack(from, to [16]byte, building *VaultContent) {
	if building != nil {
		for i := range building.Extents {
			if building.Extents[i].Pack == from {
				building.Extents[i].Pack = to
			}
		}
	}
	_ = w.tree.Walk(func(n *VaultNode, _ int) error {
		if n.Content == nil || n.Content == building {
			return nil
		}
		if !slices.ContainsFunc(n.Content.Extents, func(x VaultExtent) bool { return x.Pack == from }) {
			return nil
		}
		c := &VaultContent{ID: n.Content.ID, ChunkLog2: n.Content.ChunkLog2, Extents: slices.Clone(n.Content.Extents)}
		for i := range c.Extents {
			if c.Extents[i].Pack == from {
				c.Extents[i].Pack = to
			}
		}
		n.Content = c
		return nil
	})
}

// Delete removes a file, or a folder with everything in it.
func (w *VaultWriter) Delete(p string) error {
	if err := w.usable(); err != nil {
		return err
	}
	_, err := w.tree.Remove(p)
	return err
}

// Move gives an entry a new folder or name; mtime and contents are kept.
func (w *VaultWriter) Move(from, to string) error {
	if err := w.usable(); err != nil {
		return err
	}
	_, err := w.tree.Move(from, to)
	return err
}

// ForgetGrave drops packs a collection deleted from the graveyard this
// commit writes.
func (w *VaultWriter) ForgetGrave(ids [][16]byte) {
	for _, id := range ids {
		w.deleted[id] = true
	}
}

// Repack copies the live extents in packs - entry by entry in index order,
// each entry's extents in order, the bytes as they are, nothing encrypted
// again - into new packs by the canonical layout. Those packs go to the
// graveyard when this generation commits.
func (w *VaultWriter) Repack(ctx context.Context, packs [][16]byte, read VaultPackReader) error {
	if err := w.usable(); err != nil {
		return err
	}
	set := make(map[[16]byte]bool, len(packs))
	for _, p := range packs {
		set[p] = true
	}
	var files []*VaultNode
	_ = w.tree.Walk(func(n *VaultNode, _ int) error {
		if n.Content != nil && slices.ContainsFunc(n.Content.Extents, func(x VaultExtent) bool { return set[x.Pack] }) {
			files = append(files, n)
		}
		return nil
	})
	for _, n := range files {
		old := n.Content
		c := &VaultContent{ID: old.ID, ChunkLog2: old.ChunkLog2}
		for _, x := range old.Extents {
			if !set[x.Pack] {
				c.Extents = append(c.Extents, x)
				continue
			}
			b, err := read(ctx, x.Pack, x.Offset, x.Length)
			if err != nil {
				return err
			}
			if int64(len(b)) != x.Length {
				return vaultDamage(e2e.VaultPackPath(x.Pack), "the pack is shorter than its extents")
			}
			if err := w.appendBody(ctx, c, b, false); err != nil {
				return err
			}
		}
		n.Content = c
	}
	return nil
}

// Commit closes the open pack and seals the new generation. The caller
// stores Commit.File as the index of Commit.Index.Generation; until that
// succeeded nothing of the change is visible, and the writer is spent either
// way.
func (w *VaultWriter) Commit(ctx context.Context) (*VaultCommit, error) {
	if err := w.usable(); err != nil {
		return nil, err
	}
	w.done = true
	if w.open != nil {
		if err := w.closePack(ctx, true, nil); err != nil {
			return nil, err
		}
	}
	table := w.tree.packTable()
	live := make(map[[16]byte]bool, len(table))
	for _, p := range table {
		live[p] = true
	}
	var grave []VaultGrave
	for _, g := range w.base.Grave {
		if !w.deleted[g.Pack] && !live[g.Pack] {
			grave = append(grave, g)
		}
	}
	for _, p := range w.base.Packs {
		if !live[p] && !w.deleted[p] {
			grave = append(grave, VaultGrave{Pack: p, Died: w.gen})
		}
	}
	body, table, err := EncodeVaultBody(w.tree, grave)
	if err != nil {
		return nil, err
	}
	if e2e.VaultIndexFileSize(int64(len(body))) > e2e.VaultIndexWriteMax {
		return nil, ErrVaultIndexTooLarge
	}
	sealID, err := w.drawID()
	if err != nil {
		return nil, err
	}
	file, err := SealVaultIndex(w.keys, w.gen, body, sealID)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(grave, func(x, y VaultGrave) int { return compareVaultIDs(x.Pack, y.Pack) })
	return &VaultCommit{
		Index: &VaultIndex{
			Generation: w.gen,
			Tree:       w.tree,
			Packs:      table,
			Grave:      grave,
			BodyLen:    len(body),
			Size:       int64(len(file)),
		},
		File:    file,
		Written: slices.Clone(w.written),
	}, nil
}
