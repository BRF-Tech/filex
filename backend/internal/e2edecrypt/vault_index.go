package e2edecrypt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

// The index of a vault - docs/E2E-VAULT-FORMAT.md → "The index". One file per
// generation:
//
//	[0..8)   magic "filexvlt"   [8] 0x01   [9] 'I'   [10..16) zero
//	[16..24) generation, uint64 BE (= its name)
//	[24..40) seal id
//	[40..)   AES-256-GCM(index key, nonce 0, body ‖ zeros, AD = bytes 0..39) ‖ tag
//
// and its body, unsigned LEB128 integers (minimal, at most 2^53 - 1):
//
//	body    = version:u8 flags:uvarint
//	          pack_count  pack_id:16B ×
//	          entry_count entry ×
//	          grave_count (pack_id:16B died:uvarint) ×
//	          ext_len ext
//	entry   = kind:u8 parent:uvarint name_len name mtime:uvarint
//	          [kind 2: size:uvarint content] ext_len ext
//	content = 0x00 | 0x01 content_id:16B chunk_log2:u8 extent_count extent ×
//	extent  = pack:uvarint offset:uvarint length:uvarint
//
// A tree, its pack table and its graveyard have ONE encoding (canonical
// order: pre-order, siblings by the bytes of their names), so two writers that
// agree on them write the same bytes - which the test vectors check.

const (
	vaultBodyVersion  = 1
	vaultKindFolder   = 1
	vaultKindFile     = 2
	vaultContentNone  = 0
	vaultContentChunk = 1
	vaultMaxUvarint   = 1<<53 - 1
	// vaultIndexWarnBytes is where a client starts warning about the index
	// size (the writer refuses at e2e.VaultIndexWriteMax).
	vaultIndexWarnBytes = 24 << 20
)

// ErrVaultDamaged is wrapped by every error that says a vault file does not
// verify or breaks a rule of the format. Readers fall back to an older
// generation on it; nothing else is damage.
var ErrVaultDamaged = errors.New("damaged")

// ErrVaultIndexTooLarge: the change would make the index larger than a writer
// may write.
var ErrVaultIndexTooLarge = fmt.Errorf("the vault's index would pass %d MiB", e2e.VaultIndexWriteMax>>20)

// VaultGrave is one graveyard entry: a pack the latest tree no longer uses,
// which an older generation still kept may use.
type VaultGrave struct {
	Pack [16]byte
	Died uint64
}

// VaultIndex is one generation of a vault, decoded and checked.
type VaultIndex struct {
	Generation uint64
	Tree       *VaultTree
	// Packs is the pack table: every pack the tree uses, ascending.
	Packs [][16]byte
	// Grave is the graveyard, ascending by pack id.
	Grave []VaultGrave
	// ReadOnly, when not "", is why a writer must not write this vault
	// (extension bytes a newer filex wrote).
	ReadOnly string
	// BodyLen is the length of the body; Size the index file's (0 for an
	// index that was not read from a file).
	BodyLen int
	Size    int64
	// ModTime is when the index file was written, where the source says.
	ModTime time.Time
}

// emptyVaultIndex is generation 0: a vault whose creation stopped before its
// first index, or the base of the creation commit.
func emptyVaultIndex() *VaultIndex { return &VaultIndex{Tree: NewVaultTree()} }

// vaultDamage is the error of a vault file that breaks the format.
func vaultDamage(where, format string, a ...any) error {
	return &CorruptError{Path: where, Err: fmt.Errorf(format+": %w", append(a, ErrVaultDamaged)...)}
}

// ─────────────────────────────── uvarint ──────────────────────────────────

func appendVaultUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// vaultDecoder reads a body; the first problem sticks.
type vaultDecoder struct {
	b   []byte
	off int
	err error
}

func (d *vaultDecoder) fail(format string, a ...any) {
	if d.err == nil {
		d.err = fmt.Errorf(format, a...)
	}
}

func (d *vaultDecoder) remaining() int { return len(d.b) - d.off }

func (d *vaultDecoder) byte() byte {
	if d.err != nil {
		return 0
	}
	if d.off >= len(d.b) {
		d.fail("the index body ends early")
		return 0
	}
	c := d.b[d.off]
	d.off++
	return c
}

func (d *vaultDecoder) bytes(n uint64) []byte {
	if d.err != nil {
		return nil
	}
	if n > uint64(d.remaining()) {
		d.fail("the index body ends early")
		return nil
	}
	out := d.b[d.off : d.off+int(n)]
	d.off += int(n)
	return out
}

func (d *vaultDecoder) id() (out [16]byte) {
	copy(out[:], d.bytes(vaultIDLen))
	return out
}

// uvarint reads one minimal unsigned LEB128 of at most 2^53 - 1.
func (d *vaultDecoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	var v uint64
	for n := 0; ; n++ {
		if n == 8 {
			d.fail("a number in the index is larger than 2^53 - 1")
			return 0
		}
		if d.off >= len(d.b) {
			d.fail("the index body ends early")
			return 0
		}
		c := d.b[d.off]
		d.off++
		v |= uint64(c&0x7f) << (7 * n)
		if c&0x80 == 0 {
			if n > 0 && c == 0 {
				d.fail("a number in the index is not written minimally")
				return 0
			}
			break
		}
	}
	if v > vaultMaxUvarint {
		d.fail("a number in the index is larger than 2^53 - 1")
		return 0
	}
	return v
}

// ─────────────────────────────── decoding ─────────────────────────────────

// vaultFlagName names a required-feature bit of the body's flags.
func vaultFlagName(bit int) string {
	if bit == 0 {
		return "vault deduplication"
	}
	return fmt.Sprintf("vault index flag bit %d", bit)
}

// DecodeVaultBody reads and checks an index body - plaintext is the body
// followed by its zero padding - as the index of generation gen of a vault
// with packs of 2^packLog2 bytes. A body this build cannot read (another
// version, a flag it does not know) is an *UnsupportedError; anything that
// breaks a rule of the format wraps ErrVaultDamaged.
func DecodeVaultBody(plaintext []byte, packLog2 int, gen uint64) (*VaultIndex, error) {
	return decodeVaultBody(plaintext, packLog2, gen, "the vault index")
}

func decodeVaultBody(plaintext []byte, packLog2 int, gen uint64, where string) (*VaultIndex, error) {
	d := &vaultDecoder{b: plaintext}
	ver := d.byte()
	if d.err == nil && ver != vaultBodyVersion {
		return nil, &UnsupportedError{Features: []string{fmt.Sprintf("vault index version %d", ver)}}
	}
	flags := d.uvarint()
	if d.err == nil && flags != 0 {
		var names []string
		for bit := 0; bit < 53; bit++ {
			if flags&(1<<bit) != 0 {
				names = append(names, vaultFlagName(bit))
			}
		}
		return nil, &UnsupportedError{Features: names}
	}
	idx := &VaultIndex{Generation: gen, Tree: NewVaultTree()}

	// The pack table.
	packCount := d.uvarint()
	if d.err == nil && packCount > uint64(d.remaining()/vaultIDLen) {
		d.fail("the pack table is longer than the index")
	}
	if d.err == nil {
		idx.Packs = make([][16]byte, 0, packCount)
	}
	for i := uint64(0); d.err == nil && i < packCount; i++ {
		id := d.id()
		if i > 0 && compareVaultIDs(idx.Packs[i-1], id) >= 0 {
			d.fail("the pack table is not in ascending order")
		}
		idx.Packs = append(idx.Packs, id)
	}
	used := make([]bool, len(idx.Packs))

	// The entries. The smallest entry is 6 bytes (a folder with a 1-byte
	// name), which bounds what a hostile count can make us allocate.
	entryCount := d.uvarint()
	if d.err == nil && entryCount > e2e.VaultEntriesReadMax {
		d.fail("the index holds %d entries, more than a reader accepts (%d)", entryCount, e2e.VaultEntriesReadMax)
	}
	if d.err == nil && entryCount > uint64(d.remaining()/6) {
		d.fail("the index body ends early")
	}
	type level struct {
		pos  uint64
		node *VaultNode
		last string
		has  bool
	}
	chain := []level{{pos: 0, node: idx.Tree.root}}
	packArea := int64(1) << packLog2
	for i := uint64(0); d.err == nil && i < entryCount; i++ {
		kind := d.byte()
		parent := d.uvarint()
		nameLen := d.uvarint()
		if d.err == nil && (nameLen < 1 || nameLen > NameMaxBytes) {
			d.fail("entry %d: a name of %d bytes", i+1, nameLen)
		}
		name := string(d.bytes(nameLen))
		mtime := d.uvarint()
		if d.err != nil {
			break
		}
		if kind != vaultKindFolder && kind != vaultKindFile {
			d.fail("entry %d: an entry of unknown kind %d", i+1, kind)
			break
		}
		if prob := vaultNameProblem(name); prob != "" {
			d.fail("entry %d: the name %q is not allowed (%s)", i+1, name, prob)
			break
		}
		if parent > i {
			d.fail("entry %d: its folder comes after it", i+1)
			break
		}
		// Leave folders until the parent is the one we are in: pre-order.
		for len(chain) > 0 && chain[len(chain)-1].pos != parent {
			chain = chain[:len(chain)-1]
		}
		if len(chain) == 0 {
			d.fail("entry %d: not in pre-order, or its parent is not a folder", i+1)
			break
		}
		top := &chain[len(chain)-1]
		if top.has && name <= top.last {
			d.fail("entry %d: %q is not after %q, the name before it in its folder", i+1, name, top.last)
			break
		}
		top.last, top.has = name, true
		if len(chain) > e2e.VaultMaxDepth {
			d.fail("entry %d: nested deeper than %d", i+1, e2e.VaultMaxDepth)
			break
		}
		n := &VaultNode{Name: name, Dir: kind == vaultKindFolder, MTime: int64(mtime)}
		if kind == vaultKindFile {
			size := d.uvarint()
			ck := d.byte()
			if d.err != nil {
				break
			}
			switch ck {
			case vaultContentNone:
				if size != 0 {
					d.fail("entry %d: a file of %d bytes with no content", i+1, size)
				}
			case vaultContentChunk:
				if size == 0 {
					d.fail("entry %d: an empty file with content", i+1)
					break
				}
				c := &VaultContent{ID: d.id(), ChunkLog2: int(d.byte())}
				if d.err == nil && !ValidChunkLog2(c.ChunkLog2) {
					d.fail("entry %d: a chunk size of 2^%d", i+1, c.ChunkLog2)
				}
				if d.err == nil && (size+(1<<c.ChunkLog2)-1)>>c.ChunkLog2 > 1<<32 {
					d.fail("entry %d: more chunks than a STREAM counts", i+1)
				}
				extCount := d.uvarint()
				if d.err == nil && (extCount == 0 || extCount > uint64(d.remaining()/3)) {
					d.fail("entry %d: %d extents", i+1, extCount)
				}
				var sum int64
				for j := uint64(0); d.err == nil && j < extCount; j++ {
					p, off, ln := d.uvarint(), d.uvarint(), d.uvarint()
					if d.err != nil {
						break
					}
					switch {
					case p >= uint64(len(idx.Packs)):
						d.fail("entry %d: an extent in pack %d of a table of %d", i+1, p, len(idx.Packs))
					case ln < 1:
						d.fail("entry %d: an empty extent", i+1)
					case off < e2e.VaultPackHeaderLen:
						d.fail("entry %d: an extent inside a pack header", i+1)
					case int64(off+ln) > packArea:
						d.fail("entry %d: an extent past the end of its pack", i+1)
					}
					if d.err != nil {
						break
					}
					used[p] = true
					c.Extents = append(c.Extents, VaultExtent{Pack: idx.Packs[p], Offset: int64(off), Length: int64(ln)})
					sum += int64(ln)
					if sum > 1<<60 {
						d.fail("entry %d: its extents are larger than any file", i+1)
					}
				}
				if d.err == nil && sum != StreamCiphertextSize(int64(size), c.ChunkLog2) {
					d.fail("entry %d: its extents hold %d bytes, a %d-byte file needs %d", i+1, sum, size, StreamCiphertextSize(int64(size), c.ChunkLog2))
				}
				n.Content = c
			default:
				d.fail("entry %d: content of unknown kind %d", i+1, ck)
			}
			n.Size = int64(size)
		}
		extLen := d.uvarint()
		ext := d.bytes(extLen)
		if d.err != nil {
			break
		}
		if len(ext) > 0 {
			n.ext = slices.Clone(ext)
			idx.ReadOnly = "a newer filex wrote this vault: it carries data this version would lose by writing"
		}
		idx.Tree.attach(top.node, n)
		idx.Tree.count++
		if n.Dir {
			chain = append(chain, level{pos: i + 1, node: n})
		}
	}
	for p, u := range used {
		if d.err == nil && !u {
			d.fail("the pack table lists %x, which no file uses", idx.Packs[p])
		}
	}

	// The graveyard.
	graveCount := d.uvarint()
	if d.err == nil && graveCount > uint64(d.remaining()/(vaultIDLen+1)) {
		d.fail("the graveyard is longer than the index")
	}
	for i := uint64(0); d.err == nil && i < graveCount; i++ {
		g := VaultGrave{Pack: d.id(), Died: d.uvarint()}
		if d.err != nil {
			break
		}
		if n := len(idx.Grave); n > 0 && compareVaultIDs(idx.Grave[n-1].Pack, g.Pack) >= 0 {
			d.fail("the graveyard is not in ascending order")
		}
		if _, live := slices.BinarySearchFunc(idx.Packs, g.Pack, compareVaultIDs); live {
			d.fail("the graveyard names %x, which is in the pack table", g.Pack)
		}
		if g.Died < 2 || g.Died > gen {
			d.fail("the graveyard says %x died in generation %d (this is %d)", g.Pack, g.Died, gen)
		}
		idx.Grave = append(idx.Grave, g)
	}
	extLen := d.uvarint()
	if ext := d.bytes(extLen); d.err == nil && len(ext) > 0 {
		idx.ReadOnly = "a newer filex wrote this vault: it carries data this version would lose by writing"
	}
	if d.err != nil {
		return nil, vaultDamage(where, "%s", d.err.Error())
	}
	idx.BodyLen = d.off
	for _, c := range plaintext[d.off:] {
		if c != 0 {
			return nil, vaultDamage(where, "the padding after the body is not zero")
		}
	}
	return idx, nil
}

// ─────────────────────────────── encoding ─────────────────────────────────

// EncodeVaultBody writes a tree and its graveyard in the one canonical
// encoding, and returns the pack table it wrote.
func EncodeVaultBody(t *VaultTree, grave []VaultGrave) ([]byte, [][16]byte, error) {
	table := t.packTable()
	packPos := make(map[[16]byte]uint64, len(table))
	for i, p := range table {
		packPos[p] = uint64(i)
	}
	b := []byte{vaultBodyVersion}
	b = appendVaultUvarint(b, 0)
	b = appendVaultUvarint(b, uint64(len(table)))
	for _, p := range table {
		b = append(b, p[:]...)
	}
	b = appendVaultUvarint(b, uint64(t.Len()))
	pos := map[*VaultNode]uint64{t.root: 0}
	var next uint64
	err := t.Walk(func(n *VaultNode, depth int) error {
		next++
		pos[n] = next
		if prob := vaultNameProblem(n.Name); prob != "" {
			return fmt.Errorf("%s: %s: %w", n.Path(), prob, ErrVaultBadName)
		}
		if depth > e2e.VaultMaxDepth {
			return ErrVaultTooDeep
		}
		if n.HasExt() {
			return errors.New("e2edecrypt: refusing to write an entry with extension bytes")
		}
		kind := byte(vaultKindFolder)
		if !n.Dir {
			kind = vaultKindFile
		}
		b = append(b, kind)
		b = appendVaultUvarint(b, pos[n.parent])
		b = appendVaultUvarint(b, uint64(len(n.Name)))
		b = append(b, n.Name...)
		b = appendVaultUvarint(b, uint64(max(n.MTime, 0)))
		if !n.Dir {
			b = appendVaultUvarint(b, uint64(n.Size))
			switch {
			case n.Size == 0:
				b = append(b, vaultContentNone)
			case n.Content == nil:
				return fmt.Errorf("e2edecrypt: %s: its bytes are not stored yet", n.Path())
			default:
				c := n.Content
				if c.BodyLen() != StreamCiphertextSize(n.Size, c.ChunkLog2) {
					return fmt.Errorf("e2edecrypt: %s: its extents do not hold its body", n.Path())
				}
				b = append(b, vaultContentChunk)
				b = append(b, c.ID[:]...)
				b = append(b, byte(c.ChunkLog2))
				b = appendVaultUvarint(b, uint64(len(c.Extents)))
				for _, x := range c.Extents {
					b = appendVaultUvarint(b, packPos[x.Pack])
					b = appendVaultUvarint(b, uint64(x.Offset))
					b = appendVaultUvarint(b, uint64(x.Length))
				}
			}
		}
		b = appendVaultUvarint(b, 0)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	g := slices.Clone(grave)
	slices.SortFunc(g, func(x, y VaultGrave) int { return compareVaultIDs(x.Pack, y.Pack) })
	b = appendVaultUvarint(b, uint64(len(g)))
	for i, e := range g {
		if i > 0 && g[i-1].Pack == e.Pack {
			return nil, nil, fmt.Errorf("e2edecrypt: the graveyard names %x twice", e.Pack)
		}
		if _, live := packPos[e.Pack]; live {
			return nil, nil, fmt.Errorf("e2edecrypt: the graveyard names %x, which the tree uses", e.Pack)
		}
		b = append(b, e.Pack[:]...)
		b = appendVaultUvarint(b, e.Died)
	}
	b = appendVaultUvarint(b, 0)
	return b, table, nil
}

// ─────────────────────────────── the file ─────────────────────────────────

func vaultIndexHeader(gen uint64, sealID [16]byte) []byte {
	h := make([]byte, e2e.VaultIndexHeaderLen)
	copy(h, e2e.VaultMagicPrefix)
	h[8] = byte(e2e.VaultFormat)
	h[9] = e2e.VaultKindIndex
	binary.BigEndian.PutUint64(h[16:24], gen)
	copy(h[24:40], sealID[:])
	return h
}

// SealVaultIndex makes the index file of generation gen from a body, sealed
// under sealID (16 random bytes, new for every sealing).
func SealVaultIndex(k *VaultKeys, gen uint64, body []byte, sealID [16]byte) ([]byte, error) {
	size := e2e.VaultIndexFileSize(int64(len(body)))
	if size > e2e.VaultIndexWriteMax {
		return nil, ErrVaultIndexTooLarge
	}
	header := vaultIndexHeader(gen, sealID)
	plain := make([]byte, size-e2e.VaultIndexHeaderLen-gcmTagLen)
	copy(plain, body)
	key, err := k.IndexKey(sealID)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	aead, err := vaultAEAD(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, size)
	out = append(out, header...)
	out = aead.Seal(out, make([]byte, ivLen), plain, header)
	if int64(len(out)) != size {
		return nil, errors.New("e2edecrypt: the sealed index is not its padded size")
	}
	return out, nil
}

// OpenVaultIndex opens and checks the index file of generation gen. A file
// that is too large is refused before anything is decrypted.
func OpenVaultIndex(k *VaultKeys, packLog2 int, gen uint64, file []byte) (*VaultIndex, error) {
	where := e2e.VaultIndexPath(gen)
	if !e2e.ValidVaultIndexSize(int64(len(file))) {
		return nil, vaultDamage(where, "%d bytes is not the size of an index file", len(file))
	}
	if err := e2e.CheckVaultIndexHeader(file, gen); err != nil {
		return nil, vaultDamage(where, "%s", err.Error())
	}
	var sealID [16]byte
	copy(sealID[:], file[24:40])
	key, err := k.IndexKey(sealID)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	aead, err := vaultAEAD(key)
	if err != nil {
		return nil, err
	}
	header := file[:e2e.VaultIndexHeaderLen]
	plain, err := aead.Open(nil, make([]byte, ivLen), file[e2e.VaultIndexHeaderLen:], header)
	if err != nil {
		return nil, vaultDamage(where, "it does not verify (damaged, or another vault's)")
	}
	idx, err := decodeVaultBody(plain, packLog2, gen, where)
	if err != nil {
		return nil, err
	}
	if e2e.VaultIndexFileSize(int64(idx.BodyLen)) != int64(len(file)) {
		return nil, vaultDamage(where, "its size is not the padded size of the body it holds")
	}
	idx.Size = int64(len(file))
	return idx, nil
}

// VaultWarning is what a client says before a change to a vault that is
// close to its limits, or "" (docs/E2E-VAULT-FORMAT.md → "Limits").
func VaultWarning(entries int, indexSize int64) string {
	switch {
	case entries >= e2e.VaultEntriesWarn:
		return fmt.Sprintf("This vault holds %s of at most %s files and folders.", groupThousands(entries), groupThousands(e2e.VaultEntriesWriteMax))
	case indexSize >= vaultIndexWarnBytes:
		return fmt.Sprintf("This vault's index is %d MiB of at most %d MiB.", indexSize>>20, e2e.VaultIndexWriteMax>>20)
	}
	return ""
}

func groupThousands(n int) string {
	s := fmt.Sprint(n)
	var out strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(' ')
		}
		out.WriteRune(c)
	}
	return out.String()
}

// sameVaultBytes compares two ids or bodies (tests and the writer's checks).
func sameVaultBytes(a, b []byte) bool { return bytes.Equal(a, b) }
