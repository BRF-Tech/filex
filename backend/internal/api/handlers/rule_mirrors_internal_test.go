package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/tagname"
)

// B12: every tag item carries the server's identity for it, so a client
// tells two spellings of one tag apart by the server's rule (full case
// folding: "STRASSE" and "straße" are one tag, which the browser's copy
// could not see).
func TestTagItems_CarryTheServersKey(t *testing.T) {
	items := itemsOf([]*model.Tag{
		{ID: 1, Kind: "personal", Name: "IŞIK"},
		{ID: 2, Kind: "team", Name: "straße"},
	})
	require.Len(t, items, 2)
	for _, it := range items {
		assert.Equal(t, tagname.Key(it.Name), it.Key, it.Name)
		assert.NotEmpty(t, it.Key)
	}
	assert.Equal(t, tagname.Key("STRASSE"), tagname.Key("straße"), "the case the client copy missed")
}

// B16: the rule behind "this event cannot happen here", once, on the server
// (it was lib/webhookEvents eventOffReason + eventFixableBy).
func TestEventsOff_TheRule(t *testing.T) {
	everything := eventFacts{antivirus: true, escrow: true, appPlugins: true, approval: true, accountAdmin: true, callerAdmin: true}
	assert.Empty(t, eventsOff(everything), "with every service on, every event can happen")

	nothing := eventsOff(eventFacts{})
	for ev, reason := range map[string]string{
		"file.infected":       "antivirus",
		"e2e.escrow_used":     "escrow",
		"plugin.notice":       "app_plugins",
		"e2e.request_created": "e2e_approval",
		"e2e.request_decided": "e2e_approval",
	} {
		require.Contains(t, nothing, ev)
		assert.Equal(t, reason, nothing[ev].Reason, ev)
		assert.False(t, nothing[ev].Fixable, ev)
	}

	// A new request is sent to administrator accounts alone: a member is not
	// offered it even where the policy asks for approval.
	member := eventsOff(eventFacts{antivirus: true, escrow: true, appPlugins: true, approval: true})
	assert.Contains(t, member, "e2e.request_created")
	assert.NotContains(t, member, "e2e.request_decided", "the answer reaches the person who asked")

	// Who could switch it on: the instance's administrator for a service, the
	// account's own administrator role for the tenant's encryption policy.
	tenantAdmin := eventsOff(eventFacts{accountAdmin: true})
	assert.False(t, tenantAdmin["file.infected"].Fixable, "a tenant admin cannot set the instance up")
	assert.True(t, tenantAdmin["e2e.request_created"].Fixable)
	assert.True(t, tenantAdmin["e2e.request_decided"].Fixable)
	operator := eventsOff(eventFacts{callerAdmin: true})
	assert.True(t, operator["file.infected"].Fixable)
	assert.False(t, operator["e2e.request_decided"].Fixable)
}

// Every reason has its sentence in the server catalogue (srvtext.Text gives
// the key back for a missing one, lesson #1295).
func TestEventsOff_EveryReasonIsSaid(t *testing.T) {
	for reason, key := range eventOffKeys {
		assert.True(t, srvtext.Has(key), "%s: %s is not in the catalogue", reason, key)
	}
	for _, o := range eventsOff(eventFacts{}) {
		_, ok := eventOffKeys[o.Reason]
		assert.True(t, ok, o.Reason)
	}
}
