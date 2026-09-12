package usage

import (
	"math"
	"time"
)

// Pricing is what a provider charges, as configuration rather than as code.
//
// ⚠ Nothing here is baked into the binary as a fact. Prices change, allowances
// change, and a number compiled in is wrong the day after it ships — with no
// symptom, because a plausible figure looks exactly like a correct one. The
// defaults below carry the date they were read and are meant to be edited by
// the operator, who is the one holding the invoice.
type Pricing struct {
	// Currency is a display label. No conversion is ever performed.
	Currency string `json:"currency"`

	// StoragePerGBMonth is charged on the average stored over the period.
	StoragePerGBMonth float64 `json:"storage_per_gb_month"`
	// FreeStorageGB is deducted from the average before charging.
	FreeStorageGB float64 `json:"free_storage_gb"`

	// EgressPerGB is charged on downloads beyond the allowances.
	EgressPerGB float64 `json:"egress_per_gb"`
	// FreeEgressMultiple is the allowance expressed as a multiple of stored
	// data — B2 gives 3x the average stored, per month. 0 disables it.
	FreeEgressMultiple float64 `json:"free_egress_multiple"`

	// Transactions, per ten thousand, by class.
	ClassAPer10k float64 `json:"class_a_per_10k"`
	ClassBPer10k float64 `json:"class_b_per_10k"`
	ClassCPer10k float64 `json:"class_c_per_10k"`
	ClassDPer10k float64 `json:"class_d_per_10k"`
	// FreeClassPerDay is how many transactions of each PRICED class are free
	// each day. B2 applies it to class D.
	FreeClassPerDay int64 `json:"free_class_per_day"`

	// Note carries where these numbers came from, so the next person can
	// check them instead of trusting them.
	Note string `json:"note,omitempty"`
}

// BackblazeB2 is a starting point, not an authority: read from
// backblaze.com/cloud-storage/pricing on 2026-09-11. Check it against your own
// invoice before believing any figure computed from it.
func BackblazeB2() Pricing {
	return Pricing{
		Currency:           "USD",
		StoragePerGBMonth:  0.00695, // $6.95 / TB / month
		FreeStorageGB:      10,
		EgressPerGB:        0.01,
		FreeEgressMultiple: 3,
		// Class A, B and C are free; class D is the priced one.
		ClassDPer10k:    0.004,
		FreeClassPerDay: 2500,
		Note:            "backblaze.com/cloud-storage/pricing, read 2026-09-11",
	}
}

// Cost is an estimate, broken into the lines an invoice has, with the free
// part of each shown beside the billable part.
//
// ⚠ It is an ESTIMATE even when it is computed from the provider's own report:
// allowances are applied over the period asked for rather than over the
// provider's billing month, taxes and minimums are not modelled, and a
// provider may round differently. It exists to answer "what is this costing
// me, roughly, and where is it going" — not to reconcile a bill.
type Cost struct {
	Currency string `json:"currency"`
	Days     int    `json:"days"`

	// Storage.
	AvgStoredGB    float64 `json:"avg_stored_gb"`
	BillableGBMon  float64 `json:"billable_gb_months"`
	StorageCost    float64 `json:"storage_cost"`
	FreeStorageGB  float64 `json:"free_storage_gb"`
	StorageCovered bool    `json:"storage_covered"`

	// Egress.
	DownloadedGB   float64 `json:"downloaded_gb"`
	ProviderFreeGB float64 `json:"provider_free_gb"`
	AllowanceGB    float64 `json:"allowance_gb"`
	BillableGB     float64 `json:"billable_gb"`
	EgressCost     float64 `json:"egress_cost"`
	EgressCovered  bool    `json:"egress_covered"`

	// Transactions.
	BillableOps int64   `json:"billable_ops"`
	FreeOps     int64   `json:"free_ops"`
	OpsCost     float64 `json:"ops_cost"`

	Total float64 `json:"total"`
}

// hoursPerMonth is the convention every object store bills storage on: a
// 730-hour month (365 days / 12).
const hoursPerMonth = 730.0

// Estimate prices a set of totals.
//
// The inputs are the ones the provider actually reports: an average stored
// figure derived from byte-hours where they exist, the downloaded bytes, the
// part of the download the provider already marked free, and the transaction
// counts.
func Estimate(t Totals, p Pricing) Cost {
	c := Cost{Currency: p.Currency, Days: t.Days}
	if t.Days == 0 {
		return c
	}
	period := float64(t.Days) * 24 / hoursPerMonth // the period, in months

	// ── storage ──────────────────────────────────────────────────────────
	c.AvgStoredGB = float64(t.AvgStoredBytes) / gigabyte
	c.FreeStorageGB = p.FreeStorageGB
	billableGB := math.Max(0, c.AvgStoredGB-p.FreeStorageGB)
	c.BillableGBMon = billableGB * period
	c.StorageCost = c.BillableGBMon * p.StoragePerGBMonth
	c.StorageCovered = billableGB == 0 && c.AvgStoredGB > 0

	// ── egress ───────────────────────────────────────────────────────────
	c.DownloadedGB = float64(t.DownloadedBytes) / gigabyte
	// What the provider already settled: B2 marks CDN-partner egress free in
	// the report itself, so it is subtracted before the allowance rather than
	// counted against it.
	c.ProviderFreeGB = float64(t.FreeEgressBytes) / gigabyte
	chargeable := math.Max(0, c.DownloadedGB-c.ProviderFreeGB)
	// The allowance is a multiple of what is stored, pro-rated to the period.
	c.AllowanceGB = c.AvgStoredGB * p.FreeEgressMultiple * math.Max(period, 0)
	c.BillableGB = math.Max(0, chargeable-c.AllowanceGB)
	c.EgressCost = c.BillableGB * p.EgressPerGB
	c.EgressCovered = chargeable > 0 && c.BillableGB == 0

	// ── transactions ─────────────────────────────────────────────────────
	// Only the priced classes count toward the daily free allowance: giving a
	// free-of-charge class its own allowance would consume an allowance that
	// the provider never applies.
	for _, cls := range []struct {
		count int64
		rate  float64
	}{
		{t.OpsA + t.AccountOps.A, p.ClassAPer10k},
		{t.OpsB + t.AccountOps.B, p.ClassBPer10k},
		{t.OpsC + t.AccountOps.C, p.ClassCPer10k},
		{t.OpsD + t.AccountOps.D, p.ClassDPer10k},
	} {
		if cls.rate <= 0 || cls.count <= 0 {
			continue
		}
		free := p.FreeClassPerDay * int64(t.Days)
		billable := cls.count - free
		if billable < 0 {
			billable = 0
		}
		c.FreeOps += cls.count - billable
		c.BillableOps += billable
		c.OpsCost += float64(billable) / 10000 * cls.rate
	}

	c.Total = c.StorageCost + c.EgressCost + c.OpsCost
	return c
}

// Month returns the first and last day of the month containing t, in UTC —
// the range a "this month so far" view asks for.
func Month(t time.Time) (from, to time.Time) {
	u := t.UTC()
	from = time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
	to = from.AddDate(0, 1, -1)
	return from, to
}
