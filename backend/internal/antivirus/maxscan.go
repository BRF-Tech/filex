// Package antivirus — maxscan.go
//
// The scan-size ceiling as a database-backed setting, the second of the
// antivirus family to move out of the environment (after the save-scan
// window). Files larger than this are skipped, not failed.
package antivirus

import (
	"context"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

// Bounds for MaxScanSetting, in megabytes.
//
//   - Default 100 MB matches clamd's own MaxFileSize default, which is what
//     the environment variable defaulted to before the move.
//   - Min 1 MB: a ceiling below a megabyte would skip essentially every real
//     document, i.e. it would look like scanning while scanning nothing.
//   - Max 10240 MB (10 GB): above this the scan itself is the problem — the
//     worker spools the file to a temp file before exec'ing ClamAV, so the
//     ceiling is also a disk and time budget, not just a policy.
const (
	DefaultMaxScanMB = 100
	MinMaxScanMB     = 1
	MaxMaxScanMB     = 10240
)

// bytesPerMB is the binary megabyte the rest of filex uses (100 << 20).
const bytesPerMB = 1 << 20

// MaxScanSetting declares the ceiling as a settings-table value.
//
// ⚠⚠ The environment variable is FILEX_CLAMAV_MAX and it is in BYTES, while
// the stored setting is in MEGABYTES — the number an admin types into a form.
// The variable was not renamed on purpose: an install already setting it must
// keep the ceiling it has, and it does, because SeedParse converts at seed
// time. The conversion rounds UP, so the ceiling can only ever stay the same
// or grow by less than a megabyte; rounding down would silently start
// skipping files that used to be scanned.
//
// ⚠⚠ As with every setting in this family, the variable is a SEED, not an
// override: it is consumed on a boot where no row exists, and is inert after
// that. Changing it in compose later does nothing; the admin page is where the
// value lives now.
var MaxScanSetting = dbsetting.IntSpec{
	Key:     "antivirus.max_scan_mb",
	EnvVar:  "FILEX_CLAMAV_MAX",
	Default: DefaultMaxScanMB,
	Min:     MinMaxScanMB,
	Max:     MaxMaxScanMB,
	Unit:    "MB",
	SeedParse: func(raw string) (int, bool) {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return 0, false
		}
		// Ceiling division: never shrink an existing deployment's cap.
		return int((n + bytesPerMB - 1) / bytesPerMB), true
	},
}

// MaxScanBytesFrom resolves the ceiling in force right now, in bytes.
//
// ⚠ Read at the point of use (the job's eligibility check), not cached at
// boot, so a change on the Protection page applies to the next file scanned
// without a restart.
func MaxScanBytesFrom(ctx context.Context, g dbsetting.Getter) int64 {
	return int64(MaxScanSetting.Resolve(ctx, g)) * bytesPerMB
}
