package share

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// The one PIN rule (pinrule.go): 4 to 12 characters, counted as characters.
func TestCheckPINLength(t *testing.T) {
	for pin, ok := range map[string]bool{
		"":              true, // no PIN is not this rule's business
		"123":           false,
		"1234":          true,
		"123456789012":  true,
		"1234567890123": false,
		"çşğüöı":        true, // six characters, twelve bytes
		"ççççççççççççç": false,
	} {
		err := CheckPINLength(pin)
		if ok {
			assert.NoError(t, err, "%q", pin)
		} else {
			assert.ErrorIs(t, err, ErrPINLength, "%q", pin)
			assert.True(t, IsPINLength(err))
		}
	}
	assert.Contains(t, ErrPINLength.Error(), "4-12", "the plugin host says the same bounds")
}

// ⚠ #210 (B7): POST /api/files/share took a PIN of any length while the
// visitor's PIN box stopped at 12 - a link made through the API with a longer
// PIN could not be opened from its own page. The rule is in Service.Create
// now, so every door that mints a link is held to it.
func TestCreate_RefusesAPINOutsideTheOneRule(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	svc := NewService(store)
	n := seedNode(t, store, "h-pin-rule-1")

	_, err := svc.Create(ctx, CreateOpts{NodeID: n.ID, PIN: "1234567890123"})
	require.ErrorIs(t, err, ErrPINLength)
	_, err = svc.Create(ctx, CreateOpts{NodeID: n.ID, PIN: "12"})
	require.ErrorIs(t, err, ErrPINLength)

	sh, err := svc.Create(ctx, CreateOpts{NodeID: n.ID, PIN: "123456789012"})
	require.NoError(t, err)
	assert.NotEmpty(t, sh.PinHash)
}
