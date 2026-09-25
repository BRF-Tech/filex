package sync

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// What the rest of filex asks the lazy catalogue (docs/LAZY-CATALOGUE.md):
// the explorer's listing (should this folder come from the disk? catalogue it,
// please), the coverage figures search, folder sizes and drive usage carry,
// the desktop sync's watch registration, and the admin storage page.

// Coverage reasons: why a storage's catalogue is not complete.
const (
	// CoverageFilling: a lazy storage whose background filler has not
	// converged yet (behaviour A).
	CoverageFilling = "lazy_filling"
	// CoverageVisitedOnly: a lazy storage in behaviour B — only the folders
	// people open are catalogued.
	CoverageVisitedOnly = "lazy_on_open"
	// CoverageFirstScan: any storage whose first full scan has not finished.
	CoverageFirstScan = "first_scan"
)

// CatalogueCoverage is how much of a storage its catalogue covers — the
// object the listing, search and drive-usage responses carry, and the admin
// storage page's catalogue block.
type CatalogueCoverage struct {
	// Complete: search, folder sizes and usage cover the whole storage.
	Complete bool `json:"complete"`
	// Reason says why not (CoverageFilling, CoverageVisitedOnly,
	// CoverageFirstScan); empty when complete.
	Reason string `json:"reason,omitempty"`
	// Mode is "lazy" for a lazily catalogued storage, empty otherwise.
	Mode string `json:"mode,omitempty"`
	// Fill is the lazy behaviour: "background" (A) or "on_open" (B).
	Fill string `json:"fill,omitempty"`
	// CataloguedFolders / PendingFolders: folders whose listing has been
	// applied, and folders discovered but not listed yet.
	CataloguedFolders int64 `json:"catalogued_folders"`
	PendingFolders    int64 `json:"pending_folders"`
	// WatchedFolders / MaxWatches: the watch budget, in use and in total.
	WatchedFolders int `json:"watched_folders,omitempty"`
	MaxWatches     int `json:"max_watches,omitempty"`
	// Filler is the background filler's state: filling, paused (somebody is
	// using the storage), idle, converged, refreshing, or off (behaviour B).
	Filler string `json:"filler,omitempty"`
	// HeldBackFolders: folders whose last reconcile refused deletions.
	HeldBackFolders int64 `json:"held_back_folders,omitempty"`
	// Reconciles since the syncer started.
	Reconciles int64 `json:"reconciles,omitempty"`
}

// lazyOf returns the running lazy catalogue of a storage, or nil.
func (w *Worker) lazyOf(storageID int64) *lazyCatalogue {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	syncer, ok := w.syncers[storageID]
	w.mu.Unlock()
	if !ok || syncer == nil {
		return nil
	}
	return syncer.lazy.Load()
}

// AttachEmitter wires the realtime chain the lazy catalogue announces its
// findings through (handlers' emitter: change log → size refresher → hub).
// Safe to call after Start: it is read at announce time.
func (w *Worker) AttachEmitter(e ChangeEmitter) {
	if w == nil {
		return
	}
	w.emit.Store(&emitterBox{e: e})
}

type emitterBox struct{ e ChangeEmitter }

// AttachPairRoots wires where the lazy catalogue learns which folders desktop
// sync pairs mirror right now (realtime.Hub.WatchedRoots: the roots of every
// live recursive watch on a storage). Read at refresh time, like the emitter.
func (w *Worker) AttachPairRoots(f func(storageID int64) []string) {
	if w == nil {
		return
	}
	w.pairRoots.Store(&pairRootsBox{f: f})
}

type pairRootsBox struct{ f func(int64) []string }

func (w *Worker) currentPairRoots(storageID int64) []string {
	if b, ok := w.pairRoots.Load().(*pairRootsBox); ok && b != nil && b.f != nil {
		return b.f(storageID)
	}
	return nil
}

func (w *Worker) currentEmitter() ChangeEmitter {
	if b, ok := w.emit.Load().(*emitterBox); ok && b != nil {
		return b.e
	}
	return nil
}

// CatalogueOpened tells a lazy storage that somebody listed dir: a watched,
// current folder only moves up the watch LRU; anything else is queued for a
// reconcile ahead of the background filler. Never blocks. A no-op on any
// other storage.
func (w *Worker) CatalogueOpened(storageID int64, dir string) {
	if lc := w.lazyOf(storageID); lc != nil {
		lc.opened(context.Background(), dir)
	}
}

// NoteActivity tells a lazy storage's filler that somebody is using the
// storage (a search, say), so it slows down.
func (w *Worker) NoteActivity(storageID int64) {
	if lc := w.lazyOf(storageID); lc != nil {
		lc.noteActivity()
	}
}

// CatalogueCurrent reports whether storageID is lazy and, if so, whether the
// catalogue vouches for dir right now (watched, nothing held back). The
// explorer lists a lazy folder that is not current from the disk.
func (w *Worker) CatalogueCurrent(storageID int64, dir string) (lazy, current bool) {
	lc := w.lazyOf(storageID)
	if lc == nil {
		return false, false
	}
	return true, lc.current(dir)
}

// CatalogueSubtree makes sure every folder under dir of a lazy storage gets
// catalogued — a desktop sync pair's subtree (see lazyCatalogue.subtree).
func (w *Worker) CatalogueSubtree(storageID int64, dir string) {
	lc := w.lazyOf(storageID)
	if lc == nil {
		return
	}
	lc.subtree(lc.s.ctx, dir)
}

// CatalogueCoverage describes how much of st its catalogue covers, or nil
// when it covers everything. A lazy storage answers from its per-folder
// state; any other storage is incomplete only while its first scan has not
// finished.
func (w *Worker) CatalogueCoverage(ctx context.Context, st *model.Storage) *CatalogueCoverage {
	if st == nil {
		return nil
	}
	if lc := w.lazyOf(st.ID); lc != nil {
		cov := lc.coverage(ctx)
		if cov.Complete {
			return nil
		}
		return cov
	}
	if st.SyncMode == model.SyncModeLazy {
		// Configured lazy but no engine running (disabled, or not local):
		// nothing is filling it.
		return nil
	}
	if st.LastSyncAt == nil {
		return &CatalogueCoverage{Complete: false, Reason: CoverageFirstScan}
	}
	return nil
}

// CatalogueStatus is the admin storage page's catalogue block: the lazy
// engine's full state, complete or not. ok is false for a storage that is
// not running lazily.
func (w *Worker) CatalogueStatus(ctx context.Context, storageID int64) (*CatalogueCoverage, bool) {
	lc := w.lazyOf(storageID)
	if lc == nil {
		return nil, false
	}
	return lc.coverage(ctx), true
}

// CatalogueSizePartial reports, for folders of a lazy storage, whether each
// one's catalogued size leaves something out (see lazyCatalogue.sizePartial).
// Keys are canonical folder paths ("/a/b"). Nil for any other storage.
func (w *Worker) CatalogueSizePartial(ctx context.Context, storageID int64, dirs []string) map[string]bool {
	lc := w.lazyOf(storageID)
	if lc == nil {
		return nil
	}
	return lc.sizePartial(ctx, dirs, 500)
}

// dirListed is the walk's report of one directory it listed completely. On a
// lazy storage a full scan (RunOnce) records every such folder as catalogued,
// so a manual "Scan now" leaves the per-folder state as complete as the
// catalogue it just rebuilt. A no-op for every other walk.
func (s *storageSyncer) dirListed(ctx context.Context, p string, entries int) {
	if !s.recordFolders {
		return
	}
	dir := db.CatalogueFolderPath(p)
	hash := pathkey.Hash(s.storage.ID, dir)
	now := time.Now().UTC().Truncate(time.Second)
	rec := &model.CatalogueFolder{
		StorageID: s.storage.ID, PathHash: hash, Path: dir, Depth: db.CatalogueFolderDepth(dir),
		State: model.FolderCatalogued, ReconciledAt: &now, Entries: entries,
	}
	if prior, err := s.store.GetCatalogueFolder(ctx, s.storage.ID, hash); err == nil && prior != nil {
		rec.VisitedAt = prior.VisitedAt
		if lc := s.lazy.Load(); lc != nil && lc.watch.has(dir) {
			rec.State, rec.WatchedAt = model.FolderWatched, prior.WatchedAt
		}
	}
	_ = s.store.RecordCatalogueFolder(ctx, rec)
}

// lazyPointer is the storageSyncer's handle on its running lazy engine.
type lazyPointer = atomic.Pointer[lazyCatalogue]
