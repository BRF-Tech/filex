package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// `filex settings reseed` (task #258): since 0.57 a start writes the
// environment only into an empty row, so writing it over an administrator's
// value is something an operator asks for, by key, with --all or --external.

func TestReseedFromConfig_AsksForWhat(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	_, err := ReseedFromConfig(context.Background(), store, config.Default(), nil, false, false)
	require.Error(t, err, "nothing named: nothing is guessed")

	require.NoError(t, store.UpsertSetting(context.Background(), "sync.default_interval_minutes", "45"))
	_, err = ReseedFromConfig(context.Background(), store, config.Default(), []string{"sync.default_interval_minutes", "no.such"}, false, false)
	require.ErrorContains(t, err, "no.such")
	v, _ := store.GetSetting(context.Background(), "sync.default_interval_minutes")
	assert.Equal(t, "45", v, "an unknown key writes nothing at all")
}

func TestReseedFromConfig_EachKindOfSetting(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	for k, v := range map[string]string{
		"sync.default_interval_minutes": "45", // an instance setting
		"drafts.limit":                  "70", // the dbsetting family
		"branding.name":                 "Typed In The Panel",
	} {
		require.NoError(t, store.UpsertSetting(ctx, k, v))
	}
	cfg := config.Default()
	cfg.Sync.DefaultInterval = 5 * time.Minute
	cfg.Seed.SiteName = "From Compose"
	t.Setenv("FILEX_DRAFTS_LIMIT", "12")

	wrote, err := ReseedFromConfig(ctx, store, cfg, []string{"sync.default_interval_minutes", "drafts.limit", "branding.name"}, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"branding.name", "drafts.limit", "sync.default_interval_minutes"}, wrote)
	for k, want := range map[string]string{
		"sync.default_interval_minutes": "5",
		"drafts.limit":                  "12",
		"branding.name":                 "From Compose",
	} {
		v, _ := store.GetSetting(ctx, k)
		assert.Equal(t, want, v, k)
	}
}

func TestReseedFromConfig_TheExternalServicesOnRequest(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", false, "http://moved", "s", "{}", time.Time{}, "disabled"))
	var cfg config.Config
	cfg.ExternalServices.OnlyOffice.URL = "http://ds"

	// A start leaves the administrator's row as it is...
	seedExternalDefaults(ctx, store, cfg)
	oo, err := store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.Equal(t, "http://moved", oo.URL)
	assert.False(t, oo.Enabled)

	// ...and --external writes the environment's.
	wrote, err := ReseedFromConfig(ctx, store, cfg, nil, false, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"external:onlyoffice"}, wrote)
	oo, err = store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.Equal(t, "http://ds", oo.URL)
	assert.True(t, oo.Enabled)
}

// The trusted-proxy list (loginguard proxyseed.go): reseeded by its key and by
// --all, and never dropped to `auto` when the configuration names no list.
func TestReseedFromConfig_TheTrustedProxies(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	require.NoError(t, store.UpsertSetting(ctx, "login.trusted_proxies", "auto, 192.0.2.10"))

	cfg := config.Default()
	cfg.TrustedProxies = "auto, 172.18.0.1"
	wrote, err := ReseedFromConfig(ctx, store, cfg, []string{"login.trusted_proxies"}, false, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"login.trusted_proxies"}, wrote)
	v, _ := store.GetSetting(ctx, "login.trusted_proxies")
	assert.Equal(t, "auto, 172.18.0.1", v)

	require.NoError(t, store.UpsertSetting(ctx, "login.trusted_proxies", "auto, 192.0.2.10"))
	wrote, err = ReseedFromConfig(ctx, store, config.Default(), nil, true, false)
	require.NoError(t, err)
	assert.NotContains(t, wrote, "login.trusted_proxies")
	v, _ = store.GetSetting(ctx, "login.trusted_proxies")
	assert.Equal(t, "auto, 192.0.2.10", v, "--all with no list configured keeps the one typed on the page")
}
