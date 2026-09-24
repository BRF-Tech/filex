package wasmplugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// The merge seam of feat/043-apps-polish (overrides stored as a delta) and
// feat/043-signing (`applies.engine_ext`), through the REAL manifest field —
// no test hook. An administrator's override saved while LibreOffice is
// missing offers the office types once LibreOffice appears, and stops when it
// goes; an office type the administrator removed stays removed whatever the
// engine does; and a row nobody overrode still gets them from withEngineExt.
//
// ⚠ Before the seam was connected manifestEngineExt answered nil: an
// overridden "Sign…" would never have been offered on a .docx again, and a
// non-overridden one would have lost nothing — so only an override showed it.
func TestOverride_EngineExtFromTheManifestFollowsTheEngine(t *testing.T) {
	h := newHarness(t, nil)
	st, _, err := h.reg.Install(context.Background(), echoInput(t, func(m map[string]any) {
		m["permissions"] = append(m["permissions"].([]any), "engines:libreoffice")
		for _, a := range m["actions"].([]any) {
			a := a.(map[string]any)
			switch a["id"] {
			case "upper", "deliver":
				a["applies"] = map[string]any{"kind": "file", "ext": []any{"pdf"}, "engine_ext": map[string]any{"libreoffice": []any{"docx", "odt"}}}
			}
		}
	}))
	require.NoError(t, err)
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)
	ctx := context.Background()

	extOf := func(id string) []string {
		ans, err := h.reg.ActionsFor(ctx, true)
		require.NoError(t, err)
		for _, a := range ans.Actions {
			if a.ID == id {
				assert.Nil(t, a.Applies.EngineExt, "a client never sees the engine map")
				return a.Applies.Ext
			}
		}
		t.Fatalf("no %s action", id)
		return nil
	}
	runs := func(id, ext string) bool {
		_, _, applies, err := h.reg.ResolveAction(ctx, "echo", id, true)
		require.NoError(t, err)
		return Matches(applies, []Item{{Kind: "file", Ext: ext}})
	}

	// Saved WITHOUT LibreOffice. The editor shows the office types all the
	// same (the admin decides about .docx once, not once per install of
	// LibreOffice); the admin adds .txt to `upper`.
	h.reg.engines = &engineSet{bins: map[string]string{}}
	require.NoError(t, h.reg.PutOverrides(ctx, p.Row.ID, []OverrideRow{
		{ID: "upper", Enabled: true, Applies: &wire.Applies{Kind: "file", Ext: []string{"pdf", "docx", "odt", "txt"}}},
	}))
	stored, err := h.reg.opts.Store.ListAppPluginOverrides(ctx, p.Row.ID)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.JSONEq(t, `{"v":2,"ext_add":["txt"]}`, stored[0].AppliesJSON, "the change against the manifest AND its engine types, nothing frozen")
	rows, err := h.reg.Overrides(ctx, p.Row.ID)
	require.NoError(t, err)
	for _, r := range rows {
		if r.ID == "upper" {
			require.NotNil(t, r.Applies)
			assert.Equal(t, []string{"pdf", "docx", "odt", "txt"}, r.Applies.Ext, "the editor gets its own list back, engine types included")
		}
	}
	assert.Equal(t, []string{"pdf", "txt"}, extOf("upper"), "no LibreOffice: no office types")
	assert.Equal(t, []string{"pdf"}, extOf("deliver"))
	assert.False(t, runs("upper", "docx"), "the run check agrees with the menu")

	// LibreOffice appears: the office types come with it — overridden or not.
	h.reg.engines = &engineSet{bins: map[string]string{"libreoffice": "/usr/bin/soffice"}}
	assert.Equal(t, []string{"pdf", "txt", "docx", "odt"}, extOf("upper"), "LibreOffice arrived: the overridden action offers the office types")
	assert.Equal(t, []string{"pdf", "docx", "odt"}, extOf("deliver"), "…and so does the one nobody overrode")
	assert.True(t, runs("upper", "docx"))

	// The admin takes .odt out while LibreOffice is here: it stays out, and
	// the second fold (withEngineExt) does not bring it back.
	require.NoError(t, h.reg.PutOverrides(ctx, p.Row.ID, []OverrideRow{
		{ID: "upper", Enabled: true, Applies: &wire.Applies{Kind: "file", Ext: []string{"pdf", "docx", "txt"}}},
	}))
	assert.Equal(t, []string{"pdf", "txt", "docx"}, extOf("upper"))
	assert.False(t, runs("upper", "odt"), "a removed engine type came back through the second fold")

	// LibreOffice goes: the office types go with it.
	h.reg.engines = &engineSet{bins: map[string]string{}}
	assert.Equal(t, []string{"pdf", "txt"}, extOf("upper"))
	assert.False(t, runs("upper", "docx"))
}
