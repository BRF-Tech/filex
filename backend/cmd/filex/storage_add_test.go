package main

// `filex storage add` wrote every storage as `poll` every 900 seconds: the
// other sync modes (fsnotify, ondemand, lazy) and the lazy catalogue's
// settings could only be set afterwards in the admin panel. The flags choose
// them now, held to the rules the admin API holds them to.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

func TestStorageAdd_HasTheSyncFlags(t *testing.T) {
	c := storageAddCmd()
	for _, f := range []string{"sync-mode", "sync-interval", "lazy-fill", "lazy-max-watches", "lazy-watch-ttl"} {
		assert.NotNil(t, c.Flags().Lookup(f), "--%s", f)
	}
	assert.Equal(t, string(model.SyncModePoll), c.Flags().Lookup("sync-mode").DefValue, "the default stays poll")
}

func TestStorageAdd_TheSyncModeIsChosen(t *testing.T) {
	for _, c := range []struct {
		mode string
		want model.SyncMode
	}{
		{"", model.SyncModePoll},
		{"poll", model.SyncModePoll},
		{"fsnotify", model.SyncModeFSNotify},
		{"ondemand", model.SyncModeOnDemand},
	} {
		m, interval, cfg, err := storageAddSync("local", c.mode, 3600, `{"root":"/srv"}`, nil)
		require.NoError(t, err, c.mode)
		assert.Equal(t, c.want, m, c.mode)
		assert.Equal(t, 3600, interval, c.mode)
		assert.JSONEq(t, `{"root":"/srv"}`, cfg, "%s: the config is left as given", c.mode)
	}
}

func TestStorageAdd_LazyWritesItsSettingsIntoTheConfig(t *testing.T) {
	m, _, cfg, err := storageAddSync("local", "lazy", 900, `{"root":"/srv"}`, map[string]any{
		storage.LazyFillKey:       storage.LazyFillOnOpen,
		storage.LazyMaxWatchesKey: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, model.SyncModeLazy, m)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(cfg), &got))
	assert.Equal(t, "/srv", got["root"], "the driver's own settings stay")
	assert.Equal(t, storage.LazyFillOnOpen, got[storage.LazyFillKey])
	assert.EqualValues(t, 50, got[storage.LazyMaxWatchesKey])
	_, set := got[storage.LazyWatchTTLKey]
	assert.False(t, set, "a lazy setting not given keeps its default (not written)")
}

func TestStorageAdd_RefusesWhatTheAdminAPIRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		driver, mode string
		interval     int
		lazy         map[string]any
	}{
		"lazy on a storage that is not local":  {"s3", "lazy", 900, nil},
		"a mode nothing implements":            {"local", "push", 900, nil},
		"a typo":                               {"local", "pol", 900, nil},
		"a lazy setting without the lazy mode": {"local", "poll", 900, map[string]any{storage.LazyFillKey: storage.LazyFillOnOpen}},
		"a lazy_fill that is not one of two":   {"local", "lazy", 900, map[string]any{storage.LazyFillKey: "sometimes"}},
		"lazy_max_watches below its bound":     {"local", "lazy", 900, map[string]any{storage.LazyMaxWatchesKey: 0}},
		"a scan interval that is not positive": {"local", "poll", 0, nil},
	} {
		_, _, _, err := storageAddSync(c.driver, c.mode, c.interval, `{}`, c.lazy)
		assert.Error(t, err, name)
	}
}
