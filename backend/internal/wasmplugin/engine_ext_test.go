package wasmplugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// An action is offered on an extension that needs an engine only while the
// engine is there. 2026-09-21, a tester: "İmzala…" was offered on a .docx
// on an installation without LibreOffice, and the click opened a page
// saying there was no LibreOffice to convert it.
func TestActionsFor_EngineExtensionsExistOnlyWithTheEngine(t *testing.T) {
	h := newHarness(t, nil)
	_, _, err := h.reg.Install(context.Background(), echoInput(t, func(m map[string]any) {
		acts := m["actions"].([]any)
		for _, a := range acts {
			a := a.(map[string]any)
			if a["id"] == "upper" {
				a["applies"] = map[string]any{"kind": "file", "ext": []any{"txt"}, "engine_ext": map[string]any{"ffmpeg": []any{"MP4", ".webm"}}}
			}
		}
	}))
	require.NoError(t, err)
	upper := func() wire.Applies {
		ans, err := h.reg.ActionsFor(context.Background(), true)
		require.NoError(t, err)
		for _, a := range ans.Actions {
			if a.ID == "upper" {
				return a.Applies
			}
		}
		t.Fatal("no upper action")
		return wire.Applies{}
	}
	within := []Item{{Kind: "file", Ext: "mp4"}}

	h.reg.engines = &engineSet{bins: map[string]string{}}
	got := upper()
	assert.Equal(t, []string{"txt"}, got.Ext, "no ffmpeg: only the extension that needs nothing")
	assert.Nil(t, got.EngineExt, "a client never sees the engine map")
	assert.False(t, Matches(got, within))
	_, _, applies, err := h.reg.ResolveAction(context.Background(), "echo", "upper", true)
	require.NoError(t, err)
	assert.False(t, Matches(applies, within), "the run check agrees with the menu")

	h.reg.engines = &engineSet{bins: map[string]string{"ffmpeg": "/usr/bin/ffmpeg"}}
	got = upper()
	assert.Equal(t, []string{"txt", "mp4", "webm"}, got.Ext, "normalised like ext")
	assert.True(t, Matches(got, within))
	_, _, applies, err = h.reg.ResolveAction(context.Background(), "echo", "upper", true)
	require.NoError(t, err)
	assert.True(t, Matches(applies, within))
}

func TestValidateApplies_EngineExt(t *testing.T) {
	// Nothing to add to: an empty ext/mime list already means any file.
	err := validateApplies(&wire.Applies{EngineExt: map[string][]string{"libreoffice": {"docx"}}})
	require.Error(t, err)
	err = validateApplies(&wire.Applies{Ext: []string{"pdf"}, EngineExt: map[string][]string{"word": {"docx"}}})
	require.Error(t, err, "an unknown engine")
	err = validateApplies(&wire.Applies{Ext: []string{"pdf"}, EngineExt: map[string][]string{"libreoffice": {" "}}})
	require.Error(t, err, "an empty extension")
}
