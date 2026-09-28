package wasmplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ui_call (M3): the interface asks its module ─────────────────────────

// installEchoWithUI installs the echo module as an app that ALSO has an
// interface: the same module (describe stays a subset), a `ui` block and an
// interface view.
func (h *harness) installEchoWithUI(t *testing.T) (*Installed, []byte) {
	t.Helper()
	bundle := uiZip(t, uiDoc())
	in := echoInput(t, func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{}}
		m["views"] = append(m["views"].([]any), map[string]any{"id": "panel", "placement": "modal", "ui": "index.html", "label": map[string]any{"en": "Panel", "tr": "Pano"}})
	})
	in.UI = bytes.NewReader(bundle)
	m, err := ParseManifest(in.Manifest)
	require.NoError(t, err)
	in.Granted = permStrings(m.Perms)
	st, _, err := h.reg.Install(context.Background(), in)
	require.NoError(t, err)
	p, _ := h.reg.ByID(st.ID)
	return p, bundle
}

func TestUICall_TheInterfaceReachesItsModuleWithRefsNotPaths(t *testing.T) {
	h := newHarness(t, nil)
	p, _ := h.installEchoWithUI(t)
	assert.True(t, p.HasModule())
	h.writeFile(t, "doc.txt", "hello from the file")
	out, err := h.reg.UICall(context.Background(), "echo", "panel", h.st.ID, []string{"doc.txt"}, nil, "tr", "echo", json.RawMessage(`{"n":7}`))
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, "panel", got["view"])
	assert.Equal(t, "echo", got["method"])
	assert.Equal(t, map[string]any{"n": float64(7)}, got["params"])
	assert.EqualValues(t, 1, got["inputs"])
	assert.Equal(t, "hello from the file", got["first"], "the file is read through its ref")
	assert.Equal(t, "tr", got["locale"])

	_, err = h.reg.UICall(context.Background(), "echo", "panel", 0, nil, nil, "tr", "refuse", nil)
	require.Error(t, err)
	assert.Equal(t, "Bugün olmaz", err.(*CallError).Message, "the app's own refusal, in the reader's language")

	_, err = h.reg.UICall(context.Background(), "echo", "panel", h.st.ID, []string{"doc.txt"}, nil, "en", "write", nil)
	require.Error(t, err, "a call from an interface runs in screen mode: no file writes")

	_, err = h.reg.UICall(context.Background(), "echo", "hello", 0, nil, nil, "en", "echo", nil)
	assert.Error(t, err, "hello is a surface view, not an interface")
}

func TestUICall_AnInterfaceOnlyAppHasNoModuleToCall(t *testing.T) {
	h := newBareHarness(t, nil)
	h.installUI(t, uiManifest(t, nil), uiZip(t, uiDoc()))
	_, err := h.reg.UICall(context.Background(), "drawio", "editor", 0, nil, nil, "en", "anything", nil)
	require.Error(t, err)
	assert.Equal(t, CodeUnsupported, err.(*CallError).Code)
}

// A call from an interface starts a module instance like a view event does, so
// it shares the app's ceiling on concurrent screen calls (enterCall): a burst
// beyond it is told the app is busy instead of each getting an instance.
func TestUICall_SharesTheScreenCallCeiling(t *testing.T) {
	h := newHarness(t, nil)
	p, _ := h.installEchoWithUI(t)
	const burst = 12
	errs := make(chan error, burst)
	var wg sync.WaitGroup
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			_, err := h.reg.UICall(ctx, p.Row.Name, "panel", 0, nil, nil, "en", "stall", nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	busy := 0
	for err := range errs {
		if IsCode(err, "busy") {
			busy++
		}
	}
	assert.Greater(t, busy, 0, "calls beyond the ceiling are refused as busy")
}
