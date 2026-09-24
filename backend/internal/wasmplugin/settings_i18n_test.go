package wasmplugin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A manifest setting's texts may be one string or a {lang: …} map, like every
// other text an app shows. 2026-09-21: the signing app's settings were
// English inside the Turkish admin panel; given as maps, an older host
// refused the whole manifest ("cannot unmarshal object into … Field.settings
// .label of type string").
func TestInstall_SettingsTextInEveryLanguageOrOneString(t *testing.T) {
	withSettings := func(settings []any) func(m map[string]any) {
		return func(m map[string]any) {
			m["permissions"] = append(m["permissions"].([]any), "settings")
			m["settings"] = settings
		}
	}
	localised := []any{
		map[string]any{"key": "stamp", "type": "bool",
			"label": map[string]any{"en": "Add a time stamp", "tr": "Zaman damgası ekle"},
			"help":  map[string]any{"en": "Off by default.", "tr": "Varsayılan olarak kapalı."}},
		map[string]any{"key": "mode", "type": "select",
			"label":   "Mode",
			"options": []any{map[string]any{"value": "a", "label": map[string]any{"en": "Fast", "tr": "Hızlı"}}}},
	}

	h := newHarness(t, nil)
	st, _, err := h.reg.Install(context.Background(), echoInput(t, withSettings(localised)))
	require.NoError(t, err, "a localised setting installs")
	_, fields, err := h.reg.Settings(context.Background(), st.ID)
	require.NoError(t, err)
	require.Len(t, fields, 2)
	assert.Equal(t, "Zaman damgası ekle", fields[0].Localized("tr").Label)
	assert.Equal(t, "Hızlı", fields[1].Localized("tr").Options[0].Label)
	// What the admin panel receives keeps the maps — it resolves {en, tr}
	// itself (lib/surfaceValues.storageFieldOf → labelOf).
	p, _ := h.reg.ByID(st.ID)
	raw, err := json.Marshal(p.Manifest.Manifest)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"label":{"en":"Add a time stamp","tr":"Zaman damgası ekle"}`)
	assert.Contains(t, string(raw), `"label":"Mode"`, "a plain string stays one")

	// A plain-string manifest — every app written before this — installs as it did.
	h2 := newHarness(t, nil)
	_, _, err = h2.reg.Install(context.Background(), echoInput(t, withSettings([]any{
		map[string]any{"key": "stamp", "type": "bool", "label": "Add a time stamp", "help": "Off by default."},
	})))
	require.NoError(t, err, "a plain-string setting installs")

	// A map promises every language the app declares.
	h3 := newHarness(t, nil)
	_, _, err = h3.reg.Install(context.Background(), echoInput(t, withSettings([]any{
		map[string]any{"key": "stamp", "type": "bool", "label": map[string]any{"en": "Add a time stamp"}},
	})))
	require.Error(t, err, "a localised label missing a declared language is refused")
}
