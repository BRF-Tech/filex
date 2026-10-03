package ldap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// ⚠⚠ Off by default: a person whose directory password is right but whom the
// first-login rule refuses gets the wrong-password answer, the same value.
// show_refusal_reason on: the reason travels (still ErrUnauthorized), and only
// after a right password.
func TestRefusalReason_OnlyWhenTheOperatorSaysAndOnlyAfterTheBind(t *testing.T) {
	ctx := context.Background()
	entry := groupEntry("cn=ayse,dc=example,dc=com", "ayse@example.com", "cn=Guests,dc=example,dc=com")

	off, _, _ := fixture(t, entry, map[string]any{"auto_create": false})
	_, _, refused := off.Login(ctx, "ayse@example.com", "pw")
	_, _, wrong := off.Login(ctx, "ayse@example.com", "not-pw")
	assert.Same(t, auth.ErrUnauthorized, refused, "off: the right password is not told apart")
	assert.Same(t, auth.ErrUnauthorized, wrong)

	on, _, _ := fixture(t, entry, map[string]any{"auto_create": false, "show_refusal_reason": "true"})
	_, _, err := on.Login(ctx, "ayse@example.com", "pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Equal(t, auth.SSOReasonAutoCreateOff, auth.SSOReason(err))
	_, _, err = on.Login(ctx, "ayse@example.com", "not-pw")
	assert.Same(t, auth.ErrUnauthorized, err, "a wrong password is never told anything")

	groups, _, _ := fixture(t, entry, map[string]any{"allowed_groups": "Staff", "show_refusal_reason": true})
	_, _, err = groups.Login(ctx, "ayse@example.com", "pw")
	assert.Equal(t, auth.SSOReasonGroupNotAllowed, auth.SSOReason(err))
	// The file protocols read it as the refusal it is.
	_, err = groups.VerifyPassword(ctx, "ayse@example.com", "pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
}
