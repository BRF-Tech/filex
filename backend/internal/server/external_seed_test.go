package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
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
