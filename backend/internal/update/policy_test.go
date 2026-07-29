package update

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		in                  string
		major, minor, patch int
		pre                 string
		ok                  bool
	}{
		{"v0.7.6", 0, 7, 6, "", true},
		{"0.7.6", 0, 7, 6, "", true},
		{"1.2.3-rc.1", 1, 2, 3, "rc.1", true},
		{"0.1.0-dev", 0, 1, 0, "dev", true},
		{"v1.0.0+build7", 1, 0, 0, "", true},
		{"0.7", 0, 0, 0, "", false},
		{"latest", 0, 0, 0, "", false},
		{"", 0, 0, 0, "", false},
	} {
		v, err := ParseVersion(tc.in)
		if !tc.ok {
			assert.Error(t, err, "%q must not parse", tc.in)
			continue
		}
		require.NoError(t, err, tc.in)
		assert.Equal(t, [3]int{tc.major, tc.minor, tc.patch}, [3]int{v.Major, v.Minor, v.Patch}, tc.in)
		assert.Equal(t, tc.pre, v.Pre, tc.in)
	}
}

func TestStepTo(t *testing.T) {
	v := func(s string) Version { p, _ := ParseVersion(s); return p }
	assert.Equal(t, StepPatch, v("0.7.5").StepTo(v("0.7.6")))
	assert.Equal(t, StepMinor, v("0.7.6").StepTo(v("0.8.0")))
	assert.Equal(t, StepMajor, v("0.9.9").StepTo(v("1.0.0")))
	assert.Equal(t, StepNone, v("0.7.6").StepTo(v("0.7.6")))
	assert.Equal(t, StepNone, v("0.7.6").StepTo(v("0.7.5")), "downgrade is not a step")
	// A pre-release is never a target: an install must not drift onto an rc.
	assert.Equal(t, StepNone, v("0.7.6").StepTo(v("0.8.0-rc.1")))
}

// rel builds a release with the common "clean patch" shape.
func rel(ver string, opts ...func(*Release)) Release {
	r := Release{Version: ver, AutoOK: true}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func withMigrations(r *Release)      { r.Migrations = true }
func notAuto(r *Release)             { r.AutoOK = false }
func minVer(s string) func(*Release) { return func(r *Release) { r.MinVersion = s } }

func decide(t *testing.T, current string, pol Policy, mode InstallMode, releases ...Release) Decision {
	t.Helper()
	cur, err := ParseVersion(current)
	require.NoError(t, err)
	return Decide(Input{
		Current:  cur,
		Manifest: &Manifest{Releases: releases},
		Policy:   pol,
		Mode:     mode,
		Now:      time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC),
	})
}

func TestDecide_PatchIsAutomatic(t *testing.T) {
	d := decide(t, "v0.7.5", PolicyPatch, ModeBinary, rel("v0.7.5"), rel("v0.7.6"))
	assert.Equal(t, ActionAuto, d.Action)
	assert.Equal(t, StepPatch, d.Step)
	assert.Equal(t, "v0.7.6", d.Target.Version)
}

func TestDecide_MinorNeedsConfirmation(t *testing.T) {
	d := decide(t, "v0.7.6", PolicyPatch, ModeBinary, rel("v0.8.0"))
	assert.Equal(t, ActionConfirm, d.Action)
	assert.Equal(t, StepMinor, d.Step)
}

func TestDecide_MajorIsInstructOnly(t *testing.T) {
	// Even under the most permissive policy, x never moves by itself.
	d := decide(t, "v0.9.0", PolicyMinor, ModeBinary, rel("v1.0.0"))
	assert.Equal(t, ActionInstruct, d.Action)
	assert.Equal(t, StepMajor, d.Step)
}

func TestDecide_ZeroDotXMinorNeverAuto(t *testing.T) {
	// PolicyMinor would allow y-moves, but 0.x gives no compatibility promise.
	d := decide(t, "v0.7.6", PolicyMinor, ModeBinary, rel("v0.8.0"))
	assert.Equal(t, ActionConfirm, d.Action)
	assert.Contains(t, d.Reason, "0.x")
}

func TestDecide_MinorAutoOnceStable(t *testing.T) {
	d := decide(t, "v1.2.0", PolicyMinor, ModeBinary, rel("v1.3.0"))
	assert.Equal(t, ActionAuto, d.Action)
}

func TestDecide_MigrationBlocksAutoPatch(t *testing.T) {
	// A patch carrying a schema change is a packaging mistake; treat it as
	// "not really a patch" and make a human take the backup decision.
	d := decide(t, "v0.7.5", PolicyPatch, ModeBinary, rel("v0.7.6", withMigrations))
	assert.Equal(t, ActionConfirm, d.Action)
	assert.Contains(t, d.Reason, "schema")
}

func TestDecide_MigrationInSkippedReleaseAlsoBlocks(t *testing.T) {
	// Jumping 0.7.4 → 0.7.6 runs 0.7.5's migration too, even though the target
	// itself is clean. The whole span must be examined.
	d := decide(t, "v0.7.4", PolicyPatch, ModeBinary,
		rel("v0.7.5", withMigrations), rel("v0.7.6"))
	assert.Equal(t, ActionConfirm, d.Action)
	assert.Contains(t, d.Reason, "schema")
	assert.Len(t, d.Skipped, 2)
}

func TestDecide_KillSwitch(t *testing.T) {
	// auto_ok=false pulls a release out of automatic distribution without
	// deleting the tag.
	d := decide(t, "v0.7.5", PolicyPatch, ModeBinary, rel("v0.7.6", notAuto))
	assert.Equal(t, ActionConfirm, d.Action)
	assert.Contains(t, d.Reason, "auto_ok")
}

func TestDecide_MinVersionForcesStepping(t *testing.T) {
	d := decide(t, "v0.5.0", PolicyPatch, ModeBinary, rel("v0.7.6", minVer("v0.7.0")))
	assert.Equal(t, ActionInstruct, d.Action)
	assert.Contains(t, d.Reason, "minimum")
}

func TestDecide_DockerNeverSelfApplies(t *testing.T) {
	// The same patch that is ActionAuto on a binary install is ActionInstruct
	// in a container: replacing the binary inside an image is a ghost upgrade
	// that reverts on the next `up`.
	d := decide(t, "v0.7.5", PolicyPatch, ModeDocker, rel("v0.7.6"))
	assert.Equal(t, ActionInstruct, d.Action)
	assert.Contains(t, d.Reason, "container")
}

func TestDecide_PolicyOffAndManualAnnounceOnly(t *testing.T) {
	for _, p := range []Policy{PolicyOff, PolicyManual} {
		d := decide(t, "v0.7.5", p, ModeBinary, rel("v0.7.6"))
		assert.Equal(t, ActionConfirm, d.Action, string(p))
		assert.Contains(t, d.Reason, "announced")
	}
}

func TestDecide_UpToDate(t *testing.T) {
	d := decide(t, "v0.7.6", PolicyPatch, ModeBinary, rel("v0.7.6"))
	assert.Equal(t, ActionNone, d.Action)
}

func TestParsePolicy_UnknownFallsBackToManual(t *testing.T) {
	// An unreadable setting must never grant MORE automation than asked for.
	assert.Equal(t, PolicyManual, ParsePolicy("aggressive"))
	assert.Equal(t, PolicyManual, ParsePolicy(""))
	assert.Equal(t, PolicyPatch, ParsePolicy("PATCH"))
	assert.Equal(t, PolicyOff, ParsePolicy("off"))
}

func TestWindow(t *testing.T) {
	w := ParseWindow("03:00-05:00")
	require.True(t, w.Set)
	at := func(h, m int) time.Time { return time.Date(2026, 7, 29, h, m, 0, 0, time.UTC) }
	assert.True(t, w.Allows(at(3, 30)))
	assert.False(t, w.Allows(at(6, 0)))

	// Crossing midnight.
	night := ParseWindow("22:00-04:00")
	assert.True(t, night.Allows(at(23, 0)))
	assert.True(t, night.Allows(at(1, 0)))
	assert.False(t, night.Allows(at(12, 0)))

	// A typo must not block updates forever — it degrades to "any time".
	assert.True(t, ParseWindow("gece yarisi").Allows(at(12, 0)))
	assert.True(t, ParseWindow("").Allows(at(12, 0)))
}

func TestManifestLatestIgnoresPrereleasesAndOrder(t *testing.T) {
	m := &Manifest{Releases: []Release{
		rel("v0.7.6"), rel("v0.8.0-rc.1"), rel("v0.7.10"), rel("v0.7.9"),
	}}
	got, ok := m.Latest()
	require.True(t, ok)
	// v0.7.10 > v0.7.9 numerically, not lexically — and the rc is skipped.
	assert.Equal(t, "v0.7.10", got.Version)
}
