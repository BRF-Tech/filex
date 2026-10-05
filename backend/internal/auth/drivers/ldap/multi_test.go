package ldap

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

// Several directories (docs/LDAP.md → Several directories): two drivers on
// one store, "ldap" and "ldap-partner", each with its own fake directory.
func twoDirectories(t *testing.T, main, partner *fakeConn, partnerCfg map[string]any) (*Driver, *Driver, db.Store) {
	t.Helper()
	store := newStore(t)
	mk := func(fc *fakeConn, cfg map[string]any) *Driver {
		d := New(store)
		base := map[string]any{"url": "ldaps://directory.invalid", "base_dn": "dc=example,dc=com"}
		for k, v := range cfg {
			base[k] = v
		}
		require.NoError(t, d.Init(context.Background(), base))
		d.dial = func(context.Context) (conn, error) { return fc, nil }
		return d
	}
	pc := map[string]any{"directory": "ldap-partner", "label": "Partner AD"}
	for k, v := range partnerCfg {
		pc[k] = v
	}
	return mk(main, nil), mk(partner, pc), store
}

// An account belongs to the directory that made it: the other one's
// matching entry and password do not sign it in.
func TestDirectories_AnAccountIsItsDirectorys(t *testing.T) {
	ctx := context.Background()
	mainFC := &fakeConn{entries: []*goldap.Entry{person("cn=ceo,dc=corp", "ceo@corp.example")}, userPassword: "main-pw"}
	partnerFC := &fakeConn{entries: []*goldap.Entry{person("cn=ceo,dc=partner", "ceo@corp.example")}, userPassword: "partner-pw"}
	main, partner, store := twoDirectories(t, mainFC, partnerFC, nil)
	assert.Equal(t, "ldap-partner", partner.Directory())

	u, _, err := main.Login(ctx, "ceo@corp.example", "main-pw")
	require.NoError(t, err)
	assert.Equal(t, model.MainDirectory, u.AuthDirectory)

	_, _, err = partner.Login(ctx, "ceo@corp.example", "partner-pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "the partner directory cannot sign in the main directory's account")
	_, err = partner.VerifyPassword(ctx, "ceo@corp.example", "partner-pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)

	// An account made here: only the main directory, as before there were several.
	_, err = store.CreateUser(ctx, "local@corp.example", "$2a$10$hash", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	partnerFC.entries = []*goldap.Entry{person("cn=local,dc=partner", "local@corp.example")}
	_, _, err = partner.Login(ctx, "local@corp.example", "partner-pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)

	// Its own people it signs in, and says so on the account.
	partnerFC.entries = []*goldap.Entry{person("cn=pat,dc=partner", "pat@partner.example")}
	pat, _, err := partner.Login(ctx, "pat@partner.example", "partner-pw")
	require.NoError(t, err)
	assert.Equal(t, "ldap-partner", pat.AuthDirectory)
	assert.Equal(t, model.AuthSourceLDAP, pat.AuthSource)
}

// email_domains: a directory signs in and makes accounts only for its own.
func TestDirectories_EmailDomains(t *testing.T) {
	ctx := context.Background()
	partnerFC := &fakeConn{entries: []*goldap.Entry{person("cn=x,dc=partner", "boss@corp.example")}, userPassword: "pw"}
	_, partner, store := twoDirectories(t, &fakeConn{}, partnerFC, map[string]any{"email_domains": "partner.example, @partner.test"})
	assert.Equal(t, []string{"partner.example", "partner.test"}, partner.emailDomains)

	_, _, err := partner.Login(ctx, "boss@corp.example", "pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	_, err = store.GetUserByEmail(ctx, "boss@corp.example")
	assert.Error(t, err, "no account was made for an address outside its domains")

	partnerFC.entries = []*goldap.Entry{person("cn=pat,dc=partner", "pat@partner.test")}
	_, _, err = partner.Login(ctx, "pat@partner.test", "pw")
	require.NoError(t, err)
}

// Each directory's sync counts only its own accounts as no longer listed,
// and flags only its own groups as removed.
func TestDirectories_SyncKeepsToItsOwn(t *testing.T) {
	ctx := context.Background()
	mainFC := &fakeConn{
		entries: []*goldap.Entry{person("cn=ada,dc=corp", "ada@corp.example", "cn=staff,ou=groups,dc=corp")},
		groups:  []*goldap.Entry{dirGroup("u-staff", "staff")},
	}
	partnerFC := &fakeConn{
		entries: []*goldap.Entry{person("cn=pat,dc=partner", "pat@partner.example", "cn=guests,ou=groups,dc=partner")},
		groups:  []*goldap.Entry{dirGroup("u-guests", "guests")},
	}
	main, partner, store := twoDirectories(t, mainFC, partnerFC, nil)
	_, err := main.SyncDirectory(ctx)
	require.NoError(t, err)
	rep, err := partner.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ldap-partner", rep.Provider)
	assert.Zero(t, rep.Missing, "the main directory's people are not the partner's to miss")

	staff, guests := groupNamed(t, store, "staff"), groupNamed(t, store, "guests")
	require.NotNil(t, staff)
	require.NotNil(t, guests)
	assert.Equal(t, "ldap:u-staff", staff.DirectoryID)
	assert.Equal(t, "ldap-partner:u-guests", guests.DirectoryID)

	// The main directory again: the partner's group is not its to flag.
	rep, err = main.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Zero(t, rep.GroupsRemoved)
	assert.Zero(t, rep.Missing)
	got, err := store.GetGroup(ctx, guests.ID)
	require.NoError(t, err)
	assert.Empty(t, got.DirectoryState)

	// Pat's account is the partner's; the main directory lists a Pat too:
	// skipped and said, not taken.
	mainFC.entries = append(mainFC.entries, person("cn=pat,dc=corp", "pat@partner.example"))
	rep, err = main.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Skipped)
	require.NotEmpty(t, rep.Problems)
	assert.Contains(t, rep.Problems[0], "ldap-partner")
	pat, err := store.GetUserByEmail(ctx, "pat@partner.example")
	require.NoError(t, err)
	assert.Equal(t, "ldap-partner", pat.AuthDirectory)
}

// An LDAP account from before users.auth_directory is the main directory's.
func TestDirectories_OlderAccountsAreTheMainDirectorys(t *testing.T) {
	u := &model.User{AuthSource: model.AuthSourceLDAP}
	assert.Equal(t, model.MainDirectory, u.DirectoryOwner())
	assert.Empty(t, (&model.User{AuthSource: model.AuthSourceLocal}).DirectoryOwner())
	assert.Equal(t, "ldap-partner", (&model.User{AuthSource: model.AuthSourceLDAP, AuthDirectory: "ldap-partner"}).DirectoryOwner())
}
