package dbsetting_test

// The text-shaped spec. Its distinguishing behaviour is Check: text has no
// bounds to clamp to, so the only thing standing between an operator's typo
// and a feature that silently does nothing is a validation hook that runs at
// SAVE time.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

// strSpec stands in for antivirus.AddrSetting: empty is legal ("not configured
// yet"), anything else has to look like host:port.
var strSpec = dbsetting.StringSpec{
	Key:     "test.addr",
	EnvVar:  "FILEX_TEST_ADDR",
	Default: "",
	Check: func(v string) error {
		if v == "" || strings.Contains(v, ":") {
			return nil
		}
		return errors.New("must be host:port")
	},
}

// modeSpec stands in for antivirus.ModeSetting: a closed set, case-folded.
var modeSpec = dbsetting.StringSpec{
	Key:       "test.mode",
	EnvVar:    "FILEX_TEST_MODE",
	Default:   "binary",
	Normalize: func(raw string) string { return strings.ToLower(strings.TrimSpace(raw)) },
	Check:     dbsetting.OneOf("binary", "daemon"),
}

func TestStringCanonical_NormalisesThenChecks(t *testing.T) {
	v, err := modeSpec.Canonical("  DAEMON ")
	require.NoError(t, err)
	assert.Equal(t, "daemon", v, "the stored text is the canonical text")

	_, err = modeSpec.Canonical("socket")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binary, daemon", "the error lists what is allowed")

	// Default trimming applies when no Normalize is given.
	v, err = strSpec.Canonical("  clamav:3310  ")
	require.NoError(t, err)
	assert.Equal(t, "clamav:3310", v)

	// ⚠ Validate and Canonical are the same gate, so the API cannot accept a
	// value the read path would then reject.
	assert.NoError(t, strSpec.Validate("clamav:3310"))
	assert.Error(t, strSpec.Validate("clamav 3310"))
}

func TestStringResolve_Order(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "binary", modeSpec.Resolve(ctx, nil), "no store → Default")

	m := newMem()
	assert.Equal(t, "binary", modeSpec.Resolve(ctx, m), "no row → Default")

	m.rows[modeSpec.Key] = "daemon"
	assert.Equal(t, "daemon", modeSpec.Resolve(ctx, m))

	// Case and whitespace in a hand-written row still resolve.
	m.rows[modeSpec.Key] = " Daemon "
	assert.Equal(t, "daemon", modeSpec.Resolve(ctx, m))

	// ⚠ A stored value that no longer passes Check falls back to Default and
	// logs — the string analogue of clamping on read. It happens with rows
	// written by hand, or written before Check grew stricter.
	m.rows[modeSpec.Key] = "carrier-pigeon"
	assert.Equal(t, "binary", modeSpec.Resolve(ctx, m))

	m.rows[modeSpec.Key] = "daemon"
	m.getErr = assertErr
	assert.Equal(t, "binary", modeSpec.Resolve(ctx, m), "a broken store is not intent")
}

func TestStringSeed_ConsumedOnceAndOnlyWhenEmpty(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FILEX_TEST_ADDR", "clamav:3310")

	m := newMem()
	require.NoError(t, strSpec.Seed(ctx, m))
	assert.Equal(t, "clamav:3310", m.rows[strSpec.Key])
	assert.Equal(t, 1, m.writes)

	t.Setenv("FILEX_TEST_ADDR", "somewhere-else:3310")
	require.NoError(t, strSpec.Seed(ctx, m))
	assert.Equal(t, "clamav:3310", m.rows[strSpec.Key], "the row wins over the variable")
	assert.Equal(t, 1, m.writes)
}

// ⚠ Unlike IntSpec, which clamps an out-of-range seed to the nearest bound, an
// unusable string seed is ignored entirely: there is no nearest legal string,
// so the choice is between storing nonsense and leaving Default in force with
// a log line naming the variable.
func TestStringSeed_UnusableSeedIsIgnoredNotStored(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FILEX_TEST_ADDR", "clamav 3310")
	m := newMem()
	require.NoError(t, strSpec.Seed(ctx, m))
	assert.Equal(t, 0, m.writes)
	assert.Equal(t, "", strSpec.Resolve(ctx, m))
}

func TestStringSeedValue_DerivedSeedRespectsFirstBootOnly(t *testing.T) {
	ctx := context.Background()
	m := newMem()

	// A derived first value (e.g. "an address was configured, so the mode is
	// daemon") is seeded like any other.
	require.NoError(t, modeSpec.SeedValue(ctx, m, "daemon"))
	assert.Equal(t, "daemon", m.rows[modeSpec.Key])

	// ⚠⚠ And it can never overwrite a choice an admin has since made. This is
	// the reason SeedValue exists instead of callers calling UpsertSetting.
	m.rows[modeSpec.Key] = "binary"
	require.NoError(t, modeSpec.SeedValue(ctx, m, "daemon"))
	assert.Equal(t, "binary", m.rows[modeSpec.Key])
}

func TestOneOf(t *testing.T) {
	check := dbsetting.OneOf("a", "b")
	assert.NoError(t, check("a"))
	assert.NoError(t, check("b"))
	assert.Error(t, check("c"))
	assert.Error(t, check(""))
}
