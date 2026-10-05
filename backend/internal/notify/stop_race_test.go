package notify_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// Stop must not race a delivery another goroutine started - a write hook
// still emitting as the server shuts down, or a request's goroutine outliving
// the test that stops the service in its cleanup. It did: the in-flight count
// was a WaitGroup, the hook's Add from zero and Stop's Wait were not ordered,
// and `go test -race` reported it (the 0.52.0 release's -race run,
// TestE2EPolicy_TheMCPFileWriteToolAsksTheRule).
func TestStop_WhileADeliveryFromAnotherGoroutineIsInFlight(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	svc := notify.New(store, notify.Config{WebhookURL: srv.URL, HTTPTimeout: 10 * time.Second, RetryBackoffs: []time.Duration{}})
	sent := make(chan error, 1)
	go func() {
		_, err := svc.Send(context.Background(), notify.Event{
			Event:    notify.EventReplicaFail,
			Severity: notify.SeverityInfo,
			Title:    "in flight at stop",
		})
		sent <- err
	}()
	// Give the delivery time to reach the webhook, which holds it. ⚠ A sleep,
	// not a wait on `hits`: reading what the webhook saw would order the
	// sender's Add before Stop (through the socket and the atomic), and the
	// race detector would then have nothing to report even on the old code.
	time.Sleep(time.Second)
	svc.Stop()
	require.NoError(t, <-sent)
	assert.Equal(t, int32(1), hits.Load(), "the delivery never reached the webhook")
}

// Once stopped, the service starts no delivery: a Send still records the
// notification, its webhook is not called, and the row says why.
func TestStop_ASendAfterStopStartsNoDelivery(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	svc := notify.New(store, notify.Config{WebhookURL: srv.URL, HTTPTimeout: 2 * time.Second, RetryBackoffs: []time.Duration{}})
	svc.Stop()

	id, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventReplicaFail,
		Severity: notify.SeverityInfo,
		Title:    "after stop",
	})
	require.NoError(t, err)
	require.Greater(t, id, int64(0))
	svc.Wait()

	row, err := store.GetNotification(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "skipped", row.WebhookStatus)
	assert.Contains(t, row.WebhookError, "stopped")
	assert.Equal(t, int32(0), hits.Load(), "a stopped service called the webhook")
}
