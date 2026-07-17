package handlers_test

// Surface tests for the writehook gate: an AI upload must flow through
// the single post-write door — antivirus enqueue + a canonical
// file.uploaded event stamped with origin "ai".

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// whFakeSink captures notify.Send calls from the writehook (emission is
// async — consumers wait on the channel).
type whFakeSink struct {
	notify.Service
	mu     sync.Mutex
	events []notify.Event
	ch     chan notify.Event
}

func (f *whFakeSink) Send(_ context.Context, e notify.Event) (int64, error) {
	f.mu.Lock()
	f.events = append(f.events, e)
	f.mu.Unlock()
	f.ch <- e
	return 1, nil
}

func (f *whFakeSink) wait(t *testing.T) notify.Event {
	t.Helper()
	select {
	case e := <-f.ch:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no writehook event within 2s")
		return notify.Event{}
	}
}

// TestAIUpload_WritehookAVAndEvent — POST /api/ai/upload must enqueue an
// antivirus scan for the persisted node AND emit file.uploaded with
// origin "ai". (Both used to be manager-upload-only.)
func TestAIUpload_WritehookAVAndEvent(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	// aiFixture builds the router with nil Notify/AVScan (writehook gets
	// Configure(nil, nil) inside BuildRouter) — install fakes afterwards;
	// the hooks read the package sinks at call time.
	sink := &whFakeSink{ch: make(chan notify.Event, 8)}
	var (
		scanMu  sync.Mutex
		scanned []*model.Node
	)
	writehook.Configure(func(_ context.Context, n *model.Node) {
		scanMu.Lock()
		scanned = append(scanned, n)
		scanMu.Unlock()
	}, sink)
	t.Cleanup(func() { writehook.Configure(nil, nil) })

	// Root-level target → the parent walk succeeds and the node persists.
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path":    "main://scan-me.txt",
		"content": "writehook upload",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Event: canonical file.uploaded, stamped origin "ai".
	e := sink.wait(t)
	assert.Equal(t, notify.EventFileUploaded, e.Event)
	require.NotNil(t, e.Node)
	assert.Equal(t, "/scan-me.txt", e.Node.Path)
	assert.Equal(t, "scan-me.txt", e.Node.Name)
	assert.Equal(t, writehook.OriginAI, e.Meta["origin"])

	// AV: enqueued synchronously with the persisted node.
	scanMu.Lock()
	defer scanMu.Unlock()
	require.Len(t, scanned, 1, "AI upload must enqueue exactly one antivirus scan")
	assert.Equal(t, "/scan-me.txt", scanned[0].Path)
	assert.NotZero(t, scanned[0].ID, "scan must reference the persisted node")
}
