// Package replica is the orchestration layer that ties the storage
// ReplicatedDriver to the DB (failure rows, rules, settings) and to
// the notify subsystem (webhook + bell events).
//
// The split is:
//   - internal/storage/replicated.go — the wrapper Driver itself.
//   - internal/storage/rules.go      — the path-glob rule engine.
//   - this package                   — DB-backed adapter that
//     implements storage.FailureRecorder + storage.EventNotifier and
//     also runs the reconciliation queue handler + cron status report.
//
// Keeping the storage package pure of DB and notify imports lets us
// unit-test the wrapper in isolation against in-memory recorders.
package replica

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// FailureRecorder is a DB-backed storage.FailureRecorder.
type FailureRecorder struct {
	store db.Store
}

// NewFailureRecorder binds the recorder to a store.
func NewFailureRecorder(store db.Store) *FailureRecorder {
	return &FailureRecorder{store: store}
}

// Record persists or upserts a failure row.
func (r *FailureRecorder) Record(ctx context.Context, path, op, errCode, errMsg string) error {
	return r.store.UpsertReplicaFailure(ctx, path, op, errCode, errMsg)
}

// Resolve flips the matching row's resolved_at.
func (r *FailureRecorder) Resolve(ctx context.Context, path, op string) error {
	return r.store.ResolveReplicaFailure(ctx, path, op)
}

// Notifier is the storage.EventNotifier impl backed by notify.Service.
type Notifier struct {
	svc notify.Service
}

// NewNotifier wires the adapter.
func NewNotifier(svc notify.Service) *Notifier {
	return &Notifier{svc: svc}
}

// NotifyReplicaFail emits a replica_fail event.
func (n *Notifier) NotifyReplicaFail(ctx context.Context, path, op string, err error, attempt int) {
	if n.svc == nil {
		return
	}
	_, _ = n.svc.Send(ctx, notify.Event{
		Event:    notify.EventReplicaFail,
		Severity: notify.SeverityWarning,
		Title:    "Replica " + op + " failed",
		Body:     "Path " + path + " — " + err.Error(),
		Meta: map[string]any{
			"path":    path,
			"op":      op,
			"error":   err.Error(),
			"attempt": attempt,
		},
	})
}

// NotifyPrimaryReadFail emits a primary_read_fail event when the read
// fallback served from replica.
func (n *Notifier) NotifyPrimaryReadFail(ctx context.Context, path string, err error) {
	if n.svc == nil {
		return
	}
	_, _ = n.svc.Send(ctx, notify.Event{
		Event:    notify.EventPrimaryReadFail,
		Severity: notify.SeverityError,
		Title:    "Primary read failed, served from replica",
		Body:     "Path " + path + " was served from replica after primary error: " + err.Error(),
		Meta: map[string]any{
			"path":          path,
			"primary_error": err.Error(),
		},
	})
}

// Compile-time interface checks.
var (
	_ storage.FailureRecorder = (*FailureRecorder)(nil)
	_ storage.EventNotifier   = (*Notifier)(nil)
)
