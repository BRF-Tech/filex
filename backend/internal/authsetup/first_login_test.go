package authsetup_test

// The first-login settings on the Identity providers page: in the schema for
// every managed provider, handed to the driver as booleans/lists, and — the
// upgrade promise — absent means "open an account", exactly as before.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestSchema_FirstLoginFieldsForEveryManagedProvider(t *testing.T) {
	for _, name := range authsetup.Managed {
		ac, ok := authsetup.FieldOf(name, "auto_create")
		require.True(t, ok, name)
		assert.Equal(t, authsetup.FieldBool, ac.Kind)
		if authsetup.IsOS(name) {
			assert.Equal(t, "false", ac.Default, "%s: an operating-system provider opens no account unless told to", name)
		} else {
			assert.Equal(t, "true", ac.Default, "%s: the default is to open an account", name)
		}
		ag, ok := authsetup.FieldOf(name, "allowed_groups")
		require.True(t, ok, name)
		assert.Equal(t, authsetup.FieldText, ag.Kind)
	}
	ga, ok := authsetup.FieldOf("ldap", "group_attr")
	require.True(t, ok)
	assert.Equal(t, "memberOf", ga.Default)
	_, ok = authsetup.FieldOf("oidc", "group_attr")
	assert.False(t, ok, "OIDC reads groups from role_claim")
}

func TestDriverConfig_FirstLoginDefaultsAndValues(t *testing.T) {
	b := box(t, testKey)
	for _, name := range authsetup.Managed {
		// Nothing stored: the default applies - open, except an operating-system
		// provider, which is closed.
		cfg, _, err := authsetup.DriverConfig(&authsetup.Stored{Name: name, Values: map[string]string{}}, b, authsetup.Options{})
		require.NoError(t, err)
		assert.Equal(t, !authsetup.IsOS(name), cfg["auto_create"], name)
		assert.Equal(t, !authsetup.IsOS(name), auth.FirstLoginPolicyFrom(cfg).AutoCreate, name)

		cfg, _, err = authsetup.DriverConfig(&authsetup.Stored{Name: name, Values: map[string]string{
			"auto_create": "false", "allowed_groups": "a, b",
		}}, b, authsetup.Options{})
		require.NoError(t, err)
		p := auth.FirstLoginPolicyFrom(cfg)
		assert.False(t, p.AutoCreate, name)
		assert.Equal(t, []string{"a", "b"}, p.AllowedGroups, name)
	}
}

// The header proxy's saved `auto_provision` (its old name) still decides when
// nothing says auto_create.
func TestDriverConfig_HeaderProxyKeepsTheOlderName(t *testing.T) {
	b := box(t, testKey)
	cfg, _, err := authsetup.DriverConfig(&authsetup.Stored{Name: "proxy-header", Values: map[string]string{"auto_provision": "false"}}, b, authsetup.Options{})
	require.NoError(t, err)
	assert.False(t, auth.FirstLoginPolicyFrom(cfg).AutoCreate)
	_, has := cfg["auto_provision"]
	assert.False(t, has, "folded into auto_create")

	cfg, _, err = authsetup.DriverConfig(&authsetup.Stored{Name: "proxy-header", Values: map[string]string{"auto_provision": "false", "auto_create": "true"}}, b, authsetup.Options{})
	require.NoError(t, err)
	assert.True(t, auth.FirstLoginPolicyFrom(cfg).AutoCreate, "the new name wins")
}

// The e-mail token is the environment's alone: it reaches the LDAP driver from
// Options, never from a page field.
func TestEmailToken_IsEnvironmentOnly(t *testing.T) {
	b := box(t, testKey)
	for _, name := range authsetup.Managed {
		_, ok := authsetup.FieldOf(name, "email_token")
		assert.False(t, ok, "%s: no page field may edit the e-mail token", name)
	}
	cfg, _, err := authsetup.DriverConfig(&authsetup.Stored{Name: "ldap", Values: map[string]string{"email_token": "evil"}}, b, authsetup.Options{LoginEmailToken: "evim"})
	require.NoError(t, err)
	assert.Equal(t, "evim", cfg["email_token"])
	cfg, _, err = authsetup.DriverConfig(&authsetup.Stored{Name: "ldap", Values: map[string]string{}}, b, authsetup.Options{})
	require.NoError(t, err)
	_, has := cfg["email_token"]
	assert.False(t, has, "unset keeps the pre-existing behaviour")
}

// A save through Merge keeps the new fields and drops keys outside the schema.
func TestMerge_FirstLoginFieldsAreStored(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	cur := &authsetup.Stored{Name: "ldap", Values: map[string]string{}}
	next, changed, err := authsetup.Merge(cur, authsetup.Change{Values: map[string]any{
		"auto_create": false, "allowed_groups": []any{"a", "b"}, "group_attr": "isMemberOf", "email_token": "x",
	}}, box(t, testKey))
	require.NoError(t, err)
	assert.Equal(t, []string{"allowed_groups", "auto_create", "group_attr"}, changed)
	assert.Equal(t, "false", next.Values["auto_create"])
	assert.Equal(t, "a, b", next.Values["allowed_groups"])
	require.NoError(t, authsetup.Save(context.Background(), store, next))
}
