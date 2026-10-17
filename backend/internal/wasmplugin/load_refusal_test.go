package wasmplugin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── A module that did not load, said for what it is (0.57) ─────────────
//
// The 0.57 chain (2026-10-16, Firefox, one worker) refused two installs
// through the wizard - convert after 17.6 s, sign after 30.6 s, their call
// budgets (15 s, 30 s) to a fraction of a second - while two race-test jobs
// started beside the suite on a machine already stalled on memory and disk.
// Both were told "The module describes itself differently from its
// manifest - the build and the manifest do not belong together", and both
// installed a minute later in 6 s and 2 s from the same files. Every load
// that did not end running was answered as describe_mismatch, and describe
// had the call budget a screen gets.
//
// RED PROOF (int/057-wave 6d2feab13): loadRefusal, setCallFailure,
// DescribeBudget and ErrCodeLoadTimeout did not exist - Install and Upgrade
// returned installErr(ErrCodeDescribeMismatch, …) for every state but
// running, Describe ran under Manifest.CallTimeout(), and a call whose budget
// ran out while its instance was starting was plugin_trap.

func TestLoadRefusal_OnlyAModuleThatAnsweredAsAnotherProgramIsAMismatch(t *testing.T) {
	cases := map[string]struct {
		state string
		err   error
		code  string
	}{
		"describe answered as another program": {StateRefused, &CallError{Code: CodeRefused, Export: "describe", Message: "describe says sign 2.0.0 (v1), the installed manifest says sign 1.0.0 (v1)"}, ErrCodeDescribeMismatch},
		"describe ran out of time":             {StateFailed, &CallError{Code: CodeTimeout, Export: "describe", Message: "the plugin did not start within 1m0s"}, ErrCodeLoadTimeout},
		"describe crashed":                     {StateFailed, &CallError{Code: CodePluginTrap, Export: "describe", Message: "the plugin could not be started"}, ErrCodeLoadFailed},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := &Installed{logs: &logRing{}}
			p.setCallFailure(tc.state, tc.err)
			ie := installError(t, loadRefusal(p))
			assert.Equal(t, tc.code, ie.Code)
			assert.Equal(t, tc.err.Error(), ie.Message, "Go's words stay the detail")
		})
	}

	// A later failure with no call behind it does not keep the old code.
	p := &Installed{logs: &logRing{}}
	p.setCallFailure(StateFailed, &CallError{Code: CodeTimeout, Export: "describe"})
	p.setState(StateFailed, "the interface bundle is gone")
	assert.Equal(t, ErrCodeLoadFailed, installError(t, loadRefusal(p)).Code)
}

func TestLoadRefusal_ATimeoutIsSaidAsABusyServerNotAsOtherFiles(t *testing.T) {
	ref := InstallRefusal{Code: ErrCodeLoadTimeout, Message: "describe (timeout): the plugin did not start within 1m0s"}
	en, tr := ref.Said("en"), ref.Said("tr")
	assert.Contains(t, en.Message, "busy")
	assert.Contains(t, tr.Message, "meşgul")
	for _, got := range []*InstallRefusal{en, tr} {
		assert.NotContains(t, got.Message, "belong together")
		assert.NotContains(t, got.Message, "birbirine ait")
	}
	failed := (&InstallRefusal{Code: ErrCodeLoadFailed, Message: "describe (plugin_trap): the plugin could not be started"}).Said("en")
	assert.Contains(t, failed.Message, "did not start")
	assert.Contains(t, failed.Message, "plugin_trap", "the detail is in the sentence: it is all there is to go on")
}

func TestDescribe_GetsAMinuteWhateverTheCallBudget(t *testing.T) {
	for _, s := range []int{0, 5, 15, 30, MaxCallTimeout} {
		m := &Manifest{}
		m.Limits.CallTimeoutS = s
		assert.Equal(t, MinDescribeTimeout*time.Second, DescribeBudget(m), "call_timeout_s %d", s)
	}
}

func TestCall_ABudgetThatRunsOutWhileTheInstanceStartsIsATimeout(t *testing.T) {
	c := fixture(t)
	in, _ := json.Marshal(wire.DescribeInput{HostVersion: HostVersion})
	_, err := c.Call(context.Background(), "describe", in, time.Nanosecond)
	require.Error(t, err)
	assert.True(t, IsCode(err, CodeTimeout), "a start cut short by the budget is not a crash: %v", err)
}
