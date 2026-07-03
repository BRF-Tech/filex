// Package version exposes build-time version metadata.
//
// Set at build time via:
//
//	-ldflags='-X github.com/brf-tech/filex/backend/internal/version.Version=v0.1.0
//	          -X github.com/brf-tech/filex/backend/internal/version.Commit=abc1234
//	          -X github.com/brf-tech/filex/backend/internal/version.Date=2026-04-28T12:34:56Z'
package version

var (
	// Version is the semver string baked in at link time.
	Version = "0.1.0-dev"
	// Commit is the short git SHA.
	Commit = "unknown"
	// Date is the ISO-8601 build time (UTC).
	Date = "unknown"
)

// String returns a "v0.1.0 (abc1234, 2026-04-28T…)"-style summary.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
