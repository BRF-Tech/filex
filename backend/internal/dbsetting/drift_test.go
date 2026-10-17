package dbsetting_test

// The variable that no longer does anything (task #258): a start names, once,
// every seeded variable its stored row overrules - and nothing else. The
// comparison is the seed's own reading of the variable, so a spelling or a
// clamp that stores the same value is not a drift.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

var (
	driftInt  = dbsetting.IntSpec{Key: "test.drift_minutes", EnvVar: "FILEX_TEST_DRIFT_MINUTES", Default: 30, Min: 2, Max: 60}
	driftBool = dbsetting.BoolSpec{Key: "test.drift_switch", EnvVar: "FILEX_TEST_DRIFT_SWITCH", Default: true}
	driftText = dbsetting.StringSpec{Key: "test.drift_mode", EnvVar: "FILEX_TEST_DRIFT_MODE", Default: "off", Check: dbsetting.OneOf("off", "daemon", "binary")}
)

func TestDrift_AVariableTheStoredRowOverrulesIsNamed(t *testing.T) {
	t.Setenv(driftInt.EnvVar, "45")
	t.Setenv(driftBool.EnvVar, "off")
	t.Setenv(driftText.EnvVar, "daemon")
	m := newMem()
	m.rows[driftInt.Key] = "20"
	m.rows[driftBool.Key] = "true"
	m.rows[driftText.Key] = "binary"

	got := dbsetting.WarnDrift(context.Background(), m, driftInt, driftBool, driftText)

	assert.Equal(t, []dbsetting.Drift{
		{Key: driftInt.Key, Source: driftInt.EnvVar},
		{Key: driftBool.Key, Source: driftBool.EnvVar},
		{Key: driftText.Key, Source: driftText.EnvVar},
	}, got)
	assert.Equal(t, 0, m.writes, "naming a drift writes nothing")
}

func TestDrift_TheSameValueInAnotherSpellingIsNotOne(t *testing.T) {
	t.Setenv(driftInt.EnvVar, "999") // clamps to Max, as the seed would store it
	t.Setenv(driftBool.EnvVar, "yes")
	t.Setenv(driftText.EnvVar, " daemon ")
	m := newMem()
	m.rows[driftInt.Key] = "60"
	m.rows[driftBool.Key] = "true"
	m.rows[driftText.Key] = "daemon"

	assert.Empty(t, dbsetting.DriftOf(context.Background(), m, driftInt, driftBool, driftText))
}

func TestDrift_NothingStoredOrNothingSetOrNothingUsableIsNotOne(t *testing.T) {
	ctx := context.Background()

	t.Setenv(driftInt.EnvVar, "45")
	assert.Empty(t, dbsetting.DriftOf(ctx, newMem(), driftInt), "no row: the seed writes it, nothing overrules it")

	m := newMem()
	m.rows[driftBool.Key] = "false"
	assert.Empty(t, dbsetting.DriftOf(ctx, m, driftBool), "the variable is not set")

	t.Setenv(driftText.EnvVar, "sometimes")
	m.rows[driftText.Key] = "daemon"
	assert.Empty(t, dbsetting.DriftOf(ctx, m, driftText), "a value the seed would refuse is the seed's log line, not a drift")
}

func TestDrift_ARowWrittenByThisStartsSeedIsNotOne(t *testing.T) {
	t.Setenv(driftInt.EnvVar, "45")
	m := newMem()
	ctx := context.Background()
	dbsetting.SeedAll(ctx, m, driftInt)
	assert.Equal(t, "45", m.rows[driftInt.Key])
	assert.Empty(t, dbsetting.WarnDrift(ctx, m, driftInt), "SeedAll then WarnDrift: the boot's order")
}

func TestDrifted(t *testing.T) {
	assert.True(t, dbsetting.Drifted("45", "20"))
	assert.False(t, dbsetting.Drifted("45", "45"))
	assert.False(t, dbsetting.Drifted("45", ""), "an empty row holds nothing to win with")
	assert.False(t, dbsetting.Drifted("45", "  "))
}
