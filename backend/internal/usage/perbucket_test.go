package usage_test

// The bucket table, the trend and the account line are added up HERE, with
// the same source rule as SumBuckets (0.54 audit, D9). The admin page summed
// the day rows itself, of whichever source - the day a measured source sits
// beside a provider's report, it would have added an estimate to an invoice.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/usage"
)

func mixedDays() []usage.Day {
	d1, d2 := day(2026, 9, 10), day(2026, 9, 11)
	return []usage.Day{
		{Date: d1, Source: usage.SourceProvider, Scope: usage.ScopeBucket, Bucket: "photos", StoredBytes: 100, UploadedBytes: 10, OpsA: 1},
		{Date: d2, Source: usage.SourceProvider, Scope: usage.ScopeBucket, Bucket: "photos", ByteHours: 24 * 300, DownloadedBytes: 7, OpsB: 2},
		{Date: d1, Source: usage.SourceProvider, Scope: usage.ScopeBucket, Bucket: "docs", Location: "eu-central", StoredBytes: 50},
		{Date: d1, Source: usage.SourceProvider, Scope: usage.ScopeAccount, OpsC: 5, OpsD: 1},
		// An estimate beside the invoice: never added to it.
		{Date: d1, Source: usage.SourceFilex, Scope: usage.ScopeBucket, Bucket: "photos", StoredBytes: 1_000_000, UploadedBytes: 1_000_000},
	}
}

func TestPerBucket_OneRowPerBucketAndRegionOfThePreferredSource(t *testing.T) {
	rows := usage.PerBucket(mixedDays())
	require.Len(t, rows, 2)
	assert.Equal(t, "photos", rows[0].Label, "largest mean storage first")
	assert.InDelta(t, (100.0+300.0)/2, rows[0].AvgStoredBytes, 0.001, "a reading is averaged over the window's days, byte-hours as a size")
	assert.EqualValues(t, 10, rows[0].UploadedBytes, "the measured row is not added to the provider's")
	assert.EqualValues(t, 7, rows[0].DownloadedBytes)
	assert.EqualValues(t, 3, rows[0].Ops)
	assert.Equal(t, "docs (eu-central)", rows[1].Label, "the region is named, a second region would be a second row")
	assert.Equal(t, "eu-central", rows[1].Location)
}

func TestTrend_OnePointPerDayEveryBucketTogether(t *testing.T) {
	tr := usage.Trend(mixedDays())
	require.Len(t, tr, 2)
	assert.Equal(t, "2026-09-10", tr[0].Date)
	assert.InDelta(t, 150.0, tr[0].StoredBytes, 0.001, "photos + docs, never the estimate")
	assert.Equal(t, "2026-09-11", tr[1].Date)
	assert.InDelta(t, 300.0, tr[1].StoredBytes, 0.001)
}

func TestSumBuckets_TheAccountLineHasItsOwnTotal(t *testing.T) {
	tot := usage.SumBuckets(mixedDays())
	assert.EqualValues(t, 6, tot.AccountOpsTotal)
}

func TestPerBucket_NothingIsAnEmptyTableNotNull(t *testing.T) {
	assert.NotNil(t, usage.PerBucket(nil))
	assert.NotNil(t, usage.Trend(nil))
}

// The notes are said in the reader's language (srvtext.WithReader on ctx).
func TestService_TheNotesAreInTheReadersLanguage(t *testing.T) {
	svc := usage.NewService(fakeSettings{
		"usage.provider":       "b2",
		"usage.report_storage": "b2-reports",
	}, (&countingLookup{drv: localReports(t, map[string]string{})}).lookup, time.Hour)
	ctx := srvtext.WithReader(context.Background(), "tr")
	rep, err := svc.Report(ctx, day(2026, 9, 10), day(2026, 9, 10))
	require.NoError(t, err)
	require.NotEmpty(t, rep.Notes)
	assert.Contains(t, rep.Notes, srvtext.Text("tr", "server.usage.note.no_report", nil))
	for _, n := range rep.Notes {
		assert.NotContains(t, n, "server.usage", "a catalogue key is never the note")
	}
	assert.NotNil(t, rep.Buckets)
	assert.NotNil(t, rep.Trend)
}
