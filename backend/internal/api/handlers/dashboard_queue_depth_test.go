package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/queue"
	_ "github.com/brf-tech/filex/backend/internal/queue/drivers/sqlite"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// "Queue depth" on the Panel is the job queue's backlog: pending plus running,
// the two numbers the Queue page shows. It used to be the number of storage
// watchers the sync worker had started — the release-candidate sweep
// (2026-09-21) read "KUYRUK DERİNLİĞİ 2" on the Panel while the Queue page said
// 0 pending, 0 running. Here: two enabled storages (the old answer would be 2
// with a started worker), and a queue holding two pending, one running, one
// failed and one finished job — the depth is 3, and the failed and finished
// ones do not count.
func TestDashboard_QueueDepthIsTheJobQueueBacklog(t *testing.T) {
	ctx := context.Background()
	conn, store := testutil.NewTestDB(t)

	q := queue.MustGet("sqlite")
	if err := q.Init(ctx, map[string]any{"db": conn}); err != nil {
		t.Fatalf("queue init: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := q.Enqueue(ctx, queue.Op{Type: queue.TypeThumb}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	done, _ := q.Dequeue(ctx, nil)
	_ = q.Ack(ctx, done.ID)
	failed, _ := q.Dequeue(ctx, nil)
	_ = q.Fail(ctx, failed.ID, "no", false)
	_, _ = q.Dequeue(ctx, nil) // stays running
	// two stay pending

	for _, name := range []string{"one", "two"} {
		if _, err := store.CreateStorage(ctx, &model.Storage{Name: name, Driver: "local", Enabled: true, ConfigJSON: []byte(`{"path":"/tmp/x"}`)}); err != nil {
			t.Fatalf("storage: %v", err)
		}
	}

	h := handlers.NewDashboard(store, nil, q)
	rec := httptest.NewRecorder()
	h.Get(rec, httptest.NewRequest(http.MethodGet, "/api/admin/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		QueueDepth int `json:"queue_depth"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.QueueDepth != 3 {
		t.Fatalf("queue_depth = %d, want 3 (2 pending + 1 running; failed and done excluded)", body.QueueDepth)
	}

	// No queue at all (FILEX_QUEUE off): an empty queue, not a crash.
	rec = httptest.NewRecorder()
	handlers.NewDashboard(store, nil, nil).Get(rec, httptest.NewRequest(http.MethodGet, "/api/admin/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard without a queue: %d", rec.Code)
	}
}
