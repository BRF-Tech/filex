package auth

// AdoptAccount: the account an older sign-in path keyed by a bare login name is
// taken over — re-keyed to the address the e-mail rule gives it, or (bound to
// SSO) signed in to as it is — within the sign-in's tenant only, and never into
// a second account. Nobody is refused by it.

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func adoptRows(t *testing.T, store db.Store, action string) []*model.AuditEntry {
	t.Helper()
	rows, err := store.ListAuditRecent(context.Background(), 100)
	require.NoError(t, err)
	var out []*model.AuditEntry
	for _, r := range rows {
		if r.Action == action {
			out = append(out, r)
		}
	}
	return out
}

func metaKeys(m map[string]any) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// alarmSpy records what the platform operator is told, and restores the
// registration when the test ends.
type alarmSpy struct {
	mu   sync.Mutex
	told []LegacyAccountElsewhere
}

func spyAlarm(t *testing.T) *alarmSpy {
	t.Helper()
	s := &alarmSpy{}
	SetLegacyAccountAlarm(func(_ context.Context, e LegacyAccountElsewhere) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.told = append(s.told, e)
	})
	t.Cleanup(func() { SetLegacyAccountAlarm(nil) })
	return s
}

func (s *alarmSpy) calls() []LegacyAccountElsewhere {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LegacyAccountElsewhere(nil), s.told...)
}

func TestAdoptAccount_ReKeysTheOlderAccountInPlace(t *testing.T) {
	ctx := WithLoginHost(context.Background(), "files.example.test")
	_, store := dbtest.NewTestDB(t)
	old, err := store.CreateUser(ctx, "alex", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.UpdateUserRole(ctx, old.ID, model.RoleAdmin))

	u, err := AdoptAccount(ctx, store, Adoption{Driver: "ldap", LoginName: " Alex ", Email: "Alex@Local"})
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, old.ID, u.ID, "the same row: files, shares, permissions and role stay where they are")
	assert.Equal(t, "alex@local", u.Email)
	assert.Equal(t, model.RoleAdmin, u.Role)
	_, err = store.GetUserByEmail(ctx, "alex")
	assert.Error(t, err, "the bare key is gone")
	users, err := store.ListUsers(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 1, "no second account")

	rows := adoptRows(t, store, AuditAccountAdopted)
	require.Len(t, rows, 1)
	r := rows[0]
	require.NotNil(t, r.UserID)
	assert.Equal(t, old.ID, *r.UserID)
	assert.Equal(t, "user", r.TargetType)
	assert.Equal(t, strconv.FormatInt(old.ID, 10), r.TargetID)
	assert.Equal(t, "ldap", r.Metadata["provider"])
	assert.Equal(t, "alex", r.Metadata["old_email"])
	assert.Equal(t, "alex@local", r.Metadata["new_email"])
	assert.Equal(t, "files.example.test", r.Metadata["host"])
	assert.Equal(t, true, r.Metadata["renamed"])
	// Nothing else: no password, no token, no group list — and no
	// had_local_password flag on an account that had none.
	assert.Equal(t, []string{"host", "new_email", "old_email", "provider", "renamed"}, metaKeys(r.Metadata))
}

// Decision 1: a local password does not stop the adoption — only the old
// directory path ever opened a bare-name row — and the password is left EXACTLY
// as it was, so the local sign-in and the directory one both keep working.
func TestAdoptAccount_ALocalPasswordIsAdoptedAndKept(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	old, err := store.CreateUser(ctx, "alex", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	const hash = "$2a$10$abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXY"
	require.NoError(t, store.UpdateUserPassword(ctx, old.ID, hash))

	u, err := AdoptAccount(ctx, store, Adoption{Driver: "ldap", LoginName: "alex", Email: "alex@local"})
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, old.ID, u.ID)
	assert.Equal(t, "alex@local", u.Email)
	kept, err := store.GetUser(ctx, old.ID)
	require.NoError(t, err)
	assert.Equal(t, hash, kept.PasswordHash, "the local password is untouched")

	rows := adoptRows(t, store, AuditAccountAdopted)
	require.Len(t, rows, 1)
	assert.Equal(t, true, rows[0].Metadata["had_local_password"])
	assert.Equal(t, true, rows[0].Metadata["renamed"])
	assert.Empty(t, adoptRows(t, store, AuditAccountNotAdopted))
}

// Decision 2: an account bound to SSO is signed in to AS IT IS — its e-mail is
// what the OIDC provider finds it by. The adoption is recorded once, however
// often the person signs in.
func TestAdoptAccount_AnSSOAccountIsSignedInToAsItIs(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	old, err := store.CreateUser(ctx, "alex", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	st, err := store.GetSupertenant(ctx)
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, old.ID, st.ID, "oidc-sub-1"))

	for i := 0; i < 3; i++ {
		u, err := AdoptAccount(ctx, store, Adoption{Driver: "ldap", LoginName: "alex", Email: "alex@local"})
		require.NoError(t, err)
		require.NotNil(t, u)
		assert.Equal(t, old.ID, u.ID)
		assert.Equal(t, "alex", u.Email)
	}
	kept, err := store.GetUser(ctx, old.ID)
	require.NoError(t, err)
	assert.Equal(t, "alex", kept.Email, "the e-mail the OIDC provider finds it by is not changed")
	assert.Equal(t, "oidc-sub-1", kept.OIDCSubject)
	_, err = store.GetUserByEmail(ctx, "alex@local")
	assert.Error(t, err)

	rows := adoptRows(t, store, AuditAccountAdopted)
	require.Len(t, rows, 1, "recorded once, not at every sign-in")
	assert.Equal(t, false, rows[0].Metadata["renamed"])
	assert.Equal(t, AdoptedSSOAccount, rows[0].Metadata["reason"])
	require.NotNil(t, rows[0].UserID)
	assert.Equal(t, old.ID, *rows[0].UserID)
}

// Nothing to adopt: no older account, or a login name that is already an
// address (the older path kept an address as it was — there is no older key).
func TestAdoptAccount_NothingToAdopt(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)

	u, err := AdoptAccount(ctx, store, Adoption{Driver: "ldap", LoginName: "alex", Email: "alex@local"})
	require.NoError(t, err)
	assert.Nil(t, u)

	upn, err := store.CreateUser(ctx, "alex@corp.example", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	u, err = AdoptAccount(ctx, store, Adoption{Driver: "ldap", LoginName: "alex@corp.example", Email: "alex@corp.example"})
	require.NoError(t, err)
	assert.Nil(t, u)
	// Even when the address now differs (the entry gained a mail attribute):
	// an account keyed by an address is not the bare-name path's to hand over.
	u, err = AdoptAccount(ctx, store, Adoption{Driver: "ldap", LoginName: "alex@corp.example", Email: "alex.smith@corp.example"})
	require.NoError(t, err)
	assert.Nil(t, u)
	kept, err := store.GetUser(ctx, upn.ID)
	require.NoError(t, err)
	assert.Equal(t, "alex@corp.example", kept.Email)
	assert.Empty(t, adoptRows(t, store, AuditAccountAdopted))
	assert.Empty(t, adoptRows(t, store, AuditAccountNotAdopted))
}

// Decision 3: the tenant boundary is never crossed. Another tenant's account —
// with or without a password, bound to SSO or not — is neither returned nor
// changed, and a sign-in whose tenant cannot be told adopts nothing.
func TestAdoptAccount_NeverCrossesTheTenantBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mark   func(t *testing.T, store db.Store, id int64)
		host   string
		reason string
	}{
		{"another tenant's", func(*testing.T, db.Store, int64) {}, "globex.example.test", NotAdoptedOtherTenant},
		{"another tenant's, with a local password", func(t *testing.T, store db.Store, id int64) {
			require.NoError(t, store.UpdateUserPassword(context.Background(), id, "$2a$10$abcdefghijklmnopqrstuv"))
		}, "globex.example.test", NotAdoptedOtherTenant},
		{"another tenant's, bound to SSO", func(t *testing.T, store db.Store, id int64) {
			p, err := store.GetProviderBySlug(context.Background(), "initech")
			require.NoError(t, err)
			require.NoError(t, store.SetUserProvider(context.Background(), id, p.ID, "oidc-sub-1"))
		}, "globex.example.test", NotAdoptedOtherTenant},
		{"a sign-in whose tenant cannot be told", func(*testing.T, db.Store, int64) {}, "", NotAdoptedNoTenant},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := spyAlarm(t)
			ctx := WithLoginHost(context.Background(), tc.host)
			_, store := dbtest.NewTestDB(t)
			theirs := seedTenant(t, store, "initech", "initech.example.test")
			seedTenant(t, store, "globex", "globex.example.test")
			old, err := store.CreateUser(ctx, "alex", "", model.RoleUser, "en", model.TimezoneUnset)
			require.NoError(t, err)
			require.NoError(t, store.SetUserProvider(ctx, old.ID, theirs, ""))
			tc.mark(t, store, old.ID)
			before, err := store.GetUser(ctx, old.ID)
			require.NoError(t, err)

			u, err := AdoptAccount(ctx, store, Adoption{Driver: "ldap", LoginName: "alex", Email: "alex@local",
				Homing: TenantHoming{MultiTenant: true}})
			require.NoError(t, err)
			assert.Nil(t, u, "another tenant's account is never returned")

			after, err := store.GetUser(ctx, old.ID)
			require.NoError(t, err)
			assert.Equal(t, before, after, "and never changed")
			assert.Empty(t, adoptRows(t, store, AuditAccountAdopted))
			rows := adoptRows(t, store, AuditAccountNotAdopted)
			require.Len(t, rows, 1)
			assert.Equal(t, tc.reason, rows[0].Metadata["reason"])
			assert.Nil(t, rows[0].UserID, "a row naming no user: only the platform operator reads it")

			if tc.reason == NotAdoptedNoTenant {
				assert.Empty(t, spy.calls(), "no tenant was told apart, so there is nothing to tell the operator")
			} else {
				require.Len(t, spy.calls(), 1)
			}
		})
	}
}

// The platform operator hears about an older account in another tenant ONCE
// per account, ever: the audit row is the mark, and it names no user (a tenant
// administrator never reads another tenant's name from it).
func TestAdoptAccount_OtherTenantIsToldToTheOperatorOnce(t *testing.T) {
	spy := spyAlarm(t)
	ctx := WithLoginHost(context.Background(), "globex.example.test")
	_, store := dbtest.NewTestDB(t)
	theirs := seedTenant(t, store, "initech", "initech.example.test")
	seedTenant(t, store, "globex", "globex.example.test")
	alex, err := store.CreateUser(ctx, "alex", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, alex.ID, theirs, ""))
	a := Adoption{Driver: "ldap", LoginName: "alex", Email: "alex@local", Homing: TenantHoming{MultiTenant: true}}

	for i := 0; i < 3; i++ {
		u, err := AdoptAccount(ctx, store, a)
		require.NoError(t, err)
		assert.Nil(t, u)
	}
	told := spy.calls()
	require.Len(t, told, 1, "once per older account, however often the person tries")
	assert.Equal(t, LegacyAccountElsewhere{Driver: "ldap", Account: "alex", UserID: alex.ID,
		AccountTenant: "initech", LoginTenant: "globex"}, told[0])
	rows := adoptRows(t, store, AuditLegacyAccountElsewhere)
	require.Len(t, rows, 1)
	assert.Nil(t, rows[0].UserID)
	assert.Equal(t, strconv.FormatInt(alex.ID, 10), rows[0].TargetID)
	assert.Equal(t, []string{"account", "account_tenant", "host", "login_tenant", "provider"}, metaKeys(rows[0].Metadata))

	// Another older account is its own matter.
	bora, err := store.CreateUser(ctx, "bora", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, bora.ID, theirs, ""))
	_, err = AdoptAccount(ctx, store, Adoption{Driver: "ldap", LoginName: "bora", Email: "bora@local", Homing: a.Homing})
	require.NoError(t, err)
	require.Len(t, spy.calls(), 2)
	assert.Equal(t, bora.ID, spy.calls()[1].UserID)
}

// Multi-tenant: an account in the sign-in's own tenant is adopted — by the
// login's host, and by the pin when the login has none — and stays there.
func TestAdoptAccount_MultiTenantTakesTheSignInsOwnTenant(t *testing.T) {
	for _, tc := range []struct {
		name   string
		host   string
		homing TenantHoming
	}{
		{"by host", "globex.example.test", TenantHoming{MultiTenant: true}},
		{"by pin", "", TenantHoming{MultiTenant: true, Pin: "globex"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := spyAlarm(t)
			ctx := WithLoginHost(context.Background(), tc.host)
			_, store := dbtest.NewTestDB(t)
			p := seedTenant(t, store, "globex", "globex.example.test")
			old, err := store.CreateUser(ctx, "alex", "", model.RoleUser, "en", model.TimezoneUnset)
			require.NoError(t, err)
			require.NoError(t, store.SetUserProvider(ctx, old.ID, p, ""))

			u, err := AdoptAccount(ctx, store, Adoption{Driver: "ldap", LoginName: "alex", Email: "alex@local", Homing: tc.homing})
			require.NoError(t, err)
			require.NotNil(t, u)
			assert.Equal(t, old.ID, u.ID)
			require.NotNil(t, u.ProviderID)
			assert.Equal(t, p, *u.ProviderID, "adoption never moves an account between tenants")
			assert.Empty(t, spy.calls())
		})
	}
}

// raceStore gives the new address to somebody else the moment the adoption
// writes it — another sign-in, an administrator — so the unique index refuses
// the re-key.
type raceStore struct {
	db.Store
	winner *model.User
}

func (r *raceStore) UpdateUserEmail(ctx context.Context, id int64, email string) error {
	if r.winner == nil {
		u, err := r.Store.CreateUser(ctx, email, "", model.RoleUser, "en", model.TimezoneUnset)
		if err != nil {
			return err
		}
		r.winner = u
	}
	return r.Store.UpdateUserEmail(ctx, id, email)
}

func TestAdoptAccount_ALostRaceSignsInToTheWinnerNeverASecondAccount(t *testing.T) {
	ctx := context.Background()
	_, raw := dbtest.NewTestDB(t)
	store := &raceStore{Store: raw}
	old, err := raw.CreateUser(ctx, "alex", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)

	u, err := AdoptAccount(ctx, store, Adoption{Driver: "ldap", LoginName: "alex", Email: "alex@local"})
	require.NoError(t, err, "the unique index said no; the account that holds the address is the answer")
	require.NotNil(t, u)
	require.NotNil(t, store.winner)
	assert.Equal(t, store.winner.ID, u.ID)
	users, err := raw.ListUsers(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 2, "the older account and the winner — nothing opened by the adoption")
	kept, err := raw.GetUser(ctx, old.ID)
	require.NoError(t, err)
	assert.Equal(t, "alex", kept.Email)
	assert.Empty(t, adoptRows(t, raw, AuditAccountAdopted), "nothing was adopted")
}

func seedTenant(t *testing.T, store db.Store, slug, host string) int64 {
	t.Helper()
	p, err := store.CreateProvider(context.Background(), &model.Provider{
		Slug: slug, Name: slug, Host: host, AuthType: "local", Enabled: true,
	})
	require.NoError(t, err)
	return p.ID
}
