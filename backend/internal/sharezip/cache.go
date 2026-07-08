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
package sharezip

import (
	"archive/zip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

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
}

// New returns a Cache rooted at dir. An empty dir disables caching (Enabled
// reports false and callers fall back to streaming).
func New(dir string) *Cache {
	return &Cache{dir: dir, gens: map[string]*Gen{}}
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
// generation others may be waiting on.
func (c *Cache) run(cachePath string, files []File, nodeID int64, drv storage.Driver, g *Gen) {
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
	tmp, err := os.CreateTemp(c.dir, ".tmp-*.zip")
	if err != nil {
		g.err = err
		return
	}
	tmpName := tmp.Name()
	if err := writeZip(context.Background(), tmp, drv, files, &g.done); err != nil {
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
func writeZip(ctx context.Context, out io.Writer, drv storage.Driver, files []File, done *atomic.Int64) error {
	zw := zip.NewWriter(out)
	for _, f := range files {
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
		if _, cpErr := io.Copy(fw, rc); cpErr != nil {
			_ = rc.Close()
			_ = zw.Close()
			return cpErr
		}
		_ = rc.Close()
		done.Add(1)
	}
	return zw.Close()
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
