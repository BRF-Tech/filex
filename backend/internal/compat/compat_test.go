package compat

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The rule is 6 minor releases or 12 months, whichever is longer; a window
// that does not say so is the rule changed without the docs and the
// CONTRIBUTING rule changing with it (web/tests/docs/compatWindow.test.ts
// reads the same file).
func TestRule_IsSixMinorsOrTwelveMonths(t *testing.T) {
	require.Equal(t, Rule{Minors: 6, Months: 12}, CurrentRule())
}

func TestWindowsJSON_Parses(t *testing.T) {
	_, err := parse(raw)
	require.NoError(t, err)
	require.NotEmpty(t, All())
	for _, w := range All() {
		require.Equal(t, w, Get(w.ID))
	}
}

// The old sha256-only build signature: 0.55.0 (2026-10-09) shipped the
// signed text, so 0.61 is the earliest release that may refuse it, and no
// release before 2027-10-09 does.
func TestUntil_TheStorageBuildSignature(t *testing.T) {
	v, d := Get("storage-build-signature-sha256").Until()
	require.Equal(t, "0.61", v)
	require.Equal(t, "2027-10-09", d)
}

func TestOpen_TheLaterOfTheTwoEndsHolds(t *testing.T) {
	w := Get("storage-build-signature-sha256")
	day := func(s string) time.Time {
		tm, err := time.Parse(DateLayout, s)
		require.NoError(t, err)
		return tm
	}
	cases := []struct {
		release, on string
		open        bool
	}{
		{"0.56", "2026-10-10", true},          // the very next release
		{"0.60", "2028-01-01", true},          // a year on, but only five minors
		{"0.61", "2027-10-08", true},          // six minors, but not yet a year
		{"0.75", "2027-10-08", true},          // many minors, the date still binds
		{"0.61", "2027-10-09", false},         // both ends reached
		{"0.62.3", "2027-11-01", false},       // after both
		{"v1.0", "2027-12-01", false},         // a major after the window
		{"not-a-version", "2030-01-01", true}, // unreadable: keep accepting
	}
	for _, c := range cases {
		require.Equal(t, c.open, w.Open(c.release, day(c.on)), "%s on %s", c.release, c.on)
	}
}

func TestParse_RefusesADefectiveFile(t *testing.T) {
	bad := map[string]string{
		"no rule":       `{"rule":{"minors":0,"months":12},"windows":[]}`,
		"unknown field": `{"rule":{"minors":6,"months":12},"windows":[],"extra":1}`,
		"repeated id":   `{"rule":{"minors":6,"months":12},"windows":[{"id":"a","form":"f","since":"0.1","since_date":"2026-01-01","docs":"d"},{"id":"a","form":"f","since":"0.1","since_date":"2026-01-01","docs":"d"}]}`,
		"bad since":     `{"rule":{"minors":6,"months":12},"windows":[{"id":"a","form":"f","since":"next","since_date":"2026-01-01","docs":"d"}]}`,
		"bad date":      `{"rule":{"minors":6,"months":12},"windows":[{"id":"a","form":"f","since":"0.1","since_date":"01/01/2026","docs":"d"}]}`,
		"removed alone": `{"rule":{"minors":6,"months":12},"windows":[{"id":"a","form":"f","since":"0.1","since_date":"2026-01-01","docs":"d","removed":"0.9"}]}`,
		"no docs":       `{"rule":{"minors":6,"months":12},"windows":[{"id":"a","form":"f","since":"0.1","since_date":"2026-01-01","docs":""}]}`,
	}
	for name, b := range bad {
		_, err := parse([]byte(b))
		require.Error(t, err, name)
	}
}

func TestGet_AnUnknownIDIsADefect(t *testing.T) {
	require.Panics(t, func() { Get("no-such-form") })
}
