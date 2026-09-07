package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SyncMode describes how a Storage is synced into the DB cache.
type SyncMode string

const (
	SyncModePoll     SyncMode = "poll"     // periodic remote scan
	SyncModeFSNotify SyncMode = "fsnotify" // local FS event-driven
	SyncModeOnDemand SyncMode = "ondemand" // explicit user-triggered

	// SyncModePush was declared for "the backend pushes changes at us"
	// (a webhook receiver) and NOTHING WAS EVER BUILT BEHIND IT. The sync
	// worker has no `push` branch, so such a storage falls through to the
	// poll loop: it works, but it does not do what its own setting says.
	//
	// The constant stays so that legacy rows can still be recognised and
	// named in a log line. It is rejected on write (ValidateSyncMode) and
	// the worker warns about any row that still carries it.
	SyncModePush SyncMode = "push"
)

// SyncModes lists the modes the sync worker actually implements — the set an
// operator may choose from. Order is the order they appear in the docs.
func SyncModes() []SyncMode {
	return []SyncMode{SyncModePoll, SyncModeFSNotify, SyncModeOnDemand}
}

// Implemented reports whether the sync worker has a branch for this mode.
// An unimplemented mode is not a harmless label: the storage silently polls,
// so the operator reads their own configuration and believes something else
// is happening.
func (m SyncMode) Implemented() bool {
	switch m {
	case "", SyncModePoll, SyncModeFSNotify, SyncModeOnDemand:
		return true
	default:
		return false
	}
}

// ValidateSyncMode rejects a sync_mode nothing implements — a typo (which
// used to store and silently poll) and `push` (declared, never built).
//
// The empty string is accepted: it means "unset", and the write path defaults
// it to poll, which is also the column default.
func ValidateSyncMode(m SyncMode) error {
	if m.Implemented() {
		return nil
	}
	if m == SyncModePush {
		return fmt.Errorf(`sync_mode %q is not implemented: nothing in filex receives backend pushes, so the storage would silently poll instead. Use %q and trigger POST /api/admin/storages/{id}/sync from whatever writes to the backend, or %q`,
			string(SyncModePush), string(SyncModeOnDemand), string(SyncModePoll))
	}
	names := make([]string, 0, len(SyncModes()))
	for _, v := range SyncModes() {
		names = append(names, string(v))
	}
	return fmt.Errorf("invalid sync_mode %q (valid: %s)", string(m), strings.Join(names, ", "))
}

// Storage is a configured backend (local FS / S3 / SFTP / WebDAV / …).
type Storage struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	Driver        string          `json:"driver"`
	MountPath     string          `json:"mount_path"`
	ConfigJSON    json.RawMessage `json:"config"`
	SyncMode      SyncMode        `json:"sync_mode"`
	SyncIntervalS int             `json:"sync_interval_s"`
	LastSyncAt    *time.Time      `json:"last_sync_at,omitempty"`
	LastSyncToken string          `json:"last_sync_token,omitempty"`
	Enabled       bool            `json:"enabled"`
	ReadOnly      bool            `json:"read_only"`
	// RBACEnabled turns on per-user/per-item access control for this storage.
	// When false (default) the storage is visible to every authenticated user
	// and capability is governed purely by account role (RBAC-off passthrough,
	// preserving pre-00012 behavior). When true the storage is hidden by
	// default and only paths explicitly granted (directly or via an ancestor
	// folder) are visible; see internal/acl. Added in migration 00012.
	RBACEnabled bool      `json:"rbac_enabled"`
	CreatedAt   time.Time `json:"created_at"`
	// Replica pairing — `role` and `replica_of_id` are LEGACY columns
	// retained for backwards compatibility with v0.1.16 deployments
	// (SQLite can't DROP COLUMN cleanly). The current model lives in
	// `replica_target_id` — a foreign key into the new
	// `replication_targets` table. Set it via the Replikasyon page;
	// the wrapper Driver fan-outs writes to the linked target.
	Role            string `json:"role,omitempty"`
	ReplicaOfID     *int64 `json:"replica_of_id,omitempty"`
	ReplicaMode     string `json:"replica_mode,omitempty"`
	ReplicaTargetID *int64 `json:"replica_target_id,omitempty"`
}

// ReplicationTarget is a backup-only sink that the replica engine
// fans writes out to. It is NOT a regular storage — operators never
// write to it directly, and it never appears in the Depolar list or
// the file explorer. Defined in v0.1.18 (migration 00009).
type ReplicationTarget struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	Driver     string          `json:"driver"`
	ConfigJSON json.RawMessage `json:"config"`
	Mode       string          `json:"mode"` // "async" | "sync"
	Enabled    bool            `json:"enabled"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// SyncRun is a row in sync_runs.
type SyncRun struct {
	ID           int64      `json:"id"`
	StorageID    int64      `json:"storage_id"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	CursorBefore string     `json:"cursor_before,omitempty"`
	CursorAfter  string     `json:"cursor_after,omitempty"`
	SeenCount    int        `json:"seen_count"`
	Added        int        `json:"added"`
	Updated      int        `json:"updated"`
	Deleted      int        `json:"deleted"`
	Status       string     `json:"status"` // running, ok, partial, failed, aborted
	Error        string     `json:"error,omitempty"`
}

// SyncConflict captures backend/DB drift requiring human attention.
type SyncConflict struct {
	ID           int64      `json:"id"`
	NodeID       *int64     `json:"node_id,omitempty"`
	StorageID    int64      `json:"storage_id"`
	StorageKey   string     `json:"storage_key,omitempty"`
	DBEtag       string     `json:"db_etag,omitempty"`
	BackendEtag  string     `json:"backend_etag,omitempty"`
	DBMtime      *time.Time `json:"db_mtime,omitempty"`
	BackendMtime *time.Time `json:"backend_mtime,omitempty"`
	DetectedAt   time.Time  `json:"detected_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	Resolution   string     `json:"resolution,omitempty"` // backend_wins, db_wins, manual
}
