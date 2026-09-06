package antivirus_test

// The scan-size ceiling after its move out of the environment. The test that
// matters most is the seed conversion: FILEX_CLAMAV_MAX is in bytes, the
// stored setting is in megabytes, and an install that already sets the
// variable must not quietly end up with a smaller ceiling than it had.

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/antivirus"
)

// writableSettings is settingsMap plus the write half, for seeding.
type writableSettings map[string]string

func (w writableSettings) GetSetting(_ context.Context, key string) (string, error) {
	return w[key], nil
}

func (w writableSettings) UpsertSetting(_ context.Context, key, value string) error {
	w[key] = value
	return nil
}

func TestMaxScan_DefaultAndStored(t *testing.T) {
	ctx := context.Background()
	assert.EqualValues(t, 100<<20, antivirus.MaxScanBytesFrom(ctx, nil))
	assert.EqualValues(t, 250<<20,
		antivirus.MaxScanBytesFrom(ctx, settingsMap{antivirus.MaxScanSetting.Key: "250"}))
}

func TestMaxScan_ClampsStoredValue(t *testing.T) {
	ctx := context.Background()
	assert.EqualValues(t, 1<<20,
		antivirus.MaxScanBytesFrom(ctx, settingsMap{antivirus.MaxScanSetting.Key: "0"}),
		"a ceiling below a megabyte would look like scanning while scanning nothing")
	assert.EqualValues(t, int64(10240)<<20,
		antivirus.MaxScanBytesFrom(ctx, settingsMap{antivirus.MaxScanSetting.Key: "999999"}))
}

// ⚠ The migration promise: a deployment already setting FILEX_CLAMAV_MAX keeps
// the ceiling it had. Conversion rounds UP, so the cap never shrinks — rounding
// down would start skipping files that used to be scanned, silently.
func TestMaxScan_SeedsFromLegacyBytesVariable(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		envBytes string
		wantMB   string
		wantCap  int64
	}{
		{"104857600", "100", 100 << 20},                // the old default, exactly
		{"52428800", "50", 50 << 20},                   // 50 MB, exactly
		{"50000000", "48", 48 << 20},                   // 47.68 MB rounds UP to 48
		{"1", "1", 1 << 20},                            // one byte rounds up to the floor
		{"1099511627776", "10240", int64(10240) << 20}, // 1 TB clamps to the ceiling
	} {
		w := writableSettings{}
		t.Setenv(antivirus.MaxScanSetting.EnvVar, tc.envBytes)
		require.NoError(t, antivirus.MaxScanSetting.Seed(ctx, w))
		assert.Equal(t, tc.wantMB, w[antivirus.MaxScanSetting.Key], "env %s", tc.envBytes)
		assert.Equal(t, tc.wantCap, antivirus.MaxScanBytesFrom(ctx, w), "env %s", tc.envBytes)

		old, err := strconv.ParseInt(tc.envBytes, 10, 64)
		require.NoError(t, err)
		if old <= int64(10240)<<20 {
			assert.GreaterOrEqual(t, antivirus.MaxScanBytesFrom(ctx, w), old,
				"the seeded ceiling must never be smaller than the byte value it came from")
		}
	}
}

// Once a row exists the variable is inert, exactly like every other setting in
// this family.
func TestMaxScan_EnvIsSeedNotOverride(t *testing.T) {
	ctx := context.Background()
	w := writableSettings{}

	t.Setenv(antivirus.MaxScanSetting.EnvVar, "52428800")
	require.NoError(t, antivirus.MaxScanSetting.Seed(ctx, w))
	require.Equal(t, "50", w[antivirus.MaxScanSetting.Key])

	// Admin lowers it on the Protection page.
	require.NoError(t, w.UpsertSetting(ctx, antivirus.MaxScanSetting.Key, "20"))

	// Operator edits compose and restarts.
	t.Setenv(antivirus.MaxScanSetting.EnvVar, "1073741824")
	require.NoError(t, antivirus.MaxScanSetting.Seed(ctx, w))
	assert.Equal(t, "20", w[antivirus.MaxScanSetting.Key], "the stored row wins")
	assert.EqualValues(t, 20<<20, antivirus.MaxScanBytesFrom(ctx, w))
}

func TestMaxScan_UnparseableSeedIsIgnored(t *testing.T) {
	ctx := context.Background()
	w := writableSettings{}
	t.Setenv(antivirus.MaxScanSetting.EnvVar, "100MB")
	require.NoError(t, antivirus.MaxScanSetting.Seed(ctx, w))
	assert.Empty(t, w)
	assert.EqualValues(t, 100<<20, antivirus.MaxScanBytesFrom(ctx, w))
}
