package model

import (
	"encoding/json"
	"time"
)

// ReplicaMode controls how replica writes/deletes are handled per
// path pattern. Default-on rule is ModeMirror (Burak E2 / SPEC §4.4).
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

// ReplicaFailure is one row in replica_failures. Each (path, op) pair
// has at most one row — repeated failures bump attempts via UPSERT.
type ReplicaFailure struct {
	ID            int64      `json:"id"`
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
