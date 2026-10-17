package dbsetting_test

// The four behaviours of a DB-backed, env-seeded setting, each pinned so the
// next setting that copies this shape cannot quietly diverge: resolution
// order, clamping on read, refusal on write, and the seed being consumed
// exactly once.

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

// memStore is a settings table in a map.
type memStore struct {
	rows    map[string]string
	getErr  error
	setErr  error
	writes  int
	lastKey string
}

func newMem() *memStore { return &memStore{rows: map[string]string{}} }

func (m *memStore) GetSetting(_ context.Context, key string) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	return m.rows[key], nil
}

func (m *memStore) UpsertSetting(_ context.Context, key, value string) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.writes++
	m.lastKey = key
	m.rows[key] = value
	return nil
}

var spec = dbsetting.IntSpec{
	Key:     "test.window_minutes",
	EnvVar:  "FILEX_TEST_WINDOW_MINUTES",
	Default: 30,
	Min:     2,
	Max:     60,
	Unit:    "minutes",
}

func TestResolve_DefaultWhenUnset(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, 30, spec.Resolve(ctx, newMem()), "no row → default")
	assert.Equal(t, 30, spec.Resolve(ctx, nil), "no store → default")

	blank := newMem()
	blank.rows[spec.Key] = ""
	assert.Equal(t, 30, spec.Resolve(ctx, blank), "blank row → default")

	broken := newMem()
	broken.getErr = errors.New("db down")
	assert.Equal(t, 30, spec.Resolve(ctx, broken), "store error → default")
}

func TestResolve_ExplicitValueHonoured(t *testing.T) {
	m := newMem()
	m.rows[spec.Key] = "7"
	assert.Equal(t, 7, spec.Resolve(context.Background(), m))
}

// Out of range clamps to the nearest bound — the operator's intent ("as short
// as you allow" / "as long as you allow") is honoured, the impossible number
// is not. Unparseable text has no intent to honour, so it falls to the default.
func TestResolve_ClampsAndFallsBack(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		stored string
		want   int
		why    string
	}{
		{"0", 2, "zero is not a value; clamps up to the floor"},
		{"1", 2, "below the floor"},
		{"-5", 2, "negative"},
		{"61", 60, "above the ceiling"},
		{"10080", 60, "a week clamps to the ceiling"},
		{"2", 2, "the floor itself is legal"},
		{"60", 60, "the ceiling itself is legal"},
		{"not-a-number", 30, "garbage carries no intent → default"},
		{"30m", 30, "a duration string is not minutes → default"},
	} {
		m := newMem()
		m.rows[spec.Key] = tc.stored
		assert.Equal(t, tc.want, spec.Resolve(ctx, m), "%s (%s)", tc.stored, tc.why)
	}
}

// The write side refuses rather than clamps: an operator typing 5 seconds is
// told no while they are looking at the field.
func TestValidate_RefusesOutOfRange(t *testing.T) {
	require.NoError(t, spec.Validate(2))
	require.NoError(t, spec.Validate(30))
	require.NoError(t, spec.Validate(60))

	for _, v := range []int{0, 1, -1, 61, 1440} {
		err := spec.Validate(v)
		require.Error(t, err, "value %d must be refused", v)
		assert.Contains(t, err.Error(), "between 2 and 60 minutes")
	}
}

// ⚠ The env var is a SEED, not an override. This is the test that pins it:
// the second boot, with a different env value, must change nothing.
func TestSeed_OnlyWhenNoRowExists(t *testing.T) {
	ctx := context.Background()
	m := newMem()

	t.Setenv(spec.EnvVar, "45")
	require.NoError(t, spec.Seed(ctx, m))
	assert.Equal(t, "45", m.rows[spec.Key])
	assert.Equal(t, 1, m.writes)

	// Second boot, operator has since changed the value in the UI.
	require.NoError(t, m.UpsertSetting(ctx, spec.Key, "12"))
	writesBefore := m.writes

	t.Setenv(spec.EnvVar, "59")
	require.NoError(t, spec.Seed(ctx, m))
	assert.Equal(t, "12", m.rows[spec.Key], "a stored row wins; the env var is inert")
	assert.Equal(t, writesBefore, m.writes, "seeding must not write when a row exists")
	assert.Equal(t, 12, spec.Resolve(ctx, m))
}

func TestSeed_NoEnvIsNoOp(t *testing.T) {
	ctx := context.Background()
	m := newMem()
	t.Setenv(spec.EnvVar, "")
	require.NoError(t, spec.Seed(ctx, m))
	assert.Empty(t, m.rows)
	assert.Equal(t, 30, spec.Resolve(ctx, m))
}

// A seed outside the bounds is clamped and stored clamped, so the value the
// admin page shows is the value in force. Garbage is ignored entirely — there
// is no honest number to store.
func TestSeed_ClampsAndIgnoresGarbage(t *testing.T) {
	ctx := context.Background()

	m := newMem()
	t.Setenv(spec.EnvVar, "10080")
	require.NoError(t, spec.Seed(ctx, m))
	assert.Equal(t, "60", m.rows[spec.Key])

	m2 := newMem()
	t.Setenv(spec.EnvVar, "0")
	require.NoError(t, spec.Seed(ctx, m2))
	assert.Equal(t, "2", m2.rows[spec.Key])

	m3 := newMem()
	t.Setenv(spec.EnvVar, "half an hour")
	require.NoError(t, spec.Seed(ctx, m3))
	assert.Empty(t, m3.rows, "unparseable seed stores nothing")
	assert.Equal(t, 30, spec.Resolve(ctx, m3))
}

func TestSeed_WriteFailureIsReported(t *testing.T) {
	m := newMem()
	m.setErr = errors.New("disk full")
	t.Setenv(spec.EnvVar, "40")
	require.Error(t, spec.Seed(context.Background(), m))
}

func TestSeedAll_SurvivesAFailure(t *testing.T) {
	m := newMem()
	m.setErr = errors.New("disk full")
	t.Setenv(spec.EnvVar, "40")
	// Must not panic; boot continues with the default in force.
	dbsetting.SeedAll(context.Background(), m, spec)
	assert.Equal(t, 30, spec.Resolve(context.Background(), m))
}

// The one resolution order, named and asserted for each of the three kinds
// (task #258 moved a dozen environment-only settings onto it): the stored row,
// else the environment's value on a start with no row, else the default.
func TestResolutionOrder_DbThenEnvThenDefault(t *testing.T) {
	ctx := context.Background()
	intSpec := spec
	boolSpec := dbsetting.BoolSpec{Key: "test.switch", EnvVar: "FILEX_TEST_SWITCH", Default: true}
	strSpec := dbsetting.StringSpec{Key: "test.lang", EnvVar: "FILEX_TEST_LANG", Default: "en"}

	// 3. Nothing anywhere: the default.
	m := newMem()
	dbsetting.SeedAll(ctx, m, intSpec, boolSpec, strSpec)
	assert.Equal(t, 30, intSpec.Resolve(ctx, m))
	assert.True(t, boolSpec.Resolve(ctx, m))
	assert.Equal(t, "en", strSpec.Resolve(ctx, m))

	// 2. The environment, on a start with no row.
	t.Setenv(intSpec.EnvVar, "10")
	t.Setenv(boolSpec.EnvVar, "off")
	t.Setenv(strSpec.EnvVar, "tr")
	m = newMem()
	dbsetting.SeedAll(ctx, m, intSpec, boolSpec, strSpec)
	assert.Equal(t, 10, intSpec.Resolve(ctx, m))
	assert.False(t, boolSpec.Resolve(ctx, m))
	assert.Equal(t, "tr", strSpec.Resolve(ctx, m))

	// 1. The row, whatever the environment says on a later start.
	require.NoError(t, m.UpsertSetting(ctx, intSpec.Key, "50"))
	require.NoError(t, m.UpsertSetting(ctx, boolSpec.Key, "true"))
	require.NoError(t, m.UpsertSetting(ctx, strSpec.Key, "de"))
	dbsetting.SeedAll(ctx, m, intSpec, boolSpec, strSpec)
	assert.Equal(t, 50, intSpec.Resolve(ctx, m))
	assert.True(t, boolSpec.Resolve(ctx, m))
	assert.Equal(t, "de", strSpec.Resolve(ctx, m))
}

// Reseed is the explicit way back to the environment: the variable's value
// over the row, for a variable that is set; nothing else is touched, and a
// value the seed would refuse leaves the row as it was.
func TestReseed_TheEnvironmentOverTheRowOnRequest(t *testing.T) {
	ctx := context.Background()
	m := newMem()
	other := dbsetting.IntSpec{Key: "test.other", EnvVar: "FILEX_TEST_OTHER", Default: 5, Min: 1, Max: 9}
	require.NoError(t, m.UpsertSetting(ctx, spec.Key, "12"))
	require.NoError(t, m.UpsertSetting(ctx, other.Key, "7"))

	t.Setenv(spec.EnvVar, "45")
	done := dbsetting.Reseed(ctx, m, nil, spec, other)
	assert.Equal(t, []string{spec.Key}, done)
	assert.Equal(t, "45", m.rows[spec.Key])
	assert.Equal(t, "7", m.rows[other.Key], "no variable: the row stands")

	// Narrowed to one key.
	t.Setenv(other.EnvVar, "3")
	t.Setenv(spec.EnvVar, "50")
	done = dbsetting.Reseed(ctx, m, []string{other.Key}, spec, other)
	assert.Equal(t, []string{other.Key}, done)
	assert.Equal(t, "45", m.rows[spec.Key])
	assert.Equal(t, "3", m.rows[other.Key])

	// A value the seed refuses leaves the row as it was.
	t.Setenv(spec.EnvVar, "lots")
	done = dbsetting.Reseed(ctx, m, nil, spec)
	assert.Empty(t, done)
	assert.Equal(t, "45", m.rows[spec.Key])
}
