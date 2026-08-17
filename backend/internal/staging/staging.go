// Package staging is filex's own upload staging area: the bytes of an upload
// land here first, on local disk, and are transferred to the storage driver
// afterwards by a background op.
//
// Why this exists: before it, the only chunked upload path required
// storage.MultipartUploader — implemented by the S3 driver alone — and on that
// path the browser PUT its parts straight to S3, so filex never saw the bytes.
// Every other write was one synchronous request in which the client waited for
// the backend, which on a slow backend means the progress bar shows the
// *backend's* speed and a 4 GB upload over a flaky link starts from zero.
//
// # Layout
//
//	<root>/<id>/manifest.json
//	<root>/<id>/000001.part
//	<root>/<id>/000002.part
//	…
//
// Numbered parts, deliberately — NOT a single append-only <id>.part with a byte
// offset. A single file plus an offset serves a sequential resumable client and
// nothing else; an S3-compatible UploadPart API (planned directly on top of this
// layer) receives parts out of order, numbered, each needing its own ETag.
// Building the sequential-only version means building the staging layer twice.
// The numbered store serves both: the sequential protocol is the special case
// where parts arrive in order, and the per-part md5 makes the S3 composite ETag
// (md5(concat(part md5s))-N) computable without re-reading the data.
//
// # The offset contract
//
// Offset is the total size of the CONTIGUOUS RUN OF PARTS FROM PART 1. It is
// the resume point and it is authoritative: a client that lost its state asks
// for the manifest and continues from there. Parts written out of order beyond
// a hole are kept but do not move the offset until the hole is filled.
//
// # Durability
//
// A part is written to a temporary file, fsynced, and only then renamed into
// place and recorded in the manifest — so a connection that dies mid-chunk
// leaves the offset exactly where it was, which is the whole point of resume.
// The manifest is rewritten the same way (temp + rename), so it is never
// observed half-written.
package staging

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ManifestName is the per-upload metadata file inside the staging directory.
const ManifestName = "manifest.json"

// ManifestVersion is bumped when the on-disk shape changes incompatibly.
const ManifestVersion = 1

// MaxParts mirrors S3's own multipart ceiling. Keeping the staging store inside
// the same limit means a staged upload can always be replayed as an S3
// multipart upload without re-chunking for count reasons.
const MaxParts = 10000

// DiskHeadroomFactor is the multiple of the declared size that must be free on
// the staging filesystem before an upload is accepted. 1.2 leaves room for the
// manifest, for filesystem overhead, and for the fact that other uploads are
// landing at the same time.
const DiskHeadroomFactor = 1.2

// Errors returned by the area. Callers map these onto status codes.
var (
	ErrNotFound     = errors.New("staging: upload not found")
	ErrBadID        = errors.New("staging: invalid upload id")
	ErrBadPart      = errors.New("staging: invalid part")
	ErrShortPart    = errors.New("staging: short part body")
	ErrIncomplete   = errors.New("staging: upload incomplete")
	ErrNoDiskSpace  = errors.New("staging: not enough free disk space")
	ErrTooManyParts = errors.New("staging: too many parts")
)

// idRe is the only shape an upload id may have. Enforced before any path is
// built from it, so `..` and separators can never reach filepath.Join.
var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,63}$`)

// ValidID reports whether id is safe to use as a directory name.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Part is one staged chunk: its number (1-based), its byte length and the md5
// of its bytes.
type Part struct {
	N    int    `json:"n"`
	Size int64  `json:"size"`
	MD5  string `json:"md5"`
}

// Manifest is the on-disk record of what is physically staged. It is the
// authority for the resume offset; the DB row mirrors it so listings and the
// sweeper do not have to open every manifest.
type Manifest struct {
	Version   int    `json:"version"`
	ID        string `json:"id"`
	TotalSize int64  `json:"total_size"`
	ChunkSize int64  `json:"chunk_size"`
	Hash      string `json:"hash,omitempty"`
	// Variable switches off the fixed grid.
	//
	// ⚠ The two modes exist because the two protocols disagree. filex's own
	// resumable upload knows the total size up front and cuts it into equal
	// chunks, so a part's exact length is checkable and a short chunk can be
	// refused. S3 multipart knows neither: parts arrive with arbitrary sizes
	// and the total is only known at CompleteMultipartUpload. Forcing S3 onto
	// the fixed grid would mean rejecting perfectly legal uploads; giving S3
	// its own staging area would mean two sweepers, two disk guards and two
	// GCs. So one area, two modes.
	Variable  bool      `json:"variable,omitempty"`
	Parts     []Part    `json:"parts"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PartCount is how many parts a complete upload has under this manifest's grid.
func (m *Manifest) PartCount() int {
	if m.Variable {
		// There is no grid: what is staged is what is staged.
		return len(m.Parts)
	}
	if m.ChunkSize <= 0 {
		return 0
	}
	return int((m.TotalSize + m.ChunkSize - 1) / m.ChunkSize)
}

// ExpectedPartSize is the exact length part n must have: a full chunk, or the
// remainder for the final part. Returns 0 for an out-of-range number.
func (m *Manifest) ExpectedPartSize(n int) int64 {
	if m.Variable {
		// Unknowable by design: only the client knows how big its next part
		// is, which is why WriteVariablePart takes no expected length.
		return 0
	}
	if n < 1 || m.ChunkSize <= 0 || n > m.PartCount() {
		return 0
	}
	off := int64(n-1) * m.ChunkSize
	if rem := m.TotalSize - off; rem < m.ChunkSize {
		return rem
	}
	return m.ChunkSize
}

// Part returns the recorded part n.
func (m *Manifest) Part(n int) (Part, bool) {
	for _, p := range m.Parts {
		if p.N == n {
			return p, true
		}
	}
	return Part{}, false
}

// Offset is the resume point: the total size of the contiguous run of parts
// starting at part 1. A part written past a hole does not count until the hole
// is filled — which is exactly what a sequential client needs to be told.
func (m *Manifest) Offset() int64 {
	byN := make(map[int]int64, len(m.Parts))
	for _, p := range m.Parts {
		byN[p.N] = p.Size
	}
	var off int64
	for n := 1; ; n++ {
		sz, ok := byN[n]
		if !ok {
			break
		}
		off += sz
	}
	return off
}

// Received is the total staged size across ALL parts, holes included. Used for
// reporting only — never as a resume point.
func (m *Manifest) Received() int64 {
	var t int64
	for _, p := range m.Parts {
		t += p.Size
	}
	return t
}

// Complete reports whether every part is present and the sizes add up to the
// declared total.
func (m *Manifest) Complete() bool {
	if m.Variable {
		// A variable upload is complete when its parts are contiguous from 1:
		// there is no declared total to compare against, so the hole check is
		// the whole check.
		return len(m.Parts) > 0 && m.Offset() == m.Received()
	}
	return m.TotalSize >= 0 && m.Offset() == m.TotalSize && len(m.Parts) == m.PartCount()
}

// CompositeETag is the S3-style multipart ETag for the staged parts:
// md5(concat(raw part md5s))-N. Computed from the manifest alone — the future
// S3 gateway can answer with it without re-reading a byte.
func (m *Manifest) CompositeETag() string {
	if len(m.Parts) == 0 {
		return ""
	}
	parts := append([]Part(nil), m.Parts...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].N < parts[j].N })
	h := md5.New()
	for _, p := range parts {
		raw, err := hex.DecodeString(p.MD5)
		if err != nil {
			return ""
		}
		_, _ = h.Write(raw)
	}
	return fmt.Sprintf("%s-%d", hex.EncodeToString(h.Sum(nil)), len(parts))
}

// Area is a staging root plus the per-upload write locks.
//
// Reads (manifest, assembled body) take no lock: every file is replaced by
// rename, so a reader either sees the old file or the new one, never a torn
// one.
type Area struct {
	root string

	mu    sync.Mutex
	locks map[string]*sync.Mutex

	// FreeBytes probes free space on the staging filesystem. Swapped in tests
	// to exercise the disk guard without filling a disk.
	FreeBytes func(dir string) (uint64, error)
}

// New returns an Area rooted at dir. An empty dir yields a disabled area —
// Enabled() reports false and callers must refuse staged uploads rather than
// silently writing somewhere unexpected.
func New(dir string) *Area {
	return &Area{root: dir, locks: map[string]*sync.Mutex{}, FreeBytes: freeBytes}
}

// Enabled reports whether a staging root is configured.
func (a *Area) Enabled() bool { return a != nil && a.root != "" }

// Root returns the staging root directory.
func (a *Area) Root() string { return a.root }

// Dir returns the staging directory for id ("" when the id is unsafe).
func (a *Area) Dir(id string) string {
	if !a.Enabled() || !ValidID(id) {
		return ""
	}
	return filepath.Join(a.root, id)
}

func (a *Area) lockFor(id string) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	l, ok := a.locks[id]
	if !ok {
		l = &sync.Mutex{}
		a.locks[id] = l
	}
	return l
}

func (a *Area) dropLock(id string) {
	a.mu.Lock()
	delete(a.locks, id)
	a.mu.Unlock()
}

// EnsureFree is the disk guard: it refuses when the staging filesystem has less
// than size*DiskHeadroomFactor available. A probe that cannot answer (unknown
// platform, permission) does NOT refuse — a guard that blocks all uploads
// because it cannot measure is worse than no guard.
func (a *Area) EnsureFree(size int64) error {
	if !a.Enabled() || size <= 0 || a.FreeBytes == nil {
		return nil
	}
	if err := os.MkdirAll(a.root, 0o755); err != nil {
		return fmt.Errorf("staging: mkdir root: %w", err)
	}
	free, err := a.FreeBytes(a.root)
	if err != nil {
		return nil
	}
	need := uint64(float64(size) * DiskHeadroomFactor)
	if free < need {
		return fmt.Errorf("%w: need %d bytes free for a %d byte upload, have %d",
			ErrNoDiskSpace, need, size, free)
	}
	return nil
}

// Create makes the staging directory and writes the initial manifest.
func (a *Area) Create(id string, totalSize, chunkSize int64, hash string) (*Manifest, error) {
	if !a.Enabled() {
		return nil, errors.New("staging: no staging directory configured")
	}
	if !ValidID(id) {
		return nil, ErrBadID
	}
	if totalSize < 0 || chunkSize <= 0 {
		return nil, fmt.Errorf("staging: bad size/chunk (%d/%d)", totalSize, chunkSize)
	}
	m := &Manifest{
		Version:   ManifestVersion,
		ID:        id,
		TotalSize: totalSize,
		ChunkSize: chunkSize,
		Hash:      hash,
		Parts:     []Part{},
		CreatedAt: time.Now().UTC(),
	}
	if n := m.PartCount(); n > MaxParts {
		return nil, fmt.Errorf("%w: %d parts at chunk %d (max %d)", ErrTooManyParts, n, chunkSize, MaxParts)
	}
	dir := a.Dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("staging: mkdir: %w", err)
	}
	if err := a.writeManifest(dir, m); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return m, nil
}

// CreateVariable starts a staging directory whose parts may be any size —
// the shape S3 multipart needs. See Manifest.Variable for why both modes live
// in one area.
func (a *Area) CreateVariable(id string) (*Manifest, error) {
	if !a.Enabled() {
		return nil, errors.New("staging: no staging directory configured")
	}
	if !ValidID(id) {
		return nil, ErrBadID
	}
	m := &Manifest{
		Version:   ManifestVersion,
		ID:        id,
		Variable:  true,
		Parts:     []Part{},
		CreatedAt: time.Now().UTC(),
	}
	dir := a.Dir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("staging: mkdir: %w", err)
	}
	if err := a.writeManifest(dir, m); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return m, nil
}

// WriteVariablePart stores part n with whatever length arrives, up to max
// bytes. Re-writing a part is idempotent — S3 clients retry a part by sending
// it again, and the last one written wins, exactly as S3 specifies.
//
// ⚠ Unlike WritePart there is no expected length to check against, so a
// truncated body cannot be detected HERE. That is not a gap: S3 puts the
// integrity check at CompleteMultipartUpload, where the client sends back the
// ETag of every part it believes it uploaded, and a part whose md5 disagrees
// is refused there.
func (a *Area) WriteVariablePart(id string, n int, r io.Reader, max int64) (*Manifest, error) {
	dir := a.Dir(id)
	if dir == "" {
		return nil, ErrBadID
	}
	m, err := a.Manifest(id)
	if err != nil {
		return nil, err
	}
	if !m.Variable {
		return nil, fmt.Errorf("%w: upload %s is not a variable-part upload", ErrBadPart, id)
	}
	if n < 1 || n > MaxParts {
		return nil, fmt.Errorf("%w: part %d outside 1..%d", ErrBadPart, n, MaxParts)
	}

	tmp, err := os.CreateTemp(dir, fmt.Sprintf(".part-%06d-*.tmp", n))
	if err != nil {
		return nil, fmt.Errorf("staging: part temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	h := md5.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, max+1))
	if copyErr != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("staging: write part: %w", copyErr)
	}
	if written > max {
		_ = tmp.Close()
		return nil, fmt.Errorf("%w: part %d exceeds %d bytes", ErrBadPart, n, max)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("staging: sync part: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("staging: close part: %w", err)
	}

	lock := a.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	m, err = a.Manifest(id)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(tmpName, partPath(dir, n)); err != nil {
		return nil, fmt.Errorf("staging: place part: %w", err)
	}
	p := Part{N: n, Size: written, MD5: hex.EncodeToString(h.Sum(nil))}
	replaced := false
	for i := range m.Parts {
		if m.Parts[i].N == n {
			m.Parts[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		m.Parts = append(m.Parts, p)
	}
	m.TotalSize = m.Received()
	if err := a.writeManifest(dir, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Manifest reads the manifest for id.
func (a *Area) Manifest(id string) (*Manifest, error) {
	dir := a.Dir(id)
	if dir == "" {
		return nil, ErrBadID
	}
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("staging: read manifest: %w", err)
	}
	m := &Manifest{}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("staging: parse manifest: %w", err)
	}
	return m, nil
}

// writeManifest replaces the manifest atomically (temp + rename).
func (a *Area) writeManifest(dir string, m *Manifest) error {
	m.UpdatedAt = time.Now().UTC()
	sort.Slice(m.Parts, func(i, j int) bool { return m.Parts[i].N < m.Parts[j].N })
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("staging: encode manifest: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("staging: manifest temp: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("staging: write manifest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("staging: sync manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("staging: close manifest: %w", err)
	}
	return os.Rename(name, filepath.Join(dir, ManifestName))
}

// partPath is <dir>/000042.part — zero padded so a directory listing sorts in
// part order, which makes a staging dir readable by a human under pressure.
func partPath(dir string, n int) string {
	return filepath.Join(dir, fmt.Sprintf("%06d.part", n))
}

// WritePart stores part n from r.
//
// want is the exact number of bytes the part must contain. A body shorter than
// want (the client's connection dropped mid-chunk) is DISCARDED and
// ErrShortPart returned: the offset must never advance over bytes we do not
// have. A body longer than want is also refused.
//
// Re-writing an existing part is allowed and idempotent — a client that is not
// sure whether its last chunk landed simply sends it again.
func (a *Area) WritePart(id string, n int, r io.Reader, want int64) (*Manifest, error) {
	dir := a.Dir(id)
	if dir == "" {
		return nil, ErrBadID
	}
	m, err := a.Manifest(id)
	if err != nil {
		return nil, err
	}
	if n < 1 || n > m.PartCount() {
		return nil, fmt.Errorf("%w: part %d outside 1..%d", ErrBadPart, n, m.PartCount())
	}
	if exp := m.ExpectedPartSize(n); want != exp {
		return nil, fmt.Errorf("%w: part %d must be %d bytes, got %d", ErrBadPart, n, exp, want)
	}

	// Land the bytes outside the lock: a chunk can take a while and a status
	// poll must not queue behind it.
	tmp, err := os.CreateTemp(dir, fmt.Sprintf(".part-%06d-*.tmp", n))
	if err != nil {
		return nil, fmt.Errorf("staging: part temp: %w", err)
	}
	tmpName := tmp.Name()
	// Discipline for our own temp files, the same one the multipart handlers
	// owe /tmp: on every path out of here the temp file is gone.
	defer func() { _ = os.Remove(tmpName) }()

	h := md5.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, want+1))
	if copyErr != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("staging: write part: %w", copyErr)
	}
	if written != want {
		_ = tmp.Close()
		return nil, fmt.Errorf("%w: part %d expected %d bytes, received %d", ErrShortPart, n, want, written)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("staging: sync part: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("staging: close part: %w", err)
	}

	lock := a.lockFor(id)
	lock.Lock()
	defer lock.Unlock()

	// Re-read under the lock: another chunk may have landed meanwhile.
	m, err = a.Manifest(id)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(tmpName, partPath(dir, n)); err != nil {
		return nil, fmt.Errorf("staging: place part: %w", err)
	}
	p := Part{N: n, Size: written, MD5: hex.EncodeToString(h.Sum(nil))}
	replaced := false
	for i := range m.Parts {
		if m.Parts[i].N == n {
			m.Parts[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		m.Parts = append(m.Parts, p)
	}
	if err := a.writeManifest(dir, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Open returns a reader over the assembled upload: every part concatenated in
// part-number order. The reader is seekable, so a driver (or the S3 SDK) can
// measure and replay the body, and so chunk 5 can serve a byte range out of
// staging while the transfer runs.
//
// Refuses an incomplete upload — assembling over a hole would hand the driver
// a silently truncated file.
func (a *Area) Open(id string) (*Reader, error) {
	m, err := a.Manifest(id)
	if err != nil {
		return nil, err
	}
	if !m.Complete() {
		return nil, fmt.Errorf("%w: %d of %d bytes staged", ErrIncomplete, m.Offset(), m.TotalSize)
	}
	dir := a.Dir(id)
	segs := make([]segment, 0, len(m.Parts))
	parts := append([]Part(nil), m.Parts...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].N < parts[j].N })
	var off int64
	for _, p := range parts {
		segs = append(segs, segment{path: partPath(dir, p.N), start: off, size: p.Size})
		off += p.Size
	}
	return &Reader{segs: segs, total: off}, nil
}

// Remove deletes the whole staging directory for id.
func (a *Area) Remove(id string) error {
	dir := a.Dir(id)
	if dir == "" {
		return ErrBadID
	}
	a.dropLock(id)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("staging: remove: %w", err)
	}
	return nil
}

// List returns the ids of every staging directory currently on disk.
func (a *Area) List() ([]string, error) {
	if !a.Enabled() {
		return nil, nil
	}
	ents, err := os.ReadDir(a.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("staging: list: %w", err)
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() && ValidID(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// Usage reports what is physically in the staging area right now: how many
// upload directories, and how many bytes of parts they hold.
//
// It measures the DISK rather than trusting a counter, and that is the point.
// The in-flight/staged-bytes metrics are moved by events (begin, chunk,
// commit, abort) so they are fresh between sweeps, then reconciled against
// this on every sweeper pass — so a restart, or a crash that left parts
// behind, cannot leave the dashboard lying about how full the staging
// filesystem is.
func (a *Area) Usage() (uploads int, bytes int64) {
	ids, err := a.List()
	if err != nil {
		return 0, 0
	}
	for _, id := range ids {
		uploads++
		ents, derr := os.ReadDir(a.Dir(id))
		if derr != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".part") {
				continue
			}
			if fi, ferr := e.Info(); ferr == nil {
				bytes += fi.Size()
			}
		}
	}
	return uploads, bytes
}

// Idle reports how long ago the upload last saw activity, measured from the
// manifest's mtime (rewritten on every accepted part).
func (a *Area) Idle(id string, now time.Time) (time.Duration, error) {
	dir := a.Dir(id)
	if dir == "" {
		return 0, ErrBadID
	}
	fi, err := os.Stat(filepath.Join(dir, ManifestName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// A directory with no manifest is debris from a crash between
			// mkdir and the first manifest write — age it by the dir itself.
			if di, derr := os.Stat(dir); derr == nil {
				return now.Sub(di.ModTime()), nil
			}
			return 0, ErrNotFound
		}
		return 0, err
	}
	return now.Sub(fi.ModTime()), nil
}

// Sweep removes staging directories idle for longer than ttl. keep, when
// non-nil, is consulted first: returning true protects an id (the caller's DB
// says it is still live, e.g. a failed transfer being kept for retry).
//
// Returns the ids it removed so the caller can log them — a sweep nobody can
// see is how 29 GB of temp files went unnoticed once already.
func (a *Area) Sweep(ttl time.Duration, now time.Time, keep func(id string) bool) ([]string, error) {
	ids, err := a.List()
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0, 4)
	for _, id := range ids {
		if keep != nil && keep(id) {
			continue
		}
		idle, err := a.Idle(id, now)
		if err != nil {
			continue
		}
		if idle < ttl {
			continue
		}
		if err := a.Remove(id); err != nil {
			continue
		}
		removed = append(removed, id)
	}
	return removed, nil
}

// segment is one part file's slice of the assembled byte range.
type segment struct {
	path  string
	start int64
	size  int64
}

// Reader concatenates the part files of a staged upload into one seekable
// stream. Only one part file is open at a time.
type Reader struct {
	segs  []segment
	total int64
	pos   int64

	cur    *os.File
	curIdx int
}

// Size is the assembled length in bytes.
func (r *Reader) Size() int64 { return r.total }

// Read implements io.Reader across the part boundary.
func (r *Reader) Read(p []byte) (int, error) {
	if r.pos >= r.total {
		return 0, io.EOF
	}
	idx := r.segIndex(r.pos)
	if idx < 0 {
		return 0, io.EOF
	}
	if r.cur == nil || r.curIdx != idx {
		if err := r.openSeg(idx); err != nil {
			return 0, err
		}
	}
	seg := r.segs[idx]
	remain := seg.start + seg.size - r.pos
	if int64(len(p)) > remain {
		p = p[:remain]
	}
	n, err := r.cur.Read(p)
	r.pos += int64(n)
	if errors.Is(err, io.EOF) && r.pos < r.total {
		// End of this part, more parts to go — not the end of the stream.
		err = nil
	}
	return n, err
}

// Seek implements io.Seeker over the assembled stream.
func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.pos + offset
	case io.SeekEnd:
		abs = r.total + offset
	default:
		return 0, fmt.Errorf("staging: bad whence %d", whence)
	}
	if abs < 0 {
		return 0, fmt.Errorf("staging: negative seek to %d", abs)
	}
	r.pos = abs
	if r.cur != nil {
		idx := r.segIndex(abs)
		if idx != r.curIdx || idx < 0 {
			_ = r.cur.Close()
			r.cur = nil
		} else if _, err := r.cur.Seek(abs-r.segs[idx].start, io.SeekStart); err != nil {
			return 0, err
		}
	}
	return abs, nil
}

// Close releases the currently open part file.
func (r *Reader) Close() error {
	if r.cur != nil {
		err := r.cur.Close()
		r.cur = nil
		return err
	}
	return nil
}

func (r *Reader) segIndex(pos int64) int {
	for i, s := range r.segs {
		if pos >= s.start && pos < s.start+s.size {
			return i
		}
	}
	return -1
}

func (r *Reader) openSeg(idx int) error {
	if r.cur != nil {
		_ = r.cur.Close()
		r.cur = nil
	}
	f, err := os.Open(r.segs[idx].path)
	if err != nil {
		return fmt.Errorf("staging: open part: %w", err)
	}
	if _, err := f.Seek(r.pos-r.segs[idx].start, io.SeekStart); err != nil {
		_ = f.Close()
		return err
	}
	r.cur = f
	r.curIdx = idx
	return nil
}
