package update

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// What an install does by itself, for every install shape and every saved
// policy. The admin page's badge says this; before #72 it said the saved
// policy, so a Homebrew install set to "patch" read "installs patches" while
// Decide turned every such patch into instructions.
func TestEffectiveOf_ModeByPolicy(t *testing.T) {
	v1, v0 := Version{Major: 1, Minor: 4, Patch: 2}, Version{Major: 0, Minor: 44, Patch: 2}

	type want struct {
		b Behavior
		l Limit
	}
	for _, tc := range []struct {
		mode InstallMode
		pol  Policy
		cur  Version
		want want
	}{
		// A plain binary carries every policy out.
		{ModeBinary, PolicyOff, v1, want{BehaviorOff, LimitNone}},
		{ModeBinary, PolicyManual, v1, want{BehaviorAnnounce, LimitNone}},
		{ModeBinary, PolicyPatch, v1, want{BehaviorPatch, LimitNone}},
		{ModeBinary, PolicyMinor, v1, want{BehaviorMinor, LimitNone}},
		// …except a minor on 0.x, which is never automatic (Decide rule 6).
		{ModeBinary, PolicyMinor, v0, want{BehaviorPatch, LimitZeroMajor}},
		{ModeBinary, PolicyPatch, v0, want{BehaviorPatch, LimitNone}},

		// A package manager owns the binary: whatever would have been applied
		// is announced. off and manual already say what happens.
		{ModePackage, PolicyOff, v1, want{BehaviorOff, LimitNone}},
		{ModePackage, PolicyManual, v1, want{BehaviorAnnounce, LimitNone}},
		{ModePackage, PolicyPatch, v1, want{BehaviorAnnounce, LimitPackage}},
		{ModePackage, PolicyMinor, v1, want{BehaviorAnnounce, LimitPackage}},
		{ModePackage, PolicyMinor, v0, want{BehaviorAnnounce, LimitPackage}},

		// A container: the same, for the image.
		{ModeDocker, PolicyOff, v1, want{BehaviorOff, LimitNone}},
		{ModeDocker, PolicyManual, v1, want{BehaviorAnnounce, LimitNone}},
		{ModeDocker, PolicyPatch, v1, want{BehaviorAnnounce, LimitContainer}},
		{ModeDocker, PolicyMinor, v1, want{BehaviorAnnounce, LimitContainer}},
		{ModeDocker, PolicyMinor, v0, want{BehaviorAnnounce, LimitContainer}},
	} {
		name := fmt.Sprintf("%s/%s/v%d", tc.mode, tc.pol, tc.cur.Major)
		t.Run(name, func(t *testing.T) {
			e := EffectiveOf(tc.pol, true, tc.mode, tc.cur)
			assert.Equal(t, tc.pol, e.Policy, "the saved policy is kept as it was set")
			assert.Equal(t, tc.want.b, e.Behavior)
			assert.Equal(t, tc.want.l, e.Limit)
			assert.Equal(t, tc.want.l == LimitNone, e.InForce())
		})
	}
}

// Checking switched off (FILEX_UPDATE_CHECK=0) means nothing is looked for,
// so nothing is announced or applied — whatever the policy and the install.
func TestEffectiveOf_CheckingOff(t *testing.T) {
	cur := Version{Major: 1}
	for _, mode := range []InstallMode{ModeBinary, ModeDocker, ModePackage} {
		for _, pol := range []Policy{PolicyManual, PolicyPatch, PolicyMinor} {
			e := EffectiveOf(pol, false, mode, cur)
			assert.Equal(t, BehaviorOff, e.Behavior, "%s/%s", mode, pol)
			assert.Equal(t, LimitDisabled, e.Limit, "%s/%s", mode, pol)
		}
		// policy off is the policy in force, not a limit on it.
		e := EffectiveOf(PolicyOff, false, mode, cur)
		assert.Equal(t, BehaviorOff, e.Behavior)
		assert.True(t, e.InForce(), mode)
	}
}

// The badge and Decide must never disagree: under every behavior other than
// patch/minor, a clean patch is not applied by itself, and a behavior that
// says it applies patches does apply one.
func TestEffectiveOf_AgreesWithDecide(t *testing.T) {
	for _, mode := range []InstallMode{ModeBinary, ModeDocker, ModePackage} {
		for _, pol := range []Policy{PolicyOff, PolicyManual, PolicyPatch, PolicyMinor} {
			for _, cur := range []string{"v0.7.5", "v1.7.5"} {
				c, _ := ParseVersion(cur)
				e := EffectiveOf(pol, true, mode, c)
				if e.Behavior == BehaviorOff {
					continue // Decide is never asked: the check does not run
				}
				next := fmt.Sprintf("v%d.%d.%d", c.Major, c.Minor, c.Patch+1)
				minor := fmt.Sprintf("v%d.%d.0", c.Major, c.Minor+1)
				patchAuto := decide(t, cur, pol, mode, rel(next)).Action == ActionAuto
				minorAuto := decide(t, cur, pol, mode, rel(minor)).Action == ActionAuto
				name := fmt.Sprintf("%s/%s/%s", mode, pol, cur)
				assert.Equal(t, e.Behavior == BehaviorPatch || e.Behavior == BehaviorMinor, patchAuto, name+": patch")
				assert.Equal(t, e.Behavior == BehaviorMinor, minorAuto, name+": minor")
			}
		}
	}
}
