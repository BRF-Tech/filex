package usage_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/usage"
)

func approx(t *testing.T, want, got float64, label string) {
	t.Helper()
	if math.Abs(want-got) > math.Max(1e-9, math.Abs(want)*1e-6) {
		t.Fatalf("%s: want %v, got %v", label, want, got)
	}
}

// A small account, entirely inside B2's free allowances — which is the state
// most filex installs are in, and the state a cost page must render as "free"
// rather than as a rounding-error total.
func TestEstimate_InsideTheAllowancesCostsNothing(t *testing.T) {
	tot := usage.Totals{
		Days:            30,
		AvgStoredBytes:  2 * 1_000_000_000, // 2 GB, under the 10 GB free
		DownloadedBytes: 1_000_000_000,     // 1 GB, under 3x stored
		OpsC:            50_000,            // class C is free on B2
	}
	c := usage.Estimate(tot, usage.BackblazeB2())

	require.Zero(t, c.StorageCost)
	require.Zero(t, c.EgressCost)
	require.Zero(t, c.OpsCost)
	require.Zero(t, c.Total)
	require.True(t, c.StorageCovered, "the page must be able to say WHY it is zero")
	require.True(t, c.EgressCovered)
}

// The arithmetic, on numbers a person can check by hand.
func TestEstimate_ChargesWhatIsBeyondTheAllowances(t *testing.T) {
	tot := usage.Totals{
		Days:           30,
		AvgStoredBytes: 1_010 * 1_000_000_000, // 1010 GB stored
		// 4 TB downloaded, of which 1 TB the provider already marked free.
		DownloadedBytes: 4_000 * 1_000_000_000,
		FreeEgressBytes: 1_000 * 1_000_000_000,
		OpsD:            100_000,
	}
	p := usage.BackblazeB2()
	c := usage.Estimate(tot, p)

	period := 30 * 24 / 730.0 // the period in months

	// storage: (1010 - 10 free) GB × the period × $0.00695
	approx(t, 1000*period*0.00695, c.StorageCost, "storage")

	// egress: 4000 - 1000 (provider-free) - 3×1010×period (allowance)
	allowance := 1010 * 3 * period
	approx(t, math.Max(0, 3000-allowance)*0.01, c.EgressCost, "egress")

	// class D: 100k calls - 2500/day × 30 days free = 25k billable
	approx(t, 25_000/10_000.0*0.004, c.OpsCost, "transactions")
	require.Equal(t, int64(25_000), c.BillableOps)
	require.Equal(t, int64(75_000), c.FreeOps)

	approx(t, c.StorageCost+c.EgressCost+c.OpsCost, c.Total, "total")
}

// A class the provider does not charge for must not consume the free-call
// allowance — otherwise a busy listing day would make real class-D calls
// look billable.
func TestEstimate_FreeClassesDoNotEatTheAllowance(t *testing.T) {
	p := usage.BackblazeB2()
	tot := usage.Totals{
		Days: 1,
		OpsC: 1_000_000, // free on B2
		OpsD: 3_000,     // 2500 free, 500 billable
		OpsA: 50_000,    // free on B2
		OpsB: 50_000,    // free on B2
	}
	c := usage.Estimate(tot, p)
	require.Equal(t, int64(500), c.BillableOps)
	approx(t, 500/10_000.0*0.004, c.OpsCost, "class D only")
}

// The account row's transactions belong in the bill even though they are not
// summed into the bucket rows.
func TestEstimate_CountsTheAccountRowsTransactions(t *testing.T) {
	p := usage.Pricing{Currency: "USD", ClassCPer10k: 1, FreeClassPerDay: 0}
	tot := usage.Totals{Days: 1, OpsC: 10_000}
	tot.AccountOps.C = 10_000

	c := usage.Estimate(tot, p)
	require.Equal(t, int64(20_000), c.BillableOps)
	approx(t, 2, c.OpsCost, "both lines are billed")
}

// An empty period is zero, not a division by zero.
func TestEstimate_EmptyPeriod(t *testing.T) {
	c := usage.Estimate(usage.Totals{}, usage.BackblazeB2())
	require.Zero(t, c.Total)
	require.Zero(t, c.Days)
}

// End to end on the reporter's own numbers: read the report, total it, price
// it. The account is tiny, so the answer must be exactly zero — and the page
// must be able to say it is zero because the allowances cover it.
func TestEstimate_OnTheReportersOwnDay(t *testing.T) {
	drv := localReports(t, map[string]string{
		"2026-09-10/usage.account-abc123.csv": b2Rows,
	})
	rows, err := usage.B2{Driver: drv, AccountID: "abc123"}.Fetch(ctxTODO(), day(2026, 9, 10), day(2026, 9, 10))
	require.NoError(t, err)

	c := usage.Estimate(usage.SumBuckets(rows), usage.BackblazeB2())
	require.Zero(t, c.Total)
	require.True(t, c.StorageCovered)
	require.Less(t, c.AvgStoredGB, 1.0, "half a gigabyte across four buckets")
}

func ctxTODO() context.Context { return context.Background() }

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
