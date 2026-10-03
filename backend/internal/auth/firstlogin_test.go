package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestFirstLoginPolicyFrom(t *testing.T) {
	// Unset means "open an account": an upgrade leaves nobody outside.
	p := FirstLoginPolicyFrom(map[string]any{})
	assert.True(t, p.AutoCreate)
	assert.Empty(t, p.AllowedGroups)

	assert.False(t, FirstLoginPolicyFrom(map[string]any{"auto_create": false}).AutoCreate)
	assert.False(t, FirstLoginPolicyFrom(map[string]any{"auto_create": "false"}).AutoCreate)
	assert.True(t, FirstLoginPolicyFrom(map[string]any{"auto_create": ""}).AutoCreate)
	// The header proxy's older name, and the new one wins over it.
	assert.False(t, FirstLoginPolicyFrom(map[string]any{"auto_provision": false}).AutoCreate)
	assert.True(t, FirstLoginPolicyFrom(map[string]any{"auto_provision": false, "auto_create": true}).AutoCreate)

	p = FirstLoginPolicyFrom(map[string]any{"allowed_groups": " staff, Yöneticiler ,,"})
	assert.Equal(t, []string{"staff", "Yöneticiler"}, p.AllowedGroups)
	p = FirstLoginPolicyFrom(map[string]any{"allowed_groups": []string{"a", "b"}})
	assert.Equal(t, []string{"a", "b"}, p.AllowedGroups)
}

func TestFirstLoginPolicyDecide(t *testing.T) {
	open := FirstLoginPolicy{AutoCreate: true}
	assert.Empty(t, open.Decide(nil))
	assert.Empty(t, open.Decide([]string{"anything"}))

	assert.Equal(t, ReasonAutoCreateOff, FirstLoginPolicy{}.Decide([]string{"staff"}))

	g := FirstLoginPolicy{AutoCreate: true, AllowedGroups: []string{"staff", "YÖNETİCİLER"}}
	assert.Equal(t, ReasonGroupNotAllowed, g.Decide(nil))
	assert.Equal(t, ReasonGroupNotAllowed, g.Decide([]string{"contractors"}))
	assert.Empty(t, g.Decide([]string{"contractors", "STAFF"}))
	// Turkish dotted/dotless I: one group, however it is typed.
	assert.Empty(t, g.Decide([]string{"yöneticiler"}))
	assert.Empty(t, g.Decide([]string{"Yöneticiler"}))
}

func TestProvisionFirstLogin_RefusalMakesNoAccountAndAnAuditRow(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := WithLoginHost(context.Background(), "files.example.test")

	for _, tc := range []struct {
		name   string
		policy FirstLoginPolicy
		groups []string
		reason string
	}{
		{"closed", FirstLoginPolicy{}, []string{"staff"}, ReasonAutoCreateOff},
		{"outside the groups", FirstLoginPolicy{AutoCreate: true, AllowedGroups: []string{"staff"}}, []string{"guests"}, ReasonGroupNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := ProvisionFirstLogin(ctx, store, FirstLogin{
				Driver: "ldap", Identifier: "ayse", Email: "ayse@x.test", Role: model.RoleUser,
				Groups: tc.groups, Policy: tc.policy,
			})
			require.Error(t, err)
			assert.Nil(t, u)
			assert.ErrorIs(t, err, ErrFirstLoginRefused)
			var ref *FirstLoginRefusal
			require.True(t, errors.As(err, &ref))
			assert.Equal(t, tc.reason, ref.Reason)

			_, gerr := store.GetUserByEmail(ctx, "ayse@x.test")
			assert.Error(t, gerr, "a refused first sign-in must not leave an account")

			rows, err := store.ListAuditRecent(ctx, 20)
			require.NoError(t, err)
			var found bool
			for _, r := range rows {
				if r.Action == AuditFirstLoginRefused && r.Metadata["reason"] == tc.reason {
					found = true
					assert.Equal(t, "ldap", r.Metadata["provider"])
					assert.Equal(t, "ayse", r.Metadata["identifier"])
					assert.Equal(t, "files.example.test", r.Metadata["host"])
					assert.NotContains(t, r.Metadata, "groups", "the audit row carries no group list")
				}
			}
			assert.True(t, found, "the audit row names the reason %q", tc.reason)
		})
	}
}

func TestProvisionFirstLogin_OpensTheAccount(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	u, err := ProvisionFirstLogin(context.Background(), store, FirstLogin{
		Driver: "ldap", Identifier: "ayse", Email: "ayse@x.test", Role: model.RoleUser,
		Groups: []string{"staff"}, Policy: FirstLoginPolicy{AutoCreate: true, AllowedGroups: []string{"Staff"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "ayse@x.test", u.Email)
}

func TestApplyStartingRole_GroupRuleIsTheOnlyTable(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	rule, err := store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Auditors", Enabled: true, Permissions: perm.ReadOnly.With(perm.AdminAudit).Strings(),
		Targets: []model.PermRuleTarget{{Kind: model.PermTargetSSOGroup, Value: "audit"}},
	})
	require.NoError(t, err)
	u, err := store.CreateUser(ctx, "ada@x.test", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)

	ApplyStartingRole(ctx, store, "ldap", u, []string{"staff", "audit"})
	held, err := store.GetUserCustomRole(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, rule.ID, held)
	assert.Equal(t, model.RoleViewer, u.Role)

	other, err := store.CreateUser(ctx, "bob@x.test", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	ApplyStartingRole(ctx, store, "ldap", other, []string{"staff"})
	held, err = store.GetUserCustomRole(ctx, other.ID)
	require.NoError(t, err)
	assert.Zero(t, held)
}
