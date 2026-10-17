package instancesettings

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
)

// memStore is the settings table, in memory.
type memStore map[string]string

func (m memStore) GetSetting(_ context.Context, key string) (string, error) { return m[key], nil }
func (m memStore) UpsertSetting(_ context.Context, key, value string) error {
	m[key] = value
	return nil
}

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// ── the resolution order: db → configuration (env / config.yaml) → default ──

func TestResolutionOrder_TheStoredRowWins(t *testing.T) {
	st := memStore{"sync.default_interval_minutes": "45"}
	cfg := config.Default()
	cfg.Sync.DefaultInterval = 5 * time.Minute // what FILEX_SYNC_INTERVAL=5m would give

	Boot(context.Background(), st, &cfg)

	assert.Equal(t, 45*time.Minute, cfg.Sync.DefaultInterval, "the row, not the environment")
	assert.Equal(t, "45", st["sync.default_interval_minutes"], "the row is not overwritten")
}

func TestResolutionOrder_TheConfigurationSeedsAStartWithNoRow(t *testing.T) {
	st := memStore{}
	cfg := config.Default()
	cfg.Sync.DefaultInterval = 5 * time.Minute

	Boot(context.Background(), st, &cfg)

	assert.Equal(t, "5", st["sync.default_interval_minutes"])
	assert.Equal(t, 5*time.Minute, cfg.Sync.DefaultInterval)
}

func TestResolutionOrder_TheDefaultWhenNeitherSaysAnything(t *testing.T) {
	st := memStore{}
	cfg := config.Default()

	Boot(context.Background(), st, &cfg)

	_, seeded := st["sync.default_interval_minutes"]
	assert.False(t, seeded, "a default is not pinned into a row: a later release may change it")
	assert.Equal(t, 15*time.Minute, cfg.Sync.DefaultInterval)
}

// Every setting in the list round-trips: a row with a value that is not the
// default ends up in the configuration the server is built from.
func TestBoot_EverySettingLandsInTheConfiguration(t *testing.T) {
	for _, s := range All() {
		t.Run(s.Key, func(t *testing.T) {
			st := memStore{}
			var want any
			switch s.Kind {
			case KindInt:
				min, _ := s.Bounds()
				want = min + 1
			case KindBool:
				want = !s.Default().(bool)
			case KindLanguage:
				want = "tr"
			}
			st[s.Key] = s.text(want)
			if s.Key == "thumbs.sweep_interval_minutes" {
				st["thumbs.sweep"] = "true"
			}
			cfg := config.Default()
			Boot(context.Background(), st, &cfg)
			got, _ := s.fromConfig(&cfg)
			assert.Equal(t, want, got)
		})
	}
}

func TestBoot_UnitsOfTheOldVariables(t *testing.T) {
	st := memStore{}
	cfg := config.Default()
	cfg.Cache.MaxBytes = 3*gib + 1           // FILEX_CACHE_MAX_BYTES, bytes
	cfg.Upload.StagingTTL = 90 * time.Minute // FILEX_UPLOAD_STAGING_TTL, a duration
	cfg.Sync.DefaultInterval = 30 * time.Second

	Boot(context.Background(), st, &cfg)

	assert.Equal(t, "4", st["cache.max_gb"], "bytes are rounded UP to whole GB")
	assert.Equal(t, "2", st["upload.staging_ttl_hours"], "a duration is rounded up to whole hours")
	assert.Equal(t, "1", st["sync.default_interval_minutes"], "30 s is one minute, never zero")
}

func TestBoot_AnOutOfRangeConfigurationIsClamped(t *testing.T) {
	st := memStore{}
	cfg := config.Default()
	cfg.CoEdit.MaxMembers = 100000

	Boot(context.Background(), st, &cfg)

	assert.Equal(t, "200", st["coedit.max_members"])
	assert.Equal(t, 200, cfg.CoEdit.MaxMembers)
}

func TestBoot_TheThumbnailSweepSwitchedOffStaysOff(t *testing.T) {
	st := memStore{}
	cfg := config.Default()
	cfg.Thumbs.SweepInterval = 0 // FILEX_THUMBS_SWEEP_INTERVAL=0

	Boot(context.Background(), st, &cfg)

	assert.Equal(t, "false", st["thumbs.sweep"])
	assert.Equal(t, time.Duration(0), cfg.Thumbs.SweepInterval)

	// Switched on again on the page: the interval in force is the default's.
	st["thumbs.sweep"] = "true"
	cfg = config.Default()
	Boot(context.Background(), st, &cfg)
	assert.Equal(t, 6*time.Hour, cfg.Thumbs.SweepInterval)
}

// ── writing from the page ────────────────────────────────────────────────

func TestWrite_OneBadFieldRefusesTheWholeSave(t *testing.T) {
	st := memStore{}
	_, err := Write(context.Background(), st, map[string]json.RawMessage{
		"apps.max_input_mb":  raw(512),
		"coedit.max_members": raw(1), // under 2
	})
	var fe *FieldError
	require.True(t, errors.As(err, &fe))
	assert.Equal(t, "coedit.max_members", fe.Key)
	assert.Equal(t, "range", fe.Code)
	assert.Equal(t, 2, fe.Min)
	assert.Empty(t, st, "nothing was written")
}

func TestWrite_RefusesWhatIsNotTheSettingsKind(t *testing.T) {
	for _, c := range []struct {
		key  string
		val  any
		code string
	}{
		{"cache.max_gb", "lots", "type"},
		{"notify.enabled", "yes", "type"},
		{"instance.default_locale", "Türkçe", "language"},
		{"no.such.setting", 1, "unknown"},
	} {
		_, err := Write(context.Background(), memStore{}, map[string]json.RawMessage{c.key: raw(c.val)})
		var fe *FieldError
		require.True(t, errors.As(err, &fe), c.key)
		assert.Equal(t, c.code, fe.Code, c.key)
	}
	// A language tag, and nothing (the browser's language), are both values.
	st := memStore{}
	_, err := Write(context.Background(), st, map[string]json.RawMessage{"instance.default_locale": raw("pt-BR")})
	require.NoError(t, err)
	assert.Equal(t, "pt-BR", st["instance.default_locale"])
	_, err = Write(context.Background(), st, map[string]json.RawMessage{"instance.default_locale": raw("")})
	require.NoError(t, err)
	assert.Equal(t, "", st["instance.default_locale"])
}

func TestItems_ARestartIsSaidUntilTheNextStart(t *testing.T) {
	st := memStore{}
	cfg := config.Default()
	Boot(context.Background(), st, &cfg)

	_, err := Write(context.Background(), st, map[string]json.RawMessage{"notify.enabled": raw(false)})
	require.NoError(t, err)
	item := find(t, Items(context.Background(), st, "notifications"), "notify.enabled")
	assert.True(t, item.Restart)
	assert.True(t, item.RestartPending, "the process still runs with notifications on")
	assert.Equal(t, false, item.Value)

	// The next start runs with it.
	cfg = config.Default()
	Boot(context.Background(), st, &cfg)
	assert.False(t, cfg.Notify.Enabled)
	assert.False(t, find(t, Items(context.Background(), st, "notifications"), "notify.enabled").RestartPending)
}

func TestItems_ALiveSettingAppliesAtOnce(t *testing.T) {
	st := memStore{}
	var got any
	SetLive("notify.webhook_lang", func(_ context.Context, v any) { got = v })
	t.Cleanup(func() { SetLive("notify.webhook_lang", nil) })

	_, err := Write(context.Background(), st, map[string]json.RawMessage{"notify.webhook_lang": raw("tr")})
	require.NoError(t, err)
	assert.Equal(t, "tr", got)
	item := find(t, Items(context.Background(), st, "webhooks"), "notify.webhook_lang")
	assert.False(t, item.Restart)
	assert.False(t, item.RestartPending)
}

func TestItems_AGroupHoldsItsOwnSettingsWithTheirBounds(t *testing.T) {
	items := Items(context.Background(), memStore{}, "apps")
	keys := []string{}
	for _, it := range items {
		keys = append(keys, it.Key)
		assert.Equal(t, GroupApps, it.Group)
	}
	assert.Equal(t, []string{"apps.max_input_mb", "apps.max_output_mb", "apps.max_wasm_mb", "apps.max_ui_mb",
		"apps.request_ttl_days", "coedit.max_members", "coedit.unsaved_days"}, keys)
	in := find(t, items, "apps.max_input_mb")
	require.NotNil(t, in.Min)
	assert.Equal(t, 1, *in.Min)
	assert.Equal(t, 256, in.Default)
	assert.Equal(t, "FILEX_APP_PLUGIN_MAX_INPUT_MB", in.Env)
}

// ── the explicit way back to the configuration ──────────────────────────

func TestReseed_WritesTheConfigurationOverTheRow(t *testing.T) {
	st := memStore{"sync.default_interval_minutes": "45", "coedit.unsaved_days": "90"}
	cfg := config.Default()
	cfg.Sync.DefaultInterval = 5 * time.Minute

	done, err := Reseed(context.Background(), st, &cfg, []string{"sync.default_interval_minutes"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sync.default_interval_minutes"}, done)
	assert.Equal(t, "5", st["sync.default_interval_minutes"])
	assert.Equal(t, "90", st["coedit.unsaved_days"], "only the named setting")

	// Every setting: one the configuration leaves at its default goes back
	// to the default.
	_, err = Reseed(context.Background(), st, &cfg, nil)
	require.NoError(t, err)
	assert.Equal(t, "", st["coedit.unsaved_days"])

	_, err = Reseed(context.Background(), st, &cfg, []string{"no.such"})
	require.Error(t, err)
}

func find(t *testing.T, items []Item, key string) Item {
	t.Helper()
	for _, it := range items {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("no item %s", key)
	return Item{}
}

// ── a configuration the row overrules is said at start (Drifts) ──────────────

func TestDrifts_AConfigurationTheRowOverrulesIsNamed(t *testing.T) {
	t.Setenv("FILEX_SYNC_INTERVAL", "5m")
	st := memStore{"sync.default_interval_minutes": "45"}
	cfg := config.Default()
	cfg.Sync.DefaultInterval = 5 * time.Minute

	got := Drifts(context.Background(), st, &cfg)

	require.Len(t, got, 1)
	assert.Equal(t, "sync.default_interval_minutes", got[0].Key)
	assert.Equal(t, "FILEX_SYNC_INTERVAL", got[0].Source, "the log names what the operator edited")

	Boot(context.Background(), st, &cfg)
	assert.Equal(t, "45", st["sync.default_interval_minutes"], "said, never written over")
	assert.Equal(t, 45*time.Minute, cfg.Sync.DefaultInterval)
}

func TestDrifts_AConfigYamlKeyIsNamedByItsKey(t *testing.T) {
	t.Setenv("FILEX_COEDIT_MAX_MEMBERS", "")
	st := memStore{"coedit.max_members": "8"}
	cfg := config.Default()
	cfg.CoEdit.MaxMembers = 12

	got := Drifts(context.Background(), st, &cfg)

	require.Len(t, got, 1)
	assert.Equal(t, "config.yaml coedit.max_members", got[0].Source)
}

func TestDrifts_AVariableSetToTheDefaultStillCounts(t *testing.T) {
	t.Setenv("FILEX_SYNC_INTERVAL", "15m")
	st := memStore{"sync.default_interval_minutes": "45"}
	cfg := config.Default() // 15 minutes: what FILEX_SYNC_INTERVAL=15m gives

	got := Drifts(context.Background(), st, &cfg)

	require.Len(t, got, 1, "the operator wrote 15 minutes and runs with 45")
	assert.Equal(t, "sync.default_interval_minutes", got[0].Key)
}

func TestDrifts_NotWhenTheyAgreeNothingIsStoredOrNothingIsSet(t *testing.T) {
	t.Setenv("FILEX_SYNC_INTERVAL", "")
	t.Setenv("FILEX_CACHE_MAX_BYTES", "")
	ctx := context.Background()

	cfg := config.Default()
	cfg.Sync.DefaultInterval = 5 * time.Minute
	assert.Empty(t, Drifts(ctx, memStore{"sync.default_interval_minutes": "5"}, &cfg), "the row holds the configuration's own value")
	assert.Empty(t, Drifts(ctx, memStore{}, &cfg), "no row: the seed writes it")

	plain := config.Default()
	assert.Empty(t, Drifts(ctx, memStore{"cache.max_gb": "50", "sync.default_interval_minutes": "45"}, &plain),
		"the configuration says nothing: a value saved on the page is no drift")
}
