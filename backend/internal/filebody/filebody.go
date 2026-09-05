// Package filebody answers the one question every surface that serves file
// bytes has had to ask since uploads became staged: where are this file's
// bytes RIGHT NOW?
//
// Before staged uploads (internal/staging) the answer was always "on the
// storage driver", so every read surface — the app's download/preview, share
// links, the folder-share browse page, thumbnails, WebDAV, the archive and AI
// endpoints — called drv.Read/drv.Stat directly and that was correct. A staged
// upload breaks that assumption on purpose: the node is created and listed the
// moment the client commits, while a background op is still streaming the bytes
// from filex's staging area to the driver. During that window the driver does
// not have the object (or, on an overwrite, still has the PREVIOUS one), and a
// surface that asks the driver answers 404 — or worse, serves the old file as
// if it were the new one.
//
// So there is exactly one helper, here, and every read surface goes through it.
// This is deliberate and it is the standing project rule: a fix that lands on
// one surface and not the others turns one product into several. If a surface
// cannot use it, that is a finding to report, not a special case to write.
//
// # What it decides
//
//   - Where the bytes are. nodes.transfer_state == "staged" or "failed" → the
//     staging area (a failed transfer keeps its staging, and those bytes are
//     the only copy); anything else → the driver. The node is consulted FIRST, before the
//     driver, because an overwrite leaves a perfectly readable stale object
//     behind and "the driver answered" is not evidence that it answered with
//     the right file.
//
//   - What Stat says while staged. The committed values: the size the client
//     committed (the staging manifest is the authority, and it is the number
//     already published on the node) and an ETag computed from the staged parts
//     — never the driver's absence, and never the pre-overwrite ETag, which
//     would let a cached client keep the old file forever.
//
//   - What a FAILED transfer means. Nothing, to a reader. The staging directory
//     is kept on failure precisely so the transfer can be retried without
//     re-sending a byte, and the node stays "staged" — so reads keep coming out
//     of staging exactly as they did while the transfer was running.
//
//   - What "staged, but staging is gone" means. A clean, loud failure
//     (ErrStagingGone), never a body. This state is reachable: the sweeper
//     removes a `failed` session once it has been idle for a full TTL, and the
//     node it belongs to keeps saying "staged" forever. There is deliberately
//     NO fallback to the driver in that case — on an overwrite the driver holds
//     the previous version of the same path at plausibly the same size, and
//     serving that as the new file is a silent wrong answer, which is worse
//     than an error a human can read.
//
//   - Whether a locally prepared copy should answer instead of the backend.
//     internal/filecache keeps big files from slow storages on local disk;
//     this is where it is consulted, for the same reason as everything above:
//     one door, so every read surface gets it without per-surface code. The
//     accelerating half is invisible — Open/ReadRange simply come off the
//     local file when there is one. The visible half (the "preparing… %"
//     answer) is Prepare, which a surface must ask for, because only a
//     surface knows whether it can express "not yet" to its client.
package filebody

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filecache"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/staging"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/throughput"
)

// ErrStagingGone is returned when a node says its bytes are staged but the
// staging area cannot produce them. Callers must surface it as an error with a
// message — never as a 404 (the file exists) and never as a partial body.
var ErrStagingGone = errors.New("filebody: file is still transferring and its staged copy is gone")

// Resolver resolves byte sources. A nil *Resolver is usable and always answers
// "the driver" — that is what keeps every construction site (tests, embedders,
// list-only environments) working without wiring staging in.
type Resolver struct {
	store db.Store
	area  *staging.Area
	cache *filecache.Cache

	slowMu sync.Mutex
	slowAt map[int64]slowFlag
}

// slowFlag memoises one storage's `slow` config flag. The flag lives in the
// storage row, which would otherwise be re-read on every big-file request; a
// short TTL means an operator toggling it in the admin UI takes effect within
// half a minute rather than at the next restart.
type slowFlag struct {
	slow bool
	at   time.Time
}

const slowFlagTTL = 30 * time.Second

// New returns a Resolver over the node catalogue and the staging area. Either
// may be nil; the result then degrades to driver-only reads.
func New(store db.Store, area *staging.Area) *Resolver {
	return &Resolver{store: store, area: area, slowAt: map[int64]slowFlag{}}
}

// WithCache attaches the local file cache and returns the same Resolver, so
// wiring reads as one expression. A nil cache (or one with no directory) is
// fine and leaves every read going to the driver, which is what makes this
// package usable from tests and embedders that never configured a cache.
func (r *Resolver) WithCache(c *filecache.Cache) *Resolver {
	if r != nil {
		r.cache = c
	}
	return r
}

// Cache is the attached cache, or nil.
func (r *Resolver) Cache() *filecache.Cache {
	if r == nil {
		return nil
	}
	return r.cache
}

// operatorSlow reports the `slow: true` flag an operator can set on a storage
// config. Errors are swallowed and read as "not slow": a database hiccup must
// never turn a download into a preparing screen.
func (r *Resolver) operatorSlow(ctx context.Context, storageID int64) bool {
	if r == nil || r.store == nil || storageID <= 0 {
		return false
	}
	r.slowMu.Lock()
	if f, ok := r.slowAt[storageID]; ok && time.Since(f.at) < slowFlagTTL {
		r.slowMu.Unlock()
		return f.slow
	}
	r.slowMu.Unlock()

	slow := false
	if st, err := r.store.GetStorage(ctx, storageID); err == nil && st != nil && len(st.ConfigJSON) > 0 {
		var cfg struct {
			Slow any `json:"slow"`
		}
		if json.Unmarshal(st.ConfigJSON, &cfg) == nil {
			switch v := cfg.Slow.(type) {
			case bool:
				slow = v
			case string:
				slow = v == "1" || v == "true" || v == "yes"
			case float64:
				slow = v != 0
			}
		}
	}
	r.slowMu.Lock()
	r.slowAt[storageID] = slowFlag{slow: slow, at: time.Now()}
	r.slowMu.Unlock()
	return slow
}

// Source is one file's resolved byte source. Obtain it from Resolve; the zero
// value is not usable.
type Source struct {
	// Staged reports that the bytes are served out of filex's staging area
	// because the transfer to the driver has not finished (or has failed and
	// is waiting to be retried).
	Staged bool
	// UploadID is the staging session id, empty unless Staged.
	UploadID string
	// NodeID is the catalogue row the decision was taken from, 0 when the
	// path is not in the catalogue (pre-sync files, driver-only reads).
	NodeID int64

	drv  storage.Driver
	rel  string
	area *staging.Area
	man  *staging.Manifest
	node *model.Node

	// storageID labels the throughput measurement taken on Open/ReadRange
	// and identifies the storage whose speed decides whether this file is
	// worth caching. One field, one storage — the meter and the cache must
	// never be able to disagree about which backend they are talking about.
	storageID int64

	// cache wiring, resolved lazily and at most once per Source.
	res        *Resolver
	cacheKnown bool
	cacheKey   string
}

// Resolve reports where the bytes of `rel` on storage `storageID` live.
//
// `node` is optional: pass it when the caller already holds the row (share
// links, thumbnails, the queue workers) to save a lookup, or nil to have the
// catalogue consulted by path. A path that is not in the catalogue resolves to
// the driver, which is what keeps freshly-written, not-yet-synced files
// readable.
//
// The returned error is ErrStagingGone (wrapped, with the node and path in the
// message) when the node claims to be staged and the staging area cannot back
// that claim. Resolve fails BEFORE any header or byte is written, which is the
// whole reason the manifest is read here and not lazily at Open time.
func (r *Resolver) Resolve(ctx context.Context, drv storage.Driver, storageID int64, rel string, node *model.Node) (*Source, error) {
	src := &Source{drv: drv, rel: rel, res: r, storageID: storageID}
	if r == nil {
		return src, nil
	}
	if node == nil && r.store != nil && storageID > 0 && rel != "" {
		// Errors are swallowed on purpose: a catalogue miss means "not staged",
		// and a database hiccup must not take down a download of a file whose
		// bytes are sitting right there on the driver.
		node, _ = r.store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, rel))
	}
	if node == nil {
		return src, nil
	}
	src.NodeID = node.ID
	src.node = node
	// ⚠ "failed" reads from staging exactly like "staged". The two differ in what
	// they say about the FUTURE (one is still coming, the other needs a retry),
	// not about where the bytes are: a failed transfer keeps its staging
	// directory precisely so the retry is free, and those bytes are the only
	// copy — the driver has nothing. Sending a failed node to the driver would
	// make the file the user uploaded unreadable at the moment it most needs to
	// be recoverable.
	if !stagedBytes(node.TransferState) {
		return src, nil
	}

	if r.store == nil {
		return nil, fmt.Errorf("%w: node %d (%s) is staged but no catalogue is wired", ErrStagingGone, node.ID, node.Path)
	}
	if r.area == nil || !r.area.Enabled() {
		return nil, fmt.Errorf("%w: node %d (%s) is staged but no staging area is configured", ErrStagingGone, node.ID, node.Path)
	}
	row, err := r.store.GetStagedUploadByNode(ctx, node.ID)
	if err != nil || row == nil {
		return nil, fmt.Errorf("%w: no staging session for node %d (%s)", ErrStagingGone, node.ID, node.Path)
	}
	man, err := r.area.Manifest(row.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: staging %s for node %d (%s): %v", ErrStagingGone, row.ID, node.ID, node.Path, err)
	}
	// A node only reaches "staged" through a commit, and a commit refuses an
	// incomplete manifest — so an incomplete one here means the staging
	// directory was damaged after the fact. Assembling it would hand out a
	// silently truncated file.
	if !man.Complete() {
		return nil, fmt.Errorf("%w: staging %s for node %d (%s) holds %d of %d bytes",
			ErrStagingGone, row.ID, node.ID, node.Path, man.Offset(), man.TotalSize)
	}
	src.Staged, src.UploadID, src.area, src.man = true, row.ID, r.area, man
	return src, nil
}

// Stat describes the file. While staged it answers with the COMMITTED
// description — the size that was committed and an ETag derived from the staged
// parts — because the driver has nothing to describe yet, and because on an
// overwrite its answer would describe the previous version.
func (s *Source) Stat(ctx context.Context) (storage.Object, error) {
	if !s.Staged {
		return s.drv.Stat(ctx, s.rel)
	}
	mime := s.node.Mime
	mtime := s.node.UpdatedAt
	if mtime.IsZero() {
		mtime = s.man.UpdatedAt
	}
	return storage.Object{
		Path:  s.rel,
		Name:  path.Base(s.rel),
		Size:  s.man.TotalSize,
		Kind:  storage.KindFile,
		Mime:  mime,
		Etag:  s.man.CompositeETag(),
		Mtime: mtime,
	}, nil
}

// Size is the file's length in bytes while staged, and -1 otherwise (ask Stat).
func (s *Source) Size() int64 {
	if !s.Staged {
		return -1
	}
	return s.man.TotalSize
}

// Open returns the whole body.
//
// A driver read is wrapped so the bytes and the time they took land in
// internal/throughput. This is the single read funnel — every surface that
// streams a file (browser, WebDAV, share link, ShareX, the desktop app) goes
// through here — which is why the measurement belongs here and not in each of
// them.
//
// ⚠ Only driver-backed bodies are measured. The staging area and the prepared
// cache copy are local disk; folding their speed into the average would tell
// the meter that a NAS runs at NVMe speed, and the storage would never be
// classified slow again — which would switch the cache off for exactly the
// backends it exists for. That is why the cache-hit return above is NOT
// wrapped, while the fill that populates the cache (Prepare) IS: the fill is
// the one read that really does come off the driver.
func (s *Source) Open(ctx context.Context) (io.ReadCloser, error) {
	if !s.Staged {
		if e, ok := s.acquire(ctx); ok {
			return e.Reader()
		}
		rc, err := s.drv.Read(ctx, s.rel)
		if err != nil {
			return nil, err
		}
		return throughput.CountingReader(rc, s.storageID, throughput.Read), nil
	}
	rd, err := s.area.Open(s.UploadID)
	if err != nil {
		return nil, fmt.Errorf("%w: staging %s: %v", ErrStagingGone, s.UploadID, err)
	}
	return rd, nil
}

// CanRange reports whether ReadRange will work. Staging always can — the
// assembled staging reader is a seekable io.ReadSeekCloser across part
// boundaries — so a file being transferred is seekable in a video player and
// resumable in a downloader exactly like a stored one. So can a prepared local
// copy, which is how a slow backend that cannot range gains seeking.
func (s *Source) CanRange() bool {
	if s.Staged {
		return true
	}
	if _, ok := s.drv.(storage.RangeReader); ok {
		return true
	}
	// A prepared local copy can range even when the driver cannot. Only the
	// ALREADY-KNOWN key is consulted: CanRange has no context to spend on a
	// catalogue lookup, and every path that can reach a cache entry has been
	// through Prepare, Status, Open or ReadRange first — each of which
	// computes and memoises it.
	//
	// Ready() takes a short soft pin, so the entry that made this answer true
	// cannot be evicted before the ReadRange that follows it.
	return s.cacheKnown && s.cacheKey != "" && s.cache().Ready(s.cacheKey)
}

// ReadRange returns a window of the body, with the same contract as
// storage.RangeReader: off >= 0, length < 0 means "to the end", length == 0
// yields an immediate EOF, an offset at or past EOF is EOF rather than an
// error, and a length past EOF is clamped.
func (s *Source) ReadRange(ctx context.Context, off, length int64) (io.ReadCloser, error) {
	if off < 0 {
		return nil, fmt.Errorf("filebody: negative offset %d", off)
	}
	if !s.Staged {
		if e, ok := s.acquire(ctx); ok {
			return e.RangeReader(off, length)
		}
		rr, ok := s.drv.(storage.RangeReader)
		if !ok {
			return nil, storage.ErrUnsupported
		}
		rc, err := rr.ReadRange(ctx, s.rel, off, length)
		if err != nil {
			return nil, err
		}
		return throughput.CountingReader(rc, s.storageID, throughput.Read), nil
	}
	if length == 0 {
		return storage.EmptyReadCloser(), nil
	}
	rd, err := s.area.Open(s.UploadID)
	if err != nil {
		return nil, fmt.Errorf("%w: staging %s: %v", ErrStagingGone, s.UploadID, err)
	}
	if off >= rd.Size() {
		_ = rd.Close()
		return storage.EmptyReadCloser(), nil
	}
	if _, err := rd.Seek(off, io.SeekStart); err != nil {
		_ = rd.Close()
		return nil, err
	}
	return storage.LimitReadCloser(rd, length), nil
}

// ---------------------------------------------------------------------------
// The local cache for big files on slow storage (internal/filecache).
//
// Two halves, deliberately split:
//
//   - INVISIBLE: Open and ReadRange come off the prepared local copy whenever
//     there is one. Every read surface in the tree already goes through this
//     package, so every one of them gets it — the app download, share links,
//     the browse page, thumbnails, WebDAV, the archive and AI endpoints — with
//     no per-surface code. That is the standing rule, not a convenience.
//
//   - VISIBLE: Prepare, which starts the fetch and reports progress. It is
//     opt-in per surface because only the surface knows whether its client can
//     be told "not yet": a browser can be shown a preparing page, an XHR can be
//     given 202, and a public share link — which has already spent one of its
//     capped downloads by the time it reads bytes — can be told neither, so it
//     never asks.
// ---------------------------------------------------------------------------

// Prep is what the cache is doing for one file. A nil *Prep means "serve this
// the way you always did".
type Prep struct {
	// Ready means the local copy exists; just serve, it will come off disk.
	Ready bool
	// Percent is fetch progress, 0..99, meaningful only while !Ready.
	Percent int
	// Size is the file's length in bytes, so a caller can say how big the
	// thing being prepared is.
	Size int64
}

// Prepare decides whether this file should be served from a locally prepared
// copy and, if it should, makes sure one is being made.
//
// nil means the file does not qualify (too small, storage not slow, bytes
// still in staging, caching off, or the cache has no room) — the caller
// serves exactly as it did before this existed. A *Prep with Ready=false is
// the answer the owner asked for: tell the user we are preparing, show a
// percentage, and start the download when it is ready.
//
// `stat` is the live driver description the caller already holds; passing it
// avoids a second round trip to the backend, which on a slow backend is the
// exact cost this feature exists to remove.
//
// ⚠ A cache failure is never a download failure. Anything that goes wrong
// here answers nil and the request streams from the driver.
func (s *Source) Prepare(ctx context.Context, stat storage.Object) *Prep {
	c := s.cache()
	if c == nil || s.Staged || stat.Kind == storage.KindDirectory {
		return nil
	}
	if !c.Qualifies(s.storageID, stat.Size, s.res.operatorSlow(ctx, s.storageID)) {
		return nil
	}
	key := s.remember(s.keyFromStat(stat))
	if key == "" {
		return nil
	}
	if c.Ready(key) {
		return &Prep{Ready: true, Size: stat.Size}
	}
	// The fill is a driver read like any other, so it is measured like any
	// other — and it is the single best sample the meter ever gets, because
	// nothing paces it but the backend. It is observed exactly ONCE: the fill
	// callback runs at most once per key (StartOrGet coalesces concurrent
	// askers onto one Fill) and filecache closes this reader once, which is
	// when CountingReader records. Every other read of the same file after
	// this comes off local disk and is deliberately not counted.
	fill, err := c.StartOrGet(ctx, key, stat.Size, func(fctx context.Context) (io.ReadCloser, error) {
		rc, err := s.drv.Read(fctx, s.rel)
		if err != nil {
			return nil, err
		}
		return throughput.CountingReader(rc, s.storageID, throughput.Read), nil
	})
	if err != nil || fill == nil {
		return nil
	}
	if fill.Done() {
		if fill.Err() != nil {
			return nil
		}
		return &Prep{Ready: true, Size: stat.Size}
	}
	return &Prep{Ready: false, Percent: fill.Percent(), Size: stat.Size}
}

// Status reports on a prepared copy WITHOUT starting one. It is what a
// progress poll asks: a poll that could start work would let anyone warm the
// cache for any file they can see, and would restart a fetch that had just
// failed, once per poll.
//
// nil means "nothing is being prepared" — for a poller that is the signal to
// go ahead and ask for the file.
func (s *Source) Status(ctx context.Context, stat storage.Object) *Prep {
	c := s.cache()
	if c == nil || s.Staged {
		return nil
	}
	key := s.remember(s.keyFromStat(stat))
	if key == "" {
		return nil
	}
	if c.Ready(key) {
		return &Prep{Ready: true, Size: stat.Size}
	}
	if f, ok := c.Filling(key); ok {
		return &Prep{Ready: false, Percent: f.Percent(), Size: stat.Size}
	}
	return nil
}

// cache returns the attached cache, or nil when there is none to consult.
func (s *Source) cache() *filecache.Cache {
	if s == nil || s.res == nil || s.res.cache == nil || !s.res.cache.Enabled() {
		return nil
	}
	return s.res.cache
}

// keyFromStat builds the cache key from a live driver description. It is the
// authoritative form: the key carries the content signature, so a file that
// changed on the backend gets a different key and cannot be answered from a
// stale copy.
func (s *Source) keyFromStat(stat storage.Object) string {
	if s.node == nil || s.node.ID <= 0 {
		return ""
	}
	return filecache.Key(s.node.ID, filecache.Signature(stat.Etag, stat.Size, stat.Mtime))
}

// remember memoises a key computed from a stat the caller already had, so the
// rest of the request reuses it instead of asking the backend again.
func (s *Source) remember(key string) string {
	s.cacheKnown, s.cacheKey = true, key
	return key
}

// key is the lazy form used by the invisible half, where the caller has not
// stat'ed anything. It is computed at most once per Source and only for files
// the catalogue already says are big enough to qualify on a slow storage —
// so the extra Stat it costs is paid on the requests where a cache hit is
// worth an order of magnitude more, and never on the small ones.
func (s *Source) key(ctx context.Context) string {
	if s.cacheKnown {
		return s.cacheKey
	}
	s.cacheKnown = true
	c := s.cache()
	if c == nil || s.Staged || s.node == nil || s.node.ID <= 0 {
		return ""
	}
	if !c.Qualifies(s.storageID, s.node.Size, s.res.operatorSlow(ctx, s.storageID)) {
		return ""
	}
	stat, err := s.drv.Stat(ctx, s.rel)
	if err != nil {
		return ""
	}
	s.cacheKey = s.keyFromStat(stat)
	return s.cacheKey
}

// acquire pins a ready cache entry for this file, if there is one.
func (s *Source) acquire(ctx context.Context) (*filecache.Entry, bool) {
	c := s.cache()
	if c == nil {
		return nil, false
	}
	return c.Acquire(s.key(ctx))
}

// stagedBytes reports whether a node's bytes live in the staging area rather
// than on the storage driver.
func stagedBytes(state string) bool {
	return state == model.TransferStateStaged || state == model.TransferStateFailed
}
