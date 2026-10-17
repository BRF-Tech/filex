package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/external"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// Task #258 (the owner's decision of 2026-10-15): the environment SEEDS an
// external service, like every other setting a variable gives a first value
// to. An administrator who switched an env-configured ONLYOFFICE off on
// External services keeps it off across restarts; until 0.57 the next start
// wrote the environment back, switch included. `filex settings reseed
// --external` (force) is the explicit way back.
func TestSeedExternalDefaults_TheAdministratorsSwitchStandsAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", false, "http://ds", "pinned-secret", "{}", time.Time{}, "disabled"))
	require.NoError(t, store.UpsertExternalService(ctx, "drawio", false, "http://drawio", "", "{}", time.Time{}, "disabled"))

	var cfg config.Config
	cfg.ExternalServices.OnlyOffice.URL = "http://ds"
	cfg.ExternalServices.OnlyOffice.JWTSecret = "pinned-secret"
	seedExternalDefaults(ctx, store, cfg)

	oo, err := store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	require.NotNil(t, oo)
	assert.False(t, oo.Enabled, "switched off on the page: it stays off")
	assert.Equal(t, "http://ds", oo.URL)

	// An edited address stands too.
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true, "http://ds-moved", "pinned-secret", "{}", time.Time{}, "ok"))
	seedExternalDefaults(ctx, store, cfg)
	oo, err = store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.Equal(t, "http://ds-moved", oo.URL)

	// On request, the environment again - the switch included.
	wrote := applyExternalFromConfig(ctx, store, cfg, true)
	assert.Equal(t, []string{"onlyoffice"}, wrote)
	oo, err = store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.True(t, oo.Enabled)
	assert.Equal(t, "http://ds", oo.URL)

	// draw.io, which the environment does not name: the administrator's.
	dio, err := store.GetExternalService(ctx, "drawio")
	require.NoError(t, err)
	require.NotNil(t, dio)
	assert.False(t, dio.Enabled)
	assert.Equal(t, "http://drawio", dio.URL)
}

// A service nothing was configured in yet - no row, or a row with no address
// (what a first start without the variable leaves) - takes the environment's
// values: adding FILEX_ONLYOFFICE_URL to an install that never set ONLYOFFICE
// up works on the next start.
func TestSeedExternalDefaults_AnUnconfiguredServiceTakesTheEnvironment(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	seedExternalDefaults(ctx, store, config.Config{})
	oo, err := store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	require.NotNil(t, oo)
	require.Empty(t, oo.URL)

	var cfg config.Config
	cfg.ExternalServices.OnlyOffice.URL = "http://ds"
	cfg.ExternalServices.OnlyOffice.JWTSecret = "s3cret"
	seedExternalDefaults(ctx, store, cfg)
	oo, err = store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.True(t, oo.Enabled)
	assert.Equal(t, "http://ds", oo.URL)
	assert.Equal(t, "s3cret", oo.SecretEnc)
}

// FILEX_ONLYOFFICE_LANG (GitHub Discussion #93) gives the editor's language a
// first value: written onto a row that has no language of its own, the way
// ONLYOFFICE's list writes it, and - since 0.57 (task #258) - never over the
// one the admin page chose. It touches nothing else on the row: an install
// that configured ONLYOFFICE on the admin page and gives only the language
// keeps its switch, address, secret and callback address.
func TestSeedExternalDefaults_TheEnvironmentSeedsTheEditorLanguageAndNothingElse(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true, "https://docs.ui", "ui-secret",
		`{"callback_url":"http://filex:5212"}`, time.Time{}, "ok"))

	var cfg config.Config
	cfg.ExternalServices.OnlyOffice.EditorLang = "de-DE"
	seedExternalDefaults(ctx, store, cfg)

	oo, err := store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	require.NotNil(t, oo)
	assert.Equal(t, "de", external.EditorLangFromOptions(oo.OptionsJSON), "the environment's language, as ONLYOFFICE writes it")
	assert.True(t, oo.Enabled, "a language-only environment does not switch the service off")
	assert.Equal(t, "https://docs.ui", oo.URL)
	assert.Equal(t, "ui-secret", oo.SecretEnc)
	assert.Equal(t, "http://filex:5212", external.CallbackURLFromOptions(oo.OptionsJSON))

	// An edit on External services stands across restarts.
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true, "https://docs.ui", "ui-secret",
		`{"callback_url":"http://filex:5212","editor_lang":"es"}`, time.Time{}, "ok"))
	seedExternalDefaults(ctx, store, cfg)
	oo, err = store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.Equal(t, "es", external.EditorLangFromOptions(oo.OptionsJSON), "the page's language stands")

	// On request, the environment's.
	assert.True(t, seedOnlyOfficeEditorLang(ctx, store, cfg.ExternalServices.OnlyOffice.EditorLang, true))
	oo, err = store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.Equal(t, "de", external.EditorLangFromOptions(oo.OptionsJSON))
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true, "https://docs.ui", "ui-secret",
		`{"callback_url":"http://filex:5212","editor_lang":"es"}`, time.Time{}, "ok"))

	// No variable: the setting is the admin page's, left as it is.
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true, "https://docs.ui", "ui-secret",
		`{"editor_lang":"es"}`, time.Time{}, "ok"))
	seedExternalDefaults(ctx, store, config.Config{})
	oo, err = store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.Equal(t, "es", external.EditorLangFromOptions(oo.OptionsJSON))

	// A value the editor does not offer is not written.
	cfg.ExternalServices.OnlyOffice.EditorLang = "klingon"
	seedExternalDefaults(ctx, store, cfg)
	oo, err = store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.Equal(t, "es", external.EditorLangFromOptions(oo.OptionsJSON))

	// "auto", on request, is automatic: the key goes.
	cfg.ExternalServices.OnlyOffice.EditorLang = "auto"
	applyExternalFromConfig(ctx, store, cfg, true)
	oo, err = store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.Empty(t, external.EditorLangFromOptions(oo.OptionsJSON))
	assert.True(t, oo.Enabled)
}

// FILEX_ONLYOFFICE_CALLBACK_URL without FILEX_ONLYOFFICE_URL, on an install
// that configured ONLYOFFICE on External services: the callback address is
// written, the service stays on at its address. Red on the old code: the boot
// re-asserted the row as "no address", switching it off and clearing the URL.
func TestSeedExternalDefaults_ACallbackAddressAloneLeavesTheServiceAsItWas(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true, "https://docs.ui", "ui-secret",
		`{"editor_lang":"tr"}`, time.Time{}, "ok"))

	var cfg config.Config
	cfg.ExternalServices.OnlyOffice.CallbackURL = "http://filex:5212/"
	seedExternalDefaults(ctx, store, cfg)

	oo, err := store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	require.NotNil(t, oo)
	assert.True(t, oo.Enabled, "the admin page's switch stands")
	assert.Equal(t, "https://docs.ui", oo.URL, "the admin page's address stands")
	assert.Equal(t, "ui-secret", oo.SecretEnc)
	assert.Equal(t, "http://filex:5212", external.CallbackURLFromOptions(oo.OptionsJSON), "the environment's callback address")
	assert.Equal(t, "tr", external.EditorLangFromOptions(oo.OptionsJSON), "the other options are kept")
	assert.Equal(t, "ok", oo.LastState)
}

// The first boot of an install that pins only the language: the row is
// created (switched off, nothing to reach yet) and carries the language.
func TestSeedExternalDefaults_TheEditorLanguageOnTheFirstBoot(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	var cfg config.Config
	cfg.ExternalServices.OnlyOffice.EditorLang = "tr"
	seedExternalDefaults(ctx, store, cfg)

	oo, err := store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	require.NotNil(t, oo)
	assert.Equal(t, "tr", external.EditorLangFromOptions(oo.OptionsJSON))
	assert.False(t, oo.Enabled)
}
