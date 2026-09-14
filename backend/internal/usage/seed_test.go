package usage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/usage"
)

type seedRows map[string]string

func (s seedRows) GetSetting(_ context.Context, key string) (string, error) { return s[key], nil }
func (s seedRows) UpsertSetting(_ context.Context, key, value string) error {
	s[key] = value
	return nil
}

// Every usage setting declared a FILEX_USAGE_* variable and nothing ever read
// one: SeedSettings was never written, so a compose file that set them came up
// with an unconfigured page and no word about why. The variables seed the
// rows on first boot, like the antivirus family, and an admin's later edit
// wins over them.
func TestSeedSettings_FromTheEnvironmentOnFirstBootOnly(t *testing.T) {
	t.Setenv("FILEX_USAGE_PROVIDER", "B2")
	t.Setenv("FILEX_USAGE_REPORT_STORAGE", "b2-reports")
	t.Setenv("FILEX_USAGE_ACCOUNT_ID", "c9d3b1144c06")
	t.Setenv("FILEX_USAGE_PREFIX", "")
	t.Setenv("FILEX_USAGE_PRICING", `{"storage_gb_month": 0.006}`)

	ctx := context.Background()
	rows := seedRows{}
	usage.SeedSettings(ctx, rows)

	s := (&usage.Service{Get: rows}).Settings(ctx)
	assert.Equal(t, usage.ProviderB2, s.Provider, "normalized like a typed value")
	assert.Equal(t, "b2-reports", s.ReportStorage)
	assert.Equal(t, "c9d3b1144c06", s.AccountID)
	assert.True(t, s.Configured())

	rows[usage.SettingReportStorage.Key] = "renamed-in-the-ui"
	usage.SeedSettings(ctx, rows)
	assert.Equal(t, "renamed-in-the-ui", rows[usage.SettingReportStorage.Key], "the seed is spent once a row exists")
}

// A value that fails its check is not stored: the page stays unconfigured
// rather than configured with nonsense.
func TestSeedSettings_RefusesAnUnknownProvider(t *testing.T) {
	t.Setenv("FILEX_USAGE_PROVIDER", "dropbox")
	t.Setenv("FILEX_USAGE_REPORT_STORAGE", "")
	t.Setenv("FILEX_USAGE_ACCOUNT_ID", "")
	t.Setenv("FILEX_USAGE_PREFIX", "")
	t.Setenv("FILEX_USAGE_PRICING", "")
	rows := seedRows{}
	usage.SeedSettings(context.Background(), rows)
	assert.Empty(t, rows[usage.SettingProvider.Key])
}
