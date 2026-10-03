package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// A tenant's realm goes into the addresses a provider makes up (the owner's
// decision, #128): `alex@acme.local` in realm acme, `alex@local` for the
// platform's own tenant and every single-tenant install — so two tenants can
// each have an `alex`.
func TestDeriveEmailInARealm(t *testing.T) {
	cases := []struct{ name, domain, realm, token, want string }{
		{"alex", "", "acme", "", "alex@acme.local"},
		{"Alex", "", " ACME ", "evim", "alex@acme.evim"},
		{"alex", "", "", "", "alex@local"},
		{"alex", "corp.example", "acme", "", "alex@corp.example"}, // a real domain is the same in every realm
		{"alex@example.com", "", "acme", "", "alex@example.com"},            // an address stays as it is
	}
	for _, c := range cases {
		assert.Equal(t, c.want, DeriveEmail(c.name, c.domain, c.realm, c.token), "%+v", c)
	}
	assert.Equal(t, "acme.local", TokenDomain("acme", ""))
	assert.Equal(t, "local", TokenDomain("", ""))
}

func TestLoginNameOfInARealm(t *testing.T) {
	got, ok := LoginNameOf("Alex@Acme.Local", "acme", "")
	require.True(t, ok)
	assert.Equal(t, "alex", got)
	assert.Equal(t, "alex@acme.local", DeriveEmail(got, "", "acme", ""))

	// Another realm's address, the platform's own form, and a real mailbox are
	// not ours to take apart.
	for _, c := range []struct{ email, realm string }{
		{"alex@beta.local", "acme"},
		{"alex@local", "acme"},
		{"alex@acme.local", ""},
		{"alex@acme.local.example", "acme"},
	} {
		_, ok := LoginNameOf(c.email, c.realm, "")
		assert.False(t, ok, "%+v", c)
	}
}

func TestNamesIn(t *testing.T) {
	u := &model.User{Email: "alex@acme.local", Username: "alex2"}
	assert.True(t, NamesIn(u, "alex", "acme", ""), "the login name the address was made from")
	assert.True(t, NamesIn(u, "ALEX2", "acme", ""), "the username")
	assert.True(t, NamesIn(u, "alex@acme.local", "", ""), "the address")
	assert.False(t, NamesIn(u, "alex", "beta", ""), "not in another realm")
	assert.False(t, NamesIn(u, "alex", "", ""), "not in the platform's own realm")
	assert.False(t, NamesIn(u, "", "acme", ""))
}

func ptr(v int64) *int64 { return &v }

// ResolveIn never leaves the tenant, and looks in the owner's order: the
// derived address, then the username, then the address as typed.
func TestResolveInStaysInTheTenant(t *testing.T) {
	const acme, beta, super = 10, 20, 1
	m := &memUsers{byID: map[int64]*model.User{
		1: {ID: 1, Email: "alex@acme.local", Username: "alex", ProviderID: ptr(acme)},
		2: {ID: 2, Email: "alex@beta.local", Username: "alex2", ProviderID: ptr(beta)},
		3: {ID: 3, Email: "alex@local", Username: "alex3", ProviderID: ptr(super)},
		4: {ID: 4, Email: "boss@corp.example", Username: "boss", ProviderID: ptr(beta)},
		5: {ID: 5, Email: "legacy@corp.example", Username: "legacy"},
	}}
	ctx := context.Background()
	inAcme := &Tenant{ProviderID: acme, Realm: "acme"}
	inBeta := &Tenant{ProviderID: beta, Realm: "beta"}
	inMain := &Tenant{ProviderID: super, Main: true}

	got := func(tn *Tenant, id string) int64 {
		t.Helper()
		u, err := ResolveIn(ctx, m, tn, id)
		if errors.Is(err, ErrNotFound) {
			return 0
		}
		require.NoError(t, err)
		return u.ID
	}

	// `alex` is a different person in every realm.
	assert.EqualValues(t, 1, got(inAcme, "alex"))
	assert.EqualValues(t, 2, got(inBeta, "alex"), "beta's alex is found by the derived address, although the username alex is acme's")
	assert.EqualValues(t, 3, got(inMain, "alex"))

	// ⚠ Another tenant's account is never found, by any name.
	assert.Zero(t, got(inAcme, "alex2"), "beta's username")
	assert.Zero(t, got(inAcme, "alex@beta.local"), "beta's address")
	assert.Zero(t, got(inAcme, "boss@corp.example"))
	assert.Zero(t, got(inMain, "alex@acme.local"))
	assert.Zero(t, got(inAcme, "legacy"), "an account with no tenant is the platform's own")

	assert.EqualValues(t, 4, got(inBeta, "BOSS@corp.example"), "an address as typed")
	assert.EqualValues(t, 2, got(inBeta, "alex2"), "a username inside the tenant")
	assert.EqualValues(t, 5, got(inMain, "legacy"), "no tenant = the platform's own")

	// No tenant: exactly Resolve.
	u, err := ResolveIn(ctx, m, nil, "alex2")
	require.NoError(t, err)
	assert.EqualValues(t, 2, u.ID)
}
