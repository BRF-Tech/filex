package replica

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// ErrNotLinked is what Wrappers answers for a storage that does not replicate:
// it has no replication target, its target is switched off or gone, or the
// storage itself is gone.
var ErrNotLinked = errors.New("replica: storage is not linked to an enabled replication target")

// ErrNoReplica is what the repair calls answer when no storage replicates at
// all (the admin endpoints turn it into 503 "no replica configured").
var ErrNoReplica = errors.New("no replica configured")

// Link is a storage's live replication: the wrapper the running process
// writes through, and the target it fans out to.
type Link struct {
	StorageID int64
	TargetID  int64
	Driver    *storage.ReplicatedDriver
}

// Wrappers finds the live replication wrapper of a storage - the one the
// server's resolver hands every writer (internal/server storage_cache.go).
//
// ⚠ Repairs and the initial copy go through THIS, not through a wrapper of
// their own: the Service was once built with a nil wrapper ("v0.1 skips
// that"), so every Fix all answered "no replica configured" whatever was
// linked (#186).
type Wrappers interface {
	// Replicated answers ErrNotLinked for a storage that does not replicate.
	Replicated(ctx context.Context, storageID int64) (Link, error)
}

// Service orchestrates reconciliation, the initial copy and the cron status
// report. Wires store, queue, notify and the live wrappers.
//
// The queue handler for op type "replica_retry" should be Service.
// HandleRetry — the bootstrap registers it on the queue Pool; the initial
// copy's is HandleInitialCopy (TypeInitialCopy).
type Service struct {
	store    db.Store
	wrappers Wrappers
	queue    queue.Driver
	notifier notify.Service

	// stop coordinates shutdown of the cron goroutine.
	stop chan struct{}

	// The initial copy's clock and slice size (initial.go); tests shorten them.
	now        func() time.Time
	slice      time.Duration
	sliceFiles int
	retryAfter time.Duration
}

// New wires a Service. wrappers may be nil (tests of the report alone): then
// nothing can be repaired or copied and those calls answer ErrNoReplica.
func New(store db.Store, wrappers Wrappers, q queue.Driver, n notify.Service) *Service {
	return &Service{
		store:      store,
		wrappers:   wrappers,
		queue:      q,
		notifier:   n,
		stop:       make(chan struct{}),
		now:        time.Now,
		slice:      defaultSlice,
		retryAfter: defaultRetryAfter,
	}
}

// Linked returns the storages that replicate: linked to a replication target
// that exists and is enabled.
func (s *Service) Linked(ctx context.Context) ([]*model.Storage, error) {
	storages, err := s.store.ListStorages(ctx)
	if err != nil {
		return nil, err
	}
	targets, err := s.store.ListReplicationTargets(ctx)
	if err != nil {
		return nil, err
	}
	enabled := map[int64]bool{}
	for _, t := range targets {
		if t.Enabled {
			enabled[t.ID] = true
		}
	}
	var out []*model.Storage
	for _, st := range storages {
		if st.ReplicaTargetID != nil && enabled[*st.ReplicaTargetID] {
			out = append(out, st)
		}
	}
	return out, nil
}

// HasLinked reports whether any storage replicates. The repair endpoints
// answer 503 "no replica configured" only when none does.
func (s *Service) HasLinked(ctx context.Context) (bool, error) {
	linked, err := s.Linked(ctx)
	return len(linked) > 0, err
}

// Reconciled is what one "Repair all" did: retries it queued, and retries it
// found already waiting in the queue and left alone.
type Reconciled struct {
	Queued        int `json:"queued"`
	AlreadyQueued int `json:"already_queued"`
}

// RetryDedupKey coalesces the retries of one failure: while a retry of it is
// waiting in the queue, another request for it adds nothing.
//
// ⚠ Every press of "Repair all" queued a full set again. The list looks the
// same until a retry has run, which invites another press, and each one
// doubled the queue and told the bell "retries queued" once more.
func RetryDedupKey(storageID int64, path, op string) string {
	return queue.TypeReplicaRetry + ":" + strconv.FormatInt(storageID, 10) + ":" + op + ":" + path
}

// enqueueRetry queues one retry, or reports it already waiting.
func (s *Service) enqueueRetry(ctx context.Context, storageID int64, path, op string) (queued bool, err error) {
	_, err = s.queue.Enqueue(ctx, queue.Op{
		Type: queue.TypeReplicaRetry,
		Payload: map[string]any{
			"storage_id": storageID,
			"path":       path,
			"op":         op,
		},
		Priority:    50,
		MaxAttempts: 3,
		DedupKey:    RetryDedupKey(storageID, path, op),
	})
	if errors.Is(err, queue.ErrDuplicate) {
		return false, nil
	}
	return err == nil, err
}

// ReconcileAll enqueues a replica_retry op for every unresolved failure
// currently recorded that has none waiting already.
func (s *Service) ReconcileAll(ctx context.Context) (Reconciled, error) {
	var out Reconciled
	if s.queue == nil {
		return out, fmt.Errorf("replica reconcile: queue not configured")
	}
	failures, err := s.unresolved(ctx)
	if err != nil {
		return out, err
	}
	for _, f := range failures {
		queued, err := s.enqueueRetry(ctx, f.StorageID, f.Path, f.Op)
		switch {
		case err != nil:
			slog.Warn("replica reconcile: enqueue failed",
				slog.String("path", f.Path), slog.String("err", err.Error()))
		case queued:
			out.Queued++
		default:
			out.AlreadyQueued++
		}
	}
	queued := out.Queued
	if queued > 0 && s.notifier != nil {
		_, _ = s.notifier.Send(ctx, notify.Event{
			Event:    notify.EventReplicaReconcileDone,
			Severity: notify.SeverityInfo,
			Title:    "Replica reconciliation queued",
			Body:     fmt.Sprintf("Queued %d replica_retry ops; check the queue page for progress", queued),
			Meta:     map[string]any{"queued": queued},
		})
	}
	return out, nil
}

// failurePage is the most rows the store hands out in one page.
const failurePage = 1000

// maxReconcile bounds one Fix all: a backlog larger than this is replayed by
// pressing again once the first retries have run.
const maxReconcile = 100000

// unresolved reads every unresolved failure, page by page.
//
// ⚠ One call asking for 10,000 rows got 100: the store clamps a page above
// 1,000 to 100, so Fix all replayed the newest hundred failures and said
// nothing of the rest.
func (s *Service) unresolved(ctx context.Context) ([]*model.ReplicaFailure, error) {
	var out []*model.ReplicaFailure
	for offset := 0; offset < maxReconcile; offset += failurePage {
		page, _, err := s.store.ListReplicaFailures(ctx, true, failurePage, offset)
		if err != nil {
			return out, err
		}
		out = append(out, page...)
		if len(page) < failurePage {
			break
		}
	}
	return out, nil
}

// FixOne enqueues a single retry for one (storage, path, op), unless one is
// waiting in the queue already (queued=false, no error).
func (s *Service) FixOne(ctx context.Context, storageID int64, path, op string) (queued bool, err error) {
	if s.queue == nil {
		return false, fmt.Errorf("replica reconcile: queue not configured")
	}
	return s.enqueueRetry(ctx, storageID, path, op)
}

// payloadInt reads an integer from a queue payload, which comes back from
// JSON as a float64 (or a json.Number, or a string from an older writer).
func payloadInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}

// HandleRetry is the queue.Handler for op type "replica_retry". The
// bootstrap calls Pool.Register(queue.TypeReplicaRetry, svc.HandleRetry).
//
// The retry goes through the storage's LIVE wrapper (Wrappers), so it writes
// to the target the storage is linked to now. A storage that no longer
// replicates - unlinked, its target switched off or deleted, the storage gone
// - leaves nothing to repair: its failure is resolved and the op succeeds.
//
// The handler is structured to never panic and always either:
//   - succeed (success → Ack on the queue side)
//   - return an error so the queue requeues with backoff
func (s *Service) HandleRetry(ctx context.Context, op queue.Op) error {
	if s.wrappers == nil {
		return fmt.Errorf("replica retry: %w", ErrNoReplica)
	}
	storageID := payloadInt(op.Payload["storage_id"])
	path, _ := op.Payload["path"].(string)
	opName, _ := op.Payload["op"].(string)
	if path == "" || opName == "" {
		return fmt.Errorf("replica retry: missing path/op in payload")
	}
	switch opName {
	case "write", "delete", "move", "copy":
	default:
		return fmt.Errorf("replica retry: unknown op %q", opName)
	}
	link, err := s.wrappers.Replicated(ctx, storageID)
	if errors.Is(err, ErrNotLinked) {
		return s.store.ResolveReplicaFailure(ctx, storageID, path, opName)
	}
	if err != nil {
		return fmt.Errorf("replica retry: %w", err)
	}
	return link.Driver.Repair(ctx, path, opName)
}

// GenerateReport computes the singleton replica_status_reports row
// and sends the full event to the webhook (in-app gets a summary).
//
// The full failed-paths list goes only to webhooks (the user posts it
// into their own system — F2/F3) — the in-app body keeps the message
// short so the bell doesn't drown in JSON.
func (s *Service) GenerateReport(ctx context.Context) error {
	failed, err := s.store.CountUnresolvedReplicaFailures(ctx)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	repaired, err := s.store.CountRecentlyResolvedReplicaFailures(ctx, cutoff)
	if err != nil {
		return err
	}
	// total_files: best-effort using the primary's storage size if we
	// can introspect it via List on the root. v0.1 leaves this at 0
	// when the primary doesn't have a cheap object count — the rep
	// is still useful with just failed/repaired.
	total := int64(0)

	// Sample of failures (up to 100) for the webhook payload.
	sample, _, _ := s.store.ListReplicaFailures(ctx, true, 100, 0)
	summary := map[string]any{
		"failed_count":   failed,
		"repaired_count": repaired,
		"total_files":    total,
		"sample":         sample,
	}
	summaryJSON, _ := json.Marshal(summary)

	if err := s.store.UpsertReplicaStatusReport(ctx, total, failed, repaired, summaryJSON); err != nil {
		return err
	}

	// The report row above is upserted on every run so the latest counts are
	// always available. The *notification*, however, is only worth emitting
	// when it's actionable — otherwise an N-minute cron with "0 failures, 0
	// repaired" and no webhook just floods the in-app bell with hundreds of
	// unread no-op reports. Notify only when there's something to say
	// (failed/repaired > 0) OR a webhook URL is configured (the operator has
	// opted in to receive every cron report at their own endpoint).
	if s.notifier != nil {
		webhookURL, _ := s.notifier.WebhookConfig()
		if failed > 0 || repaired > 0 || webhookURL != "" {
			// Webhook gets the full list (paginated via the same store
			// helper) — caller has already opted-in by configuring
			// FILEX_WEBHOOK_URL. In-app body stays terse.
			full, _, _ := s.store.ListReplicaFailures(ctx, true, 100000, 0)
			body := fmt.Sprintf("Cron report: %d unresolved failures, %d repaired in last 24h", failed, repaired)
			_, _ = s.notifier.Send(ctx, notify.Event{
				Event:    notify.EventReplicaStatusReport,
				Severity: notify.SeverityInfo,
				Title:    "Replica status report",
				Body:     body,
				Meta: map[string]any{
					"failed_count":   failed,
					"repaired_count": repaired,
					"total_files":    total,
					"failed_paths":   full,
				},
			})
		}
	}
	return nil
}

// Stop signals the cron goroutine to exit.
func (s *Service) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}
