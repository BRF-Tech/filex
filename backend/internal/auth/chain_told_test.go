package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// A refusal a provider may tell (show_refusal_reason, auth.RefusedAfterPassword)
// is what the chain answers when nobody signs the person in - ahead of a driver
// that could not judge - and a later success still wins.
func TestChain_ATellableRefusalIsTheAnswer(t *testing.T) {
	ctx := context.Background()
	told := auth.RefusedAfterPassword(true, &auth.FirstLoginRefusal{Reason: auth.ReasonGroupNotAllowed})

	local := newStub("local", "other")
	dir := newStub("ldap", "x")
	dir.failWith = told
	down := newStub("ldap2", "x")
	down.failWith = errors.New("ldap: dial: connection refused")
	_, _, err := auth.NewLoginChain(local, dir, down).Login(ctx, "alex", "pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Equal(t, auth.SSOReasonGroupNotAllowed, auth.SSOReason(err))

	ok := newStub("pam", "pw")
	u, _, err := auth.NewLoginChain(local, dir, ok).Login(ctx, "alex", "pw")
	assert.NoError(t, err)
	assert.NotNil(t, u, "a later provider that signs the person in wins")

	// Off, the same refusal is the plain answer: nothing to tell.
	dir.failWith = auth.RefusedAfterPassword(false, &auth.FirstLoginRefusal{Reason: auth.ReasonGroupNotAllowed})
	_, _, err = auth.NewLoginChain(local, dir).Login(ctx, "alex", "pw")
	assert.Same(t, auth.ErrUnauthorized, err)
}
