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
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// SettingKey is the settings table row that stores the retention days value.
const SettingKey = "trash.retention_days"

// Prefix is the in-storage directory soft-deleted objects are renamed into.
// It mirrors the manager's unexported trashPrefix; listings everywhere filter
// it out and Restore renames back out of it.
const Prefix = ".filex-trash"

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

// Service is the retention engine entry point.
type Service struct {
	Store    db.Store
	Resolver StorageResolver
	Quota    *quota.Service
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
func (s *Service) PurgeExpired(ctx context.Context) (PurgeResult, error) {
	days := s.RetentionDays(ctx)
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	return s.purgeOlderThan(ctx, cutoff)
}

// EmptyOlderThan ignores the configured retention and purges anything older
// than the supplied days value (admin "empty trash now" operation). Pass 0
// to wipe every soft-deleted node regardless of age.
func (s *Service) EmptyOlderThan(ctx context.Context, olderThanDays int) (PurgeResult, error) {
	cutoff := time.Now()
	if olderThanDays > 0 {
		cutoff = cutoff.Add(-time.Duration(olderThanDays) * 24 * time.Hour)
	} else {
		// 0 days = purge everything currently in the trash.
		cutoff = cutoff.Add(24 * time.Hour) // future cutoff matches everything in past
	}
	return s.purgeOlderThan(ctx, cutoff)
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
	// Move the file back on disk. Best-effort: keep going even if the
	// driver step fails (admin can recover via SQL + storage CLI).
	if s.Resolver != nil {
		if drv, err := s.Resolver(n.StorageID); err == nil {
			if mv, ok := drv.(storage.Mover); ok {
				if err := mv.Move(ctx, n.Path, origPath); err != nil &&
					!errors.Is(err, storage.ErrNotFound) {
					slog.Warn("trash restore move failed",
						slog.Int64("node_id", n.ID),
						slog.String("from", n.Path),
						slog.String("to", origPath),
						slog.String("err", err.Error()))
				}
			}
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
		// Prefer the original path stashed in storage_key; fall back
		// to current `path` (legacy rows pre-`.filex-trash/`). Show the
		// ORIGINAL basename, not the `<unix>-<rand>__name` trash-key the
		// node was renamed to on soft-delete.
		if n.StorageKey != "" {
			entry.Path = n.StorageKey
			entry.Name = path.Base(n.StorageKey)
		} else {
			entry.Path = n.Path
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
func (s *Service) purgeOlderThan(ctx context.Context, cutoff time.Time) (PurgeResult, error) {
	if s == nil || s.Store == nil {
		return PurgeResult{}, errors.New("trash: service not initialised")
	}
	const batchSize = 500
	var res PurgeResult
	for {
		batch, err := s.Store.ListTrashedExpired(ctx, cutoff, batchSize)
		if err != nil {
			return res, fmt.Errorf("trash: list: %w", err)
		}
		if len(batch) == 0 {
			return res, nil
		}
		for _, n := range batch {
			res.Scanned++
			if err := s.purgeOne(ctx, n); err != nil {
				slog.Warn("trash purge one failed",
					slog.Int64("node_id", n.ID),
					slog.String("err", err.Error()))
				res.Failed++
				continue
			}
			res.Deleted++
			res.Bytes += n.Size
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
	if s.Resolver != nil {
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
	owner, _ := s.Store.GetNodeOwner(ctx, n.ID)
	if err := s.Store.HardDeleteNode(ctx, n.ID); err != nil {
		return err
	}
	if owner != nil && s.Quota != nil && n.Type == model.NodeTypeFile {
		_ = s.Quota.SubUsage(ctx, *owner, n.Size)
	}
	return nil
}

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
		if c.Type == model.NodeTypeFile && drv != nil {
			if d, ok := drv.(storage.Deleter); ok {
				if err := d.Delete(ctx, c.Path); err != nil && !errors.Is(err, storage.ErrNotFound) {
					slog.Warn("trash storage delete failed",
						slog.Int64("node_id", c.ID),
						slog.String("err", err.Error()))
				}
			}
		}
		owner, _ := s.Store.GetNodeOwner(ctx, c.ID)
		if err := s.Store.HardDeleteNode(ctx, c.ID); err != nil {
			continue
		}
		if owner != nil && s.Quota != nil && c.Type == model.NodeTypeFile {
			_ = s.Quota.SubUsage(ctx, *owner, c.Size)
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
