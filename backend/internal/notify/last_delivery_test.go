package notify_test

// Last-delivery persistence tests (migration 00019): every delivery
// attempt — real dispatch or admin test-fire — must land in the
// webhook_targets last_status / last_error / last_delivery_at columns.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// TestTargetDelivery_PersistsFailureThenSuccess drives one target
// through a failing dispatch (HTTP 500) and then a succeeding test-fire
// (HTTP 204), asserting the persisted columns after each.
func TestTargetDelivery_PersistsFailureThenSuccess(t *testing.T) {
	_, store := dbtest.NewTestDB(t)

	status := http.StatusInternalServerError
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()

	target, err := store.CreateWebhookTarget(context.Background(), &model.WebhookTarget{
		Name:    "persist-hook",
		URL:     srv.URL,
		Enabled: true,
	})
	require.NoError(t, err)
	require.Nil(t, target.LastStatus, "fresh target starts with no delivery state")
	require.Nil(t, target.LastError)
	require.Nil(t, target.LastDeliveryAt)

	svc := notify.New(store, notify.Config{
		HTTPTimeout:   2 * time.Second,
		RetryBackoffs: []time.Duration{}, // single attempt
	})
	defer svc.Stop()

	// 1) Real dispatch against a 500 endpoint → failed, code persisted.
	_, err = svc.Send(context.Background(), notify.Event{
		Event:    notify.EventFileUploaded,
		Severity: notify.SeverityInfo,
		Title:    "up",
		Body:     "/a.txt",
	})
	require.NoError(t, err)
	svc.Wait()

	got, err := store.GetWebhookTarget(context.Background(), target.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastStatus)
	assert.Equal(t, http.StatusInternalServerError, *got.LastStatus)
	require.NotNil(t, got.LastError)
	assert.Contains(t, *got.LastError, "HTTP 500")
	require.NotNil(t, got.LastDeliveryAt)
	assert.WithinDuration(t, time.Now().UTC(), got.LastDeliveryAt.UTC(), time.Minute)

	// 2) Admin test-fire against a now-healthy endpoint → success, error
	// cleared, code + timestamp updated.
	status = http.StatusNoContent
	res := svc.TestTarget(context.Background(), got)
	assert.Equal(t, "sent", res.Status)

	got, err = store.GetWebhookTarget(context.Background(), target.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastStatus)
	assert.Equal(t, http.StatusNoContent, *got.LastStatus)
	assert.Nil(t, got.LastError, "a successful delivery clears last_error")
	require.NotNil(t, got.LastDeliveryAt)
}

// TestTargetDelivery_TransportErrorPersistsZero — no HTTP response at
// all (connection refused) must persist last_status = 0 + the error.
func TestTargetDelivery_TransportErrorPersistsZero(t *testing.T) {
	_, store := dbtest.NewTestDB(t)

	// Grab a port nothing listens on.
	l, err := deadURL()
	require.NoError(t, err)

	target, err2 := store.CreateWebhookTarget(context.Background(), &model.WebhookTarget{
		Name:    "dead-hook",
		URL:     l,
		Enabled: true,
	})
	require.NoError(t, err2)

	svc := notify.New(store, notify.Config{
		HTTPTimeout:   time.Second,
		RetryBackoffs: []time.Duration{},
	})
	defer svc.Stop()

	res := svc.TestTarget(context.Background(), target)
	assert.Equal(t, "failed", res.Status)

	got, err3 := store.GetWebhookTarget(context.Background(), target.ID)
	require.NoError(t, err3)
	require.NotNil(t, got.LastStatus)
	assert.Equal(t, 0, *got.LastStatus, "transport failure has no HTTP status")
	require.NotNil(t, got.LastError)
	assert.NotEmpty(t, *got.LastError)
}

// deadURL returns a URL on a port that was just closed — connection
// refused territory on every platform.
func deadURL() (string, error) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	return url, nil
}
