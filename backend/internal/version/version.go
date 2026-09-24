// Package version exposes build-time version metadata.
//
// Set at build time via:
//
//	-ldflags='-X github.com/brf-tech/filex/backend/internal/version.Version=v0.1.0
//	          -X github.com/brf-tech/filex/backend/internal/version.Commit=abc1234
//	          -X github.com/brf-tech/filex/backend/internal/version.Date=2026-04-28T12:34:56Z'
package version

import "strings"

var (
	// Version is the semver string baked in at link time.
	Version = "0.1.0-dev"
	// Commit is the short git SHA.
	Commit = "unknown"
	// Date is the ISO-8601 build time (UTC).
	Date = "unknown"
)

// String returns a "v0.1.0 (abc1234, 2026-04-28T…)"-style summary. A part
// the build did not stamp is left out rather than printed: a development
// build read "0.1.0-dev (unknown, unknown)" on the login page and the About
// page — English placeholder words in every language (QA #41, #35).
func String() string {
	var parts []string
	for _, p := range []string{Commit, Date} {
		if p != "" && p != "unknown" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return Version
	}
	return Version + " (" + strings.Join(parts, ", ") + ")"
}
