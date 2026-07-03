package replica

import (
	"context"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// stubStore implements only the store methods GenerateReport touches; the
// embedded db.Store interface is nil, so any other call would panic (which is
// what we want — it proves the function's surface area).
type stubStore struct {
	db.Store
	failed   int64
	repaired int64
	upserts  int
}

func (s *stubStore) CountUnresolvedReplicaFailures(context.Context) (int64, error) {
	return s.failed, nil
}
func (s *stubStore) CountRecentlyResolvedReplicaFailures(context.Context, time.Time) (int64, error) {
	return s.repaired, nil
}
func (s *stubStore) ListReplicaFailures(context.Context, bool, int, int) ([]*model.ReplicaFailure, int64, error) {
	return nil, 0, nil
}
func (s *stubStore) UpsertReplicaStatusReport(context.Context, int64, int64, int64, []byte) error {
	s.upserts++
	return nil
}

type stubNotifier struct {
	notify.Service
	webhookURL string
	sends      int
}

func (n *stubNotifier) WebhookConfig() (string, bool) { return n.webhookURL, n.webhookURL != "" }
func (n *stubNotifier) Send(context.Context, notify.Event) (int64, error) {
	n.sends++
	return 1, nil
}

// TestGenerateReport_NotifyGating asserts the report row is always upserted but
// the in-app notification is only emitted when there's something to report
// (failed/repaired > 0) or a webhook is configured to forward to.
func TestGenerateReport_NotifyGating(t *testing.T) {
	cases := []struct {
		name       string
		failed     int64
		repaired   int64
		webhookURL string
		wantSends  int
	}{
		{"clean run, no webhook → silent", 0, 0, "", 0},
		{"clean run, webhook set → notify", 0, 0, "https://hook.example", 1},
		{"failures present → notify", 2, 0, "", 1},
		{"repairs present → notify", 0, 3, "", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &stubStore{failed: c.failed, repaired: c.repaired}
			notifier := &stubNotifier{webhookURL: c.webhookURL}
			svc := New(store, nil, nil, notifier)

			if err := svc.GenerateReport(context.Background()); err != nil {
				t.Fatalf("GenerateReport: %v", err)
			}
			if store.upserts != 1 {
				t.Errorf("report row upserts = %d, want 1 (always)", store.upserts)
			}
			if notifier.sends != c.wantSends {
				t.Errorf("notifier sends = %d, want %d", notifier.sends, c.wantSends)
			}
		})
	}
}
