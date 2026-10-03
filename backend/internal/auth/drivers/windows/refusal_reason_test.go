package windows

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// show_refusal_reason on: the refusals that come AFTER Windows accepted the
// password say why - the first-login rule, and a system account only the SID
// gave away. Refused by name (no password offered), a wrong password, or an
// account the operating system itself refused: the one answer, whatever the
// setting. Off (the default): the one answer for all of them.
func TestRefusalReason_AfterThePasswordAndOnlyWhenSwitchedOn(t *testing.T) {
	ctx := context.Background()
	on, _ := newDriver(t, people(), map[string]any{"show_refusal_reason": true})

	_, _, err := on.Login(ctx, "ayse", "pw-ayse")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Equal(t, auth.SSOReasonAutoCreateOff, auth.SSOReason(err))

	created, _ := newDriver(t, people(), map[string]any{"show_refusal_reason": true, "auto_create": true})
	_, _, err = created.Login(ctx, "bilgiislem", "pw-renamed")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Equal(t, auth.SSOReasonForbiddenAccount, auth.SSOReason(err), "the SID named it after the password")

	for _, c := range [][2]string{{"Administrator", "pw-admin"}, {"ayse", "wrong"}, {"kilitli", "pw-locked"}} {
		_, _, err = created.Login(ctx, c[0], c[1])
		assert.Same(t, auth.ErrUnauthorized, err, c[0])
	}

	off, _ := newDriver(t, people(), nil)
	_, _, err = off.Login(ctx, "ayse", "pw-ayse")
	assert.Same(t, auth.ErrUnauthorized, err, "off: the wrong-password answer")
}
