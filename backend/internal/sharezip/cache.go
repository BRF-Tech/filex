// Package sharezip caches the ZIP archive produced when a shared FOLDER is
// downloaded ("download all"). Without a cache, every download re-walks the
// folder and re-reads + re-compresses every file from object storage — slow for
// large folders (e.g. a receipt month with hundreds of images). The cache is
// keyed by node id + a content signature (file set + sizes + mtimes), so any
// change to the folder invalidates it and the next request (or the background
// warmer) regenerates.
//
// A small in-memory generation registry deduplicates concurrent builds: if a
// zip for a given signature is already being generated (by another download or
// by the warmer), other callers attach to the same job and can watch its
// progress instead of kicking off a second build.
//
// # Nothing outlives its share
//
// A cached archive exists to serve one folder share. When that share expires,
// is revoked or runs out of downloads, the archive is garbage — regenerable
// garbage — and Sweep deletes it on the warmer's next pass. A build in flight
// for a share that dies mid-build stops and takes its partial file with it
// (shareGone). Both read the same view of active shares, ActiveShares, so
// "active" means one thing in this package.
package sharezip

import (
	"archive/zip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// ErrShareGone ends a build whose folder share stopped existing while it ran.
// Callers treat it as "no cached archive" — there is nobody left to serve.
var ErrShareGone = errors.New("sharezip: build abandoned, the share is no longer active")

// activeCheckInterval throttles the "is this share still there?" check a
// running build makes. The check is a mutex and a map lookup — no query, no
// listing — so this is only here to keep it off the per-chunk path. A var so
// tests can shrink it; nothing else writes it.
var activeCheckInterval = 5 * time.Second

const (
	// tmpMaxAge is how long an unclaimed .tmp- file is given before the
	// sweeper treats it as the wreckage of a build that died with its
	// process. Builds still running hold their temp file by name and are
	// never swept, whatever their age.
	tmpMaxAge = time.Hour

	tmpPrefix = ".tmp-"
)

// cachedZipName matches a published archive: <nodeID>-<signature>.zip. The
// sweeper deletes only names it matches, so a cache directory that happens to
// hold something else loses nothing.
var cachedZipName = regexp.MustCompile(`^(\d+)-[0-9a-f]{16}\.zip$`)

// File is one file collected from a folder walk (metadata only — no bytes read
// yet). Used to compute the cache signature and, on a miss, to build the zip.
type File struct {
	Path  string // driver path (for Read)
	Rel   string // path inside the zip
	Size  int64
	Mtime time.Time
}

// Gen tracks one in-flight (or just-finished) zip build so concurrent callers
// can dedup and watch progress.
type Gen struct {
	Total    int
	done     atomic.Int64
	finished chan struct{}
	err      error
}

// Percent returns build progress 0..100. It is capped at 99 while building —
// "100" is reserved for a finished file on disk (checked via Cache.Cached), so
// a poller only sees 100 once the archive is actually downloadable.
func (g *Gen) Percent() int {
	if g.Total <= 0 {
		return 99
	}
	p := int(g.done.Load() * 100 / int64(g.Total))
	if p > 99 {
		p = 99
	}
	if p < 0 {
		p = 0
	}
	return p
}

// Wait blocks until the build finishes (returns its error) or ctx is cancelled.
func (g *Gen) Wait(ctx context.Context) error {
	select {
	case <-g.finished:
		return g.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Cache is a local-disk cache of folder-share ZIPs plus a generation registry.
// The zero value is unusable; construct with New.
type Cache struct {
	dir  string
	mu   sync.Mutex
	gens map[string]*Gen
	// tmps are the temp files of builds running right now, held by name so
	// the sweeper cannot delete a build out from under itself.
	tmps map[string]struct{}
	// active is the shared view of live folder shares (see ActiveShares).
	// nil until a Warmer wires one up, and everything that reads it treats
	// nil as "I know nothing", which deletes nothing and abandons nothing.
	active *ActiveShares
}

// New returns a Cache rooted at dir. An empty dir disables caching (Enabled
// reports false and callers fall back to streaming).
func New(dir string) *Cache {
	return &Cache{dir: dir, gens: map[string]*Gen{}, tmps: map[string]struct{}{}}
}

// Track wires the cache to the view of active folder shares. Both things that
// need to know whether a share is alive read it from here: the sweeper (what
// may be deleted) and a running build (whether to give up). NewWarmer calls
// this; a cache with no warmer keeps its pre-sweeper behaviour.
func (c *Cache) Track(a *ActiveShares) {
	c.mu.Lock()
	c.active = a
	c.mu.Unlock()
}

// Enabled reports whether caching is configured.
func (c *Cache) Enabled() bool { return c.dir != "" }

// Plan walks the folder and returns the cache path it would occupy plus the
// file list (no generation, no bytes read). The cache path is
// <dir>/<nodeID>-<sig>.zip.
func (c *Cache) Plan(ctx context.Context, drv storage.Driver, root string, nodeID int64) (string, []File, error) {
	files, err := collectFiles(ctx, drv, root)
	if err != nil {
		return "", nil, err
	}
	cachePath := filepath.Join(c.dir, fmt.Sprintf("%d-%s.zip", nodeID, signature(files)))
	return cachePath, files, nil
}

// Cached reports whether a finished archive exists at cachePath.
func (c *Cache) Cached(cachePath string) (os.FileInfo, bool) {
	fi, err := os.Stat(cachePath)
	if err != nil || fi.IsDir() {
		return nil, false
	}
	return fi, true
}

// StartOrGet returns a generation for cachePath, starting one if none is
// running. If the archive already exists on disk it returns an
// instantly-finished generation (so Wait returns immediately).
func (c *Cache) StartOrGet(cachePath string, files []File, nodeID int64, drv storage.Driver) *Gen {
	c.mu.Lock()
	defer c.mu.Unlock()

	if g, ok := c.gens[cachePath]; ok {
		return g
	}
	if _, err := os.Stat(cachePath); err == nil {
		g := &Gen{Total: len(files), finished: make(chan struct{})}
		g.done.Store(int64(len(files)))
		close(g.finished)
		return g
	}
	g := &Gen{Total: len(files), finished: make(chan struct{})}
	c.gens[cachePath] = g
	go c.run(cachePath, files, nodeID, drv, g)
	return g
}

// Warm ensures a fresh cached archive exists for a folder, generating it if
// missing. Blocks until the (possibly already-running) build completes. The
// bool reports whether a (re)generation was needed (false = cache already
// fresh). Used by the background warmer. A no-op when caching is disabled.
func (c *Cache) Warm(ctx context.Context, drv storage.Driver, root string, nodeID int64) (bool, error) {
	if !c.Enabled() {
		return false, nil
	}
	cachePath, files, err := c.Plan(ctx, drv, root, nodeID)
	if err != nil {
		return false, err
	}
	if _, ok := c.Cached(cachePath); ok {
		return false, nil
	}
	return true, c.StartOrGet(cachePath, files, nodeID, drv).Wait(ctx)
}

// run builds the archive into a temp file then publishes it atomically. The
// build uses context.Background so a disconnecting downloader never aborts a
// generation others may be waiting on — but it does stop when the share it is
// building for stops existing (see shareGone).
func (c *Cache) run(cachePath string, files []File, nodeID int64, drv storage.Driver, g *Gen) {
	startedAt := time.Now()
	defer func() {
		close(g.finished)
		c.mu.Lock()
		if c.gens[cachePath] == g {
			delete(c.gens, cachePath)
		}
		c.mu.Unlock()
	}()

	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		g.err = err
		return
	}
	tmp, err := os.CreateTemp(c.dir, tmpPrefix+"*.zip")
	if err != nil {
		g.err = err
		return
	}
	tmpName := tmp.Name()
	c.holdTmp(tmpName)
	defer c.releaseTmp(tmpName)

	check := func() error {
		if c.shareGone(nodeID, startedAt) {
			return ErrShareGone
		}
		return nil
	}
	if err := writeZip(context.Background(), tmp, drv, files, &g.done, check); err != nil {
		// Every failure path, abandonment included, takes the partial file
		// with it: nothing bounds how big it got, so leaving one behind is
		// how a disk fills.
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		g.err = err
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		g.err = err
		return
	}
	if err := os.Rename(tmpName, cachePath); err != nil {
		_ = os.Remove(tmpName)
		g.err = err
		return
	}
	pruneOld(c.dir, nodeID, cachePath)
}

// collectFiles walks root and returns every file under it (metadata only).
// Internal dirs (trash, thumbnails, keepdir) are skipped so the archive matches
// what the streaming path would produce.
func collectFiles(ctx context.Context, drv storage.Driver, root string) ([]File, error) {
	var out []File
	var walk func(dir, prefix string) error
	walk = func(dir, prefix string) error {
		objs, err := drv.List(ctx, dir)
		if err != nil {
			return err
		}
		for _, o := range objs {
			if o.Name == ".filex-trash" || o.Name == ".thumbs" || o.Name == ".keepdir" {
				continue
			}
			entry := prefix + o.Name
			switch o.Kind {
			case storage.KindDirectory:
				if err := walk(o.Path, entry+"/"); err != nil {
					return err
				}
			case storage.KindFile:
				out = append(out, File{Path: o.Path, Rel: entry, Size: o.Size, Mtime: o.Mtime})
			}
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return nil, err
	}
	return out, nil
}

// signature is a content hash over the file set (sorted rel path + size +
// mtime). Any add/delete/replace changes it, which invalidates the cache.
func signature(files []File) string {
	sorted := make([]File, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Rel < sorted[j].Rel })
	sum := md5.New()
	for _, f := range sorted {
		fmt.Fprintf(sum, "%s\x00%d\x00%d\n", f.Rel, f.Size, f.Mtime.Unix())
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

// writeZip builds the archive into out, reading each file from the driver and
// bumping done after each. Individually unreadable files are skipped (not
// fatal), matching the streaming path's tolerance.
//
// check is asked, at most every activeCheckInterval, whether the build should
// still be running; its error aborts the build. It is consulted both between
// files and DURING a file's copy, because "between files" is no bound at all
// on a folder whose one file is 15 GB.
func writeZip(ctx context.Context, out io.Writer, drv storage.Driver, files []File, done *atomic.Int64, check func() error) error {
	g := &checkGate{check: check, last: time.Now()}
	zw := zip.NewWriter(out)
	for _, f := range files {
		if err := g.due(); err != nil {
			_ = zw.Close()
			return err
		}
		rc, err := drv.Read(ctx, f.Path)
		if err != nil {
			done.Add(1)
			continue
		}
		fw, cErr := zw.Create(f.Rel)
		if cErr != nil {
			_ = rc.Close()
			_ = zw.Close()
			return cErr
		}
		if _, cpErr := io.Copy(fw, &gatedReader{r: rc, gate: g}); cpErr != nil {
			_ = rc.Close()
			_ = zw.Close()
			return cpErr
		}
		_ = rc.Close()
		done.Add(1)
	}
	return zw.Close()
}

// checkGate runs check at most once per activeCheckInterval.
type checkGate struct {
	check func() error
	last  time.Time
}

func (g *checkGate) due() error {
	if g == nil || g.check == nil {
		return nil
	}
	now := time.Now()
	if now.Sub(g.last) < activeCheckInterval {
		return nil
	}
	g.last = now
	return g.check()
}

// gatedReader lets the gate interrupt a single long copy. Returning the error
// from Read is what stops io.Copy, and the caller then discards the partial
// temp file.
type gatedReader struct {
	r    io.Reader
	gate *checkGate
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if err := r.gate.due(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// shareGone reports whether nodeID's folder share has been observed to be gone
// by a view of the shares taken AFTER this build started.
//
// ⚠ This is NOT a size limit and must never become one. It refuses no folder
// and changes nothing a visitor can download: a live share is built however
// large it is, and the on-demand path is untouched. All it does is stop
// working for something that has ceased to exist — the incident behind it was
// a 16.7 GB folder shared for eleven minutes whose ZIP went on building for
// three hours after the link had died, then sat on disk forever. Please do not
// "restore symmetry" by adding a byte ceiling next to it; the owner considered
// one and rejected it.
//
// The "after this build started" clause is what protects a brand-new share: a
// build kicked off on demand seconds after a link is minted must not be killed
// by a view of the world that predates the link.
func (c *Cache) shareGone(nodeID int64, startedAt time.Time) bool {
	c.mu.Lock()
	a := c.active
	c.mu.Unlock()
	if a == nil {
		return false
	}
	nodes, at, ok := a.Snapshot()
	if !ok || !at.After(startedAt) {
		return false
	}
	_, live := nodes[nodeID]
	return !live
}

func (c *Cache) holdTmp(name string) {
	c.mu.Lock()
	c.tmps[name] = struct{}{}
	c.mu.Unlock()
}

func (c *Cache) releaseTmp(name string) {
	c.mu.Lock()
	delete(c.tmps, name)
	c.mu.Unlock()
}

// busy reports whether path is a temp file or a target of a build running now.
func (c *Cache) busy(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.tmps[path]; ok {
		return true
	}
	_, ok := c.gens[path]
	return ok
}

// Sweep deletes cached archives that no live folder share can serve, and the
// wreckage of builds that never finished. It is the answer to a cache that only
// ever grew: pruneOld drops OLD SIGNATURES OF A NODE THAT IS STILL SHARED, so
// when a share expires its archive was simply immortal — one such file was
// 15 GB, was never downloaded once, and rode into three backups.
//
// It deletes exactly two things:
//
//   - <nodeID>-<sig>.zip whose node has no active folder share. "Active" is
//     not re-defined here: it is whatever the shared ActiveShares view says,
//     which is the same query the warmer builds from. Two definitions of
//     "active" in two places is how a sweeper eventually deletes something
//     that was still being served.
//   - .tmp-*.zip older than tmpMaxAge that no running build claims.
//
// It refuses to touch: any name that is not one of those two shapes (a cache
// directory sharing a disk with something else loses nothing), directories,
// the archive of any node that still has a share (whatever its signature —
// that is pruneOld's job), any file a build is writing or about to publish,
// and everything at all until a share listing has actually succeeded once. A
// file it cannot delete (Windows will not unlink a file a download is
// streaming) is left for the next pass.
func (c *Cache) Sweep() (removed int, freed int64) {
	if !c.Enabled() {
		return 0, 0
	}
	c.mu.Lock()
	a := c.active
	c.mu.Unlock()
	if a == nil {
		return 0, 0
	}
	nodes, _, ok := a.Snapshot()
	if !ok {
		// No successful listing yet. An empty set here would read as "no
		// share exists" and delete the whole cache.
		return 0, 0
	}
	ents, err := os.ReadDir(c.dir)
	if err != nil {
		return 0, 0
	}
	now := time.Now()
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		path := filepath.Join(c.dir, name)
		fi, ierr := e.Info()
		if ierr != nil {
			continue
		}
		switch {
		case strings.HasPrefix(name, tmpPrefix):
			if c.busy(path) || now.Sub(fi.ModTime()) < tmpMaxAge {
				continue
			}
		default:
			m := cachedZipName.FindStringSubmatch(name)
			if m == nil {
				continue // not ours
			}
			id, perr := strconv.ParseInt(m[1], 10, 64)
			if perr != nil {
				continue
			}
			if _, live := nodes[id]; live {
				continue
			}
			if c.busy(path) {
				continue
			}
		}
		if err := os.Remove(path); err != nil {
			continue
		}
		removed++
		freed += fi.Size()
	}
	return removed, freed
}

// pruneOld removes stale cached zips for a node (previous signatures) once a
// fresh one is published, so the cache doesn't accumulate one file per edit.
func pruneOld(dir string, nodeID int64, keep string) {
	matches, _ := filepath.Glob(filepath.Join(dir, fmt.Sprintf("%d-*.zip", nodeID)))
	for _, m := range matches {
		if m != keep {
			_ = os.Remove(m)
		}
	}
}
