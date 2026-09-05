// Package filecache keeps a local copy of a big file that lives on a slow
// backend, so that the request which pays for the transfer is honest about it
// ("preparing… 40 %") and every request after it is served at local-disk speed.
//
// The requirement, in the owner's words (translated from Turkish):
//
//	"if the fs is slow and the file is big, we need to do something like
//	 telling the user we're building a cache just for them and starting the
//	 download once the cache is ready."
//
// A 4 GB file on a NAS or a distant bucket used to dribble at the backend's
// speed with no explanation, no seeking and no resume; a retry paid for the
// whole object again. This package is the other half of the answer (the first
// half is storage.RangeReader): fetch it once, locally, say so while it
// happens, and then serve it like a local file.
//
// # This is internal/sharezip, generalised
//
// sharezip already caches folder-share ZIPs on disk behind a generation
// registry with Percent/Wait/StartOrGet and a "preparing…" page. The shape is
// proven, so it is reused rather than reinvented — with the one thing it does
// NOT have, and the reason this package exists as its own thing:
//
// ⚠⚠ A GLOBAL CAP. sharezip prunes per node (pruneOld), which bounds nothing:
// N nodes cost N archives. A per-node file cache with no ceiling is a disk
// incident, and this repo had one this month — leaked multipart temp files
// filled 29 GB of the host in two hours. So MaxBytes is enforced here, on
// every fill, including the bytes of fills that are still being written.
//
// # What it is keyed on
//
// nodeID + a content signature (the driver's ETag when it has one, size+mtime
// when it does not — local has no ETag, measured). A changed file therefore
// gets a different key and invalidates itself, exactly like the ZIP cache's
// content signature. Nothing is ever overwritten in place.
//
// # What it deliberately does NOT do
//
//   - It does not decide WHO may read a file. ACL, tenancy and share caps are
//     checked by the caller before a cache key is ever computed; this package
//     never sees a request. A cached entry is bytes plus a key, nothing more.
//   - It does not persist across restarts as state. The directory is re-read
//     at startup (the files are still valid — the key carries the signature),
//     but an interrupted fill leaves only a .tmp- file, which is swept.
//   - It is not internal/ops. A fill is not a user operation: nobody submitted
//     it, nobody may cancel it, and it must not survive into a tray as a row
//     that outlives the download that triggered it. It is an in-memory
//     generation registry, like sharezip's, and dying with the process is
//     correct — the next request starts it again.
package filecache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Defaults. Every one of them is overridable from the environment; see
// config.CacheConfig.
const (
	// DefaultMinSize is the smallest file worth preparing. Below it the
	// preparation costs more than the transfer it saves.
	DefaultMinSize = 64 << 20 // 64 MiB
	// DefaultMaxBytes is the global ceiling on the whole cache directory.
	DefaultMaxBytes = 20 << 30 // 20 GiB
	// DefaultPinTTL is how long a "this file is cached" answer stays true
	// for eviction purposes. It exists so that a caller which asked
	// "can I range this?" and then ranged it cannot have the entry pulled
	// out from under it in between.
	DefaultPinTTL = time.Minute
	// failCooldown keeps a failing fill from being restarted by every
	// request. A broken 5 GB object would otherwise be re-fetched from the
	// backend once per click.
	failCooldown = 30 * time.Second
)

// ErrNoRoom means the global cap cannot accommodate this file without
// evicting something that is in use. The caller must fall back to streaming
// from the driver — never to exceeding the cap.
var ErrNoRoom = errors.New("filecache: no room under the global cap")

// ErrSizeChanged means the object changed length while it was being fetched.
// The partial copy is discarded: the key promises a specific signature, and
// serving a body of a different length through http.ServeContent (which
// trusts the size it was given) would hand out a truncated or padded file.
var ErrSizeChanged = errors.New("filecache: object changed size during fetch")

// OpenFunc opens the bytes to be cached, starting at byte 0.
type OpenFunc func(ctx context.Context) (io.ReadCloser, error)

// Config configures a Cache. The zero value disables caching (Dir == "").
type Config struct {
	// Dir is the cache directory, normally <data_dir>/cache. Empty disables
	// the cache entirely and every method degrades to "not cached".
	Dir string
	// MinSize is the smallest file that qualifies. <=0 uses DefaultMinSize.
	MinSize int64
	// MaxBytes is the global ceiling. <=0 uses DefaultMaxBytes; it is never
	// unlimited, on purpose.
	MaxBytes int64
	// SlowBytesPerSec is the measured throughput below which a storage counts
	// as slow. <=0 uses DefaultSlowBytesPerSec.
	SlowBytesPerSec int64
	// PinTTL overrides DefaultPinTTL. Tests use a short one.
	PinTTL time.Duration
}

// record is one cache slot: a finished file, or one being written.
type record struct {
	key      string
	size     int64 // declared size while filling, real size once ready
	lastUsed time.Time
	pinUntil time.Time // soft pin — a promise made to a caller
	refs     int       // hard pin — open readers, plus 1 while filling
	filling  bool
	fill     *Fill
}

func (r *record) inUse(now time.Time) bool {
	return r.refs > 0 || now.Before(r.pinUntil)
}

// Cache is a global-capped, LRU-evicted local cache of file bodies. Which
// storages are slow enough to be worth caching is decided in meter.go, off the
// rate internal/throughput measures — this type keeps no samples of its own.
type Cache struct {
	dir      string
	minSize  int64
	maxBytes int64
	pinTTL   time.Duration
	slowBPS  int64

	mu        sync.Mutex
	records   map[string]*record
	total     int64
	failUntil map[string]time.Time
}

// New returns a Cache. It reads the directory once so that entries survive a
// restart, and sweeps the .tmp- files an interrupted fill leaves behind.
func New(cfg Config) *Cache {
	c := &Cache{
		dir:       cfg.Dir,
		minSize:   cfg.MinSize,
		maxBytes:  cfg.MaxBytes,
		pinTTL:    cfg.PinTTL,
		slowBPS:   cfg.SlowBytesPerSec,
		records:   map[string]*record{},
		failUntil: map[string]time.Time{},
	}
	if c.minSize <= 0 {
		c.minSize = DefaultMinSize
	}
	if c.maxBytes <= 0 {
		c.maxBytes = DefaultMaxBytes
	}
	if c.pinTTL <= 0 {
		c.pinTTL = DefaultPinTTL
	}
	if c.slowBPS <= 0 {
		c.slowBPS = DefaultSlowBytesPerSec
	}
	c.load()
	return c
}

// Enabled reports whether caching is configured at all.
func (c *Cache) Enabled() bool { return c != nil && c.dir != "" }

// MinSize is the smallest file that qualifies.
func (c *Cache) MinSize() int64 {
	if c == nil {
		return DefaultMinSize
	}
	return c.minSize
}

// MaxBytes is the global ceiling.
func (c *Cache) MaxBytes() int64 {
	if c == nil {
		return 0
	}
	return c.maxBytes
}

// Bytes is what the cache currently occupies, counting fills in flight at
// their declared size. Exported for tests and for the metrics chunk.
func (c *Cache) Bytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// Len is the number of entries, counting fills in flight.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.records)
}

var entryName = regexp.MustCompile(`^(\d+)-([0-9a-f]{16})\.bin$`)

// Signature is the content half of a cache key: the driver's ETag when there
// is one, and size+mtime when there is not.
//
// ⚠ The fallback is not a nicety. The local driver reports no ETag at all
// (measured: drivers/local never sets Object.Etag), and it is the driver an
// NFS/SMB mount uses — precisely the slow case this package exists for. A key
// that required an ETag would therefore never cache the most important case.
func Signature(etag string, size int64, mtime time.Time) string {
	if e := trimQuotes(etag); e != "" {
		return "e" + e
	}
	if size <= 0 && mtime.IsZero() {
		return ""
	}
	return fmt.Sprintf("s%d-m%d", size, mtime.Unix())
}

func trimQuotes(s string) string {
	for len(s) > 0 && (s[0] == '"' || s[0] == '\'') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == '"' || s[len(s)-1] == '\'') {
		s = s[:len(s)-1]
	}
	return s
}

// Key combines a node id with a content signature. An empty result means
// "not cacheable" — no node row (a path outside the catalogue) or no
// signature at all — and every entry point treats it as a miss.
func Key(nodeID int64, sig string) string {
	if nodeID <= 0 || sig == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sig))
	return strconv.FormatInt(nodeID, 10) + "-" + hex.EncodeToString(sum[:])[:16]
}

func (c *Cache) pathFor(key string) string { return filepath.Join(c.dir, key+".bin") }

// load indexes an existing cache directory and sweeps interrupted fills.
func (c *Cache) load() {
	if !c.Enabled() {
		return
	}
	ents, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) > 5 && name[:5] == ".tmp-" {
			// A fill that died with the process. Nothing references it.
			_ = os.Remove(filepath.Join(c.dir, name))
			continue
		}
		m := entryName.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		key := name[:len(name)-len(".bin")]
		used := fi.ModTime()
		if used.After(now) {
			used = now
		}
		c.records[key] = &record{key: key, size: fi.Size(), lastUsed: used}
		c.total += fi.Size()
	}
}

// ---------------------------------------------------------------- reading

// Entry is a pinned, ready cache entry. It holds a hard pin: eviction cannot
// remove it while the handle is alive, which is what makes "never evict an
// entry a request is streaming from" a property rather than a hope.
//
// Exactly one of Reader, RangeReader or Release must be called. Reader and
// RangeReader hand the pin to the returned ReadCloser, so closing the body
// releases it.
type Entry struct {
	c    *Cache
	rec  *record
	done atomic.Bool
}

// Size is the cached file's length in bytes.
func (e *Entry) Size() int64 { return e.rec.size }

// Path is the file on disk. Callers outside this package should prefer
// Reader/RangeReader; Path exists for tests and for surfaces that need to
// hand a filename to something else.
func (e *Entry) Path() string { return e.c.pathFor(e.rec.key) }

// Release drops the pin. Safe to call more than once, and safe to call after
// Reader/RangeReader succeeded (it is then a no-op).
func (e *Entry) Release() {
	if e == nil || !e.done.CompareAndSwap(false, true) {
		return
	}
	e.c.mu.Lock()
	if e.rec.refs > 0 {
		e.rec.refs--
	}
	e.c.mu.Unlock()
}

// Reader returns the whole body. The pin is released when it is closed.
func (e *Entry) Reader() (io.ReadCloser, error) {
	return e.RangeReader(0, -1)
}

// RangeReader returns a window, with exactly storage.RangeReader's contract:
// off >= 0; length < 0 means "to the end"; length == 0 is an immediate EOF;
// an offset at or past EOF is EOF, not an error; a length past EOF is clamped.
// The pin is released when the returned body is closed.
func (e *Entry) RangeReader(off, length int64) (io.ReadCloser, error) {
	if off < 0 {
		e.Release()
		return nil, fmt.Errorf("filecache: negative offset %d", off)
	}
	f, err := os.Open(e.Path())
	if err != nil {
		e.Release()
		return nil, err
	}
	if off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			_ = f.Close()
			e.Release()
			return nil, err
		}
	}
	var body io.Reader = f
	if length >= 0 {
		body = io.LimitReader(f, length)
	}
	return &pinnedReader{Reader: body, f: f, e: e}, nil
}

type pinnedReader struct {
	io.Reader
	f *os.File
	e *Entry
}

func (p *pinnedReader) Close() error {
	err := p.f.Close()
	p.e.Release()
	return err
}

// Acquire pins and returns a ready entry, or reports false. A fill in flight
// is not a hit — its file does not exist yet.
func (c *Cache) Acquire(key string) (*Entry, bool) {
	if !c.Enabled() || key == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.records[key]
	if !ok || rec.filling {
		return nil, false
	}
	rec.refs++
	rec.lastUsed = time.Now()
	return &Entry{c: c, rec: rec}, true
}

// Ready reports whether key is cached AND promises, for PinTTL, that it will
// still be there. Callers use it to answer "can this be served ranged?"
// before they commit to serving it that way; the soft pin is what stops the
// answer from going stale between the question and the read.
func (c *Cache) Ready(key string) bool {
	if !c.Enabled() || key == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.records[key]
	if !ok || rec.filling {
		return false
	}
	now := time.Now()
	rec.lastUsed = now
	rec.pinUntil = now.Add(c.pinTTL)
	return true
}

// Filling returns the in-flight fetch for key, if there is one. It starts
// nothing: a progress poll must be able to ask "how is it going" without
// being able to make it go.
func (c *Cache) Filling(key string) (*Fill, bool) {
	if !c.Enabled() || key == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.records[key]
	if !ok || !rec.filling {
		return nil, false
	}
	return rec.fill, true
}

// ---------------------------------------------------------------- filling

// Fill tracks one in-flight (or just-finished) fetch so concurrent callers
// deduplicate onto it and can watch its progress.
type Fill struct {
	total    int64
	written  atomic.Int64
	finished chan struct{}
	err      error
}

// Percent is 0..99 while fetching. 100 is deliberately not reachable here:
// it belongs to a finished file on disk, so a poller only ever sees 100 once
// the bytes are actually servable. (Same rule as sharezip.Gen.)
func (f *Fill) Percent() int {
	if f.total <= 0 {
		return 0
	}
	p := int(f.written.Load() * 100 / f.total)
	if p > 99 {
		p = 99
	}
	if p < 0 {
		p = 0
	}
	return p
}

// Written is the number of bytes fetched so far.
func (f *Fill) Written() int64 { return f.written.Load() }

// Done reports whether the fetch has finished (successfully or not).
func (f *Fill) Done() bool {
	select {
	case <-f.finished:
		return true
	default:
		return false
	}
}

// Wait blocks until the fetch finishes (returning its error) or ctx is done.
func (f *Fill) Wait(ctx context.Context) error {
	select {
	case <-f.finished:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Err is the fetch's error once Done.
func (f *Fill) Err() error { return f.err }

// StartOrGet makes sure a fetch for key is running and returns it. A key that
// is already cached returns a finished Fill (Done, no error), so the common
// path is one map lookup.
//
// ⚠ The fetch runs on context.WithoutCancel(ctx), taken here rather than left
// to callers. The whole point of the cache is that several requests wait on
// one fetch: if the first client hangs up and takes the fetch with it, every
// other waiter is punished for someone else's disconnect. (Post-response work
// dying with the request context is how the share download counter lost
// writes in v0.18.0 — same trap, different surface.)
//
// It returns ErrNoRoom when the global cap cannot fit `size` without evicting
// something in use. That is not an error to show a user: it means "serve this
// the way you always did".
func (c *Cache) StartOrGet(ctx context.Context, key string, size int64, open OpenFunc) (*Fill, error) {
	if !c.Enabled() || key == "" {
		return nil, ErrNoRoom
	}
	if size <= 0 {
		return nil, ErrNoRoom
	}
	c.mu.Lock()

	if rec, ok := c.records[key]; ok {
		if rec.filling {
			f := rec.fill
			c.mu.Unlock()
			return f, nil
		}
		rec.lastUsed = time.Now()
		c.mu.Unlock()
		return finishedFill(size), nil
	}
	if until, ok := c.failUntil[key]; ok {
		if time.Now().Before(until) {
			c.mu.Unlock()
			return nil, ErrNoRoom
		}
		delete(c.failUntil, key)
	}
	if !c.evictLocked(size) {
		c.mu.Unlock()
		return nil, ErrNoRoom
	}

	f := &Fill{total: size, finished: make(chan struct{})}
	rec := &record{key: key, size: size, lastUsed: time.Now(), refs: 1, filling: true, fill: f}
	c.records[key] = rec
	c.total += size
	c.mu.Unlock()

	// WithoutCancel, not Background: the fetch keeps the request's values
	// (tenant scope, tracing, logger) and loses only its cancellation.
	go c.run(context.WithoutCancel(ctx), key, rec, open, f)
	return f, nil
}

func finishedFill(size int64) *Fill {
	f := &Fill{total: size, finished: make(chan struct{})}
	f.written.Store(size)
	close(f.finished)
	return f
}

func (c *Cache) run(ctx context.Context, key string, rec *record, open OpenFunc, f *Fill) {
	err := c.fetch(ctx, key, rec, open, f)

	c.mu.Lock()
	rec.filling = false
	if rec.refs > 0 {
		rec.refs-- // the fill's own hard pin
	}
	if err != nil {
		delete(c.records, key)
		c.total -= rec.size
		c.pruneFailuresLocked()
		c.failUntil[key] = time.Now().Add(failCooldown)
	} else {
		rec.lastUsed = time.Now()
	}
	c.mu.Unlock()

	f.err = err
	close(f.finished)
}

func (c *Cache) fetch(ctx context.Context, key string, rec *record, open OpenFunc, f *Fill) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	discard := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	rc, err := open(ctx)
	if err != nil {
		discard()
		return err
	}
	n, err := io.Copy(&progressWriter{w: tmp, f: f}, rc)
	_ = rc.Close()
	if err != nil {
		discard()
		return err
	}
	if n != rec.size {
		discard()
		return fmt.Errorf("%w: declared %d, fetched %d", ErrSizeChanged, rec.size, n)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	// Publish atomically: a reader never observes a half-written entry,
	// because the name it looks under only exists once the bytes are all
	// there.
	if err := os.Rename(tmpName, c.pathFor(key)); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

type progressWriter struct {
	w io.Writer
	f *Fill
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	if n > 0 {
		p.f.written.Add(int64(n))
	}
	return n, err
}

func (c *Cache) pruneFailuresLocked() {
	if len(c.failUntil) < 64 {
		return
	}
	now := time.Now()
	for k, t := range c.failUntil {
		if now.After(t) {
			delete(c.failUntil, k)
		}
	}
}

// ---------------------------------------------------------------- eviction

// evictLocked frees room for `need` bytes, LRU first, and reports whether it
// succeeded. It refuses — and reports false — rather than break either of the
// two rules that matter:
//
//  1. the global cap is never exceeded (a cache that can grow without a
//     ceiling is a disk incident, and this repo has had one);
//  2. an entry that a request is reading from, or was just promised, is
//     never removed.
//
// Failing here is not a failure of the download: the caller streams from the
// driver exactly as it did before this package existed.
func (c *Cache) evictLocked(need int64) bool {
	if c.total+need <= c.maxBytes {
		return true
	}
	if need > c.maxBytes {
		return false // one file larger than the whole cache
	}
	now := time.Now()
	victims := make([]*record, 0, len(c.records))
	for _, rec := range c.records {
		if rec.filling || rec.inUse(now) {
			continue
		}
		victims = append(victims, rec)
	}
	sort.Slice(victims, func(i, j int) bool { return victims[i].lastUsed.Before(victims[j].lastUsed) })

	for _, rec := range victims {
		if c.total+need <= c.maxBytes {
			return true
		}
		if err := os.Remove(c.pathFor(rec.key)); err != nil && !os.IsNotExist(err) {
			// Windows refuses to unlink an open file. Leave the entry
			// indexed and try a different victim: dropping it from the
			// index while the file stays on disk would make the cap lie.
			continue
		}
		delete(c.records, rec.key)
		c.total -= rec.size
	}
	return c.total+need <= c.maxBytes
}

// Drop removes an entry by key if it is not in use. Reports whether it went.
// Used by tests and by the invalidation paths that know a node's bytes have
// been replaced out from under a key.
func (c *Cache) Drop(key string) bool {
	if !c.Enabled() || key == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.records[key]
	if !ok || rec.filling || rec.inUse(time.Now()) {
		return false
	}
	if err := os.Remove(c.pathFor(rec.key)); err != nil && !os.IsNotExist(err) {
		return false
	}
	delete(c.records, rec.key)
	c.total -= rec.size
	return true
}
