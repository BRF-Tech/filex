package authsetup

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

type fakeDir struct {
	user *model.User
	err  error
	n    int
}

func (f *fakeDir) VerifyPassword(context.Context, string, string) (*model.User, error) {
	f.n++
	return f.user, f.err
}

func TestDirectoryOf(t *testing.T) {
	assert.Nil(t, directoryOf(nil))
	one := &fakeDir{}
	assert.Same(t, one, directoryOf([]Directory{one}))
	_, isChain := directoryOf([]Directory{one, &fakeDir{}}).(directoryChain)
	assert.True(t, isChain)
}

func TestDirectoryChain(t *testing.T) {
	ctx := context.Background()
	ann := &model.User{ID: 7}
	down := errors.New("directory unreachable")

	// The second directory vouches after the first said no.
	no, yes := &fakeDir{err: auth.ErrUnauthorized}, &fakeDir{user: ann}
	u, err := directoryOf([]Directory{no, yes}).VerifyPassword(ctx, "ann", "pw")
	require.NoError(t, err)
	assert.Equal(t, ann, u)

	// The first to succeed wins; later ones are not asked.
	first, later := &fakeDir{user: ann}, &fakeDir{user: &model.User{ID: 9}}
	u, _ = directoryOf([]Directory{first, later}).VerifyPassword(ctx, "ann", "pw")
	assert.Equal(t, int64(7), u.ID)
	assert.Zero(t, later.n)

	// Everybody says no: no.
	_, err = directoryOf([]Directory{&fakeDir{err: auth.ErrUnauthorized}, &fakeDir{err: auth.ErrUnauthorized}}).VerifyPassword(ctx, "ann", "pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)

	// One is down and the other says no: the operator hears about the one that is down.
	_, err = directoryOf([]Directory{&fakeDir{err: down}, &fakeDir{err: auth.ErrUnauthorized}}).VerifyPassword(ctx, "ann", "pw")
	assert.ErrorIs(t, err, down)
	assert.NotErrorIs(t, err, auth.ErrUnauthorized)

	// One is down and the other vouches: signed in.
	u, err = directoryOf([]Directory{&fakeDir{err: down}, &fakeDir{user: ann}}).VerifyPassword(ctx, "ann", "pw")
	require.NoError(t, err)
	assert.Equal(t, ann, u)
}

func TestPamIsManagedAndConfigured(t *testing.T) {
	assert.True(t, IsManaged("pam"))
	assert.Contains(t, Managed, "pam")

	cfg, directory, err := DriverConfig(&Stored{Name: "pam", Values: map[string]string{}}, nil, Options{LoginEmailToken: "evim", MultiTenant: true})
	require.NoError(t, err)
	assert.True(t, directory, "protocol_login defaults to on")
	assert.Equal(t, false, cfg["auto_create"], "an operating-system login does not open accounts by default")
	assert.Equal(t, "evim", cfg["email_token"], "the installation's token, never a page field")
	assert.Equal(t, true, cfg["multi_tenant"])
	_, ok := FieldOf("pam", "email_token")
	assert.False(t, ok, "the e-mail token is not editable on the page")

	cfg, directory, _ = DriverConfig(&Stored{Name: "pam", Values: map[string]string{"protocol_login": "false", "auto_create": "true", "email_token": "hacked"}}, nil, Options{})
	assert.False(t, directory)
	assert.Equal(t, true, cfg["auto_create"])
	assert.NotContains(t, cfg, "email_token", "an email_token row on the page is not a thing")
}
