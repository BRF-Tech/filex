package antivirus_test

// The three settings that decide whether filex scans and how it reaches
// ClamAV, and the single Resolve that answers both questions for the
// capability flag, the admin page and the scan pipeline alike.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/antivirus"
	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

// seedStore is settingsMap plus the write half, so the seeding contract can be
// exercised without a database.
type seedStore struct {
	rows   map[string]string
	writes int
}

func newSeedStore() *seedStore { return &seedStore{rows: map[string]string{}} }

func (s *seedStore) GetSetting(_ context.Context, key string) (string, error) {
	return s.rows[key], nil
}

func (s *seedStore) UpsertSetting(_ context.Context, key, value string) error {
	s.writes++
	s.rows[key] = value
	return nil
}

func noClamOnPath(t *testing.T) {
	t.Helper()
	t.Setenv("FILEX_CLAMAV_BIN", "")
	t.Setenv("PATH", t.TempDir())
}

// ⚠⚠ The kill-switch is now a row, and a row beats the variable that seeded
// it. An install that later removes FILEX_CLAMAV=0 from compose keeps scanning
// off until someone turns it on — in the UI, where the state is visible.
func TestEnabledSetting_RowBeatsEnv(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FILEX_CLAMAV", "0")
	t.Setenv("FILEX_CLAMAV_BIN", "/bin/sh")

	off := antivirus.Resolve(ctx, settingsMap{antivirus.EnabledSetting.Key: "false"})
	assert.False(t, off.Enabled)
	assert.False(t, off.Available())

	on := antivirus.Resolve(ctx, settingsMap{antivirus.EnabledSetting.Key: "true"})
	assert.True(t, on.Enabled)
	assert.True(t, on.Available(), "the row says on, so it is on")
	assert.Equal(t, antivirus.ModeBinary, on.Mode)
	assert.Equal(t, "/bin/sh", on.Bin)

	// ⚠ With NO row yet — the window between an upgrade and SeedSettings, and
	// every store-less caller — the variable that would have seeded the row is
	// the only intent on record, so FILEX_CLAMAV=0 still means off. Ignoring it
	// here is how a documented kill-switch quietly stops working.
	unseeded := antivirus.Resolve(ctx, settingsMap{})
	assert.False(t, unseeded.Enabled)

	// And with neither a row nor a variable, the default applies: unset has
	// always meant "scan if you can".
	t.Setenv("FILEX_CLAMAV", "")
	assert.True(t, antivirus.Resolve(ctx, settingsMap{}).Enabled)
}

// Switched off is a CHOICE, not a fault: Err stays nil, and nothing about the
// transport is reported, because none is in use.
func TestResolve_DisabledIsNotAnError(t *testing.T) {
	res := antivirus.Resolve(context.Background(), settingsMap{antivirus.EnabledSetting.Key: "false"})
	assert.False(t, res.Available())
	assert.NoError(t, res.Err)
	assert.Equal(t, "", res.Mode)

	sc := antivirus.NewWithResolution(res)
	assert.False(t, sc.Supports())
	assert.Equal(t, "", sc.Mode())
	assert.Equal(t, "", sc.BinName())
}

func TestResolve_DaemonMode(t *testing.T) {
	ctx := context.Background()
	noClamOnPath(t)
	res := antivirus.Resolve(ctx, settingsMap{
		antivirus.ModeSetting.Key: antivirus.ModeDaemon,
		antivirus.AddrSetting.Key: "clamav",
	})
	require.True(t, res.Available(), "no binary anywhere, and it is still available")
	assert.Equal(t, antivirus.ModeDaemon, res.Mode)
	assert.Equal(t, "clamav", res.Addr)
	assert.Equal(t, "tcp", res.Network)
	assert.Equal(t, "clamav:3310", res.Address, "the default port completes a bare host")
	assert.Equal(t, "", res.Bin)
}

// ⚠ An address that cannot be parsed makes the scanner UNAVAILABLE and says
// why. It must not fall back to a binary: an operator who chose daemon mode
// and mistyped the address would otherwise get scanning through some other
// scanner entirely, and a green light either way.
func TestResolve_BadDaemonAddressDoesNotFallBackToBinary(t *testing.T) {
	t.Setenv("FILEX_CLAMAV_BIN", "/bin/sh")
	res := antivirus.Resolve(context.Background(), settingsMap{
		antivirus.ModeSetting.Key: antivirus.ModeDaemon,
		antivirus.AddrSetting.Key: "clamav 3310",
	})
	assert.True(t, res.Enabled)
	assert.False(t, res.Available())
	assert.Equal(t, "", res.Bin, "binary mode is not a fallback")
	require.Error(t, res.Err)
	// ⚠⚠ And the error NAMES the fault. An early version of this collapsed to
	// "no clamd address is configured", because the string spec fell back to
	// its empty default — which would have had the admin page say nothing was
	// configured while the field beside it showed the address they typed.
	assert.Contains(t, res.Err.Error(), "clamav 3310")
	assert.Contains(t, res.Err.Error(), "whitespace")
	assert.Equal(t, "clamav 3310", res.Addr, "the page still shows what is stored")

	// A different fault, a different message.
	res = antivirus.Resolve(context.Background(), settingsMap{
		antivirus.ModeSetting.Key: antivirus.ModeDaemon,
		antivirus.AddrSetting.Key: "clamav:not-a-port",
	})
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "invalid port")
}

func TestSeedSettings_FirstBootOnly(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FILEX_CLAMAV", "0")
	t.Setenv("FILEX_CLAMAV_MODE", "")
	t.Setenv("FILEX_CLAMAV_ADDR", "")
	t.Setenv("FILEX_CLAMAV_MAX", "")
	t.Setenv("FILEX_CLAMAV_SAVE_WINDOW_MINUTES", "")

	st := newSeedStore()
	antivirus.SeedSettings(ctx, st)
	assert.Equal(t, "false", st.rows[antivirus.EnabledSetting.Key])

	// An admin turns it back on in the UI; a later boot must not undo that.
	st.rows[antivirus.EnabledSetting.Key] = "true"
	antivirus.SeedSettings(ctx, st)
	assert.Equal(t, "true", st.rows[antivirus.EnabledSetting.Key],
		"FILEX_CLAMAV is a seed, and the seed is spent")
}

// ⚠⚠ Naming a clamd container in compose and getting binary mode is a
// deployment that looks configured and scans nothing. FILEX_CLAMAV_ADDR with
// no FILEX_CLAMAV_MODE therefore seeds daemon mode.
func TestSeedSettings_AddressImpliesDaemonMode(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FILEX_CLAMAV", "")
	t.Setenv("FILEX_CLAMAV_MODE", "")
	t.Setenv("FILEX_CLAMAV_ADDR", "clamav:3310")

	st := newSeedStore()
	antivirus.SeedSettings(ctx, st)
	assert.Equal(t, "clamav:3310", st.rows[antivirus.AddrSetting.Key])
	assert.Equal(t, antivirus.ModeDaemon, st.rows[antivirus.ModeSetting.Key])

	// And it is still first-boot-only: an admin who switches back to binary
	// keeps that across restarts.
	st.rows[antivirus.ModeSetting.Key] = antivirus.ModeBinary
	antivirus.SeedSettings(ctx, st)
	assert.Equal(t, antivirus.ModeBinary, st.rows[antivirus.ModeSetting.Key])
}

// An explicit FILEX_CLAMAV_MODE wins over the inference — the inference only
// fills a gap.
func TestSeedSettings_ExplicitModeWinsOverInference(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FILEX_CLAMAV", "")
	t.Setenv("FILEX_CLAMAV_MODE", "binary")
	t.Setenv("FILEX_CLAMAV_ADDR", "clamav:3310")

	st := newSeedStore()
	antivirus.SeedSettings(ctx, st)
	assert.Equal(t, antivirus.ModeBinary, st.rows[antivirus.ModeSetting.Key])
}

func TestSettings_AreAllSeeders(t *testing.T) {
	keys := map[string]bool{}
	for _, s := range antivirus.Settings() {
		keys[s.SettingKey()] = true
	}
	for _, want := range []string{
		antivirus.EnabledSetting.Key,
		antivirus.ModeSetting.Key,
		antivirus.AddrSetting.Key,
		antivirus.SaveWindowSetting.Key,
		antivirus.MaxScanSetting.Key,
	} {
		assert.True(t, keys[want], "%s must be seeded at boot", want)
	}
	// The family is a mixed list, which is what dbsetting.Seeder is for.
	var _ []dbsetting.Seeder = antivirus.Settings()
}

// ⚠⚠ enabled / mode / address take effect at the next RESTART, so the admin
// page has to be able to say so — and to stop saying so once the restart has
// happened. RestartPending is what makes that message true rather than
// decorative.
func TestRestartPending(t *testing.T) {
	ctx := context.Background()
	noClamOnPath(t)

	booted := antivirus.Resolve(ctx, settingsMap{
		antivirus.ModeSetting.Key: antivirus.ModeDaemon,
		antivirus.AddrSetting.Key: "clamav:3310",
	})
	antivirus.SetBoot(booted)
	t.Cleanup(func() { antivirus.SetBoot(antivirus.Resolution{}) })

	assert.False(t, antivirus.RestartPending(booted), "nothing changed")

	// Switched off in the UI: pending until the restart.
	off := antivirus.Resolve(ctx, settingsMap{antivirus.EnabledSetting.Key: "false"})
	assert.True(t, antivirus.RestartPending(off))

	// A different address: also pending.
	moved := antivirus.Resolve(ctx, settingsMap{
		antivirus.ModeSetting.Key: antivirus.ModeDaemon,
		antivirus.AddrSetting.Key: "clamav-2:3310",
	})
	assert.True(t, antivirus.RestartPending(moved))

	// A different mode: pending.
	bin := antivirus.Resolve(ctx, settingsMap{antivirus.ModeSetting.Key: antivirus.ModeBinary})
	assert.True(t, antivirus.RestartPending(bin))

	// The live settings are not part of it — they apply per file, so they can
	// never be pending.
	sameWithOtherLimits := antivirus.Resolve(ctx, settingsMap{
		antivirus.ModeSetting.Key:    antivirus.ModeDaemon,
		antivirus.AddrSetting.Key:    "clamav:3310",
		antivirus.MaxScanSetting.Key: "5",
	})
	assert.False(t, antivirus.RestartPending(sameWithOtherLimits))
}
