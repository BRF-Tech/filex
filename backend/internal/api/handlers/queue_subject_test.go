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

// The Queue page says WHAT a job is about. Its "Content" column printed the
// payload as JSON — `{"node_id":8}` (release-candidate sweep, 2026-09-21) —
// an internal id nobody can look up. The list now names the file.
func TestQueue_ListNamesWhatAJobIsAbout(t *testing.T) {
	ctx := context.Background()
	conn, store := testutil.NewTestDB(t)
	q := queue.MustGet("sqlite")
	if err := q.Init(ctx, map[string]any{"db": conn}); err != nil {
		t.Fatal(err)
	}
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "depo", Driver: "local", Enabled: true, ConfigJSON: []byte(`{"path":"/tmp/x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, Path: "/docs/rapor.pdf", Name: "rapor.pdf", Type: model.NodeTypeFile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(ctx, queue.Op{Type: queue.TypeContentIndex, Payload: map[string]any{"node_id": n.ID}}); err != nil {
		t.Fatal(err)
	}

	h := handlers.NewQueue(q)
	h.AttachStore(store)
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/admin/queue", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			Type    string `json:"type"`
			Subject string `json:"subject"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Subject != "depo:/docs/rapor.pdf" {
		t.Fatalf("the job does not name its file: %+v", body.Items)
	}
}
