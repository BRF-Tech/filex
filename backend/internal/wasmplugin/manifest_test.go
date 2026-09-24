package wasmplugin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

func TestParseManifest_MinimalAndDefaults(t *testing.T) {
	m, err := ParseManifest([]byte(`{"manifest_version":1,"name":"sign","version":"1.0.0","label":{"en":"Sign"},
	  "permissions":["files:read","files:write","sign","engines:libreoffice","http:tsa.example.com","http:*.example.org"],
	  "actions":[{"id":"sign","label":{"en":"Sign…"},"applies":{"ext":["PDF",".pdf"]},"output":{"mode":"sibling"}}],
	  "views":[{"id":"status","label":{"en":"Status"},"placement":"inspector"}]}`))
	require.NoError(t, err)
	assert.Equal(t, "file", m.Actions[0].Applies.Kind, "kind defaults to file")
	assert.Equal(t, []string{"pdf", "pdf"}, m.Actions[0].Applies.Ext, "extensions are lower-cased and un-dotted")
	assert.Equal(t, "{stem}-sign{ext}", m.Actions[0].Output.Name, "sibling output gets a name pattern")
	g := m.Grants()
	assert.True(t, g.Has(PermSign))
	assert.True(t, g.HasEngine("libreoffice"))
	assert.False(t, g.HasEngine("ffmpeg"))
	assert.True(t, g.HasHost("tsa.example.com"))
	assert.True(t, g.HasHost("api.example.org"), "wildcard grant covers a subdomain")
	assert.False(t, g.HasHost("example.org"), "…but not the apex")
	assert.False(t, g.HasHost("evil.example.com"))
	assert.Equal(t, []string{"*.example.org", "tsa.example.com"}, g.AllowedHosts())
	assert.Equal(t, DefaultMemoryPages, m.MemoryPages())
	assert.Equal(t, DefaultCallTimeout, m.CallTimeout())
}

func TestParseManifest_Refusals(t *testing.T) {
	cases := map[string]string{
		"wrong version":       `{"manifest_version":2,"name":"a","version":"1","label":{"en":"A"},"permissions":[]}`,
		"bad name":            `{"manifest_version":1,"name":"Bad Name","version":"1","label":{"en":"A"},"permissions":[]}`,
		"no english label":    `{"manifest_version":1,"name":"a","version":"1","label":{"tr":"A"},"permissions":[]}`,
		"unknown permission":  `{"manifest_version":1,"name":"a","version":"1","label":{"en":"A"},"permissions":["files:delete"]}`,
		"unknown engine":      `{"manifest_version":1,"name":"a","version":"1","label":{"en":"A"},"permissions":["engines:blender"]}`,
		"http with path":      `{"manifest_version":1,"name":"a","version":"1","label":{"en":"A"},"permissions":["http:api.example.com/v1"]}`,
		"unknown field":       `{"manifest_version":1,"name":"a","version":"1","label":{"en":"A"},"permisions":[]}`,
		"write without grant": `{"manifest_version":1,"name":"a","version":"1","label":{"en":"A"},"permissions":["files:read"],"actions":[{"id":"x","label":{"en":"X"},"output":{"mode":"sibling"}}]}`,
		"view not declared":   `{"manifest_version":1,"name":"a","version":"1","label":{"en":"A"},"permissions":[],"actions":[{"id":"x","label":{"en":"X"},"view":"nope"}]}`,
		"multi max":           `{"manifest_version":1,"name":"a","version":"1","label":{"en":"A"},"permissions":[],"actions":[{"id":"x","label":{"en":"X"},"applies":{"max":3}}]}`,
		"pages without grant": `{"manifest_version":1,"name":"a","version":"1","label":{"en":"A"},"permissions":[],"public_pages":[{"id":"p","label":{"en":"P"}}]}`,
		"bad placement":       `{"manifest_version":1,"name":"a","version":"1","label":{"en":"A"},"permissions":[],"views":[{"id":"v","label":{"en":"V"},"placement":"sidebar"}]}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseManifest([]byte(doc))
			assert.Error(t, err)
		})
	}
}

func TestMatches(t *testing.T) {
	pdf := wire.Applies{Kind: "file", Ext: []string{"pdf"}}
	img := wire.Applies{Kind: "file", Mime: []string{"image/*"}, Multi: true, Max: 3}
	any := wire.Applies{Kind: "any"}
	f := func(ext, mime string) Item { return Item{Kind: "file", Ext: ext, Mime: mime} }
	d := Item{Kind: "dir"}

	assert.True(t, Matches(pdf, []Item{f("pdf", "application/pdf")}))
	assert.False(t, Matches(pdf, []Item{f("txt", "text/plain")}))
	assert.False(t, Matches(pdf, []Item{f("pdf", ""), f("pdf", "")}), "not multi")
	assert.False(t, Matches(pdf, []Item{d}))
	assert.True(t, Matches(img, []Item{f("jpg", "image/jpeg"), f("png", "image/png")}))
	assert.False(t, Matches(img, []Item{f("jpg", "image/jpeg"), f("mp4", "video/mp4")}))
	assert.False(t, Matches(img, []Item{f("a", "image/a"), f("b", "image/b"), f("c", "image/c"), f("d", "image/d")}), "max 3")
	assert.True(t, Matches(any, []Item{d}))
	assert.False(t, Matches(any, nil))
}

func TestPermissionLabels(t *testing.T) {
	for _, p := range []Permission{PermFilesRead, PermFilesWrite, PermSign, PermMailSend, PermNotifySend, PermUsersLookup, PermSettings, PermState, PermPublicPages, "http:x.example.com", "engines:ffmpeg"} {
		assert.NotEqual(t, string(p), p.Label("en"), "%s needs an English label", p)
		assert.NotEqual(t, string(p), p.Label("tr"), "%s needs a Turkish label", p)
	}
}

// ⚠⚠ A permission that does nothing is not offered at the install review.
// `events:<name>` parsed, was granted and printed "Is told about {event}
// events (not wired yet)" in the list an administrator reads before trusting
// an app with their files — while `on_event` (pkg/pluginkit/exports_wasm.go)
// returns 0 and no host code calls it. The manifest is refused instead, like
// every other name the host does not know; the day events are delivered, the
// case comes back with the wiring, not before.
func TestParsePermission_EventsIsRefusedUntilItDoesSomething(t *testing.T) {
	for _, s := range []string{"events:file.uploaded", "events:", "events:*"} {
		_, err := ParsePermission(s)
		require.Error(t, err, s)
		assert.Contains(t, err.Error(), "grant nothing", s)
	}
	// The permission has no sentence to print any more, and the review would
	// never reach one: Label falls back to the raw id for an unknown name.
	assert.Equal(t, "events:file.uploaded", Permission("events:file.uploaded").Label("en"))
	assert.NotContains(t, srvtext.Keys(), "server.perm.events")
	// The neighbours still parse — the closed set did not shrink by accident.
	for _, s := range []string{"files:read", "schedule", "http:a.example.com", "engines:ffmpeg"} {
		_, err := ParsePermission(s)
		require.NoError(t, err, s)
	}
}

func TestMatches_StateAware(t *testing.T) {
	needs := wire.Applies{Kind: "file", Ext: []string{"pdf"}, State: []string{"pending"}}
	none := wire.Applies{Kind: "file", Ext: []string{"pdf"}, NoState: []string{"pending", "done"}}
	pending := Item{Kind: "file", Ext: "pdf", State: []string{"pending"}}
	done := Item{Kind: "file", Ext: "pdf", State: []string{"done"}}
	fresh := Item{Kind: "file", Ext: "pdf"}

	assert.True(t, Matches(needs, []Item{pending}))
	assert.False(t, Matches(needs, []Item{fresh}), "state required, none kept")
	assert.False(t, Matches(needs, []Item{done}), "another key does not count")
	assert.True(t, Matches(none, []Item{fresh}))
	assert.False(t, Matches(none, []Item{pending}))
	assert.False(t, Matches(none, []Item{done}))
}

func TestParseManifest_PagePlacementAndStateKeys(t *testing.T) {
	m, err := ParseManifest([]byte(`{"manifest_version":1,"name":"sign","version":"1.0.0","label":{"en":"Sign"},
	  "permissions":["files:read","files:lock"],
	  "actions":[{"id":"sign","label":{"en":"Sign"},"applies":{"ext":["pdf"],"state":[" pending "]},"view":"wizard","output":{"mode":"none"}}],
	  "views":[{"id":"wizard","label":{"en":"Wizard"},"placement":"page"}]}`))
	require.NoError(t, err)
	assert.Equal(t, "page", m.Views[0].Placement)
	assert.Equal(t, []string{"pending"}, m.Actions[0].Applies.State, "keys are trimmed")

	_, err = ParseManifest([]byte(`{"manifest_version":1,"name":"x","version":"1","label":{"en":"x"},"permissions":[],
	  "actions":[{"id":"a","label":{"en":"a"},"applies":{"state":["sign:pending"]},"output":{"mode":"none"}}]}`))
	require.Error(t, err, "a colon would collide with the <plugin>:<key> listing form")
	_, err = ParseManifest([]byte(`{"manifest_version":1,"name":"x","version":"1","label":{"en":"x"},"permissions":[],
	  "views":[{"id":"v","label":{"en":"v"},"placement":"popup"}]}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "modal, page, inspector or home")
}

func TestParseManifest_LanguagesAndConditions(t *testing.T) {
	// A plugin that claims Turkish must speak it everywhere it shows text.
	_, err := ParseManifest([]byte(`{"manifest_version":1,"name":"sign","version":"1.0.0","label":{"en":"Sign","tr":"İmza"},
	  "languages":["en","tr"],"permissions":[],
	  "actions":[{"id":"a","label":{"en":"Sign"},"applies":{"ext":["pdf"]},"output":{"mode":"none"}}]}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no "tr" text`)

	// English is what everything else falls back to, so it cannot be left out.
	_, err = ParseManifest([]byte(`{"manifest_version":1,"name":"x","version":"1","label":{"en":"x","tr":"x"},
	  "languages":["tr"],"permissions":[]}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must include")

	// A field may not depend on a field that is not there, and a choice may
	// not be empty.
	_, err = ParseManifest([]byte(`{"manifest_version":1,"name":"x","version":"1","label":{"en":"x"},"permissions":[],
	  "settings":[{"key":"name","type":"string","show_when":{"key":"mode","equals":["sibling"]}}]}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a field here")
	_, err = ParseManifest([]byte(`{"manifest_version":1,"name":"x","version":"1","label":{"en":"x"},"permissions":[],
	  "settings":[{"key":"mode","type":"select"}]}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no options")

	// The whole shape, accepted: a language pack and a conditional field.
	m, err := ParseManifest([]byte(`{"manifest_version":1,"name":"x","version":"1","label":{"en":"x"},"permissions":[],
	  "ui_locales":{"AR":{"app.name":"فايلكس"}},
	  "settings":[{"key":"mode","type":"select","options":[{"value":"sibling","label":"Beside it"}],"multi":false},
	              {"key":"name","type":"string","required_when":{"key":"mode","equals":["sibling"]}}]}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"en"}, m.Languages, "a plugin that says nothing speaks English")
	assert.Contains(t, m.UILocales, "ar", "language tags are lower-cased")
	require.NotNil(t, m.Settings[1].RequiredWhen)
	assert.Equal(t, "mode", m.Settings[1].RequiredWhen.Key)
}

// The host keeps its own directories beside the apps' (cache, spool, public,
// and assets — asset_fetch's downloads). An app named after one would share
// it, and uninstalling that app would delete everybody's.
func TestManifest_TheHostsOwnDirectoriesAreNotAppNames(t *testing.T) {
	for _, name := range []string{"cache", "spool", "public", "assets"} {
		_, err := ParseManifest([]byte(`{"manifest_version":1,"name":"` + name + `","version":"1.0.0","label":{"en":"X"}}`))
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
