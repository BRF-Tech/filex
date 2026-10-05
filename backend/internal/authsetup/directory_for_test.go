package authsetup_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// Add user asks whether an LDAP directory owns the address (its
// email_domains). Only the directories that sign people in to the tenant
// the account is made in answer: another tenant's own directory is not the
// caller's, and asking it told a tenant's administrator its label.
func TestDirectoryFor_OnlyTheTenantsDirectoriesAnswer(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	put(t, store, map[string]string{authsetup.InstancesSetting: "1"})
	acme, beta := tenantOf(t, store, "acme"), tenantOf(t, store, "beta")
	owner := acme.ID
	own, err := store.CreateAuthInstance(ctx, &model.AuthInstance{Slug: "acme-dir", Driver: "ldap", Origin: model.AuthOriginTenant,
		Label: "Acme AD", OwnerProviderID: &owner, Enabled: true,
		ConfigJSON: `{"url":"ldaps://dir.acme.example","base_dn":"dc=acme,dc=example","email_domains":"acme.example"}`})
	require.NoError(t, err)
	require.NoError(t, store.BindAuthInstance(ctx, acme.ID, own.ID, model.AuthBindExplicit))
	l := multiLive(t, store, &fakeDirectory{}, "")
	require.NoError(t, l.Reload(ctx))
	e, ok := l.Current().Entry("acme-dir")
	require.True(t, ok)
	require.NotNil(t, e.Driver, e.Err)

	slug, label, ok := l.DirectoryFor("Pat@ACME.example", acme.ID)
	require.True(t, ok, "acme's own directory owns its domain in acme")
	assert.Equal(t, "acme-dir", slug)
	assert.Equal(t, "Acme AD", label)

	_, label, ok = l.DirectoryFor("pat@acme.example", beta.ID)
	assert.False(t, ok, "another tenant's directory: %q", label)
	_, label, ok = l.DirectoryFor("pat@acme.example", 0)
	assert.False(t, ok, "the platform's own tenant: %q", label)
}
