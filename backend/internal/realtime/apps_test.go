package realtime

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An approved app version reaches every open socket — in a folder room or
// not — and nobody who has left. A reader that is not reading misses it
// instead of holding the others up.
func TestHub_AppUpdatedReachesEveryOpenSocketAndNobodyElse(t *testing.T) {
	h := NewHub()
	inRoom := NewClient(1, "Ayşe", 4)
	h.Connect(inRoom)
	h.Subscribe(inRoom, 1, "reports", "main://reports")
	drainAll(inRoom)
	noRoom := NewClient(2, "Cem", 4)
	h.Connect(noRoom)
	gone := NewClient(3, "Deniz", 4)
	h.Connect(gone)
	h.Unsubscribe(gone)
	stalled := NewClient(4, "Ece", 1)
	h.Connect(stalled)
	stalled.Send <- []byte(`{"type":"pong"}`)

	done := make(chan struct{})
	go func() {
		h.AppUpdated("drawio", "1.1.0")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a stalled reader held the notice up")
	}

	for _, c := range []*Client{inRoom, noRoom} {
		require.Len(t, c.Send, 1, c.Name)
		var m map[string]string
		require.NoError(t, json.Unmarshal(<-c.Send, &m))
		assert.Equal(t, map[string]string{"type": "app.updated", "app": "drawio", "version": "1.1.0"}, m)
	}
	assert.Empty(t, gone.Send, "a socket that closed hears nothing")
	h.mu.Lock()
	_, still := h.connected[gone]
	h.mu.Unlock()
	assert.False(t, still, "and is forgotten")
}
