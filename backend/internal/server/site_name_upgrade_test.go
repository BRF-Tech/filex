package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// Task #258: the old Settings page's "Site name" (`site_name`) and Branding's
// "Display name" (`branding.name`) were two names for one instance, and only
// the share mails read the first. 0.57 keeps one, and an upgrade must not
// lose what an operator had typed into the one that goes.

func settingOf(t *testing.T, ctx context.Context, get func(context.Context, string) (string, error), key string) string {
	t.Helper()
	v, _ := get(ctx, key)
	return v
}

func TestUpgradeSiteName_CarriedIntoAnEmptyDisplayName(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	require.NoError(t, store.UpsertSetting(ctx, "site_name", "Acme Files"))

	upgradeSiteName(ctx, store)

	assert.Equal(t, "Acme Files", settingOf(t, ctx, store.GetSetting, "branding.name"))
	all, err := store.ListSettings(ctx)
	require.NoError(t, err)
	_, still := all["site_name"]
	assert.False(t, still, "the old row is gone: the instance has one name")
}

func TestUpgradeSiteName_ADisplayNameAlreadySetStays(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	require.NoError(t, store.UpsertSetting(ctx, "site_name", "Old Name"))
	require.NoError(t, store.UpsertSetting(ctx, "branding.name", "Acme Cloud"))

	upgradeSiteName(ctx, store)

	assert.Equal(t, "Acme Cloud", settingOf(t, ctx, store.GetSetting, "branding.name"),
		"what every page already shows is not replaced")
	assert.Equal(t, "", settingOf(t, ctx, store.GetSetting, "site_name"))
}

func TestUpgradeSiteName_ATenantsOwnNameIsNotTouched(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	require.NoError(t, store.UpsertSetting(ctx, "site_name", "Platform"))
	require.NoError(t, store.UpsertSetting(ctx, "tenant.7.branding.name", "Globex"))

	upgradeSiteName(ctx, store)

	assert.Equal(t, "Platform", settingOf(t, ctx, store.GetSetting, "branding.name"))
	assert.Equal(t, "Globex", settingOf(t, ctx, store.GetSetting, "tenant.7.branding.name"))
}

func TestUpgradeSiteName_NothingToDoTwice(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	require.NoError(t, store.UpsertSetting(ctx, "site_name", "Acme Files"))
	upgradeSiteName(ctx, store)
	require.NoError(t, store.UpsertSetting(ctx, "branding.name", "Renamed Later"))

	upgradeSiteName(ctx, store)

	assert.Equal(t, "Renamed Later", settingOf(t, ctx, store.GetSetting, "branding.name"),
		"a later boot does not bring the old name back")
}

// The delete is by prefix: a key that merely starts with "site_name" is not
// the old field and survives (the old row is blanked instead).
func TestUpgradeSiteName_AnotherKeyWithTheSamePrefixSurvives(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	require.NoError(t, store.UpsertSetting(ctx, "site_name", "Acme Files"))
	require.NoError(t, store.UpsertSetting(ctx, "site_name_note", "keep me"))

	upgradeSiteName(ctx, store)

	assert.Equal(t, "Acme Files", settingOf(t, ctx, store.GetSetting, "branding.name"))
	assert.Equal(t, "keep me", settingOf(t, ctx, store.GetSetting, "site_name_note"))
	assert.Equal(t, "", settingOf(t, ctx, store.GetSetting, "site_name"))
}

// Through seedFromEnv, the boot path: an operator's old value wins over
// FILEX_SITE_NAME, which only fills an empty Display name.
func TestSeedFromEnv_AnOldSiteNameWinsOverTheEnvironmentsSeed(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	require.NoError(t, store.UpsertSetting(ctx, "site_name", "Typed In The Panel"))

	cfg := config.Default()
	cfg.Seed.SiteName = "From Compose"
	seedFromEnv(ctx, store, cfg)

	assert.Equal(t, "Typed In The Panel", settingOf(t, ctx, store.GetSetting, "branding.name"))
}
