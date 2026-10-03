package windows

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// A machine account in a tenant's realm is `alex@<realm>.<token>` (#128): the
// same Windows account signed in to through acme and through beta is two filex
// accounts. A domain account keeps its domain's address in every realm.
func TestParseAccountInARealm(t *testing.T) {
	cases := []struct {
		typed, domain, realm string
		user, dom, email     string
		ok                   bool
	}{
		{"alex", "", "acme", "alex", ".", "alex@acme.local", true},
		{`.\Alex`, "", "acme", "Alex", ".", "alex@acme.local", true},
		// The protocols hand back the account's address: in its realm it is
		// the machine's account again.
		{"alex@acme.local", "", "acme", "alex", ".", "alex@acme.local", true},
		// A domain account's address is the same in every realm.
		{`CORP\alex`, "", "acme", "alex", "CORP", "alex@corp", true},
		{"alex", "CORP", "acme", "alex", "CORP", "alex@corp", true},
		{"alex@corp.example", "", "acme", "alex@corp.example", "", "alex@corp.example", true},
		// ⚠ An AD domain that happens to end in the token is still a domain.
		{"alex@corp.local", "", "acme", "alex@corp.local", "", "alex@corp.local", true},
		{"alex@corp.local", "", "", "alex@corp.local", "", "alex@corp.local", true},
		// The platform's own machine address, typed in a tenant's realm: not
		// this sign-in's.
		{"alex@local", "", "acme", "", "", "", false},
	}
	for _, c := range cases {
		a, ok := parseAccount(c.typed, c.domain, c.realm, "")
		require.Equal(t, c.ok, ok, "%q in %q", c.typed, c.realm)
		if !ok {
			continue
		}
		assert.Equal(t, c.user, a.LogonUser, "%q user", c.typed)
		assert.Equal(t, c.dom, a.LogonDomain, "%q domain", c.typed)
		assert.Equal(t, c.email, a.Email, "%q email", c.typed)
	}
}

func windowsRealm(t *testing.T, store db.Store, realm string) context.Context {
	t.Helper()
	lr, err := auth.ResolveLoginRealm(context.Background(), store, realm, realm != "", "", "")
	require.NoError(t, err)
	return auth.WithLoginRealm(context.Background(), lr)
}

func TestLoginInARealm(t *testing.T) {
	d, store := newDriver(t, people(), map[string]any{"auto_create": true, "multi_tenant": true})
	ctx := context.Background()
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", AuthType: "local", Enabled: true})
	require.NoError(t, err)

	ua, _, err := d.Login(windowsRealm(t, store, "acme"), "ayse", "pw-ayse")
	require.NoError(t, err)
	assert.Equal(t, "ayse@acme.local", ua.Email)
	assert.Equal(t, acme.ID, *ua.ProviderID)
	ub, _, err := d.Login(windowsRealm(t, store, "beta"), "ayse", "pw-ayse")
	require.NoError(t, err)
	assert.Equal(t, "ayse@beta.local", ub.Email)
	assert.Equal(t, beta.ID, *ub.ProviderID)
	assert.NotEqual(t, ua.ID, ub.ID)

	// A domain account made in beta is beta's: acme's realm does not sign in
	// to it, nor write its groups.
	uc, _, err := d.Login(windowsRealm(t, store, "beta"), `CORP\ayse`, "pw-corp")
	require.NoError(t, err)
	assert.Equal(t, "ayse@corp", uc.Email)
	_, _, err = d.Login(windowsRealm(t, store, "acme"), `CORP\ayse`, "pw-corp")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
}
