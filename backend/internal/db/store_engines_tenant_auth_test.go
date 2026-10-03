package db_test

// Tenant self-service (migration 00076, docs/TENANT-ADMIN.md), on every
// engine: sign-in provider instances and their bindings, a tenant's own
// domains, the operator's insecure switch, and the addresses a tenant answers
// on (GetProviderByHost: host, active own domain, platform subdomain).

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

func TestTenantAuthStoreOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Enabled: true})
			require.NoError(t, err)
			beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", Enabled: true})
			require.NoError(t, err)

			// An instance, its configuration and its promoted scopes.
			ldap, err := store.CreateAuthInstance(ctx, &model.AuthInstance{
				Slug: "ldap", Driver: "ldap", Origin: model.AuthOriginPage, Enabled: true,
				ConfigJSON: `{"url":"ldaps://dir.example","bind_password":"sealed:v1:x"}`,
			})
			require.NoError(t, err)
			require.Equal(t, "ldap", ldap.Slug)
			require.True(t, ldap.Enabled)
			require.Nil(t, ldap.OwnerProviderID)
			require.Equal(t, `{"url":"ldaps://dir.example","bind_password":"sealed:v1:x"}`, ldap.ConfigJSON)

			own, err := store.CreateAuthInstance(ctx, &model.AuthInstance{
				Slug: "acme-oidc", Driver: "oidc", Label: "Acme SSO", Origin: model.AuthOriginTenant, OwnerProviderID: &acme.ID,
			})
			require.NoError(t, err)
			require.Equal(t, acme.ID, *own.OwnerProviderID)
			require.Equal(t, "{}", own.ConfigJSON, "an empty configuration is stored as an empty object")

			_, err = store.CreateAuthInstance(ctx, &model.AuthInstance{Slug: "ldap", Driver: "ldap"})
			require.Error(t, err, "a slug is one instance's")

			// Update writes the mutable fields and nothing else.
			ldap.Label, ldap.Enabled, ldap.Promoted = "Corporate directory", false, []int64{acme.ID, 0}
			ldap.Slug, ldap.Driver, ldap.Origin = "renamed", "oidc", model.AuthOriginTenant
			require.NoError(t, store.UpdateAuthInstance(ctx, ldap))
			got, err := store.GetAuthInstance(ctx, ldap.ID)
			require.NoError(t, err)
			require.Equal(t, "ldap", got.Slug, "the slug never changes")
			require.Equal(t, "ldap", got.Driver)
			require.Equal(t, model.AuthOriginPage, got.Origin)
			require.Equal(t, "Corporate directory", got.Label)
			require.False(t, got.Enabled)
			require.Equal(t, []int64{0, acme.ID}, got.Promoted)
			require.True(t, got.PromotedIn(0))
			require.False(t, got.PromotedIn(beta.ID))

			bySlug, err := store.GetAuthInstanceBySlug(ctx, "acme-oidc")
			require.NoError(t, err)
			require.Equal(t, own.ID, bySlug.ID)
			none, err := store.GetAuthInstanceBySlug(ctx, "nobody")
			require.NoError(t, err)
			require.Nil(t, none)

			// Bindings: idempotent, the first source kept, removable one by one.
			require.NoError(t, store.BindAuthInstance(ctx, acme.ID, ldap.ID, model.AuthBindUpgrade))
			require.NoError(t, store.BindAuthInstance(ctx, acme.ID, ldap.ID, model.AuthBindExplicit))
			require.NoError(t, store.BindAuthInstance(ctx, beta.ID, ldap.ID, ""))
			require.NoError(t, store.BindAuthInstance(ctx, acme.ID, own.ID, model.AuthBindExplicit))
			bs, err := store.ListAuthBindings(ctx)
			require.NoError(t, err)
			require.Len(t, bs, 3)
			src := map[[2]int64]string{}
			for _, b := range bs {
				src[[2]int64{b.ProviderID, b.InstanceID}] = b.Source
			}
			require.Equal(t, model.AuthBindUpgrade, src[[2]int64{acme.ID, ldap.ID}])
			require.Equal(t, model.AuthBindExplicit, src[[2]int64{beta.ID, ldap.ID}])
			require.NoError(t, store.UnbindAuthInstance(ctx, beta.ID, ldap.ID))
			bs, err = store.ListAuthBindings(ctx)
			require.NoError(t, err)
			require.Len(t, bs, 2)

			// Inside a transaction the binding is part of it (and rolls back).
			require.Error(t, store.WithTx(ctx, func(ctx context.Context) error {
				require.NoError(t, store.BindAuthInstance(ctx, beta.ID, own.ID, model.AuthBindExplicit))
				return context.Canceled
			}))
			bs, err = store.ListAuthBindings(ctx)
			require.NoError(t, err)
			require.Len(t, bs, 2, "a rolled-back bind left a row")

			// An instance goes with its bindings; a tenant with its own instances.
			require.NoError(t, store.DeleteProvider(ctx, acme.ID))
			gone, err := store.GetAuthInstance(ctx, own.ID)
			require.NoError(t, err)
			require.Nil(t, gone, "a tenant's own instance is deleted with the tenant")
			bs, err = store.ListAuthBindings(ctx)
			require.NoError(t, err)
			require.Empty(t, bs)
			require.NoError(t, store.DeleteAuthInstance(ctx, ldap.ID))
			list, err := store.ListAuthInstances(ctx)
			require.NoError(t, err)
			require.Empty(t, list)

			// The operator's insecure switch: its own statement, and
			// UpdateProvider never writes it.
			require.NoError(t, store.SetProviderAllowInsecureAuth(ctx, beta.ID, true))
			b2, err := store.GetProvider(ctx, beta.ID)
			require.NoError(t, err)
			require.True(t, b2.AllowInsecureAuth)
			b2.AllowInsecureAuth = false
			b2.Name = "Beta Inc"
			require.NoError(t, store.UpdateProvider(ctx, b2))
			b3, err := store.GetProvider(ctx, beta.ID)
			require.NoError(t, err)
			require.True(t, b3.AllowInsecureAuth, "UpdateProvider must not write allow_insecure_auth")
			require.Equal(t, "Beta Inc", b3.Name)
		})
	}
}

func TestProviderDomainsOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			db.SetTenantDomain("")
			t.Cleanup(func() { db.SetTenantDomain("") })

			acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Host: "files.acme.example", Enabled: true})
			require.NoError(t, err)
			beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", Enabled: true})
			require.NoError(t, err)

			d, err := store.CreateProviderDomain(ctx, &model.ProviderDomain{ProviderID: beta.ID, Domain: " Files.Beta.Example "})
			require.NoError(t, err)
			require.Equal(t, "files.beta.example", d.Domain)
			require.Equal(t, model.DomainPending, d.Status)
			require.Nil(t, d.ActiveSince)

			// One tenant per domain, whatever its state.
			_, err = store.CreateProviderDomain(ctx, &model.ProviderDomain{ProviderID: acme.ID, Domain: "files.beta.example"})
			require.Error(t, err, "a second tenant took a domain")

			// Pending: it does not route.
			p, err := store.GetProviderByHost(ctx, "files.beta.example")
			require.NoError(t, err)
			require.Nil(t, p, "a pending domain routed")

			at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			require.NoError(t, store.SetProviderDomainStatus(ctx, d.ID, model.DomainActive, model.DomainCheck{}, at))
			got, err := store.GetProviderDomain(ctx, "FILES.BETA.EXAMPLE")
			require.NoError(t, err)
			require.Equal(t, model.DomainActive, got.Status)
			require.NotNil(t, got.ActiveSince)
			require.True(t, got.ActiveSince.Equal(at), "active since %v", got.ActiveSince)

			p, err = store.GetProviderByHost(ctx, "files.beta.example")
			require.NoError(t, err)
			require.NotNil(t, p, "an active domain routes to its tenant")
			require.Equal(t, beta.ID, p.ID)

			// Staying active keeps the first moment; a suspension clears it.
			require.NoError(t, store.SetProviderDomainStatus(ctx, d.ID, model.DomainActive, model.DomainCheck{}, at.Add(6*time.Hour)))
			got, err = store.GetProviderDomainByID(ctx, d.ID)
			require.NoError(t, err)
			require.True(t, got.ActiveSince.Equal(at))
			require.NoError(t, store.SetProviderDomainStatus(ctx, d.ID, model.DomainSuspended, model.DomainCheck{Text: "points at other.example", Code: model.DomainWhyPointsElsewhere, Params: map[string]string{"found": "other.example", "target": "acme.tenants.files.example"}}, at.Add(12*time.Hour)))
			got, err = store.GetProviderDomainByID(ctx, d.ID)
			require.NoError(t, err)
			require.Equal(t, model.DomainSuspended, got.Status)
			require.Equal(t, "points at other.example", got.LastError)
			require.Equal(t, model.DomainWhyPointsElsewhere, got.LastErrorCode)
			require.Equal(t, map[string]string{"found": "other.example", "target": "acme.tenants.files.example"}, got.LastErrorParams, "the names a screen says it with, on every engine")
			require.Nil(t, got.ActiveSince)
			p, err = store.GetProviderByHost(ctx, "files.beta.example")
			require.NoError(t, err)
			require.Nil(t, p, "a suspended domain stops routing")
			// Back: what was wrong is cleared with it.
			require.NoError(t, store.SetProviderDomainStatus(ctx, d.ID, model.DomainActive, model.DomainCheck{}, at.Add(18*time.Hour)))
			got, err = store.GetProviderDomainByID(ctx, d.ID)
			require.NoError(t, err)
			require.Equal(t, "", got.LastErrorCode)
			require.Empty(t, got.LastErrorParams)
			require.NoError(t, store.SetProviderDomainStatus(ctx, d.ID, model.DomainSuspended, model.DomainCheck{Text: "no record", Code: model.DomainWhyNoRecord}, at.Add(24*time.Hour)))

			// A brought certificate: stored, the key as given (sealed by the
			// caller), and removable.
			notAfter := at.Add(90 * 24 * time.Hour)
			require.NoError(t, store.SetProviderDomainCert(ctx, d.ID, "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n", "sealed:v1:key", &notAfter))
			got, err = store.GetProviderDomainByID(ctx, d.ID)
			require.NoError(t, err)
			require.True(t, got.HasOwnCertificate())
			require.True(t, got.TLSNotAfter.Equal(notAfter))
			require.NoError(t, store.SetProviderDomainCert(ctx, d.ID, "", "", nil))
			got, err = store.GetProviderDomainByID(ctx, d.ID)
			require.NoError(t, err)
			require.False(t, got.HasOwnCertificate())
			require.Nil(t, got.TLSNotAfter)

			list, err := store.ListProviderDomains(ctx, beta.ID)
			require.NoError(t, err)
			require.Len(t, list, 1)
			all, err := store.ListProviderDomains(ctx, 0)
			require.NoError(t, err)
			require.Len(t, all, 1)
			mine, err := store.ListProviderDomains(ctx, acme.ID)
			require.NoError(t, err)
			require.Empty(t, mine)

			// The platform subdomain: <realm>.<tenant domain>, only while the
			// installation has one, never the platform's own tenant, and a
			// tenant's `host` still answers first.
			p, err = store.GetProviderByHost(ctx, "beta.tenants.files.example")
			require.NoError(t, err)
			require.Nil(t, p, "no tenant domain, no subdomain")
			db.SetTenantDomain("Tenants.Files.Example.")
			p, err = store.GetProviderByHost(ctx, "beta.tenants.files.example")
			require.NoError(t, err)
			require.NotNil(t, p)
			require.Equal(t, beta.ID, p.ID)
			p, err = store.GetProviderByHost(ctx, "x.beta.tenants.files.example")
			require.NoError(t, err)
			require.Nil(t, p, "one label in front of the tenant domain, no more")
			p, err = store.GetProviderByHost(ctx, "nobody.tenants.files.example")
			require.NoError(t, err)
			require.Nil(t, p)
			p, err = store.GetProviderByHost(ctx, "files.acme.example")
			require.NoError(t, err)
			require.Equal(t, acme.ID, p.ID)
			require.Equal(t, "beta.tenants.files.example", db.PlatformSubdomain(beta))
			plat, err := store.GetSupertenant(ctx)
			require.NoError(t, err)
			require.Equal(t, "", db.PlatformSubdomain(plat))

			// A suspended tenant answers on none of its addresses.
			beta.Enabled = false
			require.NoError(t, store.UpdateProvider(ctx, beta))
			require.NoError(t, store.SetProviderDomainStatus(ctx, d.ID, model.DomainActive, model.DomainCheck{}, at))
			for _, h := range []string{"beta.tenants.files.example", "files.beta.example"} {
				p, err = store.GetProviderByHost(ctx, h)
				require.NoError(t, err)
				require.Nil(t, p, "a suspended tenant answered on %s", h)
			}

			require.NoError(t, store.DeleteProviderDomain(ctx, d.ID))
			gone, err := store.GetProviderDomain(ctx, "files.beta.example")
			require.NoError(t, err)
			require.Nil(t, gone)
		})
	}
}
