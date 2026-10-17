package dbsetting_test

// SeedValue: the first value of a setting from a value the caller holds (a
// config.yaml key), under the same first-boot-only rule as Seed.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntSeedValue_FirstBootOnlyAndClamped(t *testing.T) {
	ctx := context.Background()
	m := newMem()
	require.NoError(t, spec.SeedValue(ctx, m, 45, "config.yaml test.window"))
	assert.Equal(t, 45, spec.Resolve(ctx, m))

	// A row exists: nothing a seed holds may overwrite it.
	m.rows[spec.Key] = "12"
	require.NoError(t, spec.SeedValue(ctx, m, 50, "config.yaml test.window"))
	assert.Equal(t, 12, spec.Resolve(ctx, m), "a seed overwrote a saved value")

	// Out of range clamps, as the environment's seed does.
	m2 := newMem()
	require.NoError(t, spec.SeedValue(ctx, m2, 999, "config.yaml test.window"))
	assert.Equal(t, "60", m2.rows[spec.Key])

	// No store, no write, no panic.
	require.NoError(t, spec.SeedValue(ctx, nil, 45, "x"))
}

func TestBoolSeedValue_FirstBootOnly(t *testing.T) {
	ctx := context.Background()
	m := newMem()
	require.NoError(t, boolSpec.SeedValue(ctx, m, false, "config.yaml test.enabled"))
	assert.Equal(t, "false", m.rows[boolSpec.Key])
	assert.False(t, boolSpec.Resolve(ctx, m))

	m.rows[boolSpec.Key] = "true"
	require.NoError(t, boolSpec.SeedValue(ctx, m, false, "config.yaml test.enabled"))
	assert.True(t, boolSpec.Resolve(ctx, m), "a seed overwrote a saved switch")
}

func TestSeedValue_AStoreThatCannotWriteSaysSo(t *testing.T) {
	ctx := context.Background()
	m := newMem()
	m.setErr = assertErr
	assert.Error(t, spec.SeedValue(ctx, m, 45, "x"))
	assert.Error(t, boolSpec.SeedValue(ctx, m, false, "x"))
}
