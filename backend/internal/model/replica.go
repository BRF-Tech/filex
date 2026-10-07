package model

import (
	"encoding/json"
	"time"
)

// ReplicaMode controls how replica writes/deletes are handled per
// path pattern. Default-on rule is ModeMirror (owner decision E2 / SPEC §4.4).
const (
	ReplicaModeMirror     = "mirror"
	ReplicaModeAppendOnly = "append_only"
	ReplicaModeSkip       = "skip"
)

// ReplicaRule is one path-glob → mode entry. Lower priority wins
// (priority asc, first match returns).
type ReplicaRule struct {
	ID          int64     `json:"id"`
	PathPattern string    `json:"path_pattern"`
	Mode        string    `json:"mode"`
	Priority    int       `json:"priority"`
	Enabled     bool      `json:"enabled"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ReplicaRuleInput is the upsert payload — id is filled in by the
// store on INSERT.
type ReplicaRuleInput struct {
	PathPattern string `json:"path_pattern"`
	Mode        string `json:"mode"`
	Priority    int    `json:"priority"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description"`
}

// ReplicaFailure is one row in replica_failures. Each (storage, path, op)
// has at most one row — repeated failures bump attempts via UPSERT.
//
// StorageID names the storage the path belongs to (migration 00094): paths
// are relative to a storage, and several storages can replicate at once, so a
// repair has to know whose wrapper to replay it through. 0 is a row written
// before 00094, which no storage can claim.
type ReplicaFailure struct {
	ID            int64      `json:"id"`
	StorageID     int64      `json:"storage_id"`
	Path          string     `json:"path"`
	Op            string     `json:"op"`
	ErrorCode     string     `json:"error_code"`
	ErrorMsg      string     `json:"error_msg"`
	Attempts      int        `json:"attempts"`
	LastAttemptAt time.Time  `json:"last_attempt_at"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
}

// ReplicaStatusReport is the singleton row produced by the cron job.
// Only one row exists at a time (id=1, CHECK constraint).
type ReplicaStatusReport struct {
	GeneratedAt   time.Time       `json:"generated_at"`
	TotalFiles    int64           `json:"total_files"`
	FailedCount   int64           `json:"failed_count"`
	RepairedCount int64           `json:"repaired_count"`
	SummaryJSON   json.RawMessage `json:"summary"`
}

// ReplicaSettings is the singleton config row (id=1).
type ReplicaSettings struct {
	ReportCron    string `json:"report_cron"`
	ReportEnabled bool   `json:"report_enabled"`
	DefaultMode   string `json:"default_mode"`
}

// Phases of a storage's initial copy (ReplicaInitialCopy.Phase).
const (
	// ReplicaCopyPending: queued, nothing walked yet.
	ReplicaCopyPending = "pending"
	// ReplicaCopyCounting: walking the storage to count its files.
	ReplicaCopyCounting = "counting"
	// ReplicaCopyCopying: walking it again, copying what the target lacks.
	ReplicaCopyCopying = "copying"
	// ReplicaCopyWaiting: the target stopped answering; the copy resumes
	// where it stopped on its own (LastError says why it waits).
	ReplicaCopyWaiting = "waiting"
	// ReplicaCopyDone: every file was looked at once.
	ReplicaCopyDone = "done"
)

// ReplicaInitialCopy is one row in replica_initial_copies (migration 00094):
// the copy of the files a storage ALREADY held when it was linked to a
// replication target. The live fan-out only sees writes made after the link;
// this is what brings the backup up to the storage's state at that moment.
//
// One row per storage, for the target it is linked to now. Times are Unix
// seconds in integer columns so every engine returns them unchanged.
type ReplicaInitialCopy struct {
	StorageID int64  `json:"storage_id"`
	TargetID  int64  `json:"target_id"`
	Phase     string `json:"phase"`
	// Cursor is the last file the walk finished with, in the walk's own order
	// (internal/replica walk.go); the next slice starts after it. "" = from
	// the beginning of the phase.
	Cursor string `json:"-"`
	// Counted: Total is final (the counting walk finished).
	Counted bool `json:"counted"`
	// Total is the number of files on the storage (so far, while counting).
	Total int64 `json:"total"`
	// Copied, Present, Excluded and Failed add up to the files looked at:
	// written to the target, already there (same size, not older), left out
	// by a `skip` rule, failed (in replica_failures, Fix all replays them).
	Copied      int64  `json:"copied"`
	Present     int64  `json:"present"`
	Excluded    int64  `json:"excluded"`
	Failed      int64  `json:"failed"`
	CopiedBytes int64  `json:"copied_bytes"`
	LastError   string `json:"last_error,omitempty"`
	// StartedUnix, UpdatedUnix, FinishedUnix: Unix seconds; FinishedUnix is
	// 0 until the copy is done.
	StartedUnix  int64 `json:"started_unix"`
	UpdatedUnix  int64 `json:"updated_unix"`
	FinishedUnix int64 `json:"finished_unix"`
	// LeaseOwner/LeaseUntil: the worker running a slice of this copy now and
	// until when its claim holds (Unix seconds). Revision moves on every write
	// so an UPDATE always changes the row (MySQL counts changed rows).
	LeaseOwner string `json:"-"`
	LeaseUntil int64  `json:"-"`
	Revision   int64  `json:"-"`
}

// Done is the number of files the copy has looked at so far.
func (c *ReplicaInitialCopy) Done() int64 {
	if c == nil {
		return 0
	}
	return c.Copied + c.Present + c.Excluded + c.Failed
}

// ReplicaLink is one row in replica_links (migration 00095): the folder a
// storage writes into on the replication target it is linked to. Chosen once,
// when the storage is first linked there (internal/replica folder.go), and
// kept through a rename of the storage or a target switched off and on.
// FolderKey is the folder lowercased: no two storages on one target share it.
type ReplicaLink struct {
	StorageID   int64  `json:"storage_id"`
	TargetID    int64  `json:"target_id"`
	Folder      string `json:"folder"`
	FolderKey   string `json:"-"`
	CreatedUnix int64  `json:"created_unix"`
}
