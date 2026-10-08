package realtime

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitFrame waits for one frame on c (the access frame is sent from a timer,
// not from the call), or fails.
func waitFrame(t *testing.T, c *Client) map[string]any {
	t.Helper()
	select {
	case raw := <-c.Send:
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		return m
	case <-time.After(2 * time.Second):
		t.Fatalf("no frame for %s", c.Name)
		return nil
	}
}

// quiet asserts nothing reached c within a moment longer than the gathering
// delay.
func quiet(t *testing.T, h *Hub, c *Client) {
	t.Helper()
	time.Sleep(h.accessDelay + 60*time.Millisecond)
	assert.Empty(t, c.Send, "%s heard a frame meant for somebody else", c.Name)
}

// #196 - a change of what somebody may do reaches every socket of THAT person
// (two tabs, an embed, the desktop app) and nobody else, and the frame names
// no path, no person and no reason.
func TestHub_AccessChangedReachesOnlyTheNamedPeople(t *testing.T) {
	h := NewHub()
	h.accessDelay = 10 * time.Millisecond
	ayseTab := NewClient(1, "Ayşe", 4)
	ayseDesktop := NewClient(1, "Ayşe", 4)
	cem := NewClient(2, "Cem", 4)
	for _, c := range []*Client{ayseTab, ayseDesktop, cem} {
		h.Connect(c)
	}
	h.Subscribe(ayseTab, 1, "reports", "main://reports")
	drainAll(ayseTab)

	h.AccessChanged(1)

	for _, c := range []*Client{ayseTab, ayseDesktop} {
		m := waitFrame(t, c)
		assert.Equal(t, map[string]any{"type": "access.changed"}, m, "nothing but the type: no path, no person, no reason")
	}
	quiet(t, h, cem)
}

// A change at the platform level (the platform's roles or policy, any change
// on a single-tenant install) tells everybody connected, in a room or not, in
// any tenant, and says that it did so the clients spread their questions.
func TestHub_AccessChangedEverywhereSaysSo(t *testing.T) {
	h := NewHub()
	h.accessDelay = 10 * time.Millisecond
	inRoom := NewClient(1, "Ayşe", 4)
	noRoom := NewClient(2, "Cem", 4)
	noRoom.Tenant = 7
	h.Connect(inRoom)
	h.Connect(noRoom)
	h.Subscribe(inRoom, 1, "", "main://")
	drainAll(inRoom)

	h.AccessChangedEverywhere()

	for _, c := range []*Client{inRoom, noRoom} {
		assert.Equal(t, map[string]any{"type": "access.changed", "scope": "all"}, waitFrame(t, c), c.Name)
	}
}

// A burst (a group edit writes several rows, each one invalidating the
// permissions) is one frame per socket, and a named change inside a burst for
// everybody is the "all" frame, once.
func TestHub_AccessChangedBurstIsOneFrame(t *testing.T) {
	h := NewHub()
	h.accessDelay = 40 * time.Millisecond
	a := NewClient(1, "Ayşe", 8)
	b := NewClient(2, "Cem", 8)
	h.Connect(a)
	h.Connect(b)

	h.AccessChanged(1)
	h.AccessChanged(1)
	h.AccessChanged(2)
	h.AccessChangedEverywhere()
	h.AccessChanged(1)

	assert.Equal(t, "all", waitFrame(t, a)["scope"])
	assert.Equal(t, "all", waitFrame(t, b)["scope"])
	quiet(t, h, a)
	quiet(t, h, b)

	// The next change after the burst is a burst of its own.
	h.AccessChanged(2)
	m := waitFrame(t, b)
	assert.Equal(t, "access.changed", m["type"])
	_, all := m["scope"]
	assert.False(t, all, "a named change after an \"all\" burst is not \"all\" again")
	quiet(t, h, a)
}

// A socket that is not reading misses the frame rather than holding the
// others up, and a socket that has closed hears nothing.
func TestHub_AccessChangedNeverBlocks(t *testing.T) {
	h := NewHub()
	h.accessDelay = 5 * time.Millisecond
	stalled := NewClient(1, "Ayşe", 1)
	stalled.Send <- []byte(`{"type":"pong"}`)
	reader := NewClient(1, "Ayşe", 4)
	gone := NewClient(1, "Ayşe", 4)
	for _, c := range []*Client{stalled, reader, gone} {
		h.Connect(c)
	}
	h.Unsubscribe(gone)

	done := make(chan struct{})
	go func() {
		h.AccessChanged(1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AccessChanged blocked")
	}
	assert.Equal(t, "access.changed", waitFrame(t, reader)["type"])
	assert.Len(t, stalled.Send, 1, "the stalled reader still holds only what it had")
	assert.Empty(t, gone.Send)
}

// Nobody named, nobody told: no id, an id of 0 (no person) or a tenant of 0
// (the platform's accounts are not a tenant) arms nothing.
func TestHub_AccessChangedForNobodyArmsNothing(t *testing.T) {
	h := NewHub()
	h.accessDelay = 5 * time.Millisecond
	c := NewClient(0, "anon", 4)
	h.Connect(c)
	h.AccessChanged()
	h.AccessChanged(0)
	h.AccessChangedInTenant(0)
	h.mu.Lock()
	armed := h.access.timer != nil
	h.mu.Unlock()
	assert.False(t, armed)
	quiet(t, h, c)
}

// A tenant's change reaches every socket of THAT tenant's accounts - named or
// not, in a room or not - and no socket of another tenant, and none of the
// platform's own accounts: the frame's timing alone would tell them that
// something changed there.
func TestHub_AccessChangedInTenantStaysInTheTenant(t *testing.T) {
	h := NewHub()
	h.accessDelay = 10 * time.Millisecond
	alpha1 := NewClient(11, "Ayşe", 4)
	alpha1.Tenant = 5
	alpha2 := NewClient(12, "Ali", 4)
	alpha2.Tenant = 5
	bravo := NewClient(21, "Cem", 4)
	bravo.Tenant = 6
	platform := NewClient(1, "Operatör", 4)
	for _, c := range []*Client{alpha1, alpha2, bravo, platform} {
		h.Connect(c)
	}
	h.Subscribe(alpha1, 1, "docs", "alpha://docs")
	h.Subscribe(bravo, 2, "docs", "bravo://docs")
	drainAll(alpha1)
	drainAll(bravo)

	h.AccessChangedInTenant(5)

	for _, c := range []*Client{alpha1, alpha2} {
		assert.Equal(t, map[string]any{"type": "access.changed", "scope": "all"}, waitFrame(t, c), c.Name)
	}
	quiet(t, h, bravo)
	quiet(t, h, platform)

	// A named account of another tenant in the same burst hears the plain
	// frame, once; the tenant's own sockets still hear theirs once.
	h.AccessChangedInTenant(5)
	h.AccessChanged(21)
	assert.Equal(t, "all", waitFrame(t, alpha1)["scope"])
	m := waitFrame(t, bravo)
	_, all := m["scope"]
	assert.False(t, all, "named, not the whole of tenant 6")
	quiet(t, h, platform)
	quiet(t, h, bravo)
}
