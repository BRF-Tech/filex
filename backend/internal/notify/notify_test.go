package notify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// TestSend_RecordsAndDelivers covers the happy path: an event hits the
// store, the webhook receives the JSON body, and the row's webhook
// status moves to "sent".
func TestSend_RecordsAndDelivers(t *testing.T) {
	_, store := dbtest.NewTestDB(t)

	var (
		bodySeen []byte
		hits     atomic.Int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body := make([]byte, 4096)
		n, _ := r.Body.Read(body)
		bodySeen = append(bodySeen[:0], body[:n]...)
		// Verify content type.
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer secret-token-xx", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	svc := notify.New(store, notify.Config{
		WebhookURL:    srv.URL,
		WebhookToken:  "secret-token-xx",
		HTTPTimeout:   2 * time.Second,
		RetryBackoffs: []time.Duration{},
	})
	defer svc.Stop()

	id, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventReplicaFail,
		Severity: notify.SeverityWarning,
		Title:    "Replica write failed",
		Body:     "fileman/foo.pdf — connection timeout",
		Meta:     map[string]any{"path": "fileman/foo.pdf", "op": "write"},
	})
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	svc.Wait() // wait for the dispatch goroutine
	assert.Equal(t, int32(1), hits.Load(), "webhook hit exactly once")

	// Verify body shape.
	var got map[string]any
	require.NoError(t, json.Unmarshal(bodySeen, &got))
	assert.Equal(t, string(notify.EventReplicaFail), got["event"])
	assert.Equal(t, string(notify.SeverityWarning), got["severity"])

	// DB state: webhook_status should be sent.
	row, err := store.GetNotification(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "sent", row.WebhookStatus)
}

// TestSend_RetriesThenSucceeds — server returns 500 twice then 200,
// webhook should retry within the budget and end up "sent".
func TestSend_RetriesThenSucceeds(t *testing.T) {
	_, store := dbtest.NewTestDB(t)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := notify.New(store, notify.Config{
		WebhookURL:    srv.URL,
		HTTPTimeout:   2 * time.Second,
		RetryBackoffs: []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond},
	})
	defer svc.Stop()

	id, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventReplicaFail,
		Severity: notify.SeverityWarning,
		Title:    "retry test",
	})
	require.NoError(t, err)

	svc.Wait()
	assert.GreaterOrEqual(t, hits.Load(), int32(3))

	row, err := store.GetNotification(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "sent", row.WebhookStatus)
}

// TestSend_AllRetriesFail — server always 500s; after exhausting the
// budget the row is marked "failed".
func TestSend_AllRetriesFail(t *testing.T) {
	_, store := dbtest.NewTestDB(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	svc := notify.New(store, notify.Config{
		WebhookURL:    srv.URL,
		HTTPTimeout:   2 * time.Second,
		RetryBackoffs: []time.Duration{5 * time.Millisecond, 5 * time.Millisecond, 5 * time.Millisecond},
	})
	defer svc.Stop()

	id, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventReplicaFail,
		Severity: notify.SeverityWarning,
		Title:    "doomed",
	})
	require.NoError(t, err)
	svc.Wait()

	row, err := store.GetNotification(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "failed", row.WebhookStatus)
	assert.Contains(t, row.WebhookError, "HTTP 500")
}

// TestSend_NoWebhook — when no URL is configured, in-app row still
// persists and webhook_status becomes "skipped".
func TestSend_NoWebhook(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	svc := notify.New(store, notify.Config{})
	defer svc.Stop()

	id, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventReplicaFail,
		Severity: notify.SeverityInfo,
		Title:    "no webhook test",
	})
	require.NoError(t, err)
	svc.Wait()

	row, err := store.GetNotification(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "skipped", row.WebhookStatus)
}

// TestList_UserScopedAndBroadcast — list filters by user_id but
// includes broadcasts.
func TestList_UserScopedAndBroadcast(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	svc := notify.New(store, notify.Config{})
	defer svc.Stop()

	// Real users so the FK constraint is satisfied.
	uid1 := dbtest.SeedUserWithRole(t, store, "u1@test", "pw11111111", "user")
	uid2 := dbtest.SeedUserWithRole(t, store, "u2@test", "pw22222222", "user")

	// Each gets a private one + one broadcast.
	_, err := svc.Send(context.Background(), notify.Event{Event: "e1", Severity: notify.SeverityInfo, Title: "t1", UserID: &uid1})
	require.NoError(t, err)
	_, err = svc.Send(context.Background(), notify.Event{Event: "e2", Severity: notify.SeverityInfo, Title: "t2", UserID: &uid2})
	require.NoError(t, err)
	_, err = svc.Send(context.Background(), notify.Event{Event: "e3", Severity: notify.SeverityInfo, Title: "t3"})
	require.NoError(t, err)

	rows1, total1, err := svc.List(context.Background(), &uid1, false, 100, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total1, "user 1 sees own + broadcast")
	assert.Len(t, rows1, 2)

	rows2, total2, err := svc.List(context.Background(), &uid2, false, 100, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total2, "user 2 sees own + broadcast")
	assert.Len(t, rows2, 2)

	// Admin (nil) sees them all.
	all, totalAll, err := svc.List(context.Background(), nil, false, 100, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 3, totalAll)
	assert.Len(t, all, 3)
}

// TestMarkAllRead — clears unread for one user.
func TestMarkAllRead(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	svc := notify.New(store, notify.Config{})
	defer svc.Stop()

	uid := dbtest.SeedUserWithRole(t, store, "alice@test", "pw11111111", "user")
	_, err := svc.Send(context.Background(), notify.Event{Event: "a", Severity: notify.SeverityInfo, Title: "a", UserID: &uid})
	require.NoError(t, err)
	_, err = svc.Send(context.Background(), notify.Event{Event: "b", Severity: notify.SeverityInfo, Title: "b", UserID: &uid})
	require.NoError(t, err)

	count, err := svc.UnreadCount(context.Background(), &uid)
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)

	require.NoError(t, svc.MarkAllRead(context.Background(), &uid))

	count, err = svc.UnreadCount(context.Background(), &uid)
	require.NoError(t, err)
	assert.EqualValues(t, 0, count)
}

// TestSignature_KnownVector — the X-Filex-Signature format is pinned to
// "sha256=" + hex(HMAC-SHA256(secret, body)) with an independently
// computed vector (openssl dgst -sha256 -hmac secret).
func TestSignature_KnownVector(t *testing.T) {
	got := notify.Signature("secret", []byte("body"))
	assert.Equal(t, "sha256=dc46983557fea127b43af721467eb9b3fde2338fe3e14f51952aa8478c13d355", got)
}

// TestSend_TargetsFanoutFilterAndSignature — webhook v2: an event fans
// out to the legacy global webhook AND every enabled, event-matching
// webhook_targets row. The filtered-out target receives nothing; the
// matching one gets X-Filex-Event + X-Filex-Delivery + a verifiable
// HMAC signature.
func TestSend_TargetsFanoutFilterAndSignature(t *testing.T) {
	_, store := dbtest.NewTestDB(t)

	var legacyHits atomic.Int32
	legacySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		legacyHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer legacySrv.Close()

	var (
		t1Hits  atomic.Int32
		t1Body  []byte
		t1Event string
		t1Deliv string
		t1Sig   string
	)
	t1Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t1Hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		t1Body = b
		t1Event = r.Header.Get("X-Filex-Event")
		t1Deliv = r.Header.Get("X-Filex-Delivery")
		t1Sig = r.Header.Get("X-Filex-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer t1Srv.Close()

	var t2Hits atomic.Int32
	t2Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t2Hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer t2Srv.Close()

	// t1 wants everything (empty filter) and signs; t2 only wants
	// file.deleted so the file.uploaded below must not reach it.
	_, err := store.CreateWebhookTarget(context.Background(), &model.WebhookTarget{
		Name: "t1", URL: t1Srv.URL, Secret: "s3cret-t1", Enabled: true,
	})
	require.NoError(t, err)
	_, err = store.CreateWebhookTarget(context.Background(), &model.WebhookTarget{
		Name: "t2", URL: t2Srv.URL, Events: "file.deleted", Enabled: true,
	})
	require.NoError(t, err)

	svc := notify.New(store, notify.Config{
		WebhookURL:    legacySrv.URL,
		HTTPTimeout:   2 * time.Second,
		RetryBackoffs: []time.Duration{},
	})
	defer svc.Stop()

	id, err := svc.Send(context.Background(), notify.Event{
		Event:    notify.EventFileUploaded,
		Severity: notify.SeverityInfo,
		Title:    "upload",
		Node:     &notify.NodeRef{StorageID: 7, Path: "/docs/a.txt", Name: "a.txt", Size: 42},
	})
	require.NoError(t, err)
	svc.Wait()

	assert.Equal(t, int32(1), legacyHits.Load(), "legacy global webhook still fires")
	assert.Equal(t, int32(1), t1Hits.Load(), "matching target fires")
	assert.Equal(t, int32(0), t2Hits.Load(), "event-filtered target stays silent")

	// Contract headers on the target delivery.
	assert.Equal(t, string(notify.EventFileUploaded), t1Event)
	assert.NotEmpty(t, t1Deliv, "X-Filex-Delivery uuid present")
	assert.Equal(t, notify.Signature("s3cret-t1", t1Body), t1Sig, "HMAC signature verifies over the received body")

	// Structured payload on the wire.
	var got map[string]any
	require.NoError(t, json.Unmarshal(t1Body, &got))
	assert.Equal(t, string(notify.EventFileUploaded), got["event"])
	require.Contains(t, got, "at")
	node, ok := got["node"].(map[string]any)
	require.True(t, ok, "payload carries node object")
	assert.Equal(t, "/docs/a.txt", node["path"])

	// Aggregated status: everything delivered → sent.
	row, err := store.GetNotification(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "sent", row.WebhookStatus)

	// In-memory last-status map has the matching target only.
	statuses := svc.TargetStatuses()
	require.Len(t, statuses, 1)
	for _, st := range statuses {
		assert.Equal(t, "sent", st.Status)
	}
}

// TestSend_TargetOnly_NoLegacy — with no global webhook but one enabled
// target the event is delivered (not "skipped").
func TestSend_TargetOnly_NoLegacy(t *testing.T) {
	_, store := dbtest.NewTestDB(t)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := store.CreateWebhookTarget(context.Background(), &model.WebhookTarget{
		Name: "only", URL: srv.URL, Enabled: true,
	})
	require.NoError(t, err)

	svc := notify.New(store, notify.Config{HTTPTimeout: 2 * time.Second, RetryBackoffs: []time.Duration{}})
	defer svc.Stop()

	id, err := svc.Send(context.Background(), notify.Event{
		Event: notify.EventShareCreated, Severity: notify.SeverityInfo, Title: "s",
	})
	require.NoError(t, err)
	svc.Wait()

	assert.Equal(t, int32(1), hits.Load())
	row, err := store.GetNotification(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "sent", row.WebhookStatus)
}

// TestSend_DisabledTargetSkipped — a disabled target does not count as
// a destination; with nothing else configured the row is "skipped".
func TestSend_DisabledTargetSkipped(t *testing.T) {
	_, store := dbtest.NewTestDB(t)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := store.CreateWebhookTarget(context.Background(), &model.WebhookTarget{
		Name: "off", URL: srv.URL, Enabled: false,
	})
	require.NoError(t, err)

	svc := notify.New(store, notify.Config{HTTPTimeout: 2 * time.Second, RetryBackoffs: []time.Duration{}})
	defer svc.Stop()

	id, err := svc.Send(context.Background(), notify.Event{
		Event: notify.EventFileDeleted, Severity: notify.SeverityInfo, Title: "x",
	})
	require.NoError(t, err)
	svc.Wait()

	assert.Equal(t, int32(0), hits.Load())
	row, err := store.GetNotification(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "skipped", row.WebhookStatus)
}

// TestSend_TargetFailureMarksFailed — one failing target among two
// destinations flips the aggregate to failed and names the target.
func TestSend_TargetFailureMarksFailed(t *testing.T) {
	_, store := dbtest.NewTestDB(t)

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badSrv.Close()

	_, err := store.CreateWebhookTarget(context.Background(), &model.WebhookTarget{
		Name: "good", URL: okSrv.URL, Enabled: true,
	})
	require.NoError(t, err)
	_, err = store.CreateWebhookTarget(context.Background(), &model.WebhookTarget{
		Name: "bad", URL: badSrv.URL, Enabled: true,
	})
	require.NoError(t, err)

	svc := notify.New(store, notify.Config{
		HTTPTimeout:   2 * time.Second,
		RetryBackoffs: []time.Duration{5 * time.Millisecond},
	})
	defer svc.Stop()

	id, err := svc.Send(context.Background(), notify.Event{
		Event: notify.EventFileMoved, Severity: notify.SeverityInfo, Title: "mv",
	})
	require.NoError(t, err)
	svc.Wait()

	row, err := store.GetNotification(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "failed", row.WebhookStatus)
	assert.Contains(t, row.WebhookError, "bad: HTTP 500")
}

// TestSetWebhook_Live — runtime webhook URL change is honoured.
func TestSetWebhook_Live(t *testing.T) {
	_, store := dbtest.NewTestDB(t)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := notify.New(store, notify.Config{HTTPTimeout: 2 * time.Second, RetryBackoffs: []time.Duration{}})
	defer svc.Stop()

	// First send — no webhook → skipped.
	id1, _ := svc.Send(context.Background(), notify.Event{Event: "x", Severity: notify.SeverityInfo, Title: "x"})
	svc.Wait()
	row, _ := store.GetNotification(context.Background(), id1)
	assert.Equal(t, "skipped", row.WebhookStatus)

	// Now configure and re-send.
	svc.SetWebhook(srv.URL, "")
	id2, _ := svc.Send(context.Background(), notify.Event{Event: "y", Severity: notify.SeverityInfo, Title: "y"})
	svc.Wait()
	row, _ = store.GetNotification(context.Background(), id2)
	assert.Equal(t, "sent", row.WebhookStatus)
	assert.Equal(t, int32(1), hits.Load())

	url, set := svc.WebhookConfig()
	assert.Equal(t, srv.URL, url)
	assert.False(t, set)
}
