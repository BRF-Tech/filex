//go:build linux

package pam

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// show_refusal_reason on: after PAM accepted the password the first-login
// rule's refusal says why. A system account refused before PAM is asked, and
// a wrong password, stay the one answer. Off (the default): the one answer.
func TestRefusalReason_AfterPAMSaidYesAndOnlyWhenSwitchedOn(t *testing.T) {
	ctx := context.Background()
	on := newRig(t, map[string]any{"show_refusal_reason": true})
	_, err := on.d.VerifyPassword(ctx, "alice", realPW)
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Equal(t, auth.SSOReasonAutoCreateOff, auth.SSOReason(err))
	_, err = on.d.VerifyPassword(ctx, "alice", "wrong")
	assert.Same(t, auth.ErrUnauthorized, err)
	_, err = on.d.VerifyPassword(ctx, "root", "root-pw")
	assert.Same(t, auth.ErrUnauthorized, err, "refused before the password: nothing to tell")

	groups := newRig(t, map[string]any{"auto_create": true, "allowed_groups": "STAFF", "show_refusal_reason": true})
	_, err = groups.d.VerifyPassword(ctx, "carol", tokPW)
	assert.Equal(t, auth.SSOReasonGroupNotAllowed, auth.SSOReason(err))

	off := newRig(t, nil)
	_, err = off.d.VerifyPassword(ctx, "alice", realPW)
	assert.Same(t, auth.ErrUnauthorized, err, "off: the wrong-password answer")
}
