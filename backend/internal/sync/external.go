package sync

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
)

// What the catalogue does with a change somebody ELSE saw: a filex-watch
// agent on the machine that owns a storage's disk, reporting through
// handlers/storage_watch.go (docs/FILEX-WATCH.md). The agent is the only
// caller today; nothing here knows about it beyond "a folder changed
// outside filex" and "I may have missed changes under this folder".
//
// The two questions have different answers per mode, which is why they live
// here, beside the modes, and not in the door:
//
//	                      a folder changed            changes may have been missed
//	lazy                  reconcile that one folder   reconcile every WATCHED folder
//	                      (its own watch's path)      under it - the only folders the
//	                                                  catalogue vouches for; any other
//	                                                  is listed from the disk when opened
//	every other mode      rescan the folder's subtree (RescanFolder), from the nearest
//	                      catalogued folder; the root is a full scan, in the background

// ExternalScanCeiling bounds a full scan an external report starts, as it
// bounds "Scan now" (handlers.Storages.TriggerSync).
const ExternalScanCeiling = 6 * time.Hour

// ExternalChanges makes the catalogue look again at dirs of storageID, folders
// whose direct contents changed outside filex. A folder filex's own trees or
// the storage's scan exclusions cover is not looked at, the way the scan does
// not look at it (scanrule).
//
// It returns the folders it could not look at yet because a scan holds the
// storage (ErrRunInProgress); the caller hands them back later. ErrNoSyncer:
// the storage is gone or switched off, and nothing will look at it.
func (w *Worker) ExternalChanges(ctx context.Context, storageID int64, dirs []string) ([]string, error) {
	if w == nil {
		return nil, ErrNoSyncer
	}
	syncer, ok := w.syncer(storageID)
	if !ok {
		return nil, ErrNoSyncer
	}
	todo := syncer.externalScope(dirs)
	if len(todo) == 0 {
		return nil, nil
	}
	if lc := syncer.lazy.Load(); lc != nil {
		for _, d := range todo {
			lc.request(d, reasonWatch)
		}
		return nil, nil
	}
	return w.rescanEach(ctx, storageID, todo)
}

// ExternalResync is ExternalChanges for a reporter that may have MISSED
// changes under dirs ("/" for the whole storage): it just started, it lost
// its connection, its queue ran over, or the operating system would not let
// it watch there. A lazy storage reconciles every folder it currently
// vouches for under dirs; any other storage rescans them, as ExternalChanges.
func (w *Worker) ExternalResync(ctx context.Context, storageID int64, dirs []string) ([]string, error) {
	if w == nil {
		return nil, ErrNoSyncer
	}
	syncer, ok := w.syncer(storageID)
	if !ok {
		return nil, ErrNoSyncer
	}
	todo := syncer.externalScope(dirs)
	if len(todo) == 0 {
		return nil, nil
	}
	if lc := syncer.lazy.Load(); lc != nil {
		seen := map[string]bool{}
		for _, d := range todo {
			for _, watched := range lc.watch.within(d) {
				if !seen[watched] {
					seen[watched] = true
					lc.request(watched, reasonWatch)
				}
			}
		}
		return nil, nil
	}
	return w.rescanEach(ctx, storageID, todo)
}

// externalScope is dirs as canonical folder paths ("/" for the root), less
// what the scan never looks at, deduplicated.
func (s *storageSyncer) externalScope(dirs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		clean, err := ScopePath(d)
		if err != nil {
			continue
		}
		if clean != "" && s.rule.Skips(clean) {
			continue
		}
		if clean == "" {
			clean = "/"
		}
		if !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	return out
}

// rescanEach rescans each folder's subtree. A folder inside another one on the
// list is left out: the outer rescan walks it.
func (w *Worker) rescanEach(ctx context.Context, storageID int64, dirs []string) ([]string, error) {
	todo := outermostFolders(dirs)
	for i, d := range todo {
		err := w.rescanUp(ctx, storageID, d)
		switch {
		case err == nil:
		case errors.Is(err, ErrRunInProgress):
			// A scan holds the storage: everything from here on waits for it.
			return append([]string(nil), todo[i:]...), nil
		case errors.Is(err, ErrNoSyncer):
			return nil, err
		case ctx.Err() != nil:
			return append([]string(nil), todo[i:]...), ctx.Err()
		default:
			slog.Warn("sync: a folder reported changed outside filex could not be rescanned",
				slog.Int64("storage", storageID), slog.String("path", d), slog.String("err", err.Error()))
		}
	}
	return nil, nil
}

// rescanUp rescans dir, or the nearest folder above it the catalogue has a
// row for (a folder made outside filex has none yet: its parent's rescan
// finds it). The root is a full scan, started in the background; one that is
// already walking is ErrRunInProgress, so the caller asks again afterwards -
// that scan may have read the root before the change.
func (w *Worker) rescanUp(ctx context.Context, storageID int64, dir string) error {
	for {
		if dir == "/" {
			started, err := w.StartScan(storageID, ExternalScanCeiling)
			if err != nil {
				return err
			}
			if !started {
				return ErrRunInProgress
			}
			return nil
		}
		_, err := w.RescanFolder(ctx, storageID, dir)
		if errors.Is(err, ErrFolderNotCatalogued) || errors.Is(err, ErrNotAFolder) {
			dir = parentFolder(dir)
			continue
		}
		return err
	}
}

// outermostFolders drops every folder inside another one of the list, and
// sorts what is left (the root alone, when it is there).
func outermostFolders(dirs []string) []string {
	sorted := append([]string(nil), dirs...)
	sort.Strings(sorted)
	var out []string
	for _, d := range sorted {
		covered := false
		for _, o := range out {
			if folderWithin(d, o) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, d)
		}
	}
	return out
}

// folderWithin reports whether p is dir or inside it (canonical paths).
func folderWithin(p, dir string) bool {
	return dir == "/" || p == dir || strings.HasPrefix(p, dir+"/")
}

// parentFolder is the folder holding p ("/" for a top-level one).
func parentFolder(p string) string {
	return db.CatalogueFolderPath(p[:max(strings.LastIndexByte(p, '/'), 0)])
}

// within lists the folders under dir (dir included) that hold a watch right
// now: the folders the lazy catalogue vouches for without asking the disk.
func (ws *watchSet) within(dir string) []string {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	var out []string
	for d := range ws.byDir {
		if folderWithin(d, dir) {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}
