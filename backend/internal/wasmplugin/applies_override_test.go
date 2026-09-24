package wasmplugin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// The signing app's "Sign…": PDFs, and office files while LibreOffice is
// there to turn them into one (applies.engine_ext on feat/043-signing).
var (
	signRule = wire.Applies{Kind: "file", Ext: []string{"pdf"}, State: []string{"pending"}}
	officeG  = map[string][]string{"libreoffice": {"docx", "odt"}}
	withLO   = map[string]bool{"libreoffice": true}
	withoutL = map[string]bool{}
)

func edited(ext ...string) wire.Applies {
	a := wire.Applies{Kind: "file", Ext: ext}
	if err := validateApplies(&a); err != nil {
		panic(err)
	}
	return a
}

func resolved(t *testing.T, d *appliesDelta, present map[string]bool) (wire.Applies, bool) {
	t.Helper()
	require.NotNil(t, d, "an override that changed something is stored")
	// Through the stored form, as effectiveActions reads it.
	back := storedDelta(encodeDelta(d), signRule)
	require.NotNil(t, back)
	return back.resolve(signRule, officeG, present)
}

// The coordinator's break-test, both ways: an override saved while
// LibreOffice is missing offers the office types once it appears, and one
// saved while it is present stops offering them once it goes.
func TestOverride_EngineTypesFollowTheEngineNotTheSave(t *testing.T) {
	// The editor starts from the manifest's list plus every engine's —
	// whether LibreOffice is here or not.
	start := editorRule(signRule, officeG, nil)
	assert.Equal(t, []string{"pdf", "docx", "odt"}, start.Ext)

	// The admin adds .txt and changes nothing else.
	d := deltaFrom(signRule, officeG, edited("pdf", "docx", "odt", "txt"))
	assert.Equal(t, []string{"txt"}, d.ExtAdd)
	assert.Empty(t, d.ExtRemove)

	got, ok := resolved(t, d, withoutL)
	assert.True(t, ok)
	assert.Equal(t, []string{"pdf", "txt"}, got.Ext, "no LibreOffice: no office types")

	got, ok = resolved(t, d, withLO)
	assert.True(t, ok)
	assert.Equal(t, []string{"pdf", "txt", "docx", "odt"}, got.Ext, "LibreOffice arrived: the office types with it")

	got, _ = resolved(t, d, withoutL)
	assert.Equal(t, []string{"pdf", "txt"}, got.Ext, "…and gone with it again")

	// ⚠ The state gate the editor never showed survives the override.
	assert.Equal(t, []string{"pending"}, got.State)
}

// A type the admin took out stays out, engine or no engine.
func TestOverride_ARemovedEngineTypeStaysRemoved(t *testing.T) {
	d := deltaFrom(signRule, officeG, edited("pdf", "odt"))
	assert.Equal(t, []string{"docx"}, d.ExtRemove)
	got, _ := resolved(t, d, withLO)
	assert.Equal(t, []string{"pdf", "odt"}, got.Ext)
	got, _ = resolved(t, d, withoutL)
	assert.Equal(t, []string{"pdf"}, got.Ext)
	// And the editor shows the admin's list, not the manifest's.
	assert.Equal(t, []string{"pdf", "odt"}, editorRule(signRule, officeG, d).Ext)
}

// "Only .docx" while LibreOffice is missing leaves no type at all — which
// Matches would read as ANY file. It is offered on nothing instead.
func TestOverride_NothingLeftIsNothingNotEverything(t *testing.T) {
	d := deltaFrom(signRule, officeG, edited("docx"))
	got, ok := resolved(t, d, withoutL)
	assert.False(t, ok, "offered on nothing")
	assert.Empty(t, got.Ext)
	got, ok = resolved(t, d, withLO)
	assert.True(t, ok)
	assert.Equal(t, []string{"docx"}, got.Ext)

	// Clearing every type in the editor is the explicit "any file".
	d = deltaFrom(signRule, officeG, edited())
	assert.True(t, d.AnyFile)
	got, ok = resolved(t, d, withoutL)
	assert.True(t, ok)
	assert.Empty(t, got.Ext)
	assert.Empty(t, got.Mime)
}

// The same list as the manifest's is no override at all.
func TestOverride_NoChangeStoresNothing(t *testing.T) {
	assert.Nil(t, deltaFrom(signRule, officeG, edited("pdf", "docx", "odt")))
	assert.Nil(t, deltaFrom(signRule, officeG, edited(".PDF", "docx", "odt")), "normalised before comparing")
	assert.Equal(t, "", encodeDelta(nil))
}

// An upgrade that adds an extension reaches an overridden action.
func TestOverride_AnUpgradeStillReachesIt(t *testing.T) {
	d := deltaFrom(signRule, officeG, edited("pdf", "docx", "odt", "txt"))
	upgraded := signRule
	upgraded.Ext = []string{"pdf", "pdfa"}
	got, _ := d.resolve(upgraded, officeG, withoutL)
	assert.Equal(t, []string{"pdf", "pdfa", "txt"}, got.Ext)
}

// A row written before deltas is the admin's explicit choice: what it says
// is kept, what it could not have said (the engine types the old editor
// never showed) follows the engine.
func TestOverride_LegacyRowsKeepWhatTheySay(t *testing.T) {
	// Flattened while LibreOffice was present: docx/odt are in the row
	// because the host folded them in; .md was the admin's.
	flat := wire.Applies{Kind: "file", Ext: []string{"pdf", "docx", "odt", "md"}}
	d := legacyDelta(signRule, officeG, flat)
	assert.Equal(t, []string{"md"}, d.ExtAdd)
	assert.Empty(t, d.ExtRemove)
	got, _ := d.resolve(signRule, officeG, withoutL)
	assert.Equal(t, []string{"pdf", "md"}, got.Ext, "LibreOffice gone: no .docx, whatever the old row held")
	got, _ = d.resolve(signRule, officeG, withLO)
	assert.Equal(t, []string{"pdf", "md", "docx", "odt"}, got.Ext)

	// A manifest type the row left out was removed by the admin.
	d = legacyDelta(signRule, officeG, wire.Applies{Kind: "file", Ext: []string{"md"}})
	assert.Equal(t, []string{"pdf"}, d.ExtRemove)

	// A row with no type at all said "any file" and still does.
	d = legacyDelta(signRule, officeG, wire.Applies{Kind: "file"})
	assert.True(t, d.AnyFile)

	// A row identical to the manifest is no override.
	assert.Nil(t, legacyDelta(signRule, officeG, wire.Applies{Kind: "file", Ext: []string{"pdf"}}))
}

// Through the registry: the admin API, the stored row, the menu and the run
// check. `upper` is given engine-gated extensions through the test hook
// (the echo manifest declares none; applies_override_engine_test.go drives
// the real `engine_ext` field); echo is granted ffmpeg.
func TestOverride_ThroughTheRegistry(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	was := manifestEngineExt
	manifestEngineExt = func(a wire.Applies) map[string][]string {
		if len(a.Ext) == 1 && a.Ext[0] == "txt" {
			return map[string][]string{"ffmpeg": {"mp4", "webm"}}
		}
		return nil
	}
	t.Cleanup(func() { manifestEngineExt = was })
	ctx := context.Background()

	upperExt := func() []string {
		ans, err := h.reg.ActionsFor(ctx, true)
		require.NoError(t, err)
		for _, a := range ans.Actions {
			if a.ID == "upper" {
				return a.Applies.Ext
			}
		}
		t.Fatal("no upper action")
		return nil
	}

	// Saved while ffmpeg is NOT here: the editor still showed mp4/webm.
	h.reg.engines = &engineSet{bins: map[string]string{}}
	require.NoError(t, h.reg.PutOverrides(ctx, p.Row.ID, []OverrideRow{
		{ID: "upper", Enabled: true, Applies: &wire.Applies{Kind: "file", Ext: []string{"txt", "mp4", "webm", "md"}}},
	}))
	stored, err := h.reg.opts.Store.ListAppPluginOverrides(ctx, p.Row.ID)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.JSONEq(t, `{"v":2,"ext_add":["md"]}`, stored[0].AppliesJSON, "the change, not the rule")

	assert.Equal(t, []string{"txt", "md"}, upperExt())
	_, _, applies, err := h.reg.ResolveAction(ctx, "echo", "upper", true)
	require.NoError(t, err)
	assert.False(t, Matches(applies, []Item{{Kind: "file", Ext: "mp4"}}), "the run check agrees with the menu")

	// ffmpeg appears: the video types come with it.
	h.reg.engines = &engineSet{bins: map[string]string{"ffmpeg": "/usr/bin/ffmpeg"}}
	assert.Equal(t, []string{"txt", "md", "mp4", "webm"}, upperExt())
	_, _, applies, err = h.reg.ResolveAction(ctx, "echo", "upper", true)
	require.NoError(t, err)
	assert.True(t, Matches(applies, []Item{{Kind: "file", Ext: "mp4"}}))

	// …and the admin API hands the editor its own list back.
	rows, err := h.reg.Overrides(ctx, p.Row.ID)
	require.NoError(t, err)
	for _, r := range rows {
		if r.ID == "upper" {
			require.NotNil(t, r.Applies)
			assert.Equal(t, []string{"txt", "mp4", "webm", "md"}, r.Applies.Ext)
		}
	}
}

// Through the registry, the fail-closed half: an override that keeps only a
// type an absent engine adds is offered on NOTHING — not, as an empty list
// would read, on every file.
func TestOverride_ThroughTheRegistry_NothingLeftIsOffNotEverywhere(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	was := manifestEngineExt
	manifestEngineExt = func(a wire.Applies) map[string][]string {
		if len(a.Ext) == 1 && a.Ext[0] == "txt" {
			return map[string][]string{"ffmpeg": {"mp4"}}
		}
		return nil
	}
	t.Cleanup(func() { manifestEngineExt = was })
	ctx := context.Background()
	offered := func() bool {
		ans, err := h.reg.ActionsFor(ctx, true)
		require.NoError(t, err)
		for _, a := range ans.Actions {
			if a.ID == "upper" {
				return true
			}
		}
		return false
	}

	require.NoError(t, h.reg.PutOverrides(ctx, p.Row.ID, []OverrideRow{
		{ID: "upper", Enabled: true, Applies: &wire.Applies{Kind: "file", Ext: []string{"mp4"}}},
	}))
	h.reg.engines = &engineSet{bins: map[string]string{}}
	assert.False(t, offered(), "only .mp4, and no ffmpeg: not in anybody's menu")
	_, _, _, err := h.reg.ResolveAction(ctx, "echo", "upper", true)
	assert.Error(t, err, "and the run check refuses it")

	h.reg.engines = &engineSet{bins: map[string]string{"ffmpeg": "/usr/bin/ffmpeg"}}
	assert.True(t, offered())
}

// Rows written before deltas are rewritten at start, and keep what they said.
func TestOverride_LoadRewritesLegacyRows(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	ctx := context.Background()
	legacy, _ := json.Marshal(wire.Applies{Kind: "file", Ext: []string{"md"}})
	require.NoError(t, h.reg.opts.Store.PutAppPluginOverrides(ctx, p.Row.ID, []*model.AppPluginOverride{
		{PluginID: p.Row.ID, ActionID: "upper", Enabled: true, AppliesJSON: string(legacy)},
	}))

	// A restart: Load reads every app again and converts on the way.
	require.NoError(t, h.reg.Load(ctx))
	p, _ = h.reg.ByID(p.Row.ID)

	stored, err := h.reg.opts.Store.ListAppPluginOverrides(ctx, p.Row.ID)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.JSONEq(t, `{"v":2,"ext_add":["md"],"ext_remove":["txt"]}`, stored[0].AppliesJSON)
	ans, err := h.reg.ActionsFor(ctx, true)
	require.NoError(t, err)
	for _, a := range ans.Actions {
		if a.ID == "upper" {
			assert.Equal(t, []string{"md"}, a.Applies.Ext, "exactly what the old row said")
		}
	}

	// Idempotent: a second start changes nothing.
	h.reg.upgradeLegacyOverrides(ctx, p)
	again, _ := h.reg.opts.Store.ListAppPluginOverrides(ctx, p.Row.ID)
	assert.Equal(t, stored[0].AppliesJSON, again[0].AppliesJSON)
}
