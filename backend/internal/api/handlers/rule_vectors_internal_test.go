package handlers

// The server's half of the rule-mirror drift test (filex #211, audit B20):
// testdata/rule-mirrors.json holds cases a rule decides; the server's rule is
// checked against the file here, and the clients' copies against the same
// file in web/tests/lib/serverRuleVectors.test.ts. A rule changed on one side
// only turns one of the two red. (The onlyoffice package checks its own
// besideTypes against the file: onlyoffice/rule_vectors_internal_test.go.)

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/archivecli"
	"github.com/brf-tech/filex/backend/internal/editkind"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

type regexVectors struct {
	Pattern string   `json:"pattern"`
	OK      []string `json:"ok"`
	Bad     []string `json:"bad"`
}

type ruleVectors struct {
	EditKinds   editkind.Kinds `json:"edit_kinds"`
	Limits      clientLimits   `json:"limits"`
	ThemeKey    regexVectors   `json:"theme_key"`
	AccentHex   regexVectors   `json:"accent_hex"`
	StoreToken  regexVectors   `json:"store_token"`
	ZipPassword struct {
		From int      `json:"printable_from"`
		To   int      `json:"printable_to"`
		OK   []string `json:"ok"`
		Bad  []string `json:"bad"`
	} `json:"zip_password"`
	UISaveChunkBytes int `json:"ui_save_chunk_bytes"`
	Applies          []struct {
		Rule  wire.Applies      `json:"rule"`
		Items []wasmplugin.Item `json:"items"`
		Match bool              `json:"match"`
	} `json:"applies"`
	ActionNeed []struct {
		MinRole    string `json:"min_role"`
		OutputMode string `json:"output_mode"`
		Need       string `json:"need"`
	} `json:"action_need"`
	Conditions []struct {
		Cond   *wire.Condition `json:"cond"`
		Values map[string]any  `json:"values"`
		Met    bool            `json:"met"`
	} `json:"conditions"`
}

func loadRuleVectors(t *testing.T) ruleVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/rule-mirrors.json")
	require.NoError(t, err)
	var v ruleVectors
	require.NoError(t, json.Unmarshal(raw, &v))
	return v
}

// The lists and numbers the capabilities publish are the ones the web tests
// seed their components with (web/tests/setup.ts): the file is current.
func TestRuleVectors_PublishedRulesAreTheFile(t *testing.T) {
	v := loadRuleVectors(t)
	assert.Equal(t, editkind.Published(), v.EditKinds, "testdata/rule-mirrors.json edit_kinds")
	assert.Equal(t, currentClientLimits(), v.Limits, "testdata/rule-mirrors.json limits")
}

func TestRuleVectors_Patterns(t *testing.T) {
	v := loadRuleVectors(t)
	assert.Equal(t, themeKeyRe.String(), v.ThemeKey.Pattern, "handlers/themes.go themeKeyRe")
	for _, s := range v.ThemeKey.OK {
		assert.True(t, themeKeyRe.MatchString(s), "theme key %q", s)
	}
	for _, s := range v.ThemeKey.Bad {
		assert.False(t, themeKeyRe.MatchString(s), "theme key %q", s)
	}
	assert.Equal(t, brandingAccentRe.String(), v.AccentHex.Pattern, "handlers/branding.go brandingAccentRe")
	for _, s := range v.AccentHex.OK {
		assert.True(t, brandingAccentRe.MatchString(s), "accent %q", s)
	}
	for _, s := range v.AccentHex.Bad {
		assert.False(t, brandingAccentRe.MatchString(s), "accent %q", s)
	}
	for _, s := range v.StoreToken.OK {
		assert.True(t, appstore.ValidToken(s), "store token %q", s)
	}
	for _, s := range v.StoreToken.Bad {
		assert.False(t, appstore.ValidToken(s), "store token %q", s)
	}
}

func TestRuleVectors_ZipPassword(t *testing.T) {
	v := loadRuleVectors(t)
	assert.Equal(t, 0x20, v.ZipPassword.From)
	assert.Equal(t, 0x7e, v.ZipPassword.To)
	for _, s := range v.ZipPassword.OK {
		assert.NoError(t, archivecli.CheckPassword(s, "zip"), "%q", s)
	}
	for _, s := range v.ZipPassword.Bad {
		assert.ErrorIs(t, archivecli.CheckPassword(s, "zip"), archivecli.ErrPasswordCharset, "%q", s)
	}
}

func TestRuleVectors_UISaveChunk(t *testing.T) {
	assert.Equal(t, uiChunkMax, loadRuleVectors(t).UISaveChunkBytes, "app_ui_chunks.go uiChunkMax")
}

func TestRuleVectors_Applies(t *testing.T) {
	for i, c := range loadRuleVectors(t).Applies {
		assert.Equal(t, c.Match, wasmplugin.Matches(c.Rule, c.Items), "applies case %d", i)
	}
}

func TestRuleVectors_ActionNeed(t *testing.T) {
	for i, c := range loadRuleVectors(t).ActionNeed {
		action := &wire.Action{MinRole: c.MinRole, Output: wire.Output{Mode: c.OutputMode}}
		_, writes, _ := jobOutputMode(action, nil)
		assert.Equal(t, c.Need, pluginACLNeed(action, writes).String(), "action need case %d", i)
	}
}

func TestRuleVectors_Conditions(t *testing.T) {
	for i, c := range loadRuleVectors(t).Conditions {
		values := c.Values
		if values == nil {
			values = map[string]any{}
		}
		assert.Equal(t, c.Met, surfaceConditionMet(c.Cond, values), "condition case %d", i)
	}
}
