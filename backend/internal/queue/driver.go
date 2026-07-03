// Package queue is filex' driver-based persistent operation queue.
//
// Three drivers ship in the binary:
//
//	sqlite   — default, single-node; uses the existing filex DB file
//	postgres — production HA via SELECT FOR UPDATE SKIP LOCKED
//	redis    — work-list pattern via BLMOVE for low-latency hot paths
//
// All drivers share the same Op struct and Driver contract so callers can
// swap them via the FILEMANAGER_QUEUE_DRIVER env var without touching
// handlers. Operations survive restarts (the queue table is the source of
// truth). On boot, drivers re-queue any rows left in `running` state from
// a previous crash.
//
// The Pool type (worker.go) consumes a Driver and a map of handlers keyed
// by Op.Type. Handlers are pure Go functions; storage drivers, replica
// retry, reconcile and report jobs all attach as handlers.
package queue

import (
	"context"
	"errors"
	"time"
)

// ErrEmpty is returned by Dequeue when there is no eligible op.
var ErrEmpty = errors.New("queue: empty")

// ErrNotFound is returned when a specific op id can't be located.
var ErrNotFound = errors.New("queue: op not found")

// Status values for Op.Status. Drivers must use exactly these strings —
// the admin UI renders them verbatim.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Common Op.Type values. Callers may invent new ones — drivers do not
// enforce a registry — but the canonical handlers below are wired in
// server bootstrap.
const (
	TypeCopy          = "copy"
	TypeMove          = "move"
	TypeDelete        = "delete"
	TypeReplicaRetry  = "replica_retry"
	TypeReplicaReport = "replica_report"
	TypeReconcile     = "reconcile"
	TypeThumb         = "thumb"
)

// DefaultMaxAttempts is the retry budget when an Op enqueue request omits
// the field. Three attempts strikes a useful balance: transient network
// errors usually resolve by attempt 2; a third gives the upstream service
// a final chance before the op is parked in `failed` for the operator.
const DefaultMaxAttempts = 3

// Op is a single queued operation. Drivers persist this verbatim (with a
// JSON-encoded Payload).
type Op struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Payload     map[string]any `json:"payload"`
	Status      string         `json:"status"`
	Priority    int            `json:"priority"`
	Attempts    int            `json:"attempts"`
	MaxAttempts int            `json:"max_attempts"`
	LastError   string         `json:"last_error,omitempty"`
	EnqueuedAt  time.Time      `json:"enqueued_at"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	FinishedAt  *time.Time     `json:"finished_at,omitempty"`
	NotBefore   *time.Time     `json:"not_before,omitempty"`
}

// Stats is a snapshot of queue health for the admin dashboard.
type Stats struct {
	Pending   int64 `json:"pending"`
	Running   int64 `json:"running"`
	Failed    int64 `json:"failed"`
	Done24h   int64 `json:"done_24h"`
	Cancelled int64 `json:"cancelled"`
}

// Driver is the persistence + dispatch contract for an op queue.
//
// Implementations MUST be safe for concurrent calls — Pool spawns N
// worker goroutines, each calling Dequeue independently.
type Driver interface {
	// Init wires the driver to its backing store. Called once at boot.
	Init(ctx context.Context, cfg map[string]any) error

	// Name returns the registered driver name (e.g. "sqlite").
	Name() string

	// Enqueue persists op and returns its assigned id. Op.ID is ignored.
	Enqueue(ctx context.Context, op Op) (id string, err error)

	// Dequeue atomically picks and marks-running the next eligible op.
	// types limits the candidate set — empty slice means any type.
	// Returns ErrEmpty when nothing is ready (callers sleep + retry).
	Dequeue(ctx context.Context, types []string) (Op, error)

	// Ack marks the op completed. Done24h counters bump.
	Ack(ctx context.Context, id string) error

	// Fail records a failure. If retry is true, the op goes back to
	// pending with attempts++; otherwise it terminates as failed.
	Fail(ctx context.Context, id string, errMsg string, retry bool) error

	// List returns ops filtered by status (or empty for any). Pagination
	// via limit/offset; total returns the unpaginated count.
	List(ctx context.Context, status string, limit, offset int) (ops []Op, total int64, err error)

	// Get returns a single op by id. Returns ErrNotFound when missing.
	Get(ctx context.Context, id string) (Op, error)

	// Stats returns aggregated counters for the admin dashboard.
	Stats(ctx context.Context) (Stats, error)

	// Cancel transitions a pending op to cancelled. Running ops cannot
	// be cancelled in v0.1 — the worker checks ctx.Err() but cooperative
	// shutdown is on the caller's handler.
	Cancel(ctx context.Context, id string) error

	// Retry transitions a failed op back to pending (resetting attempts
	// to attempts ≥ 0 — drivers may bump this if they wish). Used by
	// admin UI's "retry" button.
	Retry(ctx context.Context, id string) error

	// RecoverOrphans flips long-running rows back to pending. Called on
	// boot to handle ungraceful shutdowns. olderThan is the threshold
	// (running rows whose started_at < now - olderThan re-queued).
	RecoverOrphans(ctx context.Context, olderThan time.Duration) (int64, error)

	// Close releases resources. Pool calls this on Stop.
	Close() error
}
