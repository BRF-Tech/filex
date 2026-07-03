package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRulesEngine_DefaultMirror — empty rule set returns mirror.
func TestRulesEngine_DefaultMirror(t *testing.T) {
	e := DefaultRules()
	assert.Equal(t, ModeMirror, e.Match("anything/here.bin"))
}

// TestRulesEngine_PriorityOrder — priority asc, first match wins.
func TestRulesEngine_PriorityOrder(t *testing.T) {
	rules := []RuleSpec{
		{ID: 1, Pattern: "fileman/temp/*", Mode: ModeSkip, Priority: 10, Enabled: true},
		{ID: 2, Pattern: "*", Mode: ModeMirror, Priority: 999, Enabled: true},
		{ID: 3, Pattern: "fileman/temp/*", Mode: ModeMirror, Priority: 50, Enabled: true},
	}
	e := NewRulesEngine(func() ([]RuleSpec, ReplicaMode) { return rules, ModeMirror })

	// /temp/foo.tmp must hit rule 1 (priority 10) → skip
	assert.Equal(t, ModeSkip, e.Match("fileman/temp/foo.tmp"))
	// Anything else → catch-all rule 2 → mirror
	assert.Equal(t, ModeMirror, e.Match("fileman/data/x.bin"))
}

// TestRulesEngine_DisabledSkipped — disabled rules are ignored.
func TestRulesEngine_DisabledSkipped(t *testing.T) {
	rules := []RuleSpec{
		{ID: 1, Pattern: "secret/*", Mode: ModeAppendOnly, Priority: 1, Enabled: false},
		{ID: 2, Pattern: "secret/*", Mode: ModeMirror, Priority: 100, Enabled: true},
	}
	e := NewRulesEngine(func() ([]RuleSpec, ReplicaMode) { return rules, ModeMirror })
	assert.Equal(t, ModeMirror, e.Match("secret/data.txt"))
}

// TestRulesEngine_AppendOnly — sensitive paths use append_only.
func TestRulesEngine_AppendOnly(t *testing.T) {
	rules := []RuleSpec{
		{ID: 1, Pattern: "fileman/sensitive/*", Mode: ModeAppendOnly, Priority: 5, Enabled: true},
		{ID: 2, Pattern: "*", Mode: ModeMirror, Priority: 999, Enabled: true},
	}
	e := NewRulesEngine(func() ([]RuleSpec, ReplicaMode) { return rules, ModeMirror })
	assert.Equal(t, ModeAppendOnly, e.Match("fileman/sensitive/audit.log"))
	assert.Equal(t, ModeMirror, e.Match("fileman/data/x.bin"))
}

// TestRulesEngine_Reload — new rules take effect after Reload.
func TestRulesEngine_Reload(t *testing.T) {
	current := []RuleSpec{}
	e := NewRulesEngine(func() ([]RuleSpec, ReplicaMode) { return current, ModeMirror })

	// Initially empty → mirror.
	assert.Equal(t, ModeMirror, e.Match("foo"))

	// Replace and reload.
	current = []RuleSpec{
		{ID: 1, Pattern: "*", Mode: ModeSkip, Priority: 1, Enabled: true},
	}
	require := assert.New(t)
	rl, ok := e.(Reloader)
	require.True(ok)
	require.NoError(rl.Reload())
	assert.Equal(t, ModeSkip, e.Match("foo"))
}
