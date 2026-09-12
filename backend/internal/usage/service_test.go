package usage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/usage"
)

// fakeSettings is the settings table, as a map.
type fakeSettings map[string]string

func (f fakeSettings) GetSetting(_ context.Context, key string) (string, error) {
	return f[key], nil
}

// countingLookup resolves a storage name and records how often it was asked,
// so the cache can be proven rather than assumed.
type countingLookup struct {
	drv   storage.Driver
	calls int
}

func (c *countingLookup) lookup(_ context.Context, name string) (storage.Driver, error) {
	c.calls++
	if name != "b2-reports" {
		return nil, storage.ErrNotFound
	}
	return c.drv, nil
}

func TestService_UnconfiguredIsNotZeroSpend(t *testing.T) {
	svc := usage.NewService(fakeSettings{}, nil, 0)
	rep, err := svc.Report(context.Background(), day(2026, 9, 1), day(2026, 9, 30))
	require.NoError(t, err)
	require.False(t, rep.Configured,
		"an unconfigured instance must say so — an empty chart reads as 'you spent nothing'")
	require.Empty(t, rep.Days)
	require.Zero(t, rep.Cost.Total)
}

func TestService_ReadsTheReportAndPricesIt(t *testing.T) {
	drv := localReports(t, map[string]string{
		"2026-09-10/usage.account-abc123.csv": b2Rows,
	})
	look := &countingLookup{drv: drv}
	svc := usage.NewService(fakeSettings{
		"usage.provider":       "b2",
		"usage.report_storage": "b2-reports",
		"usage.account_id":     "abc123",
	}, look.lookup, time.Hour)

	rep, err := svc.Report(context.Background(), day(2026, 9, 10), day(2026, 9, 10))
	require.NoError(t, err)
	require.True(t, rep.Configured)
	require.Len(t, rep.Days, 5)
	require.Equal(t, int64(645), rep.Totals.OpsC)
	require.Equal(t, int64(87), rep.Totals.AccountOps.C)
	require.Equal(t, "USD", rep.Cost.Currency)

	// The page must be told the prices are ours, not the operator's contract.
	require.True(t, rep.Settings.PricingIsDefault)
	require.Contains(t, rep.Notes[len(rep.Notes)-1], "not your contract")

	// Second call inside the TTL comes from the cache: one lookup, one fetch.
	rep2, err := svc.Report(context.Background(), day(2026, 9, 10), day(2026, 9, 10))
	require.NoError(t, err)
	require.Len(t, rep2.Days, 5)
	require.Equal(t, 1, look.calls, "the provider is not re-read on every page load")
	require.Contains(t, rep2.Notes, "served from cache")

	// …and a settings change drops it.
	svc.Invalidate()
	_, err = svc.Report(context.Background(), day(2026, 9, 10), day(2026, 9, 10))
	require.NoError(t, err)
	require.Equal(t, 2, look.calls)
}

// A range the provider has not published is an explicit note, not an empty
// page: "no report yet" and "nothing happened" are different answers.
func TestService_SaysWhenTheProviderHasPublishedNothing(t *testing.T) {
	drv := localReports(t, map[string]string{})
	look := &countingLookup{drv: drv}
	svc := usage.NewService(fakeSettings{
		"usage.provider":       "b2",
		"usage.report_storage": "b2-reports",
	}, look.lookup, time.Hour)

	rep, err := svc.Report(context.Background(), day(2026, 9, 1), day(2026, 9, 3))
	require.NoError(t, err)
	require.Empty(t, rep.Days)
	require.Contains(t, rep.Notes[0], "published no report")
}

// An operator's own price table wins over ours, and is then not labelled as a
// guess.
func TestService_OperatorPricingWins(t *testing.T) {
	drv := localReports(t, map[string]string{
		"2026-09-10/usage.account-abc123.csv": b2Rows,
	})
	look := &countingLookup{drv: drv}
	svc := usage.NewService(fakeSettings{
		"usage.provider":       "b2",
		"usage.report_storage": "b2-reports",
		"usage.pricing":        `{"currency":"EUR","storage_per_gb_month":0.01,"free_storage_gb":0}`,
	}, look.lookup, time.Hour)

	rep, err := svc.Report(context.Background(), day(2026, 9, 10), day(2026, 9, 10))
	require.NoError(t, err)
	require.False(t, rep.Settings.PricingIsDefault)
	require.Equal(t, "EUR", rep.Cost.Currency)
	require.Greater(t, rep.Cost.StorageCost, 0.0, "no free allowance now, so a small bill appears")
	for _, n := range rep.Notes {
		require.NotContains(t, n, "not your contract")
	}
}

// A price table that does not parse must fall back to the defaults rather than
// to zero — a broken row that reads as "free" is the silent version of wrong.
func TestService_UnreadablePricingFallsBackLoudly(t *testing.T) {
	drv := localReports(t, map[string]string{
		"2026-09-10/usage.account-abc123.csv": b2Rows,
	})
	look := &countingLookup{drv: drv}
	svc := usage.NewService(fakeSettings{
		"usage.provider":       "b2",
		"usage.report_storage": "b2-reports",
		"usage.pricing":        "{not json",
	}, look.lookup, time.Hour)

	rep, err := svc.Report(context.Background(), day(2026, 9, 10), day(2026, 9, 10))
	require.NoError(t, err)
	require.True(t, rep.Settings.PricingIsDefault)
	require.Equal(t, usage.BackblazeB2().StoragePerGBMonth, rep.Settings.Pricing.StoragePerGBMonth)
}

// A storage name that no longer resolves is an error the operator can act on,
// not an empty report.
func TestService_MissingReportStorageIsAnError(t *testing.T) {
	look := &countingLookup{drv: nil}
	svc := usage.NewService(fakeSettings{
		"usage.provider":       "b2",
		"usage.report_storage": "gone",
	}, look.lookup, time.Hour)

	_, err := svc.Report(context.Background(), day(2026, 9, 10), day(2026, 9, 10))
	require.Error(t, err)
	require.Contains(t, err.Error(), "gone")
}
