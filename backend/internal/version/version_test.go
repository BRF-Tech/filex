package version

import "testing"

// An unstamped part is left out, never printed as "unknown".
func TestString_LeavesOutWhatTheBuildDidNotStamp(t *testing.T) {
	v, c, d := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = v, c, d })

	Version, Commit, Date = "0.1.0-dev", "unknown", "unknown"
	if got := String(); got != "0.1.0-dev" {
		t.Fatalf("%q", got)
	}
	Version, Commit, Date = "v0.43.0", "abc1234", "unknown"
	if got := String(); got != "v0.43.0 (abc1234)" {
		t.Fatalf("%q", got)
	}
	Version, Commit, Date = "v0.43.0", "abc1234", "2026-09-22T10:00:00Z"
	if got := String(); got != "v0.43.0 (abc1234, 2026-09-22T10:00:00Z)" {
		t.Fatalf("%q", got)
	}
}
