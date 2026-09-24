// Package trash implements retention-based purging of soft-deleted nodes.
//
// Nodes carry a `deleted_at` timestamp; a daily goroutine in server.Start
// calls PurgeExpired to hard-delete + remove the underlying storage object
// for nodes whose deleted_at is older than the configured retention window
// (settings key "trash.retention_days", default 30).
package trash

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// SettingKey is the settings table row that stores the retention days value.
const SettingKey = "trash.retention_days"

// Prefix is the in-storage directory soft-deleted objects are renamed into.
// It is syspath.Trash — the one list of filex's own directories, through
// which every listing filters it out — and Restore renames back out of it.
const Prefix = syspath.Trash

// NewKey returns a fresh storage-relative trash key for base:
// `.filex-trash/<unix>-<rand>__<base>` — the exact shape vfDelete mints, so
// every surface (manager, AI, DAV) lands deletions in the same trash layout.
func NewKey(base string) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s/%d-%s__%s", Prefix, time.Now().Unix(), hex.EncodeToString(b[:]), base)
}

// DefaultRetentionDays is used when the setting is missing or unparseable.
const DefaultRetentionDays = 30

// StorageResolver maps storage_id → live driver. Same shape used elsewhere.
type StorageResolver func(int64) (storage.Driver, error)

// OnPurge is called once per node the instant before its row is destroyed for
// good, so the caches keyed on that node id can release their bytes.
//
// It exists because a purge is the ONE moment at which "this node will never
// come back" is true — a soft delete is reversible, and a cache dropped there
// would be a cache dropped for a file the user can restore. Wiring it as a
// callback keeps this package free of the thumbnail pipeline and the upload
// staging area, which both live above it.
//
// Best-effort: an implementation must not fail the purge.
type OnPurge func(ctx context.Context, nodeID int64)

// Service is the retention engine entry point.
type Service struct {
	Store    db.Store
	Resolver StorageResolver
	Quota    *quota.Service
	// Reclaim, when wired, releases the per-node caches (the cached thumbnail
	// JPEG, any staging directory still holding this node's bytes). See
	// OnPurge. Nil is a no-op, which is what the unit tests use.
	Reclaim OnPurge

	// sweep admits one purge sweep at a time (see sweepLock): the nightly
	// retention run, EmptyOlderThan and every RunEmpty take it.
	sweep sweepLock
}

// New constructs a Service.
func New(store db.Store, resolver StorageResolver, q *quota.Service) *Service {
	return &Service{Store: store, Resolver: resolver, Quota: q}
}

// RetentionDays reads the configured retention window in days.
func (s *Service) RetentionDays(ctx context.Context) int {
	if s == nil || s.Store == nil {
		return DefaultRetentionDays
	}
	v, err := s.Store.GetSetting(ctx, SettingKey)
	if err != nil || v == "" {
		return DefaultRetentionDays
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return DefaultRetentionDays
	}
	return n
}

// PurgeResult is returned by PurgeExpired / EmptyOlderThan to summarise a run.
type PurgeResult struct {
	Scanned int   `json:"scanned"`
	Deleted int   `json:"deleted"`
	Failed  int   `json:"failed"`
	Bytes   int64 `json:"bytes"`
}

// PurgeExpired hard-deletes nodes whose deleted_at is older than the
// configured retention window.
//
// It waits its turn behind a running "empty the trash" rather than sweeping
// the same rows beside it (see sweepLock).
func (s *Service) PurgeExpired(ctx context.Context) (PurgeResult, error) {
	if s == nil || s.Store == nil {
		return PurgeResult{}, errors.New("trash: service not initialised")
	}
	if err := s.sweep.acquire(ctx); err != nil {
		return PurgeResult{}, err
	}
	defer s.sweep.release()
	days := s.RetentionDays(ctx)
	// UTC: SQLite compares deleted_at (CURRENT_TIMESTAMP, UTC) with the bound
	// parameter as TEXT, so a local-zone bound was off by the zone's offset.
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	// nil = every storage: the nightly retention sweep is not narrowed.
	return s.purgeOlderThan(ctx, cutoff, nil, nil)
}

// EmptyOlderThan ignores the configured retention and purges anything older
// than the supplied days value (admin "empty trash now" operation). Pass 0
// for olderThanDays to wipe every soft-deleted node regardless of age.
//
// storageID limits the purge to one storage; 0 means every storage the caller
// can reach.
//
// ⚠⚠ The parameter exists because it was MISSING while the UI claimed it was
// there. `POST /api/admin/trash/empty` decoded `storage_id` into a struct
// field nothing read, and the confirmation dialog said "If you picked a
// storage, only that one is affected." An admin who narrowed the operation to
// one storage and confirmed it permanently destroyed the trash of EVERY
// storage — irreversibly, with the dialog telling them the opposite.
//
// It is RunEmpty in the caller's goroutine, scoped by the caller's context,
// and it ends with that context. The admin endpoint does not use it for that
// reason: a large trash outlives any request, so the endpoint queues the same
// job on the ops queue (ops.SubmitTrashEmpty) and the run belongs to the
// server.
func (s *Service) EmptyOlderThan(ctx context.Context, olderThanDays int, storageID int64) (PurgeResult, error) {
	return s.RunEmpty(ctx, EmptyJob{
		Before:    EmptyCutoff(time.Now(), olderThanDays),
		StorageID: storageID,
		Reach:     Reach(ctx),
	}, nil, nil)
}

// EmptyCutoff is the deleted_at bound an "empty the trash" asked for at `at`
// purges below: `at` itself for 0 days (everything that was in the trash when
// it was asked for — and nothing deleted after), `at` minus the days
// otherwise. UTC, for SQLite's TEXT comparison (see PurgeExpired).
func EmptyCutoff(at time.Time, olderThanDays int) time.Time {
	if olderThanDays < 0 {
		olderThanDays = 0
	}
	return at.UTC().Add(-time.Duration(olderThanDays) * 24 * time.Hour)
}

// ConflictError reports that a restore's original path is occupied. Nothing
// was moved and nothing was un-trashed. It unwraps to os.ErrExist, the same
// error a rename collision maps to 409 with.
type ConflictError struct {
	// Path is the occupied storage-relative path.
	Path string
}

func (e *ConflictError) Error() string { return fmt.Sprintf("trash: %q already exists", e.Path) }

func (e *ConflictError) Unwrap() error { return os.ErrExist }

// occupied reports whether something other than n already holds rel, and both
// halves matter: the BYTES, which the restore's rename would replace (a file)
// or merge into (a folder), and a LIVE row, which the live-only unique index on
// (storage_id, path_hash) (migration 00032) would refuse a second of — after
// the bytes had already left the trash key.
//
// ⚠ A row soft-deleted WHERE IT STOOD (the tombstone pass never retags) has
// its own bytes at rel, if it has any. Those are not an occupant, so the byte
// half is skipped for it; only a live row can take the name from under it.
func (s *Service) occupied(ctx context.Context, drv storage.Driver, n *model.Node, rel string) bool {
	hash := pathkey.Hash(n.StorageID, rel)
	if row, err := s.Store.GetNodeByPath(ctx, n.StorageID, hash); err == nil && row != nil {
		return true
	}
	if drv == nil || pathkey.Hash(n.StorageID, n.Path) == hash {
		return false
	}
	return storage.Exists(ctx, drv, rel)
}

// Restore lifts the deleted_at flag on a node AND moves the underlying
// file back from the `.filex-trash/` location to its original path
// (saved in `storage_key` at delete time).
//
// `path` and `path_hash` flip back to the original; `parent_id` is
// re-resolved from the original path's parent dir so the listing
// re-attaches the row in the right tree.
func (s *Service) Restore(ctx context.Context, nodeID int64) error {
	if s == nil || s.Store == nil {
		return errors.New("trash: service not initialised")
	}
	n, err := s.Store.GetNode(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("trash: get node: %w", err)
	}
	if n.DeletedAt == nil {
		return nil // already live
	}
	origPath := n.StorageKey
	if origPath == "" {
		// Pre-rename row (legacy) — just clear the flag and leave
		// storage layout untouched.
		return s.Store.RestoreNode(ctx, nodeID)
	}
	var drv storage.Driver
	if s.Resolver != nil {
		if d, err := s.Resolver(n.StorageID); err == nil {
			drv = d
		}
	}
	// ⚠ Before ANY byte moves, and as an error rather than a log line. The
	// driver step below is a rename: it silently replaces a file at the
	// original path, and for a folder the rename fails with ENOTEMPTY and
	// TakeBack's per-object walk pours the trashed tree INTO the occupying
	// folder, overwriting what shares a name. That step is best-effort by
	// contract, so nothing after it can refuse — this is the only place the
	// answer fits.
	if s.occupied(ctx, drv, n, origPath) {
		return &ConflictError{Path: origPath}
	}
	// Move the file back on disk. Best-effort: keep going even if the
	// driver step fails (admin can recover via SQL + storage CLI).
	if drv != nil {
		// TakeBack mirrors Put: native rename when the driver has one,
		// Copy+Delete when it does not, and a per-object walk for a folder
		// an object store never had a real object for.
		//
		// ⚠ ErrNotFound is logged too. The contract is best-effort — the
		// row comes back whatever the driver did, and the documented fix
		// is to find the object under `.filex-trash/` by hand — which only
		// works if this line names the key. Filtering ErrNotFound out
		// left the one restore that delivers nothing without a record.
		if err := TakeBack(ctx, drv, n.Path, origPath); err != nil {
			slog.Warn("trash restore move failed",
				slog.Int64("node_id", n.ID),
				slog.String("from", n.Path),
				slog.String("to", origPath),
				slog.String("err", err.Error()))
		}
	}
	parent, err := s.Store.LookupParentByPath(ctx, n.StorageID, origPath)
	if err != nil {
		// Fall back to a root restore — better than leaving the row
		// orphaned in trash forever.
		parent = nil
	}
	return s.Store.RestoreNodeAt(ctx, nodeID, parent, origPath)
}

// OriginalPath resolves the path a trashed row is JUDGED on: where the file
// came from, which is the only path a grant, a confinement root or a restore
// is ever written about. `known` is false when the row does not record one.
//
// `storage_key` holds it — SoftDeleteAndRetag writes it there while rewriting
// `path` to the trash key. When the column is EMPTY (legacy rows; migration
// 00033 deliberately left those alone) the row's own `path` is the original,
// because nothing renamed it: the sync tombstone pass's SoftDeleteNode leaves
// `path` exactly where the file lived.
//
// ⚠ The one shape with no answer is a resolved path that is ITSELF inside
// `.filex-trash/` — the walk used to mint rows for the trash's own bytes, and
// the tombstone pass soft-deleted them where they stood (see
// sync.reconcileTrash). A trash key records the basename and nothing else, so
// the folder the file came from is not recoverable from the row by anyone.
// Every caller that authorises on the result must DENY when known is false:
// the alternative asks about `.filex-trash/…`, a path no rule is written
// about, and hands the answer to whoever holds a grant on the bin. The path is
// still returned, for display.
func OriginalPath(n *model.Node) (orig string, known bool) {
	if n == nil {
		return "", false
	}
	orig = n.StorageKey
	if orig == "" {
		orig = n.Path
	}
	return orig, orig != "" && !IsTrashPath(orig)
}

// List returns soft-deleted entries (the trash listing for the admin UI).
//
// Each entry's `Path` is the ORIGINAL path (`storage_key`) so the user
// sees where the item lived, not the internal `.filex-trash/...` key.
// `TTLDays` is the days remaining before automatic purge.
func (s *Service) List(ctx context.Context, storageID *int64, limit, offset int) ([]TrashEntry, int, error) {
	if s == nil || s.Store == nil {
		return nil, 0, errors.New("trash: service not initialised")
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, total, err := s.Store.ListTrashed(ctx, storageID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	retention := s.RetentionDays(ctx)
	now := time.Now()
	storageNames := map[int64]string{}
	if all, err := s.Store.ListStorages(ctx); err == nil {
		for _, st := range all {
			storageNames[st.ID] = st.Name
		}
	}
	out := make([]TrashEntry, 0, len(rows))
	for _, n := range rows {
		entry := TrashEntry{
			ID:        n.ID,
			StorageID: n.StorageID,
			Name:      n.Name,
			Size:      n.Size,
			Mime:      n.Mime,
		}
		entry.StorageName = storageNames[n.StorageID]
		// The ORIGINAL path (OriginalPath's rule, legacy fallback included),
		// and the ORIGINAL basename rather than the `<unix>-<rand>__name` key
		// the node was renamed to on soft-delete. A row that records no
		// original path keeps its trash key here — see OriginalPath for why
		// the handler must not authorise on it.
		entry.Path, _ = OriginalPath(n)
		if n.StorageKey != "" {
			entry.Name = path.Base(n.StorageKey)
		}
		if n.DeletedAt != nil {
			entry.DeletedAt = *n.DeletedAt
			elapsed := now.Sub(*n.DeletedAt) / (24 * time.Hour)
			remaining := retention - int(elapsed)
			if remaining < 0 {
				remaining = 0
			}
			entry.TTLDays = &remaining
		}
		out = append(out, entry)
	}
	return out, total, nil
}

// PurgeOne immediately hard-deletes a single trashed node (admin / owner).
func (s *Service) PurgeOne(ctx context.Context, nodeID int64) error {
	if s == nil || s.Store == nil {
		return errors.New("trash: service not initialised")
	}
	n, err := s.Store.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}
	return s.purgeOne(ctx, n)
}

// TrashEntry is the projection returned by List — flat shape the admin
// UI consumes directly.
type TrashEntry struct {
	ID          int64     `json:"id"`
	StorageID   int64     `json:"storage_id"`
	StorageName string    `json:"storage_name,omitempty"`
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	Mime        string    `json:"mime,omitempty"`
	DeletedAt   time.Time `json:"deleted_at"`
	TTLDays     *int      `json:"ttl_days,omitempty"`
}

// RunDailyLoop ticks PurgeExpired every interval until ctx is cancelled.
// First tick happens after `interval`, not immediately, so a flapping server
// doesn't hammer the backend on restart.
func (s *Service) RunDailyLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			res, err := s.PurgeExpired(ctx)
			if err != nil {
				slog.Warn("trash purge run failed", slog.String("err", err.Error()))
				continue
			}
			if res.Deleted > 0 || res.Failed > 0 {
				slog.Info("trash purge complete",
					slog.Int("scanned", res.Scanned),
					slog.Int("deleted", res.Deleted),
					slog.Int("failed", res.Failed),
					slog.Int64("bytes", res.Bytes))
			}
		}
	}
}

// purgeOlderThan does the heavy lifting shared by PurgeExpired and EmptyOlderThan.
//
// For each node found:
//  1. resolve storage driver (best effort — swallow lookup errors);
//  2. delete backing object via Deleter;
//  3. decrement owner quota;
//  4. hard-delete the row.
//
// ⚠⚠ It walks the trash with a cursor. It used to ask for "the oldest batch"
// on every pass and rely on the purge to empty the window — but a row it
// skipped (another storage, another tenant) or failed to purge stayed in the
// window, and once a full batch of those sat at the head every pass was the
// same batch: nothing purged, no end. A tenant emptying their own trash on an
// instance where any other tenant had 500 older deleted files spun until the
// proxy gave up; the nightly worker, which has no deadline, would never have
// stopped.
//
// scope is the set of storages it may touch (narrow): nil every storage, an
// empty slice none. progress, when set, is told the running totals after every
// row it purged or failed to purge (the ops row's progress).
func (s *Service) purgeOlderThan(ctx context.Context, cutoff time.Time, scope []int64, progress func(PurgeResult)) (PurgeResult, error) {
	if s == nil || s.Store == nil {
		return PurgeResult{}, errors.New("trash: service not initialised")
	}
	if scope != nil && len(scope) == 0 {
		return PurgeResult{}, nil // a scope that reaches no storage purges nothing
	}
	const batchSize = 500
	var res PurgeResult
	var after int64
	for {
		// Narrowed in the SQL as well as by reaches() below, so rows of
		// storages the run may not touch are never even read — and walked by
		// id, so a row passed over or failed is never read twice.
		batch, err := s.Store.ListTrashedExpired(ctx, cutoff, scope, after, batchSize)
		if err != nil {
			return res, fmt.Errorf("trash: list: %w", err)
		}
		if len(batch) == 0 {
			return res, nil
		}
		for _, n := range batch {
			after = max(after, n.ID)
			// A run that was cut short stops HERE, and says so. Carrying on
			// through the batch failed every remaining row on the dead context
			// — each one logged and counted as a failed purge — and then the
			// run returned nil, as if it had finished.
			if err := ctx.Err(); err != nil {
				return res, err
			}
			if !reaches(scope, n.StorageID) {
				continue
			}
			res.Scanned++
			// ⚠ A row that has started finishes, on a context the run's
			// cancellation does not reach. A row is several steps — the
			// storage delete, the caches, the database row — and a
			// cancellation landing between them left a row in the trash whose
			// bytes were already gone: listed, "restorable", and nothing to
			// restore. Cancelling stops the run at the next row (above).
			if err := s.purgeOne(context.WithoutCancel(ctx), n); err != nil {
				slog.Warn("trash purge one failed",
					slog.Int64("node_id", n.ID),
					slog.String("err", err.Error()))
				res.Failed++
			} else {
				res.Deleted++
				res.Bytes += n.Size
			}
			if progress != nil {
				progress(res)
			}
		}
		if len(batch) < batchSize {
			return res, nil
		}
	}
}

// purgeOne deletes the storage object (best effort), decrements quota, and
// hard-deletes the DB row.
//
// Directories first purge their trashed descendants explicitly: the nodes
// table cascades parent_id on hard delete, so removing the folder row first
// would silently drop the child rows BEFORE their storage objects and quota
// were reclaimed (leaked `.filex-trash/` objects).
func (s *Service) purgeOne(ctx context.Context, n *model.Node) error {
	if n.Type == model.NodeTypeDirectory {
		s.purgeDirDescendants(ctx, n)
	}
	if !ownsBytesAt(n.Path) {
		// ⚠⚠ Soft-deleted where it stood (see ownsBytesAt): nothing of this
		// row's is at its path, so whatever is there now arrived later. Only
		// the row goes.
		slog.Info("trash purge: row was deleted in place; leaving its path alone",
			slog.Int64("node_id", n.ID),
			slog.String("path", n.Path))
	} else if s.Resolver != nil {
		if drv, err := s.Resolver(n.StorageID); err == nil {
			if d, ok := drv.(storage.Deleter); ok {
				// `n.Path` is the actual on-disk location for trashed
				// rows (the `.filex-trash/...` key after vfDelete's
				// rename). storage_key carries the ORIGINAL path so
				// Restore can put the file back — purging that would
				// look at the wrong key, miss, and leak the trash file.
				key := n.Path
				if n.Type == model.NodeTypeFile {
					if err := d.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) {
						// Continue anyway — DB row removal still happens, but
						// log the leftover-object warning.
						slog.Warn("trash storage delete failed",
							slog.Int64("node_id", n.ID),
							slog.String("err", err.Error()))
					}
				} else {
					// Directory/marker cleanup is best-effort — object
					// stores may have a "<key>/" marker, local FS a dir.
					_ = d.Delete(ctx, key)
					_ = d.Delete(ctx, strings.TrimRight(key, "/")+"/")
				}
			}
		}
	}
	/* calisma:d3 comments */
	// Purge the node's comment rows explicitly (belt and suspenders next
	// to the node_comments FK CASCADE — engines run with FK enforcement
	// on, but an explicit delete keeps the purge self-contained).
	_ = s.Store.DeleteNodeCommentsByNode(ctx, n.ID)
	// Per-node caches, released here because this is the last instant at which
	// the node id is still meaningful — and the first at which "gone for good"
	// is true. Before this call nothing ever removed the cached thumbnail: the
	// `thumbnails` row went with the node via the FK cascade while the JPEG sat
	// in <data>/thumbs forever (issue #18).
	if s.Reclaim != nil {
		s.Reclaim(ctx, n.ID)
	}
	// ⚠ The quota release happens INSIDE HardDeleteNode, in the accounting
	// store (internal/quotastore) — the one place that also counts the bytes
	// when they land. Subtracting here as well would release every purged
	// file twice.
	return s.Store.HardDeleteNode(ctx, n.ID)
}

// ownsBytesAt reports whether a trashed row's own bytes can be at p — that is,
// whether p is inside the trash.
//
// A row trashed the ordinary way was renamed into `.filex-trash/`, and its
// bytes are there. A row soft-deleted WHERE IT STOOD never had bytes behind it
// by the time it was: the storage sync's tombstone pass writes one only after
// confirming the file is gone (confirmGone), and the queue's "already missing"
// and "could not trash, deleted outright" branches only after the bytes are.
// So anything at such a path arrived later — a new upload with the old name, a
// restore from backup, a folder that exists again — and deleting it would
// destroy a file nobody deleted; for a folder, recursively. Before this check
// the purge did exactly that, 30 days after the tombstone was written.
func ownsBytesAt(p string) bool { return IsTrashPath(p) }

// purgeDirDescendants hard-purges every trashed row still parked under a
// trashed directory's `.filex-trash/...` path (SoftDeleteAndRetag rewrites
// descendants to live there). Files get their storage object deleted and
// quota reclaimed; rows are removed explicitly rather than left to the FK
// cascade. Best-effort throughout.
func (s *Service) purgeDirDescendants(ctx context.Context, dir *model.Node) {
	prefixes := prefixVariants(dir.Path)
	if len(prefixes) == 0 {
		return
	}
	var descendants []*model.Node
	for offset := 0; ; {
		batch, _, err := s.Store.ListTrashed(ctx, &dir.StorageID, 500, offset)
		if err != nil || len(batch) == 0 {
			break
		}
		for _, c := range batch {
			if c.ID == dir.ID {
				continue
			}
			for _, pfx := range prefixes {
				if strings.HasPrefix(c.Path, pfx) {
					descendants = append(descendants, c)
					break
				}
			}
		}
		if len(batch) < 500 {
			break
		}
		offset += len(batch)
	}
	var drv storage.Driver
	if s.Resolver != nil {
		drv, _ = s.Resolver(dir.StorageID)
	}
	for _, c := range descendants {
		if c.Type == model.NodeTypeFile && drv != nil && ownsBytesAt(c.Path) {
			if d, ok := drv.(storage.Deleter); ok {
				if err := d.Delete(ctx, c.Path); err != nil && !errors.Is(err, storage.ErrNotFound) {
					slog.Warn("trash storage delete failed",
						slog.Int64("node_id", c.ID),
						slog.String("err", err.Error()))
				}
			}
		}
		// Same reclamation as purgeOne — a descendant purged through this
		// loop must not leave its cached bytes behind either.
		if s.Reclaim != nil {
			s.Reclaim(ctx, c.ID)
		}
		// Quota release is inside HardDeleteNode (internal/quotastore) — see
		// purgeOne.
		if err := s.Store.HardDeleteNode(ctx, c.ID); err != nil {
			continue
		}
	}
}

// prefixVariants returns p as a strict-descendant prefix in both path
// conventions the nodes.path column historically mixes (with/without a
// leading slash).
func prefixVariants(p string) []string {
	norm := strings.TrimRight(path.Clean("/"+strings.Trim(p, "/")), "/")
	if norm == "" || norm == "/" {
		return nil
	}
	rel := strings.TrimPrefix(norm, "/")
	return []string{norm + "/", rel + "/"}
}
