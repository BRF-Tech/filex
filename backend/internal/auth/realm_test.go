package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// realmStore is the slice of the store realm resolution and homing read.
type realmStore struct{ providers []*model.Provider }

func (s *realmStore) GetProviderByRealm(_ context.Context, realm string) (*model.Provider, error) {
	for _, p := range s.providers {
		if realm != "" && p.Realm == realm {
			return p, nil
		}
	}
	return nil, nil
}

func (s *realmStore) GetProviderByHost(_ context.Context, host string) (*model.Provider, error) {
	for _, p := range s.providers {
		if p.Host != "" && p.Host == host && p.Enabled {
			return p, nil
		}
	}
	return nil, nil
}

func (s *realmStore) GetProviderBySlug(_ context.Context, slug string) (*model.Provider, error) {
	for _, p := range s.providers {
		if p.Slug == slug {
			return p, nil
		}
	}
	return nil, nil
}

func (s *realmStore) GetSupertenant(_ context.Context) (*model.Provider, error) {
	for _, p := range s.providers {
		if p.IsSupertenant {
			return p, nil
		}
	}
	return nil, nil
}

func fixtureRealms() (*realmStore, *model.Provider, *model.Provider, *model.Provider) {
	super := &model.Provider{ID: 1, Slug: "default", Host: "files.platform.test", IsSupertenant: true, Enabled: true}
	acme := &model.Provider{ID: 10, Slug: "acme", Realm: "acme", Host: "files.acme.test", Enabled: true}
	beta := &model.Provider{ID: 20, Slug: "beta", Realm: "beta", Enabled: true}
	return &realmStore{providers: []*model.Provider{super, acme, beta}}, super, acme, beta
}

func userIn(id int64, provider *model.Provider) *model.User {
	u := &model.User{ID: id, Enabled: true}
	if provider != nil {
		pid := provider.ID
		u.ProviderID = &pid
	}
	return u
}

func TestResolveLoginRealm(t *testing.T) {
	store, super, acme, beta := fixtureRealms()
	ctx := context.Background()
	resolve := func(typed string, named bool, host string) (*LoginRealm, error) {
		return ResolveLoginRealm(ctx, store, typed, named, host, "")
	}

	// Nothing named: the platform's own realm — at its own host too.
	for _, host := range []string{"", "files.platform.test", "unknown.test"} {
		lr, err := resolve("", false, host)
		require.NoError(t, err, host)
		assert.Equal(t, super.ID, lr.Tenant.ID, host)
		assert.False(t, lr.Named, host)
		assert.Equal(t, "", lr.CounterRealm())
	}

	// The tenant's own address names it.
	lr, err := resolve("", false, "files.acme.test")
	require.NoError(t, err)
	assert.Equal(t, acme.ID, lr.Tenant.ID)
	assert.True(t, lr.Named)
	assert.Equal(t, "acme", lr.CounterRealm())

	// A typed realm, at the platform's address or at its own.
	for _, host := range []string{"", "files.platform.test", "files.acme.test"} {
		lr, err = resolve("ACME", true, host)
		require.NoError(t, err, host)
		assert.Equal(t, acme.ID, lr.Tenant.ID, host)
		assert.Equal(t, "acme", lr.CounterRealm())
	}
	lr, err = resolve("beta", true, "")
	require.NoError(t, err)
	assert.Equal(t, beta.ID, lr.Tenant.ID, "a tenant with no host is reached by its realm")

	// ⚠ Refused: a realm nobody has, and a realm that is not the address's.
	lr, err = resolve("nobody", true, "")
	require.ErrorIs(t, err, ErrUnknownRealm)
	assert.Equal(t, "nobody", lr.CounterRealm(), "still counted under what was typed")
	_, err = resolve("beta", true, "files.acme.test")
	require.ErrorIs(t, err, ErrRealmConflict)
	_, err = resolve("", true, "files.acme.test")
	require.ErrorIs(t, err, ErrRealmConflict, "the platform's realm said outright at a tenant's address")
}

func TestLoginRealmAdmits(t *testing.T) {
	store, super, acme, beta := fixtureRealms()
	ctx := context.Background()

	inAcme, err := ResolveLoginRealm(ctx, store, "acme", true, "", "")
	require.NoError(t, err)
	assert.True(t, inAcme.Admits(userIn(1, acme)))
	assert.False(t, inAcme.Admits(userIn(2, beta)), "another tenant's account")
	assert.False(t, inAcme.Admits(userIn(3, super)), "the platform's account")
	assert.False(t, inAcme.Admits(userIn(4, nil)), "an account with no tenant")
	assert.False(t, inAcme.AdmitsCredential(userIn(2, beta)), "a named realm binds a token or a key too")

	main, err := ResolveLoginRealm(ctx, store, "", false, "", "")
	require.NoError(t, err)
	assert.True(t, main.Admits(userIn(3, super)))
	assert.True(t, main.Admits(userIn(4, nil)))
	assert.False(t, main.Admits(userIn(1, acme)), "a password in the platform's realm is the platform's accounts'")
	assert.True(t, main.AdmitsCredential(userIn(1, acme)), "a token or key names its account: no realm needed")

	// A directory provider's pin extends an UN-named sign-in to its tenant,
	// for that sign-in only.
	main.NotePinned(beta.ID)
	assert.True(t, main.Admits(userIn(2, beta)))
	assert.False(t, main.Admits(userIn(1, acme)))
	inAcme.NotePinned(beta.ID)
	assert.False(t, inAcme.Admits(userIn(2, beta)), "a named realm is never widened by a pin")

	var none *LoginRealm
	assert.True(t, none.Admits(userIn(5, beta)), "no tenants: nothing to check")
	assert.Nil(t, none.Identity())
}

func TestHomeOrder(t *testing.T) {
	store, _, acme, beta := fixtureRealms()
	h := TenantHoming{MultiTenant: true, Pin: "beta"}

	// The realm named wins over host and pin.
	lr, err := ResolveLoginRealm(context.Background(), store, "acme", true, "", "")
	require.NoError(t, err)
	p, viaPin, err := h.Home(WithLoginRealm(context.Background(), lr), store)
	require.NoError(t, err)
	assert.Equal(t, acme.ID, p.ID)
	assert.False(t, viaPin)

	// Not named: the pin.
	lr, err = ResolveLoginRealm(context.Background(), store, "", false, "", "")
	require.NoError(t, err)
	ctx := WithLoginRealm(context.Background(), lr)
	realm, home, viaPin := h.DirectoryRealm(ctx, store)
	assert.Equal(t, "beta", realm)
	assert.Equal(t, beta.ID, home.ID)
	assert.True(t, viaPin)
	NoteHome(ctx, home, viaPin)
	assert.True(t, lr.Admits(userIn(9, beta)))

	// The platform's own realm said outright homes nobody.
	lr, err = ResolveLoginRealm(context.Background(), store, "", true, "", "")
	require.NoError(t, err)
	_, _, err = h.Home(WithLoginRealm(context.Background(), lr), store)
	require.True(t, errors.Is(err, ErrNoTenantForLogin))

	// A single-tenant install: nothing at all.
	p, viaPin, err = TenantHoming{}.Home(context.Background(), store)
	require.NoError(t, err)
	assert.Nil(t, p)
	assert.False(t, viaPin)
}
