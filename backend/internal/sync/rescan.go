package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// FolderRescan is what one folder rescan did (Worker.RescanFolder).
type FolderRescan struct {
	Path       string `json:"path"`
	Scanned    int    `json:"scanned"`
	Added      int    `json:"added"`
	Updated    int    `json:"updated"`
	Removed    int    `json:"removed"`
	Reconciled int    `json:"reconciled"`
	// RemovalSkipped says why nothing was moved to the trash when nothing
	// could be: part of the listing failed, or the 70% guard tripped.
	RemovalSkipped string `json:"removal_skipped,omitempty"`
}

var (
	// ErrScopeInvalid refuses a folder a rescan cannot be pointed at: one that
	// climbs out with "..", or one of filex's own trees.
	ErrScopeInvalid = errors.New("sync: not a folder that can be rescanned")
	// ErrFolderNotCatalogued is a folder the catalogue has no row for. A
	// rescan starts from the folder's row; rescan the folder above it, or run
	// a full scan.
	ErrFolderNotCatalogued = errors.New("sync: the folder is not in the catalogue")
	// ErrNotAFolder is a path whose row is a file.
	ErrNotAFolder = errors.New("sync: the path is a file, not a folder")
)

// ScopePath normalises the folder a rescan was asked for, as a storage path
// with a leading slash. "" — for "", "/" and anything that cleans to the root
// — means the whole storage, i.e. an ordinary full scan.
//
// ".." is refused rather than cleaned: a request that climbs is not the
// folder it names, whatever it resolves to. So is anything inside filex's own
// trees (syspath.Sealed), which the walk never catalogues.
func ScopePath(raw string) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("%w: the path contains a NUL byte", ErrScopeInvalid)
	}
	for _, seg := range strings.Split(strings.ReplaceAll(raw, `\`, "/"), "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: %q climbs out with \"..\"", ErrScopeInvalid, raw)
		}
	}
	clean := path.Clean("/" + raw)
	if clean == "/" {
		return "", nil
	}
	if syspath.Sealed(clean) {
		return "", fmt.Errorf("%w: %s is filex's own bookkeeping, not a folder of the storage", ErrScopeInvalid, clean)
	}
	return clean, nil
}

// RescanFolder rescans one catalogued folder's subtree of a storage instead
// of the whole storage.
//
// A full scan is the only other way to make the catalogue look at a storage
// again, and it scales with the storage: 169k rows took 23 minutes and
// rewrote seen_at on every one of them, to learn about one folder. This runs
// the same walk over the folder alone — the same drift update, the same
// staged-upload settle, the same tombstone rules — with three differences
// that keep it a folder's business:
//
//   - the tombstone candidates are bounded by the folder's exact prefix, and
//     the 70% guard compares what the listing saw with the folder's OWN live
//     row count, not the storage's last run;
//   - a listing that failed part-way removes nothing (see walkCounts.partial);
//   - no sync_runs row, and the storage's last-synced time does not move: a
//     folder rescan is not a sync of the storage.
//
// It shares the storage's one-run lock: ErrRunInProgress while any run walks.
func (w *Worker) RescanFolder(ctx context.Context, storageID int64, dir string) (FolderRescan, error) {
	clean, err := ScopePath(dir)
	if err != nil {
		return FolderRescan{Path: dir}, err
	}
	if clean == "" {
		return FolderRescan{Path: "/"}, fmt.Errorf("%w: the storage root is a full scan", ErrScopeInvalid)
	}
	w.mu.Lock()
	syncer, ok := w.syncers[storageID]
	w.mu.Unlock()
	if !ok {
		return FolderRescan{Path: clean}, errors.New("sync: no syncer for storage")
	}
	// A folder the storage's scan exclusions cover is not the scan's to look
	// at, one folder at a time or otherwise (issue #44).
	if syncer.rule.Excluded(clean) {
		return FolderRescan{Path: clean}, fmt.Errorf("%w: %s is excluded from scanning on this storage", ErrScopeInvalid, clean)
	}
	return syncer.rescanFolder(ctx, clean)
}

func (s *storageSyncer) rescanFolder(ctx context.Context, dir string) (FolderRescan, error) {
	res := FolderRescan{Path: dir}
	if !s.runMu.TryLock() {
		return res, ErrRunInProgress
	}
	defer s.runMu.Unlock()
	s.inFlight.Store(true)
	defer s.inFlight.Store(false)
	// Finding a file is not putting it there — see RunOnce.
	ctx = quotastore.WithActor(quotastore.WithOwner(ctx, 0), 0)

	folder, err := s.store.GetNodeByPath(ctx, s.storage.ID, pathkey.Hash(s.storage.ID, dir))
	switch {
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return res, err
	case err != nil || folder == nil:
		return res, fmt.Errorf("%w: %s", ErrFolderNotCatalogued, dir)
	case folder.Type != model.NodeTypeDirectory:
		return res, fmt.Errorf("%w: %s", ErrNotAFolder, dir)
	}

	// Second precision, like RunOnce: seen_at is CURRENT_TIMESTAMP.
	runStart := time.Now().Truncate(time.Second)
	baseline, err := s.store.CountLiveNodesUnder(ctx, s.storage.ID, dir)
	if err != nil {
		return res, err
	}
	list := dirLister(s.driver.List)
	if idx, ok := s.prefetchTree(ctx, dir); ok {
		list = idx.list
	}
	c := &walkCounts{}
	// The depth the full scan would be at here, so the rescan stops where it
	// stops (storage.MaxWalkDepth) and catalogues nothing it would not.
	depth := len(strings.Split(strings.Trim(dir, "/"), "/"))
	seen, err := s.walk(ctx, dir, &folder.ID, c, list, storage.NewCycleGuard(), depth)
	res.Scanned, res.Added, res.Updated, res.Reconciled = seen, c.added, c.updated, c.reconciled
	if err == nil {
		// The walk carries on past a subfolder whose walk was cancelled, so a
		// context that ran out in the last one reaches here as "no error".
		err = ctx.Err()
	}
	if err != nil {
		return res, err
	}

	stale, err := s.store.ListStaleNodesUnder(ctx, s.storage.ID, dir, runStart)
	if err != nil {
		return res, err
	}
	// ⚠ The baseline counts every live row under the folder, and the rows the
	// walk does not enter — a subtree catalogued before a scan exclusion
	// covered it — are never seen, on this rescan or any other. Left in, a
	// big one (a `.git` beside a few documents) would trip the guard on every
	// rescan of that folder, for good, and nothing deleted there would ever
	// leave the catalogue.
	walkable := int(baseline)
	for _, n := range stale {
		if s.rule.Skips(n.Path) {
			walkable--
		}
	}
	switch {
	case c.partial:
		// ⚠ Everything under a subfolder the walk could not look into looks
		// unseen, and for a FOLDER row the tombstone pass has no object to
		// Stat: it would go to the trash, where purging it deletes the folder
		// on the backend.
		res.RemovalSkipped = "part of the folder could not be listed; nothing was removed"
	case !guardOK(seen, walkable):
		res.RemovalSkipped = fmt.Sprintf("the listing saw %d of the %d entries the catalogue holds below this folder, under 70%%; nothing was removed", seen, walkable)
	default:
		res.Removed = s.tombstone(ctx, stale)
	}
	if res.RemovalSkipped != "" {
		slog.Warn("sync: folder rescan removed nothing",
			slog.String("storage", s.storage.Name), slog.String("path", dir), slog.String("why", res.RemovalSkipped))
	}
	// The folder's size, and every ancestor's, may have changed.
	if err := RecomputeFolderSizes(ctx, s.store, s.storage.ID); err != nil {
		slog.Warn("sync: folder-size recompute",
			slog.String("storage", s.storage.Name), slog.String("err", err.Error()))
	}
	slog.Info("sync: folder rescanned",
		slog.String("storage", s.storage.Name),
		slog.String("path", dir),
		slog.Int("scanned", res.Scanned),
		slog.Int("added", res.Added),
		slog.Int("updated", res.Updated),
		slog.Int("removed", res.Removed),
		slog.Int("reconciled", res.Reconciled))
	return res, ctx.Err()
}
