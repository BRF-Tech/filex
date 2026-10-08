package e2edecrypt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

// Reading a vault - docs/E2E-VAULT-FORMAT.md → "Reading". The same code reads
// a copy of a vault folder on this disk (`filex decrypt ./Kasa`, through
// DirVaultSource) and a vault on a server (`filex decrypt docs://Kasa`, `filex
// vault mount`, through a source cmd/filex builds over the API): a source
// lists and reads files, this file does everything else.

// ErrVaultObjectMissing is wrapped by a source's error for an index file or a
// pack that is not there.
var ErrVaultObjectMissing = errors.New("not in the vault")

// VaultObject is one index file or pack, as a listing shows it.
type VaultObject struct {
	Generation uint64   // an index file
	Pack       [16]byte // a pack
	Size       int64
	ModTime    time.Time // zero: the source does not say
}

// VaultSource is where a vault's files come from.
type VaultSource interface {
	// ListIndexes lists the index files (well-formed names only).
	ListIndexes(ctx context.Context) ([]VaultObject, error)
	// ListPacks lists the packs (well-formed names only).
	ListPacks(ctx context.Context) ([]VaultObject, error)
	// ReadIndex reads one index file whole. More than e2e.VaultIndexReadMax
	// bytes is refused before they are read (a damage error).
	ReadIndex(ctx context.Context, generation uint64) ([]byte, error)
	// ReadPack reads n bytes of a pack from off.
	ReadPack(ctx context.Context, id [16]byte, off, n int64) ([]byte, error)
}

// VaultLatestSource is a source that names its latest generation without a
// listing - a server answers `state`.
type VaultLatestSource interface {
	LatestGeneration(ctx context.Context) (uint64, error)
}

// ─────────────────────────── a copy on this disk ──────────────────────────

// DirVaultSource reads a copy of a vault folder: the folder (os.DirFS) or a
// zip of it, extracted. A pack is checked whole the first time it is read:
// size, magic, version, kind, size log2 and id against its name.
type DirVaultSource struct {
	FS       fs.FS
	PackLog2 int

	mu      sync.Mutex
	checked map[[16]byte]error
}

// NewDirVaultSource reads the vault folder at the root of fsys.
func NewDirVaultSource(fsys fs.FS, packLog2 int) *DirVaultSource {
	return &DirVaultSource{FS: fsys, PackLog2: packLog2, checked: map[[16]byte]error{}}
}

func (s *DirVaultSource) list(dir string, want e2e.VaultPathKind) ([]VaultObject, error) {
	entries, err := fs.ReadDir(s.FS, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []VaultObject
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		kind, id, gen := e2e.ParseVaultPath(path.Join(dir, e.Name()))
		if kind != want {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, VaultObject{Generation: gen, Pack: id, Size: info.Size(), ModTime: info.ModTime()})
	}
	return out, nil
}

// ListIndexes lists v/idx.
func (s *DirVaultSource) ListIndexes(ctx context.Context) ([]VaultObject, error) {
	return s.list("v/idx", e2e.VaultPathIndex)
}

// ListPacks lists v/p/XX.
func (s *DirVaultSource) ListPacks(ctx context.Context) ([]VaultObject, error) {
	dirs, err := fs.ReadDir(s.FS, "v/p")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []VaultObject
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		got, err := s.list("v/p/"+d.Name(), e2e.VaultPathPack)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// ReadIndex reads v/idx/G.fxi.
func (s *DirVaultSource) ReadIndex(ctx context.Context, gen uint64) ([]byte, error) {
	p := e2e.VaultIndexPath(gen)
	fi, err := fs.Stat(s.FS, p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", p, ErrVaultObjectMissing)
	}
	if err != nil {
		return nil, err
	}
	if fi.Size() > e2e.VaultIndexReadMax {
		return nil, vaultDamage(p, "%d bytes, more than an index file may hold", fi.Size())
	}
	return fs.ReadFile(s.FS, p)
}

// ReadPack reads part of v/p/XX/ID.fxp.
func (s *DirVaultSource) ReadPack(ctx context.Context, id [16]byte, off, n int64) ([]byte, error) {
	p := e2e.VaultPackPath(id)
	f, err := s.FS.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", p, ErrVaultObjectMissing)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := s.checkPack(id); err != nil {
		return nil, err
	}
	if off < 0 || n < 0 || off+n > int64(1)<<s.PackLog2 {
		return nil, vaultDamage(p, "a read past the end of the pack")
	}
	buf := make([]byte, n)
	if ra, ok := f.(io.ReaderAt); ok {
		if _, err := ra.ReadAt(buf, off); err != nil {
			return nil, err
		}
		return buf, nil
	}
	if sk, ok := f.(io.Seeker); ok {
		if _, err := sk.Seek(off, io.SeekStart); err != nil {
			return nil, err
		}
	} else if _, err := io.CopyN(io.Discard, f, off); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// checkPack checks a pack's size and header once, on a handle of its own.
func (s *DirVaultSource) checkPack(id [16]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, done := s.checked[id]; done {
		return err
	}
	f, err := s.FS.Open(e2e.VaultPackPath(id))
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	var cerr error
	if fi.Size() != int64(1)<<s.PackLog2 {
		cerr = vaultDamage(e2e.VaultPackPath(id), "%d bytes, a pack of this vault is %d", fi.Size(), int64(1)<<s.PackLog2)
	} else {
		h := make([]byte, e2e.VaultPackHeaderLen)
		if _, err := io.ReadFull(f, h); err != nil {
			return err
		}
		cerr = checkVaultPackHeader(h, id, s.PackLog2)
	}
	s.checked[id] = cerr
	return cerr
}

// ─────────────────────────────── loading ──────────────────────────────────

// VaultLoadOptions configure LoadVault.
type VaultLoadOptions struct {
	// Generation, when not 0, loads that generation instead of the latest
	// (`--generation N`): an older one that is still kept.
	Generation uint64
	// Seen is the highest generation this session has seen of the vault; a
	// lower latest is a rollback (read-only). 0: none.
	Seen uint64
	// Sleep waits before a damaged latest is fetched again; nil sleeps for
	// real (and stops with ctx).
	Sleep func(ctx context.Context, d time.Duration) error
}

// VaultState is what a reader has loaded.
type VaultState struct {
	// Index is the generation shown: the latest that verifies, or the one
	// asked for. An empty vault at generation 0 has an empty one.
	Index *VaultIndex
	// Latest is the newest generation present (Index.Generation unless it
	// is damaged or an older one was asked for).
	Latest uint64
	// DamagedLatest: the newest generation does not verify; Index is an
	// older one, read-only, until its owner continues from it.
	DamagedLatest bool
	// RolledBack: the latest is older than one this session saw.
	RolledBack bool
}

// ReadOnly is why a writer must not write from this state, or "".
func (s *VaultState) ReadOnly() string {
	switch {
	case s.DamagedLatest:
		return fmt.Sprintf("the newest state of this vault (generation %d) is damaged; you are looking at generation %d", s.Latest, s.Index.Generation)
	case s.RolledBack:
		return "the server offers an older state of this vault than it did before"
	case s.Index.Generation != s.Latest:
		return fmt.Sprintf("generation %d is an older state of this vault", s.Index.Generation)
	case s.Index.ReadOnly != "":
		return s.Index.ReadOnly
	}
	return ""
}

// Writable is ErrVaultReadOnly, with the reason, when a writer must not write
// from this state; nil when it may.
func (s *VaultState) Writable() error {
	if r := s.ReadOnly(); r != "" {
		return fmt.Errorf("%w: %s", ErrVaultReadOnly, r)
	}
	return nil
}

// vaultDamagedRetry is how long a reader waits before it fetches a damaged
// latest again: a commit may be landing on a storage without an atomic
// rename.
const vaultDamagedRetry = 2 * time.Second

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// LoadVault finds and opens the generation to show.
func LoadVault(ctx context.Context, src VaultSource, k *VaultKeys, packLog2 int, opt VaultLoadOptions) (*VaultState, error) {
	sleep := opt.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	load := func(gen uint64) (*VaultIndex, error) {
		b, err := src.ReadIndex(ctx, gen)
		if err != nil {
			if errors.Is(err, ErrVaultObjectMissing) {
				return nil, vaultDamage(e2e.VaultIndexPath(gen), "it is not there")
			}
			return nil, err
		}
		return OpenVaultIndex(k, packLog2, gen, b)
	}

	var list []VaultObject
	var latest uint64
	if ls, ok := src.(VaultLatestSource); ok {
		g, err := ls.LatestGeneration(ctx)
		if err != nil {
			return nil, err
		}
		latest = g
	} else {
		var err error
		if list, err = src.ListIndexes(ctx); err != nil {
			return nil, err
		}
		for _, o := range list {
			latest = max(latest, o.Generation)
		}
	}

	if opt.Generation != 0 {
		if latest != 0 && opt.Generation > latest {
			return nil, fmt.Errorf("this vault has no generation %d (the latest is %d)", opt.Generation, latest)
		}
		b, err := src.ReadIndex(ctx, opt.Generation)
		if errors.Is(err, ErrVaultObjectMissing) {
			return nil, fmt.Errorf("generation %d of this vault is not kept any more", opt.Generation)
		}
		if err != nil {
			return nil, err
		}
		idx, err := OpenVaultIndex(k, packLog2, opt.Generation, b)
		if err != nil {
			return nil, err
		}
		return &VaultState{Index: idx, Latest: max(latest, opt.Generation)}, nil
	}

	if latest == 0 {
		// No index file: an empty vault whose creation stopped half way -
		// unless there are packs, which only an index can make sense of.
		packs, err := src.ListPacks(ctx)
		if err != nil {
			return nil, err
		}
		if len(packs) > 0 {
			return nil, vaultDamage("v/idx", "the vault holds packs but no index")
		}
		return &VaultState{Index: emptyVaultIndex()}, nil
	}

	st := &VaultState{Latest: latest}
	idx, err := load(latest)
	if err != nil && errors.Is(err, ErrVaultDamaged) {
		if serr := sleep(ctx, vaultDamagedRetry); serr != nil {
			return nil, serr
		}
		idx, err = load(latest)
	}
	if err == nil {
		st.Index = idx
		st.RolledBack = opt.Seen > latest
		return st, nil
	}
	if !errors.Is(err, ErrVaultDamaged) {
		return nil, err
	}
	// The newest that verifies, read-only.
	if list == nil {
		var lerr error
		if list, lerr = src.ListIndexes(ctx); lerr != nil {
			return nil, lerr
		}
	}
	gens := make([]uint64, 0, len(list))
	for _, o := range list {
		if o.Generation < latest {
			gens = append(gens, o.Generation)
		}
	}
	slices.Sort(gens)
	for i := len(gens) - 1; i >= 0; i-- {
		older, oerr := load(gens[i])
		if oerr == nil {
			st.Index, st.DamagedLatest = older, true
			st.RolledBack = opt.Seen > latest
			return st, nil
		}
		if !errors.Is(oerr, ErrVaultDamaged) {
			return nil, oerr
		}
	}
	return nil, err
}

// ─────────────────────────────── contents ─────────────────────────────────

// VaultReader reads files of one vault.
type VaultReader struct {
	src  VaultSource
	keys *VaultKeys
}

// NewVaultReader reads through src with keys.
func NewVaultReader(src VaultSource, keys *VaultKeys) *VaultReader {
	return &VaultReader{src: src, keys: keys}
}

// vaultChunks is how many STREAM chunks a file of size bytes has.
func vaultChunks(size int64, log2 int) int64 {
	cs := int64(1) << log2
	return (size + cs - 1) / cs
}

// readBody fetches bytes [from, to) of a content's encrypted body: one read
// per run of bytes inside one pack.
func (r *VaultReader) readBody(ctx context.Context, c *VaultContent, from, to int64) ([]byte, error) {
	out := make([]byte, 0, to-from)
	var start int64
	for _, x := range c.Extents {
		end := start + x.Length
		if end > from && start < to {
			lo, hi := max(from, start), min(to, end)
			b, err := r.src.ReadPack(ctx, x.Pack, x.Offset+(lo-start), hi-lo)
			if err != nil {
				return nil, err
			}
			if int64(len(b)) != hi-lo {
				return nil, vaultDamage(e2e.VaultPackPath(x.Pack), "the pack is shorter than its extents")
			}
			out = append(out, b...)
		}
		start = end
		if start >= to {
			break
		}
	}
	if int64(len(out)) != to-from {
		return nil, errors.New("e2edecrypt: the extents end before the body does")
	}
	return out, nil
}

// decryptChunks opens chunks i0..i0+k-1 whose ciphertext is ct.
func (r *VaultReader) decryptChunks(n *VaultNode, ct []byte, i0 int64) ([]byte, error) {
	c := n.Content
	aead, err := r.keys.contentAEAD(c.ID)
	if err != nil {
		return nil, err
	}
	cs := int64(1) << c.ChunkLog2
	full := int(cs) + gcmTagLen
	chunks := vaultChunks(n.Size, c.ChunkLog2)
	out := make([]byte, 0, len(ct))
	for i := i0; len(ct) > 0; i++ {
		take := min(full, len(ct))
		want := min(cs, n.Size-i*cs) + gcmTagLen
		if int64(take) != want {
			return nil, &CorruptError{Path: n.Path(), Err: fmt.Errorf("chunk %d has the wrong length: %w", i, ErrContent)}
		}
		plain, err := aead.Open(out[len(out):], vaultChunkNonce(uint32(i), i == chunks-1), ct[:take], nil)
		if err != nil {
			return nil, &CorruptError{Path: n.Path(), Err: fmt.Errorf("chunk %d failed authentication (damaged, or from another vault): %w", i, ErrContent)}
		}
		out = out[:len(out)+len(plain)]
		ct = ct[take:]
	}
	return out, nil
}

// ReadChunk returns plaintext chunk i of a file (the mount's block cache
// holds these).
func (r *VaultReader) ReadChunk(ctx context.Context, n *VaultNode, i int64) ([]byte, error) {
	if n.Dir || n.Content == nil {
		return nil, fmt.Errorf("e2edecrypt: %s has no stored content", n.Path())
	}
	c := n.Content
	if i < 0 || i >= vaultChunks(n.Size, c.ChunkLog2) {
		return nil, io.EOF
	}
	full := (int64(1) << c.ChunkLog2) + gcmTagLen
	body := c.BodyLen()
	ct, err := r.readBody(ctx, c, i*full, min((i+1)*full, body))
	if err != nil {
		return nil, err
	}
	return r.decryptChunks(n, ct, i)
}

// ReadRange returns bytes [a, b) of a file's plaintext (b is clipped to its
// size): the chunks needed, fetched as few reads as the extents allow.
func (r *VaultReader) ReadRange(ctx context.Context, n *VaultNode, a, b int64) ([]byte, error) {
	b = min(b, n.Size)
	if a < 0 || a > b {
		return nil, fmt.Errorf("e2edecrypt: a bad range [%d, %d)", a, b)
	}
	if a == b {
		return []byte{}, nil
	}
	c := n.Content
	if c == nil {
		return nil, fmt.Errorf("e2edecrypt: %s has no stored content", n.Path())
	}
	cs := int64(1) << c.ChunkLog2
	full := cs + gcmTagLen
	i0, i1 := a/cs, (b-1)/cs
	ct, err := r.readBody(ctx, c, i0*full, min((i1+1)*full, c.BodyLen()))
	if err != nil {
		return nil, err
	}
	plain, err := r.decryptChunks(n, ct, i0)
	if err != nil {
		return nil, err
	}
	return plain[a-i0*cs : b-i0*cs], nil
}

// WriteTo writes a file's whole plaintext to w, one chunk at a time.
func (r *VaultReader) WriteTo(ctx context.Context, n *VaultNode, w io.Writer) (int64, error) {
	if n.Size == 0 {
		return 0, nil
	}
	var done int64
	chunks := vaultChunks(n.Size, n.Content.ChunkLog2)
	for i := int64(0); i < chunks; i++ {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		p, err := r.ReadChunk(ctx, n, i)
		if err != nil {
			return done, err
		}
		k, err := w.Write(p)
		done += int64(k)
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

// ─────────────────────────── decrypting it whole ──────────────────────────

// VaultDecryptOptions configure DecryptVault.
type VaultDecryptOptions struct {
	// Out is the directory to write; it must not exist.
	Out string
	// Generation, when not 0, decrypts that kept generation instead of the
	// latest.
	Generation uint64
	// GOOS overrides runtime.GOOS for the output-name rules (tests).
	GOOS string
	// Sleep is VaultLoadOptions.Sleep.
	Sleep func(ctx context.Context, d time.Duration) error
}

// DecryptVault writes the whole tree of a vault - the latest generation that
// verifies, or the one asked for - into a new directory, all or nothing: into
// "<out>.partial-*" first, renamed into place when every file decrypted.
func DecryptVault(ctx context.Context, src VaultSource, k *VaultKeys, packLog2 int, opts VaultDecryptOptions) (*Result, error) {
	j := &Job{goos: opts.GOOS, out: opts.Out}
	if j.goos == "" {
		j.goos = runtime.GOOS
	}
	if _, err := os.Lstat(j.out); err == nil {
		return nil, fmt.Errorf("%s: %w", j.out, ErrOutputExists)
	}
	st, err := LoadVault(ctx, src, k, packLog2, VaultLoadOptions{Generation: opts.Generation, Sleep: opts.Sleep})
	if err != nil {
		return nil, err
	}
	return j.intoPartial(func(tmp string) error {
		if st.DamagedLatest {
			j.warn("the newest state of this vault (generation %d) is damaged; this is generation %d, the newest that verifies", st.Latest, st.Index.Generation)
		}
		return j.writeVaultDir(ctx, NewVaultReader(src, k), st.Index.Tree.Root(), tmp, "")
	})
}

func (j *Job) writeVaultDir(ctx context.Context, r *VaultReader, dir *VaultNode, dst, rel string) error {
	taken := map[string]bool{}
	for _, n := range dir.Children() {
		name := j.outputName(n.Name, taken, joinRel(rel, n.Name))
		target := filepath.Join(dst, name)
		where := joinRel(rel, name)
		if n.Dir {
			if err := os.Mkdir(target, 0o700); err != nil {
				return err
			}
			j.res.Dirs++
			if err := j.writeVaultDir(ctx, r, n, target, where); err != nil {
				return err
			}
			setVaultMTime(target, n.MTime)
			continue
		}
		if err := j.writeVaultFile(ctx, r, n, target); err != nil {
			var ce *CorruptError
			if errors.As(err, &ce) {
				return err
			}
			return &CorruptError{Path: where, Err: err}
		}
		j.res.Files++
	}
	return nil
}

func (j *Job) writeVaultFile(ctx context.Context, r *VaultReader, n *VaultNode, target string) error {
	w, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := r.WriteTo(ctx, n, w)
	if cerr := w.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	setVaultMTime(target, n.MTime)
	return nil
}

// setVaultMTime gives a written file or folder the entry's mtime; a disk
// that refuses keeps the time of writing.
func setVaultMTime(p string, ms int64) {
	if ms <= 0 {
		return
	}
	t := time.UnixMilli(ms)
	_ = os.Chtimes(p, t, t)
}

// runVault is Run for a vault copy: the folder (or the extracted zip) is the
// source, read through DirVaultSource.
func (j *Job) runVault() (*Result, error) {
	info := j.Marker.Vault
	k, err := NewVaultKeys(j.keys.FMK, info.ID)
	if err != nil {
		return nil, err
	}
	defer k.Wipe()
	src := NewDirVaultSource(os.DirFS(j.root), info.PackLog2)
	return DecryptVault(context.Background(), src, k, info.PackLog2, VaultDecryptOptions{Out: j.out, Generation: j.gen, GOOS: j.goos})
}
