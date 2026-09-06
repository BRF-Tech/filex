package thumb

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// ─────────────────────── thumbnail cache reclamation ───────────────────────
//
// Every generator writes `<cacheDir>/<node id>.jpg`. Until this file existed
// nothing ever removed one: not a delete, not a purge from the trash, not
// removing the whole storage. The `thumbnails` ROW went away with the node
// (ON DELETE CASCADE) but the JPEG stayed, so a data directory only ever grew
// and an operator watching a podman volume saw exactly what issue #18 reported
// — the objects gone from S3, the bytes still on disk.
//
// Two mechanisms, and the second one is the guarantee:
//
//   - Forget: called at the moment a node is destroyed for good, so the space
//     comes back when the user asks for it rather than at the next sweep;
//   - ReapOrphans: a reconciler that repairs an install which has been leaking
//     since v0.1 — including every path Forget does not sit on, and every
//     orphan already on disk before this code shipped.
//
// # Why this cannot delete a live thumbnail
//
// Deleting cached bytes is destructive, so the rule is narrow on purpose. A
// file is removed only when ALL of these hold:
//
//  1. its name matches `<digits>.jpg` exactly — nothing else in the directory
//     is ever a candidate;
//  2. the database POSITIVELY reports that node id absent from `nodes`. A
//     trashed node still has a row, so a file in the trash keeps its thumbnail
//     and a restore is instant. A query error aborts the pass without deleting
//     anything: "I could not ask" is never read as "it is gone";
//  3. node ids are monotonic (`AUTOINCREMENT` on SQLite, `BIGSERIAL` on
//     Postgres) and never reused, so an id that is absent today cannot acquire
//     a referent tomorrow;
//  4. the file has not been touched within a grace window, so a thumbnail being
//     written right now is never judged mid-flight.

var thumbFileRe = regexp.MustCompile(`^([0-9]+)\.jpg$`)

// reapGrace is how recently a cached file may have been written and still be
// protected. Generation writes the JPEG before the "ready" row is upserted, so
// this covers the window between the two even for a node created in the same
// instant.
const reapGrace = 10 * time.Minute

// Forget removes one node's cached thumbnail — the JPEG and the row.
//
// Call it where a node is destroyed permanently (trash purge, retention
// expiry, a hard delete on a driver that cannot preserve bytes). It is NOT for
// a soft delete: a trashed file is restorable and keeps its thumbnail.
//
// Best-effort by design: a thumbnail is a cache, and failing to remove one must
// never fail the deletion it is attached to. Every failure is logged.
func (p *Pipeline) Forget(ctx context.Context, nodeID int64) {
	if p == nil || nodeID <= 0 {
		return
	}
	if p.cacheDir != "" {
		path := filepath.Join(p.cacheDir, strconv.FormatInt(nodeID, 10)+".jpg")
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("thumb: could not remove the cached file for a deleted node",
				slog.Int64("node", nodeID), slog.String("path", path), slog.String("err", err.Error()))
		}
	}
	if p.store != nil {
		if err := p.store.DeleteThumbnail(ctx, nodeID); err != nil {
			slog.Debug("thumb: could not remove the thumbnails row",
				slog.Int64("node", nodeID), slog.String("err", err.Error()))
		}
	}
}

// ReapResult is what one reconciliation pass did.
type ReapResult struct {
	Scanned int   // candidate files inspected
	Removed int   // files deleted
	Freed   int64 // bytes reclaimed
	Kept    int   // files whose node still exists (live, or trashed and restorable)
	Skipped int   // files too fresh to judge, or unparseable names
}

// ReapOrphans deletes cached thumbnails whose node no longer exists.
//
// See the file comment for the four conditions a file has to meet. Returns the
// tally so the caller can log it; an error means the pass was ABANDONED and
// nothing was deleted.
func (p *Pipeline) ReapOrphans(ctx context.Context) (ReapResult, error) {
	var res ReapResult
	if p == nil || p.cacheDir == "" || p.store == nil {
		return res, nil
	}
	ents, err := os.ReadDir(p.cacheDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return res, nil
		}
		return res, err
	}

	cutoff := time.Now().Add(-reapGrace)
	candidates := make(map[int64]string, len(ents))
	sizes := make(map[int64]int64, len(ents))
	ids := make([]int64, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		m := thumbFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			// Not ours. Somebody else's file in this directory is left alone.
			res.Skipped++
			continue
		}
		id, perr := strconv.ParseInt(m[1], 10, 64)
		if perr != nil || id <= 0 {
			res.Skipped++
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			res.Skipped++
			continue
		}
		if info.ModTime().After(cutoff) {
			// Written moments ago — possibly by a generation still finishing.
			res.Skipped++
			continue
		}
		res.Scanned++
		candidates[id] = filepath.Join(p.cacheDir, e.Name())
		sizes[id] = info.Size()
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return res, nil
	}

	// ⚠ The interlock. If this errors we return without touching a single
	// file: a database that cannot answer must never be read as "the node is
	// gone" — that is how a cleanup turns into data loss.
	alive, err := p.store.ExistingNodeIDs(ctx, ids)
	if err != nil {
		return ReapResult{}, err
	}

	for _, id := range ids {
		if alive[id] {
			res.Kept++
			continue
		}
		if err := os.Remove(candidates[id]); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				slog.Warn("thumb: could not reap an orphaned cache file",
					slog.Int64("node", id), slog.String("path", candidates[id]), slog.String("err", err.Error()))
			}
			continue
		}
		// The row is normally gone with the node (FK cascade), but an install
		// whose engine ran without FK enforcement can still carry one.
		if derr := p.store.DeleteThumbnail(ctx, id); derr != nil {
			slog.Debug("thumb: reaped file but could not remove its row",
				slog.Int64("node", id), slog.String("err", derr.Error()))
		}
		res.Removed++
		res.Freed += sizes[id]
	}
	return res, nil
}

// RunReaper reconciles once at boot and then on every tick until ctx is done.
//
// interval <= 0 disables it entirely (FILEX_THUMBS_SWEEP_INTERVAL=0) — a kill
// switch for an operator who would rather run `filex thumb` maintenance by
// hand than have the server touch the cache directory at all.
//
// ⚠ One log line per pass, including the quiet ones. The staged-upload sweeper
// learned this the expensive way: a sweeper that only speaks when it deletes
// something is indistinguishable from a sweeper that has stopped running.
func (p *Pipeline) RunReaper(ctx context.Context, interval time.Duration) {
	if p == nil || p.cacheDir == "" || interval <= 0 {
		return
	}
	pass := func() {
		res, err := p.ReapOrphans(ctx)
		if err != nil {
			slog.Warn("thumb cache sweep abandoned; nothing was deleted",
				slog.String("dir", p.cacheDir), slog.String("err", err.Error()))
			return
		}
		slog.Info("thumb cache sweep",
			slog.String("dir", p.cacheDir),
			slog.Int("scanned", res.Scanned),
			slog.Int("removed", res.Removed),
			slog.Int64("freed_bytes", res.Freed),
			slog.Int("kept", res.Kept),
			slog.Int("skipped", res.Skipped),
			slog.String("interval", interval.String()))
	}
	pass()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pass()
		}
	}
}
