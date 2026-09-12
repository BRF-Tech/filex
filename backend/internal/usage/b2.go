package usage

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// B2 reads Backblaze B2's daily usage report.
//
// # What it reads
//
// B2 writes one CSV per day into a restricted bucket of its own,
// `b2-reports-<accountId>`, under a `YYYY-MM-DD/` folder. The bucket is
// read-only from the outside — filex never writes there — and it is reachable
// over the same S3 API filex already speaks, so this needs no B2 SDK and no new
// dependency: it is handed a storage.Driver pointed at that bucket.
//
// ⚠ The file is NOT always `usage.account-<id>.csv`. Backblaze documents three
// shapes — `usage.account-{id}.csv` for a standalone account,
// `usage.{resource_name}.{reporting_location}.csv` for an organization and
// `usage.group-{groupId}.{reportingLocation}.csv` for a group — so the reader
// LISTS the day's folder instead of constructing one name. An operator whose
// account is in an organization would otherwise get an empty report and no
// error, which is the worst of both.
//
// That also means it can be exercised against any S3 server. The tests run it
// against a local one with byte-exact copies of B2's own rows, which measures
// the part that can be measured without an account: the reading, the parsing
// and the arithmetic.
//
// # What it deliberately does not do
//
// It does not compute money. Prices change, allowances change, and a number
// baked into a binary is wrong the day after it ships — pricing is
// configuration elsewhere, and this type produces only what B2 reported.
type B2 struct {
	// Driver is an initialized storage driver whose root is the reports
	// bucket. Only List/Read are used.
	Driver storage.Driver
	// AccountID is the B2 account the report belongs to. Optional: it is used
	// to build the standalone file name when the day's folder cannot be
	// listed, and is otherwise only a label.
	AccountID string
	// Prefix is where the dated folders live inside the bucket. Empty is the
	// bucket root, which is what B2 does today; it exists so an operator who
	// mirrors the reports somewhere else can still point filex at them.
	Prefix string
}

// Name implements Source.
func (B2) Name() string { return "b2" }

// b2Columns are the header names this reader understands. Parsing is BY NAME:
// B2 has added columns before, and a positional reader would silently shift
// every value one to the left the day it happens again.
const (
	colDate       = "date"
	colBucketID   = "bucket_id"
	colBucketName = "bucket_name"
	colUploadedGB = "uploaded_gb"
	colDeletedGB  = "deleted_gb"
	colDownloaded = "downloaded_bytes"
	colFavored    = "downloaded_favored_bytes"
	colStoredGB   = "stored_gb"
	colByteHours  = "storage_byte_hours"
	colTxnA       = "api_txn_class_a"
	colTxnB       = "api_txn_class_b"
	colTxnC       = "api_txn_class_c"
	colTxnD       = "api_txn_class_d"
	// colLocation appears since B2 gained regions. A bucket can report from
	// more than one, and they are priced separately, so it travels with the
	// row rather than being flattened away.
	colLocation = "reporting_location"
)

// gigabyte is the unit B2's *_gb columns are in. B2 bills in decimal GB, not
// GiB — using 1024³ here would quietly overstate every upload by 7%.
const gigabyte = 1_000_000_000.0

// Fetch implements Source. It asks for one file per day in the range.
//
// A day B2 has not published is absent from the result rather than present as
// a zero row: "nothing happened that day" and "the report is not out yet" are
// different statements, and today's report does not exist until B2 writes it.
func (b B2) Fetch(ctx context.Context, from, to time.Time) ([]Day, error) {
	if b.Driver == nil {
		return nil, errors.New("usage/b2: no storage driver")
	}
	if to.Before(from) {
		from, to = to, from
	}

	var out []Day
	for d := from.UTC().Truncate(24 * time.Hour); !d.After(to.UTC()); d = d.AddDate(0, 0, 1) {
		rows, err := b.fetchDay(ctx, d)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue // not published (yet)
			}
			return nil, fmt.Errorf("usage/b2: %s: %w", d.Format("2006-01-02"), err)
		}
		out = append(out, rows...)
	}
	SortDays(out)
	return out, nil
}

// Path is the documented STANDALONE file name for one day. It is the fallback
// when the day's folder cannot be listed, and the name to quote to an operator
// checking the bucket by hand.
func (b B2) Path(day time.Time) string {
	name := fmt.Sprintf("%s/usage.account-%s.csv", day.UTC().Format("2006-01-02"), b.AccountID)
	if p := strings.Trim(b.Prefix, "/"); p != "" {
		return p + "/" + name
	}
	return name
}

func (b B2) fetchDay(ctx context.Context, day time.Time) ([]Day, error) {
	names, err := b.usageFiles(ctx, day)
	if err != nil {
		return nil, err
	}
	var out []Day
	for _, name := range names {
		rc, err := b.Driver.Read(ctx, name)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue
			}
			return nil, err
		}
		rows, perr := b.parse(day, rc)
		rc.Close()
		if perr != nil {
			return nil, fmt.Errorf("%s: %w", name, perr)
		}
		out = append(out, rows...)
	}
	if len(out) == 0 {
		return nil, storage.ErrNotFound
	}
	return out, nil
}

// usageFiles names the usage CSVs in one day's folder.
//
// ⚠ Two families of file live beside them and must not be parsed: the AUDIT
// report (`usage.audit-*`), which is a different schema, and the LOCATIONS
// file (`*.reportingLocations.csv`), which is a lookup table. Reading either as
// usage produces rows that are wrong rather than absent.
func (b B2) usageFiles(ctx context.Context, day time.Time) ([]string, error) {
	dir := b.dayPrefix(day)
	objs, err := b.Driver.List(ctx, dir)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) && b.AccountID != "" {
			// A driver that cannot list (or a day that is not there) still
			// gets the documented standalone name a try.
			return []string{b.Path(day)}, nil
		}
		return nil, err
	}
	var out []string
	for _, o := range objs {
		name := path.Base(o.Name)
		if !strings.HasPrefix(name, "usage.") || !strings.HasSuffix(name, ".csv") {
			continue
		}
		if strings.HasPrefix(name, "usage.audit-") || strings.HasSuffix(name, ".reportingLocations.csv") {
			continue
		}
		out = append(out, strings.TrimSuffix(dir, "/")+"/"+name)
	}
	sort.Strings(out)
	if len(out) == 0 && b.AccountID != "" {
		return []string{b.Path(day)}, nil
	}
	return out, nil
}

// dayPrefix is the folder one day's reports live in.
func (b B2) dayPrefix(day time.Time) string {
	d := day.UTC().Format("2006-01-02")
	if p := strings.Trim(b.Prefix, "/"); p != "" {
		return p + "/" + d
	}
	return d
}

// parse turns one CSV into rows. Exported behaviour is tested directly; the
// method stays unexported so the file format cannot become an API.
func (b B2) parse(day time.Time, r io.Reader) ([]Day, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // a trailing empty column must not fail the day
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	// The three columns everything else is derived from. A file without them
	// is not the report we think it is, and guessing would produce plausible
	// numbers from the wrong file.
	for _, need := range []string{colDate, colBucketID, colByteHours} {
		if _, ok := idx[need]; !ok {
			return nil, fmt.Errorf("missing column %q — is this a B2 usage report?", need)
		}
	}

	var out []Day
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read row: %w", err)
		}
		get := func(col string) string {
			i, ok := idx[col]
			if !ok || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}

		rowDate := day
		if v := get(colDate); v != "" {
			if parsed, err := time.Parse("2006-01-02", v); err == nil {
				rowDate = parsed.UTC()
			}
		}

		scope := ScopeBucket
		if get(colBucketID) == "" {
			// B2's account line. It carries the transactions that belong to no
			// bucket, and adding it to the bucket rows counts them twice.
			scope = ScopeAccount
		}

		out = append(out, Day{
			Date:     rowDate,
			Provider: "b2",
			Source:   SourceProvider,
			Scope:    scope,
			Bucket:   get(colBucketName),
			Location: get(colLocation),

			StoredBytes: gbToBytes(get(colStoredGB)),
			ByteHours:   parseFloat(get(colByteHours)),

			UploadedBytes: gbToBytes(get(colUploadedGB)),
			DeletedBytes:  gbToBytes(get(colDeletedGB)),
			// ⚠ downloaded_bytes, not downloaded_gb: the GB column is rounded
			// to two decimals, which reads as 0.00 for anything under 5 MB and
			// would report a busy day of small files as no egress at all.
			DownloadedBytes: parseInt(get(colDownloaded)),
			FreeEgressBytes: parseInt(get(colFavored)),

			OpsA: parseInt(get(colTxnA)),
			OpsB: parseInt(get(colTxnB)),
			OpsC: parseInt(get(colTxnC)),
			OpsD: parseInt(get(colTxnD)),
		})
	}
	return out, nil
}

func parseInt(s string) int64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		// Some columns arrive as "1.0" in older reports.
		if f, ferr := strconv.ParseFloat(s, 64); ferr == nil {
			return int64(f)
		}
		return 0
	}
	return v
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func gbToBytes(s string) int64 {
	return int64(parseFloat(s) * gigabyte)
}
