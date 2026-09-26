package wasmplugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/update"
)

// withHost runs the test as filex `v` (what the server stamps into
// HostVersion from its build).
func withHost(t *testing.T, v string) {
	t.Helper()
	was := HostVersion
	HostVersion = v
	t.Cleanup(func() { HostVersion = was })
}

func mustVersion(t *testing.T, s string) update.Version {
	t.Helper()
	v, err := update.ParseVersion(s)
	require.NoError(t, err)
	return v
}

func TestFilexRange_TheGrammar(t *testing.T) {
	cases := []struct {
		rng  string
		host string
		want bool
	}{
		{">=0.47.0", "0.47.0", true},
		{">=0.47.0", "0.46.9", false},
		{">=0.47.0 <0.60.0", "0.59.3", true},
		{">=0.47.0 <0.60.0", "0.60.0", false},
		{"> 0.47.0", "0.47.1", true}, // the operator written apart from its version
		{">0.47.0", "0.47.0", false},
		{"<=0.47.0", "0.47.0", true},
		{"0.47.0", "0.47.0", true}, // a bare version is `=`
		{"=0.47.0", "0.47.1", false},
		{"v0.47.0", "0.47.0", true},
		{">=0.47.0 <0.50.0 || >=0.52.0", "0.51.0", false},
		{">=0.47.0 <0.50.0 || >=0.52.0", "0.53.0", true},
		{"", "0.1.0", true},
	}
	for _, c := range cases {
		r, err := parseFilexRange(c.rng)
		require.NoError(t, err, c.rng)
		assert.Equal(t, c.want, r.admits(mustVersion(t, c.host)), "%q on %s", c.rng, c.host)
	}
	for _, bad := range []string{"^0.47.0", "~0.47", ">=0.47", ">=0.47.0-rc.1", ">=", ">=0.47.0 ||", "latest"} {
		_, err := parseFilexRange(bad)
		assert.Error(t, err, "%q must be refused, not read as something else", bad)
	}
}

// ⚠ Which filex is "running": an rc counts as its release (it is tested with
// the apps written for it), a development build enforces nothing (a developer
// must be able to install the app they are writing).
func TestHostRelease_ReleaseCandidateCountsDevBuildDoesNot(t *testing.T) {
	for _, c := range []struct {
		host     string
		enforced bool
		version  string
	}{
		{"v0.47.0 (abc1234, 2026-09-26T10:00:00Z)", true, "0.47.0"},
		{"0.47.0", true, "0.47.0"},
		{"0.47.0-rc.1", true, "0.47.0"},
		{"v0.47.0-beta.2", true, "0.47.0"},
		{"0.1.0-dev", false, "0.1.0-dev"},
		{"v0.46.0-5-g1a2b3c4", false, "0.46.0-5-g1a2b3c4"},
		{"dev", false, "dev"},
		{"", false, ""},
	} {
		withHost(t, c.host)
		v, ok := hostRelease()
		assert.Equal(t, c.enforced, ok, c.host)
		if ok {
			assert.Equal(t, c.version, v.Raw, c.host)
		}
		assert.Equal(t, c.version, FilexVersion(), c.host)
		assert.Equal(t, c.enforced, CompatEnforced(), c.host)
	}
}

// packWithRange is a language pack manifest carrying a `filex` range and,
// optionally, the older `min_filex`.
func packWithRange(t *testing.T, name, version, rng, minFilex string) []byte {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(packManifest(t, name, map[string]map[string]string{"es": {"a": "b"}}), &m))
	m["version"] = version
	if rng != "" {
		m["filex"] = rng
	}
	if minFilex != "" {
		m["min_filex"] = minFilex
	}
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

// The review says it, and the install refuses it — in that order, so an
// administrator reads why before anybody ticks a box.
func TestInstall_AnAppWhoseRangeLeavesThisFilexOutIsRefused(t *testing.T) {
	withHost(t, "v0.47.0 (abc1234)")
	reg, _ := newPackRegistry(t, nil)
	ctx := context.Background()

	in := &InstallInput{Manifest: packWithRange(t, "lang-es", "1.0.0", ">=0.48.0", ""), DryRun: true, Granted: []string{}}
	_, dry, err := reg.Install(ctx, in)
	require.NoError(t, err, "the review is not a refusal")
	require.NotNil(t, dry.Compat)
	assert.Equal(t, &Compat{Requires: ">=0.48.0", OK: false, Filex: "0.47.0"}, dry.Compat)

	in.DryRun = false
	_, _, err = reg.Install(ctx, in)
	ie := installError(t, err)
	assert.Equal(t, ErrCodeIncompatible, ie.Code)
	assert.Equal(t, ">=0.48.0", ie.Requires)
	assert.Equal(t, "0.47.0", ie.Filex)
	assert.Empty(t, reg.All(), "nothing was installed")

	// The older lower-bound form is held to the same gate.
	_, _, err = reg.Install(ctx, &InstallInput{Manifest: packWithRange(t, "lang-es", "1.0.0", "", "0.48.0"), Granted: []string{}})
	assert.Equal(t, ErrCodeIncompatible, installError(t, err).Code, "min_filex 0.48.0 leaves 0.47.0 out")

	// Inside the range it installs, and the list carries the range.
	st, _, err := reg.Install(ctx, &InstallInput{Manifest: packWithRange(t, "lang-es", "1.0.0", ">=0.47.0 <0.60.0", "0.43.0"), Granted: []string{}})
	require.NoError(t, err)
	assert.Equal(t, &Compat{Requires: ">=0.47.0 <0.60.0, >=0.43.0", OK: true, Filex: "0.47.0"}, st.Compat)
}

func TestInstall_ARangeThatDoesNotParseIsABrokenManifest(t *testing.T) {
	reg, _ := newPackRegistry(t, nil)
	_, _, err := reg.Install(context.Background(), &InstallInput{Manifest: packWithRange(t, "lang-es", "1.0.0", "^0.47.0", ""), DryRun: true})
	ie := installError(t, err)
	assert.Equal(t, ErrCodeManifestInvalid, ie.Code)
	assert.Contains(t, ie.Message, "filex")
}

// A development build installs everything: the range is not held against an
// unstamped binary, and the runtime says it does not check.
func TestInstall_ADevelopmentBuildDoesNotLockAppsOut(t *testing.T) {
	withHost(t, "0.1.0-dev")
	reg, _ := newPackRegistry(t, nil)
	st, _, err := reg.Install(context.Background(), &InstallInput{Manifest: packWithRange(t, "lang-es", "1.0.0", ">=9.0.0", ""), Granted: []string{}})
	require.NoError(t, err)
	assert.Equal(t, &Compat{Requires: ">=9.0.0", OK: true, Filex: "0.1.0-dev"}, st.Compat)
	assert.False(t, CompatEnforced())
}

// ⚠ An installed app that filex has been upgraded past KEEPS RUNNING: the
// range is a promise, not a proof, and switching it off at the upgrade would
// take a language away from everybody with nobody having decided it. The
// list says it is out of range and its log says why it still runs.
func TestLoad_AnInstalledAppOutOfRangeKeepsRunning(t *testing.T) {
	withHost(t, "0.46.2")
	reg, o := newPackRegistry(t, nil)
	ctx := context.Background()
	st, _, err := reg.Install(ctx, &InstallInput{Manifest: packWithRange(t, "lang-es", "1.0.0", ">=0.45.0 <0.47.0", ""), Granted: []string{}})
	require.NoError(t, err)
	require.True(t, st.Compat.OK)
	reg.Close(ctx)

	withHost(t, "0.47.0")
	again, err := New(o)
	require.NoError(t, err)
	t.Cleanup(func() { again.Close(ctx) })
	require.NoError(t, again.Load(ctx))
	p, ok := again.ByName("lang-es")
	require.True(t, ok)
	st = again.StatusOf(p)
	assert.Equal(t, StateRunning, st.State, "not switched off")
	assert.Equal(t, &Compat{Requires: ">=0.45.0 <0.47.0", OK: false, Filex: "0.47.0"}, st.Compat)
	lines, _ := p.logs.after(0)
	var said bool
	for _, l := range lines {
		if l.Level == "warn" && strings.Contains(l.Msg, "keeps running") {
			said = true
		}
	}
	assert.True(t, said, "the log says why it still runs")
}
