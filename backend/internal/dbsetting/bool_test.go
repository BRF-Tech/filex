package dbsetting_test

// The switch-shaped spec, pinned against the same four behaviours as IntSpec.
// The one that differs is what happens to text nobody can parse: a number
// clamps to the nearest bound, a switch has no nearest bound and falls back to
// its default rather than being guessed at.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

var boolSpec = dbsetting.BoolSpec{
	Key:     "test.enabled",
	EnvVar:  "FILEX_TEST_ENABLED",
	Default: true,
}

func TestParseBool_SpellingsAnOperatorTypes(t *testing.T) {
	for _, raw := range []string{"1", "true", "TRUE", "True", " t ", "yes", "Y", "on", "ON"} {
		v, ok := dbsetting.ParseBool(raw)
		assert.True(t, ok, raw)
		assert.True(t, v, raw)
	}
	for _, raw := range []string{"0", "false", "FALSE", "f", "no", "N", "off", "Off"} {
		v, ok := dbsetting.ParseBool(raw)
		assert.True(t, ok, raw)
		assert.False(t, v, raw)
	}
	// ⚠ Refused rather than guessed. "enabled" reads as true to a human and
	// would have to be guessed at here; a guess that points a kill-switch the
	// wrong way is worse than a log line saying the value was ignored. The
	// empty string is "not set", which is a different thing from false.
	for _, raw := range []string{"", "enabled", "disable", "2", "maybe", "sure"} {
		_, ok := dbsetting.ParseBool(raw)
		assert.False(t, ok, "should refuse %q", raw)
	}
	assert.Equal(t, "true", dbsetting.FormatBool(true))
	assert.Equal(t, "false", dbsetting.FormatBool(false))
}

func TestBoolResolve_Order(t *testing.T) {
	ctx := context.Background()
	assert.True(t, boolSpec.Resolve(ctx, nil), "no store at all → Default")

	m := newMem()
	assert.True(t, boolSpec.Resolve(ctx, m), "no row → Default")

	m.rows[boolSpec.Key] = "false"
	assert.False(t, boolSpec.Resolve(ctx, m), "the row is what is in force")

	m.rows[boolSpec.Key] = "off"
	assert.False(t, boolSpec.Resolve(ctx, m), "any accepted spelling of false")

	// Unreadable row → Default, never a guess.
	m.rows[boolSpec.Key] = "sometimes"
	assert.True(t, boolSpec.Resolve(ctx, m))

	// A store that errors is not intent either.
	m.rows[boolSpec.Key] = "false"
	m.getErr = assertErr
	assert.True(t, boolSpec.Resolve(ctx, m))
}

var assertErr = errTest("store is down")

type errTest string

func (e errTest) Error() string { return string(e) }

func TestBoolSeed_ConsumedOnceAndOnlyWhenEmpty(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FILEX_TEST_ENABLED", "0")

	m := newMem()
	require.NoError(t, boolSpec.Seed(ctx, m))
	assert.Equal(t, "false", m.rows[boolSpec.Key])
	assert.Equal(t, 1, m.writes)

	// ⚠⚠ The seed is inert once a row exists. This is the behaviour every doc
	// row describing one of these variables has to state, because editing the
	// variable in compose and restarting looks like it should work and does
	// nothing.
	t.Setenv("FILEX_TEST_ENABLED", "1")
	require.NoError(t, boolSpec.Seed(ctx, m))
	assert.Equal(t, "false", m.rows[boolSpec.Key], "the row wins over the variable")
	assert.Equal(t, 1, m.writes, "no second write")
}

func TestBoolSeed_GarbageIsIgnored(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FILEX_TEST_ENABLED", "perhaps")
	m := newMem()
	require.NoError(t, boolSpec.Seed(ctx, m))
	assert.Empty(t, m.rows[boolSpec.Key], "nothing honest to store")
	assert.True(t, boolSpec.Resolve(ctx, m), "so Default is in force")
}

func TestBoolSeed_NoEnvIsNoOp(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FILEX_TEST_ENABLED", "")
	m := newMem()
	require.NoError(t, boolSpec.Seed(ctx, m))
	assert.Equal(t, 0, m.writes)
}

// SeedAll takes a mixed list — that is the whole reason Seeder exists, and it
// is what lets one line at boot cover the whole antivirus family.
func TestSeedAll_MixedKinds(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FILEX_TEST_ENABLED", "no")
	t.Setenv("FILEX_TEST_WINDOW_MINUTES", "45")
	t.Setenv("FILEX_TEST_ADDR", "clamav:3310")
	m := newMem()

	dbsetting.SeedAll(ctx, m, boolSpec, spec, strSpec)

	assert.Equal(t, "false", m.rows[boolSpec.Key])
	assert.Equal(t, "45", m.rows[spec.Key])
	assert.Equal(t, "clamav:3310", m.rows[strSpec.Key])
	assert.Equal(t, 3, m.writes)
}
