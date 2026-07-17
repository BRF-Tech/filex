package versioning

// Daily version-retention job ("Koru" v0.4). This file is additive — the
// core snapshot/restore service (service.go) is untouched; the loop here
// only CALLS the existing Cleanup(nodeID, keepN).
//
// The keep count comes from the settings table (key `versions.keep_n`,
// written by PATCH /api/admin/protection). 0 / missing / unparseable
// means the job is disabled — versions are then only trimmed by the
// snapshot path's built-in DefaultRetention. Scheduling mirrors
// trash.Service.RunDailyLoop: a ticker whose first tick fires one
// interval after startup so a flapping server doesn't hammer the backend.

import (
	"context"
	"log/slog"
	"strconv"
	"time"
)

// SettingKeyKeepN is the settings-table row holding the per-node version
// keep count. 0 = retention job disabled (unlimited).
const SettingKeyKeepN = "versions.keep_n"

// KeepN reads the configured per-node keep count. Missing, empty or
// non-numeric values (and anything < 0) resolve to 0 = disabled.
func (s *Service) KeepN(ctx context.Context) int {
	if s == nil || s.Store == nil {
		return 0
	}
	v, err := s.Store.GetSetting(ctx, SettingKeyKeepN)
	if err != nil || v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// RetentionResult summarises one retention run.
type RetentionResult struct {
	KeepN   int `json:"keep_n"`
	Nodes   int `json:"nodes"`
	Deleted int `json:"deleted"`
	Failed  int `json:"failed"`
}

// RunRetentionOnce applies Cleanup(keepN) to every node that has version
// rows. A keep_n of 0 is a no-op (disabled). Per-node failures are
// counted + logged, never fatal — one broken node must not stall the
// whole sweep.
func (s *Service) RunRetentionOnce(ctx context.Context) (RetentionResult, error) {
	var res RetentionResult
	if s == nil || s.Store == nil {
		return res, nil
	}
	keepN := s.KeepN(ctx)
	res.KeepN = keepN
	if keepN <= 0 {
		return res, nil
	}
	ids, err := s.Store.ListNodeIDsWithVersions(ctx)
	if err != nil {
		return res, err
	}
	res.Nodes = len(ids)
	for _, id := range ids {
		n, err := s.Cleanup(ctx, id, keepN)
		if err != nil {
			res.Failed++
			slog.Warn("version retention: cleanup failed",
				slog.Int64("node", id), slog.String("err", err.Error()))
			continue
		}
		res.Deleted += n
	}
	return res, nil
}

// RunRetentionLoop ticks RunRetentionOnce every interval until ctx is
// cancelled. First tick happens after `interval`, not immediately
// (trash.RunDailyLoop pattern).
func (s *Service) RunRetentionLoop(ctx context.Context, interval time.Duration) {
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
			res, err := s.RunRetentionOnce(ctx)
			if err != nil {
				slog.Warn("version retention run failed", slog.String("err", err.Error()))
				continue
			}
			if res.Deleted > 0 || res.Failed > 0 {
				slog.Info("version retention complete",
					slog.Int("keep_n", res.KeepN),
					slog.Int("nodes", res.Nodes),
					slog.Int("deleted", res.Deleted),
					slog.Int("failed", res.Failed))
			}
		}
	}
}
