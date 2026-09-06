package antivirus_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/antivirus"
)

type settingsMap map[string]string

func (s settingsMap) GetSetting(_ context.Context, key string) (string, error) {
	return s[key], nil
}

func TestSaveWindow_DefaultAndStored(t *testing.T) {
	ctx := context.Background()

	assert.Equal(t, 30*time.Minute, antivirus.SaveWindow(ctx, nil),
		"no settings source → the documented default")
	assert.Equal(t, 30*time.Minute, antivirus.SaveWindow(ctx, settingsMap{}),
		"no row → the documented default")

	stored := settingsMap{antivirus.SaveWindowSetting.Key: "5"}
	assert.Equal(t, 5*time.Minute, antivirus.SaveWindow(ctx, stored))
}

// The bounds this feature ships with, asserted as numbers so a change to them
// is a deliberate edit to a test rather than a silent widening.
func TestSaveWindow_Bounds(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, 2, antivirus.MinSaveWindowMinutes)
	assert.Equal(t, 60, antivirus.MaxSaveWindowMinutes)
	assert.Equal(t, 30, antivirus.DefaultSaveWindowMinutes)

	assert.Equal(t, 2*time.Minute,
		antivirus.SaveWindow(ctx, settingsMap{antivirus.SaveWindowSetting.Key: "0"}),
		"0 has no meaning here and must not become 'no window'")
	assert.Equal(t, time.Hour,
		antivirus.SaveWindow(ctx, settingsMap{antivirus.SaveWindowSetting.Key: "10080"}),
		"a week clamps to the ceiling")
}

func TestSaveWindowSetting_IsSeededNotOverridden(t *testing.T) {
	// The declaration itself is the contract the docs describe.
	assert.Equal(t, "antivirus.save_scan_window_minutes", antivirus.SaveWindowSetting.Key)
	assert.Equal(t, "FILEX_CLAMAV_SAVE_WINDOW_MINUTES", antivirus.SaveWindowSetting.EnvVar)
}
