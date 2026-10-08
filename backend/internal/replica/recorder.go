// Package replica is the orchestration layer that ties the storage
// ReplicatedDriver to the DB (failure rows, rules, settings) and to
// the notify subsystem (webhook + bell events).
//
// The split is:
//   - internal/storage/replicated.go — the wrapper Driver itself.
//   - internal/storage/rules.go      — the path-glob rule engine.
//   - internal/server storage_cache.go — the resolver's cache, which wraps a
//     storage linked to an enabled replication target.
//   - this package                   — DB-backed adapter that
//     implements storage.FailureRecorder + storage.EventNotifier and
//     also runs the reconciliation queue handler, the initial copy and the
//     cron status report.
//
// Keeping the storage package pure of DB and notify imports lets us
// unit-test the wrapper in isolation against in-memory recorders.
package replica

import (
	"context"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// FailureRecorder is a DB-backed storage.FailureRecorder for ONE storage:
// every row it writes or resolves carries that storage's id (migration 00094).
type FailureRecorder struct {
	store     db.Store
	storageID int64
}

// NewFailureRecorder binds the recorder to a store and a storage.
func NewFailureRecorder(store db.Store, storageID int64) *FailureRecorder {
	return &FailureRecorder{store: store, storageID: storageID}
}

// Record persists or upserts a failure row.
func (r *FailureRecorder) Record(ctx context.Context, path, op, errCode, errMsg string) error {
	return r.store.UpsertReplicaFailure(ctx, r.storageID, path, op, errCode, errMsg)
}

// Resolve flips the matching row's resolved_at.
func (r *FailureRecorder) Resolve(ctx context.Context, path, op string) error {
	return r.store.ResolveReplicaFailure(ctx, r.storageID, path, op)
}

// Notifier is the storage.EventNotifier impl backed by notify.Service.
//
// The service is read when an event is sent, not when the notifier is made:
// the server builds the storages' wrappers before its notification service
// exists, and a wrapper made then must still reach the bell.
type Notifier struct {
	svc         func() notify.Service
	storageID   int64
	storageName string
}

// NewNotifier wires the adapter to a service that already exists.
func NewNotifier(svc notify.Service) *Notifier {
	return &Notifier{svc: func() notify.Service { return svc }}
}

// NewNotifierFunc wires the adapter to a service read at send time.
func NewNotifierFunc(get func() notify.Service) *Notifier {
	return &Notifier{svc: get}
}

// ForStorage returns a copy that names the storage in what it sends.
func (n *Notifier) ForStorage(id int64, name string) *Notifier {
	cp := *n
	cp.storageID = id
	cp.storageName = name
	return &cp
}

func (n *Notifier) service() notify.Service {
	if n == nil || n.svc == nil {
		return nil
	}
	return n.svc()
}

func (n *Notifier) meta(m map[string]any) map[string]any {
	if n.storageID != 0 {
		m["storage_id"] = n.storageID
		m["storage"] = n.storageName
	}
	return m
}

// NotifyReplicaFail emits a replica_fail event.
func (n *Notifier) NotifyReplicaFail(ctx context.Context, path, op string, err error, attempt int) {
	svc := n.service()
	if svc == nil {
		return
	}
	// The facts only: the server says the alarm from them, in each reader's
	// language (internal/notify say.go, server.notify.replica_fail).
	_, _ = svc.Send(ctx, notify.Event{
		Event:    notify.EventReplicaFail,
		Severity: notify.SeverityWarning,
		Meta: n.meta(map[string]any{
			"path":    path,
			"op":      op,
			"error":   err.Error(),
			"attempt": attempt,
		}),
	})
}

// NotifyPrimaryReadFail emits a primary_read_fail event when the read
// fallback served from replica.
func (n *Notifier) NotifyPrimaryReadFail(ctx context.Context, path string, err error) {
	svc := n.service()
	if svc == nil {
		return
	}
	// The facts only (notify say.go, server.notify.primary_read_fail).
	_, _ = svc.Send(ctx, notify.Event{
		Event:    notify.EventPrimaryReadFail,
		Severity: notify.SeverityError,
		Meta: n.meta(map[string]any{
			"path":          path,
			"primary_error": err.Error(),
		}),
	})
}

// storageLabel is how a storage is named in a message: its name, or #id.
func storageLabel(id int64, name string) string {
	if name != "" {
		return name
	}
	return "#" + strconv.FormatInt(id, 10)
}

// Compile-time interface checks.
var (
	_ storage.FailureRecorder = (*FailureRecorder)(nil)
	_ storage.EventNotifier   = (*Notifier)(nil)
)
