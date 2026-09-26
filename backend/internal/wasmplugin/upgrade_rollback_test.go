package wasmplugin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ⚠⚠ An upgrade whose module fails its proof is rolled back — and stays
// rolled back after a restart. The failed compile wrote the NEW version,
// manifest and hash through the row it shares with the old entry
// (persistError), the roll-back restored the old values in memory only, and
// the next start read "0.0.2" beside the 0.0.1 files and refused the app for
// a hash mismatch: an administrator's failed upgrade broke the app one
// restart later.
func TestUpgrade_ARolledBackUpgradeStaysRolledBackAfterARestart(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	in := echoInput(t, func(m map[string]any) { m["version"] = "0.0.2" }) // the module still says 0.0.1
	_, _, err := h.reg.Upgrade(context.Background(), p.Row.ID, in)
	assert.Equal(t, ErrCodeDescribeMismatch, installError(t, err).Code)

	again, err := New(h.reg.opts)
	require.NoError(t, err)
	t.Cleanup(func() { again.Close(context.Background()) })
	require.NoError(t, again.Load(context.Background()))
	np, ok := again.ByID(p.Row.ID)
	require.True(t, ok)
	st := again.StatusOf(np)
	assert.Equal(t, "0.0.1", st.Version)
	assert.Equal(t, StateRunning, st.State, st.StateError)
}

// An app that is switched off is upgraded too — its new module proves
// itself like any other — and stays off.
func TestUpgrade_AnAppThatIsOffStaysOff(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	_, err := h.reg.SetEnabled(context.Background(), p.Row.ID, false)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(packManifest(t, "echo", map[string]map[string]string{"es": {"a": "b"}}), &raw))
	raw["version"] = "0.0.2"
	next, _ := json.Marshal(raw)
	st, _, err := h.reg.Upgrade(context.Background(), p.Row.ID, &InstallInput{Manifest: next})
	require.NoError(t, err)
	assert.Equal(t, StateDisabled, st.State)
	assert.False(t, st.Enabled)
}
