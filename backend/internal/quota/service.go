// Package quota tracks per-user storage usage and enforces the
// users.quota_bytes ceiling at upload time.
//
// quota_bytes == 0 means "unlimited". usage_bytes is incremented atomically
// in the DB on successful uploads and decremented on deletes; a periodic
// Recompute() job rebuilds it from authoritative node sizes.
package quota

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/db"
)

// ErrQuotaExceeded is returned by CheckCanWrite when the user is out of room.
var ErrQuotaExceeded = errors.New("quota: exceeded")

// ErrUserNotFound means the id doesn't name a user.
//
// usage_bytes and quota_bytes are COALESCE'd columns on `users`, so a user
// that exists always reads back (0, 0) = "unlimited, nothing used". A
// no-rows result therefore says nothing about quotas — it says there is no
// such user, and the caller should answer 404 rather than leak
// `sql: no rows in result set` as a 500 (olivov H5, 2026-08-05).
var ErrUserNotFound = errors.New("quota: user not found")

// lookupUsage reads (used, limit) and normalises the missing-user case.
func (s *Service) lookupUsage(ctx context.Context, userID int64) (int64, int64, error) {
	used, limit, err := s.Store.GetUserUsage(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrUserNotFound
	}
	return used, limit, err
}

// Service is the quota façade exposed to the rest of the codebase.
type Service struct {
	Store db.Store
}

// New constructs a Service.
func New(store db.Store) *Service { return &Service{Store: store} }

// Snapshot is the value returned by Get — used by the /me/quota handler.
type Snapshot struct {
	UsedBytes   int64   `json:"used_bytes"`
	QuotaBytes  int64   `json:"quota_bytes"` // 0 == unlimited
	PercentUsed float64 `json:"percent_used"`
	Unlimited   bool    `json:"unlimited"`
}

// CheckCanWrite returns ErrQuotaExceeded when (used + addBytes) > quota.
// quota == 0 (unlimited) and userID <= 0 (anonymous / system) always pass.
func (s *Service) CheckCanWrite(ctx context.Context, userID int64, addBytes int64) error {
	if s == nil || s.Store == nil {
		return nil
	}
	if userID <= 0 || addBytes <= 0 {
		return nil
	}
	used, limit, err := s.lookupUsage(ctx, userID)
	if errors.Is(err, ErrUserNotFound) {
		// Anonymous/system writes already pass above; an id with no user
		// row is not something to enforce a ceiling against.
		return nil
	}
	if err != nil {
		return fmt.Errorf("quota: read usage: %w", err)
	}
	if limit <= 0 {
		return nil // unlimited
	}
	if used+addBytes > limit {
		return ErrQuotaExceeded
	}
	return nil
}

// AddUsage atomically grows usage_bytes by `bytes`.
func (s *Service) AddUsage(ctx context.Context, userID int64, bytes int64) error {
	if s == nil || s.Store == nil || userID <= 0 || bytes == 0 {
		return nil
	}
	return s.Store.IncrementUserUsage(ctx, userID, bytes)
}

// SubUsage atomically shrinks usage_bytes by `bytes`. The DB layer clamps
// the result at zero.
func (s *Service) SubUsage(ctx context.Context, userID int64, bytes int64) error {
	if s == nil || s.Store == nil || userID <= 0 || bytes == 0 {
		return nil
	}
	return s.Store.IncrementUserUsage(ctx, userID, -bytes)
}

// Recompute rebuilds usage_bytes from the SUM(size) of nodes owned by the user.
// Returns ErrUserNotFound for an id that isn't a user — the underlying UPDATE
// would otherwise touch no rows and report success.
func (s *Service) Recompute(ctx context.Context, userID int64) (int64, error) {
	if s == nil || s.Store == nil || userID <= 0 {
		return 0, nil
	}
	if _, _, err := s.lookupUsage(ctx, userID); err != nil {
		return 0, err
	}
	return s.Store.RecomputeUserUsage(ctx, userID)
}

// SetQuota writes a new quota_bytes value (admin only — caller enforces).
// Returns ErrUserNotFound for an id that isn't a user, so an admin doesn't
// get a 200 for a write that landed nowhere.
func (s *Service) SetQuota(ctx context.Context, userID int64, bytes int64) error {
	if s == nil || s.Store == nil {
		return nil
	}
	if _, _, err := s.lookupUsage(ctx, userID); err != nil {
		return err
	}
	return s.Store.SetUserQuota(ctx, userID, bytes)
}

// Get returns the current snapshot. Returns ErrUserNotFound for an id that
// isn't a user.
func (s *Service) Get(ctx context.Context, userID int64) (Snapshot, error) {
	if s == nil || s.Store == nil {
		return Snapshot{Unlimited: true}, nil
	}
	used, limit, err := s.lookupUsage(ctx, userID)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{UsedBytes: used, QuotaBytes: limit}
	if limit <= 0 {
		snap.Unlimited = true
		return snap, nil
	}
	if used > 0 && limit > 0 {
		snap.PercentUsed = (float64(used) / float64(limit)) * 100.0
	}
	return snap, nil
}
