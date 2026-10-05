package ldap

// GitHub PR #90 review (0.52): an account from before migration 00084 that no
// label tells apart (no password here, no SSO identity: an empty
// auth_source) is still signed in by the directory that made it - a second
// directory, or a tenant's own, included. Labelled 'local' at the upgrade,
// only the main directory would sign it in and the others' people would lose
// their accounts.

import (
	"context"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// unlabelled is an account as migration 00084 leaves one made before it with
// no password here and no SSO identity: an empty auth_source.
func unlabelled(t *testing.T, store db.Store, email, hash string) *model.User {
	t.Helper()
	ctx := context.Background()
	u, err := store.CreateUser(ctx, email, hash, model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, store.SetUserAuthSource(ctx, u.ID, ""))
	u, err = store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, "", u.AuthSource)
	return u
}

func TestUpgrade_ASecondDirectorysAccountFromBeforeStillSignsIn(t *testing.T) {
	ctx := context.Background()
	partnerFC := &fakeConn{entries: []*goldap.Entry{person("cn=pat,dc=partner", "pat@partner.example")}, userPassword: "partner-pw"}
	mainFC := &fakeConn{entries: []*goldap.Entry{person("cn=pat,dc=corp", "pat@partner.example")}, userPassword: "main-pw"}
	main, partner, store := twoDirectories(t, mainFC, partnerFC, nil)
	before := unlabelled(t, store, "pat@partner.example", "")

	u, _, err := partner.Login(ctx, "pat@partner.example", "partner-pw")
	require.NoError(t, err, "the second directory's person keeps their account at the upgrade")
	assert.Equal(t, before.ID, u.ID)
	assert.Equal(t, model.AuthSourceLDAP, u.AuthSource)
	assert.Equal(t, "ldap-partner", u.AuthDirectory, "the directory that signed it in takes it")

	_, _, err = main.Login(ctx, "pat@partner.example", "main-pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "taken: the other directory no longer signs it in")

	// One with a password of its own here was made here: still only the main
	// directory's, label or not.
	unlabelled(t, store, "own@partner.example", "$2a$10$hash")
	partnerFC.entries = []*goldap.Entry{person("cn=own,dc=partner", "own@partner.example")}
	_, _, err = partner.Login(ctx, "own@partner.example", "partner-pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
}
