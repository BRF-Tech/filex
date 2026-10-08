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

// The environment pins a service ON at every boot. An administrator who
// switched an env-configured OnlyOffice off on External services keeps it off
// until filex restarts, and the restart writes the environment back - the
// switch included. Before 0.50 the row kept its URL, "matched" the
// environment and stayed off across restarts; nobody saw it, because the
// OnlyOffice service fell back to its boot-time values and ran anyway. Now
// that switched off is off everywhere (onlyoffice.Service.settings), the boot
// has to switch it back on, as External services and the PATCH answer say.
func TestSeedExternalDefaults_TheEnvironmentSwitchesAPinnedServiceBackOn(t *testing.T) {
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
	assert.True(t, oo.Enabled, "pinned by the environment: on again after the boot")
	assert.Equal(t, "http://ds", oo.URL)
	assert.Equal(t, "pinned-secret", oo.SecretEnc)

	// Not pinned (no draw.io address in the environment): the row is the
	// administrator's, switched off it stays.
	dio, err := store.GetExternalService(ctx, "drawio")
	require.NoError(t, err)
	require.NotNil(t, dio)
	assert.False(t, dio.Enabled, "not pinned: the administrator's switch stands")
	assert.Equal(t, "http://drawio", dio.URL)
}

// FILEX_ONLYOFFICE_LANG (GitHub Discussion #93) pins the editor's language
// the way FILEX_ONLYOFFICE_URL pins the address: written onto the row at every
// boot, the way ONLYOFFICE's list writes it. It touches nothing else on the
// row: an install that configured ONLYOFFICE on the admin page and pins only
// the language keeps its switch, address, secret and callback address.
// Red on the old code: the variable did not exist.
func TestSeedExternalDefaults_TheEnvironmentPinsTheEditorLanguageAndNothingElse(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true, "https://docs.ui", "ui-secret",
		`{"callback_url":"http://filex:5212","editor_lang":"fr"}`, time.Time{}, "ok"))

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

	// An edit on External services lasts until the next boot.
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true, "https://docs.ui", "ui-secret",
		`{"callback_url":"http://filex:5212","editor_lang":"es"}`, time.Time{}, "ok"))
	seedExternalDefaults(ctx, store, cfg)
	oo, err = store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.Equal(t, "de", external.EditorLangFromOptions(oo.OptionsJSON), "re-asserted at boot")

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

	// "auto" pins automatic: the key goes.
	cfg.ExternalServices.OnlyOffice.EditorLang = "auto"
	seedExternalDefaults(ctx, store, cfg)
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
