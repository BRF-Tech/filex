package handlers

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/model"
)

// A save that changes nothing a scan reads leaves the scan alone — on every
// engine, whatever form the engine keeps the config JSON in.
//
// RED PROOF (PR #66, 2026-09-26): PostgreSQL (JSONB) and MySQL hand the config
// back reordered and re-spaced, and the byte comparison called the same config
// a change — every save of an unchanged storage restarted its syncer and
// aborted the running scan there.
func TestScanSettingsChanged_TheSameConfigSpelledAnotherWayIsNoChange(t *testing.T) {
	stored := &model.Storage{
		Driver: "s3", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
		// What JSONB returns: keys by length then bytes, ", " and ": ".
		ConfigJSON: json.RawMessage(`{"bucket": "arsiv", "region": "eu-central", "endpoint": "https://s3.example", "scan_exclude": ["*.tmp", "cache/"]}`),
	}
	sent := *stored
	sent.ConfigJSON = json.RawMessage(`{"endpoint":"https://s3.example","bucket":"arsiv","region":"eu-central","scan_exclude":["*.tmp","cache/"]}`)
	assert.False(t, scanSettingsChanged(stored, &sent), "the same config in another spelling restarted the scan")

	changed := sent
	changed.ConfigJSON = json.RawMessage(`{"endpoint":"https://s3.example","bucket":"arsiv","region":"eu-west","scan_exclude":["*.tmp","cache/"]}`)
	assert.True(t, scanSettingsChanged(stored, &changed), "a changed region is a change")

	reordered := sent
	reordered.ConfigJSON = json.RawMessage(`{"endpoint":"https://s3.example","bucket":"arsiv","region":"eu-central","scan_exclude":["cache/","*.tmp"]}`)
	assert.True(t, scanSettingsChanged(stored, &reordered), "the order of a list is part of what it says")
}
