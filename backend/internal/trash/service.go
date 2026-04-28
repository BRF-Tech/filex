// Package trash implements retention-based purging of soft-deleted nodes.
//
// Nodes carry a `deleted_at` timestamp; a daily goroutine in server.Start
// calls PurgeExpired to hard-delete + remove the underlying storage object
// for nodes whose deleted_at is older than the configured retention window
// (settings key "trash.retention_days", default 30).
package trash

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/model"
	"gitlab.com/brftech/filemanager/backend/internal/quota"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
)

// SettingKey is the settings table row that stores the retention days value.
const SettingKey = "trash.retention_days"

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

// Restore lifts the deleted_at flag on a node so it returns to its parent.
func (s *Service) Restore(ctx context.Context, nodeID int64) error {
	if s == nil || s.Store == nil {
		return errors.New("trash: service not initialised")
	}
	return s.Store.RestoreNode(ctx, nodeID)
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
func (s *Service) purgeOne(ctx context.Context, n *model.Node) error {
	if s.Resolver != nil && n.Type == model.NodeTypeFile {
		if drv, err := s.Resolver(n.StorageID); err == nil {
			if d, ok := drv.(storage.Deleter); ok {
				key := n.StorageKey
				if key == "" {
					key = n.Path
				}
				if err := d.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) {
					// Continue anyway — DB row removal still happens, but
					// log the leftover-object warning.
					slog.Warn("trash storage delete failed",
						slog.Int64("node_id", n.ID),
						slog.String("err", err.Error()))
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
