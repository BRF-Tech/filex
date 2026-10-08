// Package usage answers a question filex could not answer at all: what does
// the storage behind it cost?
//
// # Two kinds of answer, never mixed
//
// A provider that publishes usage (Backblaze B2 does, as a daily CSV) is the
// accurate one: it is what the invoice is computed from, it reports byte-hours
// rather than a sampled size, and it knows which egress was free. Most filex
// installs are not on such a provider — they are on Hetzner, Garage, MinIO, a
// plain disk — and for those filex can only meter itself, which is an estimate.
//
// Both are useful. Blending them silently is not: a number that looks
// authoritative and is not is worse than no number. So every row carries its
// Source, and nothing in this package adds a provider row to a measured one.
//
// # Scope, and the double-count that is easy to ship
//
// B2's report has an account-level row (empty bucket) alongside the per-bucket
// rows. Summing them adds the same transactions twice — by exactly the amount
// nobody notices, since the account row is usually small. Rows therefore carry
// a Scope, and Totals refuses to mix the two.
package usage

import (
	"context"
	"sort"
	"time"
)

// Source kinds. A row's Source is how it was obtained, not who stored the
// bytes: a B2 row read from the daily report is SourceProvider, and a B2
// storage metered by filex would be SourceFilex.
const (
	// SourceProvider — read from the provider's own usage report.
	SourceProvider = "provider"
	// SourceFilex — counted by filex as it moved the bytes. An estimate:
	// anything written to the backend by another tool is invisible to it, and
	// stored size is a point-in-time reading rather than byte-hours.
	SourceFilex = "filex"
)

// Scope separates the two row shapes a provider report carries.
type Scope string

const (
	// ScopeBucket — one bucket's usage for one day. These sum.
	ScopeBucket Scope = "bucket"
	// ScopeAccount — the account's own line, which in B2's report carries the
	// transactions that belong to no bucket. It must NOT be added to the
	// bucket rows; it is reported beside them.
	ScopeAccount Scope = "account"
)

// Day is one day of usage for one bucket (or the account), normalized across
// providers. Everything provider-specific is resolved before a value gets
// here, so a consumer never branches on which provider it came from.
//
// ⚠ Zero is a real value and "not reported" is a different thing. A provider
// that does not report byte-hours leaves ByteHours at 0 and fills StoredBytes;
// a consumer that wants "how much was stored" must prefer ByteHours when it is
// non-zero rather than assume both are always present.
type Day struct {
	Date     time.Time `json:"date"`
	Provider string    `json:"provider"`
	Source   string    `json:"source"`
	Scope    Scope     `json:"scope"`
	Bucket   string    `json:"bucket,omitempty"`
	// Location is the provider's reporting region for the row, when it reports
	// one. B2 prices regions separately, so two rows for one bucket are two
	// rows, not one to be added up.
	Location string `json:"location,omitempty"`

	// StoredBytes is the size at the end of the day, where that is all the
	// provider reports.
	StoredBytes int64 `json:"stored_bytes"`
	// ByteHours is storage integrated over the day — what storage is actually
	// billed on. 0 when the provider does not report it.
	ByteHours float64 `json:"byte_hours"`

	UploadedBytes   int64 `json:"uploaded_bytes"`
	DeletedBytes    int64 `json:"deleted_bytes"`
	DownloadedBytes int64 `json:"downloaded_bytes"`
	// FreeEgressBytes is the part of the download that the provider did not
	// charge for (B2's CDN-partner egress). 0 elsewhere.
	FreeEgressBytes int64 `json:"free_egress_bytes"`

	// Transactions, in the classes every object store bills in even when it
	// names them differently: A = writes, B = reads, C = listings/metadata,
	// D = the provider's own extra class (B2 uses it for some downloads).
	OpsA int64 `json:"ops_a"`
	OpsB int64 `json:"ops_b"`
	OpsC int64 `json:"ops_c"`
	OpsD int64 `json:"ops_d"`
}

// Source produces normalized days for one provider account.
type Source interface {
	// Name identifies the provider ("b2", "filex", …).
	Name() string
	// Fetch returns every day it has in [from, to], inclusive of both ends and
	// in date order. A day the provider has not published yet is simply
	// absent — never a zero row, which would read as "nothing happened".
	Fetch(ctx context.Context, from, to time.Time) ([]Day, error)
}

// Totals is a summary over a set of days.
type Totals struct {
	From, To time.Time `json:"-"`
	Days     int       `json:"days"`

	// AvgStoredBytes is the mean of the daily storage readings — byte-hours
	// where the provider reports them, StoredBytes otherwise.
	AvgStoredBytes float64 `json:"avg_stored_bytes"`
	// PeakStoredBytes is the largest single-day reading.
	PeakStoredBytes int64 `json:"peak_stored_bytes"`

	UploadedBytes   int64 `json:"uploaded_bytes"`
	DeletedBytes    int64 `json:"deleted_bytes"`
	DownloadedBytes int64 `json:"downloaded_bytes"`
	FreeEgressBytes int64 `json:"free_egress_bytes"`

	OpsA int64 `json:"ops_a"`
	OpsB int64 `json:"ops_b"`
	OpsC int64 `json:"ops_c"`
	OpsD int64 `json:"ops_d"`

	// AccountOps holds the account-scoped transactions, kept beside the bucket
	// totals rather than inside them. See Scope.
	AccountOps struct {
		A, B, C, D int64
	} `json:"account_ops"`
	// AccountOpsTotal is A+B+C+D of AccountOps, the one number the page
	// prints beside the bucket table.
	AccountOpsTotal int64 `json:"account_ops_total"`
}

// BucketTotal is one bucket's usage over a window: a row of the page's
// bucket table. A bucket the provider reports in two regions is two rows
// (Location), never one added up.
type BucketTotal struct {
	// Label names the row as the table prints it: the bucket, with its
	// region in brackets when the provider reports one, "-" for an unnamed
	// bucket.
	Label    string `json:"label"`
	Bucket   string `json:"bucket"`
	Location string `json:"location,omitempty"`
	// AvgStoredBytes is the bucket's mean daily storage reading over the
	// window's days (storage is a reading, so it is averaged, not summed).
	AvgStoredBytes  float64 `json:"avg_stored_bytes"`
	UploadedBytes   int64   `json:"uploaded_bytes"`
	DownloadedBytes int64   `json:"downloaded_bytes"`
	// Ops is every transaction class together (A+B+C+D).
	Ops int64 `json:"ops"`
}

// TrendPoint is one day of the storage trend: every bucket's reading that
// day, together.
type TrendPoint struct {
	Date        string  `json:"date"`
	StoredBytes float64 `json:"stored_bytes"`
}

// SumBuckets totals the BUCKET rows and reports the account rows separately.
//
// ⚠ It sums only rows whose Source agrees. Handing it a mixture returns the
// provider rows and ignores the measured ones, because adding an invoice to an
// estimate produces a number that is neither.
func SumBuckets(days []Day) Totals {
	var t Totals
	if len(days) == 0 {
		return t
	}
	source := preferredSource(days)

	perDay := map[string]float64{}
	for _, d := range days {
		if d.Source != source {
			continue
		}
		if d.Scope == ScopeAccount {
			t.AccountOps.A += d.OpsA
			t.AccountOps.B += d.OpsB
			t.AccountOps.C += d.OpsC
			t.AccountOps.D += d.OpsD
			continue
		}
		key := d.Date.UTC().Format("2006-01-02")
		perDay[key] += storedReading(d)

		t.UploadedBytes += d.UploadedBytes
		t.DeletedBytes += d.DeletedBytes
		t.DownloadedBytes += d.DownloadedBytes
		t.FreeEgressBytes += d.FreeEgressBytes
		t.OpsA += d.OpsA
		t.OpsB += d.OpsB
		t.OpsC += d.OpsC
		t.OpsD += d.OpsD

		if t.From.IsZero() || d.Date.Before(t.From) {
			t.From = d.Date
		}
		if d.Date.After(t.To) {
			t.To = d.Date
		}
	}

	t.AccountOpsTotal = t.AccountOps.A + t.AccountOps.B + t.AccountOps.C + t.AccountOps.D
	t.Days = len(perDay)
	var sum float64
	for _, v := range perDay {
		sum += v
		if int64(v) > t.PeakStoredBytes {
			t.PeakStoredBytes = int64(v)
		}
	}
	if t.Days > 0 {
		t.AvgStoredBytes = sum / float64(t.Days)
	}
	return t
}

// bucketRows is the rows SumBuckets adds up: the bucket rows of the
// preferred source. ⚠ The page's bucket table and trend were summed in the
// browser from every bucket row, of whichever source - the filter SumBuckets
// applies was not there - so the day a measured source is added beside a
// provider's report the table would have added an estimate to an invoice.
func bucketRows(days []Day) []Day {
	if len(days) == 0 {
		return nil
	}
	source := preferredSource(days)
	out := make([]Day, 0, len(days))
	for _, d := range days {
		if d.Source == source && d.Scope != ScopeAccount {
			out = append(out, d)
		}
	}
	return out
}

// PerBucket is the bucket table: one row per bucket (and region) over the
// window, largest mean storage first. The mean is over the days the window
// has rows for, the same day count Totals reports.
func PerBucket(days []Day) []BucketTotal {
	rows := bucketRows(days)
	if len(rows) == 0 {
		return []BucketTotal{}
	}
	dates := map[string]bool{}
	by := map[string]*BucketTotal{}
	order := []string{}
	for _, d := range rows {
		dates[d.Date.UTC().Format("2006-01-02")] = true
		key := d.Bucket + "|" + d.Location // a bucket name has no "|"
		b, ok := by[key]
		if !ok {
			label := d.Bucket
			if label == "" {
				label = "-"
			}
			if d.Location != "" {
				label += " (" + d.Location + ")"
			}
			b = &BucketTotal{Label: label, Bucket: d.Bucket, Location: d.Location}
			by[key] = b
			order = append(order, key)
		}
		b.AvgStoredBytes += storedReading(d)
		b.UploadedBytes += d.UploadedBytes
		b.DownloadedBytes += d.DownloadedBytes
		b.Ops += d.OpsA + d.OpsB + d.OpsC + d.OpsD
	}
	n := float64(len(dates))
	out := make([]BucketTotal, 0, len(order))
	for _, k := range order {
		b := *by[k]
		b.AvgStoredBytes /= n
		out = append(out, b)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AvgStoredBytes != out[j].AvgStoredBytes {
			return out[i].AvgStoredBytes > out[j].AvgStoredBytes
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// Trend is the daily storage trend: one point per day, every bucket of the
// preferred source together, in date order.
func Trend(days []Day) []TrendPoint {
	by := map[string]float64{}
	for _, d := range bucketRows(days) {
		by[d.Date.UTC().Format("2006-01-02")] += storedReading(d)
	}
	out := make([]TrendPoint, 0, len(by))
	for date, v := range by {
		out = append(out, TrendPoint{Date: date, StoredBytes: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// storedReading is the day's storage number in bytes: byte-hours divided by
// the hours in a day when the provider reports them (so it is comparable with
// a size), the end-of-day size otherwise.
func storedReading(d Day) float64 {
	if d.ByteHours > 0 {
		return d.ByteHours / 24
	}
	return float64(d.StoredBytes)
}

// preferredSource picks the provider rows when a slice carries both kinds.
func preferredSource(days []Day) string {
	for _, d := range days {
		if d.Source == SourceProvider {
			return SourceProvider
		}
	}
	return days[0].Source
}

// SortDays orders by date, then bucket, so a table renders deterministically
// whatever order a source produced.
func SortDays(days []Day) {
	sort.SliceStable(days, func(i, j int) bool {
		if !days[i].Date.Equal(days[j].Date) {
			return days[i].Date.Before(days[j].Date)
		}
		if days[i].Scope != days[j].Scope {
			return days[i].Scope == ScopeAccount
		}
		return days[i].Bucket < days[j].Bucket
	})
}
