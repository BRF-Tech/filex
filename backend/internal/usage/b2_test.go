package usage_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/s3"
	"github.com/brf-tech/filex/backend/internal/usage"
)

// The header B2 writes, and five real rows from one account's report — the
// ones the reporter of issue #20 pasted, verbatim, including the account line
// with an empty bucket_id.
const b2Header = `date,bucket_id,bucket_name,uploaded_gb,deleted_gb,downloaded_gb,downloaded_bytes,downloaded_favored_bytes,stored_gb,storage_byte_hours,api_txn_class_a,api_txn_class_b,api_txn_class_c,api_txn_class_d`

const b2Rows = b2Header + `
2026-09-10,,,0.00,0.00,0.00,0,0,0.00,0,0,0,87,0
2026-09-10,b1a2c3,tunodo-static-assets,0.00,0.00,0.01,14819950,280738,0.02,360381552,0,58,2,0
2026-09-10,b4d5e6,vps-ops,0.02,0.00,0.00,40260,0,0.47,11093733371,27,2,3,0
2026-09-10,b7f8a9,tunodo-public,0.01,0.00,0.00,1661138,673432,0.01,118568705,101,162,608,0
2026-09-10,brep01,b2-reports-abc123,0.00,0.00,0.00,0,0,0.00,3568260,94,0,32,0
`

// localReports writes the report tree B2 would have written and returns a
// driver over it. The local driver satisfies the same storage.Driver interface
// the S3 one does, so the reader under test is the real one.
func localReports(t *testing.T, files map[string]string) storage.Driver {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	d, err := storage.Get("local")
	require.NoError(t, err)
	require.NoError(t, d.Init(context.Background(), map[string]any{"path": root}))
	return d
}

func TestB2_ReadsTheDailyReport(t *testing.T) {
	acct := "abc123"
	drv := localReports(t, map[string]string{
		"2026-09-10/usage.account-abc123.csv": b2Rows,
	})
	src := usage.B2{Driver: drv, AccountID: acct}

	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	rows, err := src.Fetch(context.Background(), day, day)
	require.NoError(t, err)
	require.Len(t, rows, 5)

	// The account line is recognised by its EMPTY bucket id, not by its name —
	// a bucket really can be called anything, including b2-reports-*.
	require.Equal(t, usage.ScopeAccount, rows[0].Scope)
	require.Equal(t, int64(87), rows[0].OpsC)

	byBucket := map[string]usage.Day{}
	for _, r := range rows {
		if r.Scope == usage.ScopeBucket {
			byBucket[r.Bucket] = r
		}
	}
	require.Len(t, byBucket, 4)

	assets := byBucket["tunodo-static-assets"]
	require.Equal(t, "b2", assets.Provider)
	require.Equal(t, usage.SourceProvider, assets.Source)
	// ⚠ downloaded_bytes, not downloaded_gb: the GB column says 0.01 for this
	// row, which would round a real 14.8 MB day to something else entirely.
	require.Equal(t, int64(14819950), assets.DownloadedBytes)
	require.Equal(t, int64(280738), assets.FreeEgressBytes)
	require.Equal(t, 360381552.0, assets.ByteHours)

	// GB columns are decimal GB. 0.02 GB is 20 MB, not 21.5 MB.
	ops := byBucket["vps-ops"]
	require.Equal(t, int64(20_000_000), ops.UploadedBytes)
	require.Equal(t, int64(27), ops.OpsA)
}

// The one that matters for the invoice: the account row must not be added to
// the bucket rows.
func TestB2_AccountRowIsNotDoubleCounted(t *testing.T) {
	drv := localReports(t, map[string]string{
		"2026-09-10/usage.account-abc123.csv": b2Rows,
	})
	rows, err := usage.B2{Driver: drv, AccountID: "abc123"}.
		Fetch(context.Background(), time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	tot := usage.SumBuckets(rows)
	// 2 + 3 + 608 + 32 across the four buckets; the account row's 87 is beside
	// them, not inside them.
	require.Equal(t, int64(645), tot.OpsC, "bucket class-C transactions")
	require.Equal(t, int64(87), tot.AccountOps.C, "the account line, reported separately")
	require.Equal(t, int64(16521348), tot.DownloadedBytes)
	require.Equal(t, int64(954170), tot.FreeEgressBytes)
	require.Equal(t, 1, tot.Days)
}

// A day B2 has not published is absent, not zero: "no report yet" and "nothing
// happened" would otherwise be the same row, and today's report does not exist
// until B2 writes it.
func TestB2_MissingDayIsSilentNotZero(t *testing.T) {
	drv := localReports(t, map[string]string{
		"2026-09-10/usage.account-abc123.csv": b2Rows,
	})
	src := usage.B2{Driver: drv, AccountID: "abc123"}

	rows, err := src.Fetch(context.Background(),
		time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Len(t, rows, 5, "only the published day produced rows")
	for _, r := range rows {
		require.Equal(t, "2026-09-10", r.Date.Format("2006-01-02"))
	}
}

// A file that is not the report must be refused rather than parsed into
// plausible-looking zeros.
func TestB2_RefusesAFileThatIsNotTheReport(t *testing.T) {
	drv := localReports(t, map[string]string{
		"2026-09-10/usage.account-abc123.csv": "name,size\nfoo.txt,12\n",
	})
	_, err := usage.B2{Driver: drv, AccountID: "abc123"}.
		Fetch(context.Background(), time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC))
	require.Error(t, err)
	require.Contains(t, err.Error(), "B2 usage report")
}

// Columns are read BY NAME. B2 has added columns before; a positional reader
// shifts every value the day it happens again.
func TestB2_ToleratesReorderedAndExtraColumns(t *testing.T) {
	reordered := "bucket_name,api_txn_class_c,date,storage_byte_hours,bucket_id,downloaded_bytes,some_new_column\n" +
		"vps-ops,3,2026-09-10,11093733371,b4d5e6,40260,whatever\n"
	drv := localReports(t, map[string]string{
		"2026-09-10/usage.account-abc123.csv": reordered,
	})
	rows, err := usage.B2{Driver: drv, AccountID: "abc123"}.
		Fetch(context.Background(), time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "vps-ops", rows[0].Bucket)
	require.Equal(t, int64(40260), rows[0].DownloadedBytes)
	require.Equal(t, int64(3), rows[0].OpsC)
	require.Equal(t, 11093733371.0, rows[0].ByteHours)
}

// TestB2_OverRealS3 runs the same reader over a real S3 server — the transport
// B2 actually serves the reports on. It needs no B2 account: the report bucket
// is an ordinary bucket, so any S3 implementation (Garage, MinIO, Hetzner)
// exercises the path B2 would take.
//
//	FILEX_TEST_S3_ENDPOINT=http://127.0.0.1:9000 FILEX_TEST_S3_BUCKET=b2-reports-test \
//	FILEX_TEST_S3_ACCESS_KEY=… FILEX_TEST_S3_SECRET_KEY=… go test ./internal/usage/
func TestB2_OverRealS3(t *testing.T) {
	bucket := os.Getenv("FILEX_TEST_S3_BUCKET")
	endpoint := os.Getenv("FILEX_TEST_S3_ENDPOINT")
	access := os.Getenv("FILEX_TEST_S3_ACCESS_KEY")
	secret := os.Getenv("FILEX_TEST_S3_SECRET_KEY")
	if bucket == "" || access == "" || secret == "" {
		t.Skip("set FILEX_TEST_S3_* to run the reader against a real S3 server")
	}

	drv, err := storage.Get("s3")
	require.NoError(t, err)
	require.NoError(t, drv.Init(context.Background(), map[string]any{
		"bucket": bucket, "endpoint": endpoint,
		"access_key": access, "secret_key": secret,
		"path_style": true,
	}))

	src := usage.B2{Driver: drv, AccountID: "abc123"}
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

	// Put the report where B2 would put it, using the driver itself.
	w, ok := drv.(storage.Writer)
	require.True(t, ok, "the s3 driver must be able to write for this test to set itself up")
	require.NoError(t, w.Write(context.Background(), src.Path(day),
		stringsReader(b2Rows), int64(len(b2Rows))))

	rows, err := src.Fetch(context.Background(), day, day)
	require.NoError(t, err)
	require.Len(t, rows, 5)

	tot := usage.SumBuckets(rows)
	require.Equal(t, int64(645), tot.OpsC)
	require.Equal(t, int64(87), tot.AccountOps.C)
}

// stringsReader keeps the import list honest — strings.NewReader in one place.
func stringsReader(s string) io.Reader { return strings.NewReader(s) }

// Backblaze names the file three different ways — standalone account,
// organization, group — so the reader lists the day's folder instead of
// constructing one name. An operator in an organization would otherwise get an
// empty report and no error at all.
func TestB2_FindsOrganizationAndGroupReports(t *testing.T) {
	orgRow := b2Header + "\n2026-09-10,b4d5e6,vps-ops,0.02,0.00,0.00,40260,0,0.47,11093733371,27,2,3,0\n"
	groupRow := b2Header + "\n2026-09-10,b7f8a9,tunodo-public,0.01,0.00,0.00,1661138,673432,0.01,118568705,101,162,608,0\n"

	drv := localReports(t, map[string]string{
		// The two shapes that are not the standalone one.
		"2026-09-10/usage.acme.us-west-004.csv":          orgRow,
		"2026-09-10/usage.group-4711.eu-central-003.csv": groupRow,
		// Neither of these is a usage report, and parsing either produces rows
		// that are wrong rather than absent.
		"2026-09-10/usage.audit-account-abc123.csv":    "nonsense,not,usage\n1,2,3\n",
		"2026-09-10/usage.acme.reportingLocations.csv": "location,name\nus-west-004,US West\n",
	})

	rows, err := usage.B2{Driver: drv}.Fetch(ctxTODO(), day(2026, 9, 10), day(2026, 9, 10))
	require.NoError(t, err, "no account id needed: the bucket already scopes it")
	require.Len(t, rows, 2, "both usage files, neither of the other two")

	names := []string{rows[0].Bucket, rows[1].Bucket}
	require.ElementsMatch(t, []string{"vps-ops", "tunodo-public"}, names)
}

// B2 reports per region since it gained more than one, and regions are priced
// separately — so the row keeps its location instead of being flattened.
func TestB2_KeepsTheReportingLocation(t *testing.T) {
	withLocation := "date,bucket_id,bucket_name,reporting_location,storage_byte_hours,downloaded_bytes\n" +
		"2026-09-10,b4d5e6,vps-ops,us-west-004,11093733371,40260\n"
	drv := localReports(t, map[string]string{
		"2026-09-10/usage.account-abc123.csv": withLocation,
	})
	rows, err := usage.B2{Driver: drv, AccountID: "abc123"}.Fetch(ctxTODO(), day(2026, 9, 10), day(2026, 9, 10))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "us-west-004", rows[0].Location)
}
