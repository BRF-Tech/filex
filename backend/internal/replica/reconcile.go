package replica

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Service orchestrates reconciliation + cron status report. Wires
// store, queue, notify and the live ReplicatedDriver.
//
// The queue handler for op type "replica_retry" should be Service.
// HandleRetry — the bootstrap registers it on the queue Pool.
type Service struct {
	store    db.Store
	driver   *storage.ReplicatedDriver
	queue    queue.Driver
	notifier notify.Service

	// stop coordinates shutdown of the cron goroutine.
	stop chan struct{}
}

// New wires a Service.
func New(store db.Store, driver *storage.ReplicatedDriver, q queue.Driver, n notify.Service) *Service {
	return &Service{
		store:    store,
		driver:   driver,
		queue:    q,
		notifier: n,
		stop:     make(chan struct{}),
	}
}

// ReconcileAll enqueues a replica_retry op for every unresolved
// failure currently recorded. Returns the number queued.
func (s *Service) ReconcileAll(ctx context.Context) (int, error) {
	if s.queue == nil {
		return 0, fmt.Errorf("replica reconcile: queue not configured")
	}
	failures, _, err := s.store.ListReplicaFailures(ctx, true, 10000, 0)
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, f := range failures {
		_, err := s.queue.Enqueue(ctx, queue.Op{
			Type: queue.TypeReplicaRetry,
			Payload: map[string]any{
				"path": f.Path,
				"op":   f.Op,
			},
			Priority:    50,
			MaxAttempts: 3,
		})
		if err != nil {
			slog.Warn("replica reconcile: enqueue failed",
				slog.String("path", f.Path), slog.String("err", err.Error()))
			continue
		}
		queued++
	}
	if queued > 0 && s.notifier != nil {
		_, _ = s.notifier.Send(ctx, notify.Event{
			Event:    notify.EventReplicaReconcileDone,
			Severity: notify.SeverityInfo,
			Title:    "Replica reconciliation queued",
			Body:     fmt.Sprintf("Queued %d replica_retry ops; check the queue page for progress", queued),
			Meta:     map[string]any{"queued": queued},
		})
	}
	return queued, nil
}

// FixOne enqueues a single retry for one (path, op) pair.
func (s *Service) FixOne(ctx context.Context, path, op string) error {
	if s.queue == nil {
		return fmt.Errorf("replica reconcile: queue not configured")
	}
	_, err := s.queue.Enqueue(ctx, queue.Op{
		Type: queue.TypeReplicaRetry,
		Payload: map[string]any{
			"path": path,
			"op":   op,
		},
		Priority:    50,
		MaxAttempts: 3,
	})
	return err
}

// HandleRetry is the queue.Handler for op type "replica_retry". The
// bootstrap calls Pool.Register(queue.TypeReplicaRetry, svc.HandleRetry).
//
// The handler is structured to never panic and always either:
//   - succeed (success → Ack on the queue side)
//   - return an error so the queue requeues with backoff
func (s *Service) HandleRetry(ctx context.Context, op queue.Op) error {
	if s.driver == nil || !s.driver.HasReplica() {
		return fmt.Errorf("replica retry: no replica configured")
	}
	path, _ := op.Payload["path"].(string)
	opName, _ := op.Payload["op"].(string)
	if path == "" || opName == "" {
		return fmt.Errorf("replica retry: missing path/op in payload")
	}

	switch opName {
	case "write":
		return s.retryWrite(ctx, path)
	case "delete":
		return s.retryDelete(ctx, path)
	case "move":
		// Retry is best-effort: we treat move as "write the dest path
		// from primary" because we don't keep the move's src.
		return s.retryWrite(ctx, path)
	case "copy":
		return s.retryWrite(ctx, path)
	default:
		return fmt.Errorf("replica retry: unknown op %q", opName)
	}
}

func (s *Service) retryWrite(ctx context.Context, path string) error {
	primary := s.driver.Primary()
	replica := s.driver.Replica()
	rc, err := primary.Read(ctx, path)
	if err != nil {
		return fmt.Errorf("read primary: %w", err)
	}
	defer rc.Close()
	stat, err := primary.Stat(ctx, path)
	if err != nil {
		return fmt.Errorf("stat primary: %w", err)
	}
	w, ok := replica.(storage.Writer)
	if !ok {
		return fmt.Errorf("replica driver lacks Writer interface")
	}
	if err := w.Write(ctx, path, rc, stat.Size); err != nil {
		return fmt.Errorf("write replica: %w", err)
	}
	return s.store.ResolveReplicaFailure(ctx, path, "write")
}

func (s *Service) retryDelete(ctx context.Context, path string) error {
	d, ok := s.driver.Replica().(storage.Deleter)
	if !ok {
		return fmt.Errorf("replica driver lacks Deleter interface")
	}
	if err := d.Delete(ctx, path); err != nil {
		return fmt.Errorf("delete replica: %w", err)
	}
	return s.store.ResolveReplicaFailure(ctx, path, "delete")
}

// GenerateReport computes the singleton replica_status_reports row
// and sends the full event to the webhook (in-app gets a summary).
//
// The full failed-paths list goes only to webhooks (kullanıcı kendi
// sistemine post eder — F2/F3) — the in-app body keeps the message
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
