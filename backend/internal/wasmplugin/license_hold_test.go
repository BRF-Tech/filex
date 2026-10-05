package wasmplugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── License holds (license_hold.go) ────────────────────────────────────
//
// "A held app runs nothing": not only its actions (State() refuses them), but
// a screen and its events, and a job that was queued while it still ran -
// every caller that reaches the module through running().

func TestLicenseHold_AHeldAppOpensNoScreen(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	_, err := h.reg.ViewEvent(context.Background(), "echo", "hello", 0, nil, nil, "en", wire.ViewEventInput{Event: "open"})
	require.NoError(t, err, "control: the screen opens while the app is not held")

	h.reg.SetLicenseHold("echo", "license: revoked")
	st, _ := p.State()
	require.Equal(t, StateUnlicensed, st)
	_, _, _, aerr := h.reg.ResolveAction(context.Background(), "echo", "upper", true)
	assert.Error(t, aerr, "control: an action of a held app is refused")

	s, err := h.reg.ViewEvent(context.Background(), "echo", "hello", 0, nil, nil, "en", wire.ViewEventInput{Event: "open"})
	title := ""
	if s != nil {
		title = s.Title.Get("en")
	}
	require.Error(t, err, "a HELD (unlicensed) app ran its module for a screen; surface title=%q", title)
	assert.True(t, IsCode(err, CodeUnsupported), "%v", err)
	assert.Contains(t, err.Error(), "license: revoked")

	h.reg.SetLicenseHold("echo", "")
	_, err = h.reg.ViewEvent(context.Background(), "echo", "hello", 0, nil, nil, "en", wire.ViewEventInput{Event: "open"})
	assert.NoError(t, err, "released: the screen opens again")
}

// A job queued while the app ran, executed by the ops worker after the hold.
func TestLicenseHold_AJobQueuedBeforeTheHoldDoesNotRun(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "docs/a.txt", "hello")
	h.reg.SetLicenseHold("echo", "license: revoked")
	job, err := h.runJob(t, p, "upper", []string{"docs/a.txt"}, "en")
	status := ""
	if job != nil {
		status = job.Status
	}
	assert.Error(t, err, "a HELD app's queued job ran; job status=%s", status)
	assert.NotEqual(t, model.AppPluginJobOK, status)
}
