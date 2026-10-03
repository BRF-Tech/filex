package db_test

// The SSO identity an account is bound to (migration 00079), on every engine.
//
// Two halves again. The upgrade: existing accounts - some sharing a subject
// in one tenant, which the unique index must not trip on (every issuer is
// NULL then) - get the new columns, and a database that had accounts gets the
// mark that keeps its OIDC providers trusting (authsetup.UpgradeOIDCTrust); a
// fresh one does not. The store: lookups that never leave a tenant, a
// case-sensitive subject (MySQL's default collation ignores case), one
// identity per tenant, and the writes that clear what they should.

import (
	"context"
	"fmt"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// versionBeforeOIDCIdentity is the last migration before 00079.
const versionBeforeOIDCIdentity = 78

func TestOIDCIdentityUpgradeOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			admin := ""
			if e.dsnEnv != "" {
				if admin = envOrSkip(t, e); admin == "" {
					return
				}
			}
			drv, err := db.Get(e.driver)
			require.NoError(t, err)
			ctx := context.Background()

			// A fresh install: no mark.
			fresh, err := drv.Open(ctx, e.fresh(t, admin))
			require.NoError(t, err)
			t.Cleanup(func() { _ = fresh.Close() })
			require.NoError(t, db.Migrate(ctx, drv, fresh))
			v, _ := drv.NewStore(fresh).GetSetting(ctx, "auth.oidc_trust.upgrade")
			assert.Empty(t, v, "%s: a fresh install keeps the default (trust off)", e.name)

			// An upgrade: accounts already there, two of one tenant sharing a
			// subject (nothing ever made it unique).
			sqlDB, err := drv.Open(ctx, e.fresh(t, admin))
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			goose.SetBaseFS(drv.MigrationsFS())
			defer goose.SetBaseFS(nil)
			require.NoError(t, goose.SetDialect(drv.Dialect()))
			require.NoError(t, goose.UpToContext(ctx, sqlDB, ".", versionBeforeOIDCIdentity))
			for i, email := range []string{"a@x.test", "b@x.test"} {
				_, err := sqlDB.ExecContext(ctx, fmt.Sprintf(
					`INSERT INTO users (email, role, locale, timezone, provider_id, oidc_subject) VALUES ('%s','user','en','UTC',(SELECT id FROM providers WHERE slug='default'),'same-sub')`, email))
				require.NoError(t, err, "%s: seeding account %d", e.name, i)
			}
			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: the upgrade must not fail on a shared subject", e.name)
			store := drv.NewStore(sqlDB)
			v, err = store.GetSetting(ctx, "auth.oidc_trust.upgrade")
			require.NoError(t, err)
			assert.Equal(t, "pending", v, "%s: an upgraded database is marked", e.name)
			a, err := store.GetUserByEmail(ctx, "a@x.test")
			require.NoError(t, err)
			assert.Equal(t, "same-sub", a.OIDCSubject)
			assert.Empty(t, a.OIDCIssuer)
			assert.True(t, a.SSOLinked)
			assert.Empty(t, a.DisabledReason)

			// And back down, and up again: what a rollback and a retry do.
			// ⚠ db.Migrate clears goose's base FS on its way out.
			goose.SetBaseFS(drv.MigrationsFS())
			require.NoError(t, goose.DownToContext(ctx, sqlDB, ".", versionBeforeOIDCIdentity), "%s: down", e.name)
			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: up again", e.name)
		})
	}
}

func TestOIDCIdentityStoreOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			super, err := store.GetSupertenant(ctx)
			require.NoError(t, err)
			require.NotNil(t, super)
			beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", Enabled: true})
			require.NoError(t, err)
			mainT := db.UserTenant{ProviderID: super.ID, Main: true}
			betaT := db.UserTenant{ProviderID: beta.ID}

			ada, err := store.CreateUser(ctx, "ada@x.test", "", model.RoleUser, "en", model.TimezoneUnset)
			require.NoError(t, err)
			bob, err := store.CreateUser(ctx, "bob@x.test", "", model.RoleUser, "en", model.TimezoneUnset)
			require.NoError(t, err)
			require.NoError(t, store.SetUserProvider(ctx, bob.ID, beta.ID, ""))

			// By address, inside one tenant only.
			got, err := store.GetUserInTenantByEmail(ctx, mainT, "ada@x.test")
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, ada.ID, got.ID)
			got, err = store.GetUserInTenantByEmail(ctx, mainT, "bob@x.test")
			require.NoError(t, err)
			assert.Nil(t, got, "%s: beta's account is not the platform's", e.name)
			got, err = store.GetUserInTenantByEmail(ctx, betaT, "ada@x.test")
			require.NoError(t, err)
			assert.Nil(t, got)

			// By identity, exact and inside the tenant.
			require.NoError(t, store.SetUserOIDCIdentity(ctx, ada.ID, "https://idp.example", "Sub-A"))
			got, err = store.GetUserByOIDCIdentity(ctx, mainT, "https://idp.example", "Sub-A")
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, ada.ID, got.ID)
			assert.True(t, got.SSOLinked)
			got, err = store.GetUserByOIDCIdentity(ctx, mainT, "https://idp.example", "sub-a")
			require.NoError(t, err)
			assert.Nil(t, got, "%s: a subject is case-sensitive", e.name)
			got, err = store.GetUserByOIDCIdentity(ctx, betaT, "https://idp.example", "Sub-A")
			require.NoError(t, err)
			assert.Nil(t, got, "%s: another tenant's identity", e.name)

			// One identity, one account per tenant.
			carl, err := store.CreateUser(ctx, "carl@x.test", "", model.RoleUser, "en", model.TimezoneUnset)
			require.NoError(t, err)
			assert.Error(t, store.SetUserOIDCIdentity(ctx, carl.ID, "https://idp.example", "Sub-A"), "%s: a second account bound to the same identity", e.name)
			require.NoError(t, store.SetUserOIDCIdentity(ctx, bob.ID, "https://idp.example", "Sub-A"), "%s: the same identity in another tenant is another account", e.name)

			// Removed by an administrator; dropped by a move to another tenant.
			had, err := store.ClearUserOIDCIdentity(ctx, ada.ID)
			require.NoError(t, err)
			assert.True(t, had)
			had, err = store.ClearUserOIDCIdentity(ctx, ada.ID)
			require.NoError(t, err)
			assert.False(t, had, "%s: nothing left to remove", e.name)
			got, err = store.GetUser(ctx, ada.ID)
			require.NoError(t, err)
			assert.False(t, got.SSOLinked)
			require.NoError(t, store.SetUserProvider(ctx, bob.ID, super.ID, ""))
			got, err = store.GetUser(ctx, bob.ID)
			require.NoError(t, err)
			assert.Empty(t, got.OIDCIssuer, "%s: a moved account is bound to nobody", e.name)
			assert.Empty(t, got.OIDCSubject)

			// Switched off by the server, then by an administrator.
			require.NoError(t, store.SetUserDisabledReason(ctx, carl.ID, model.DisabledPendingApproval))
			got, err = store.GetUser(ctx, carl.ID)
			require.NoError(t, err)
			assert.False(t, got.Enabled)
			assert.Equal(t, model.DisabledPendingApproval, got.DisabledReason)
			require.NoError(t, store.SetUserEnabled(ctx, carl.ID, true))
			got, err = store.GetUser(ctx, carl.ID)
			require.NoError(t, err)
			assert.True(t, got.Enabled)
			assert.Empty(t, got.DisabledReason, "%s: the administrator decided", e.name)

			// A tenant's own OIDC's trust.
			require.NoError(t, store.SetProviderOIDCTrustEmail(ctx, beta.ID, true))
			p, err := store.GetProvider(ctx, beta.ID)
			require.NoError(t, err)
			assert.True(t, p.OIDCTrustEmail)
			p.Name = "Beta renamed"
			require.NoError(t, store.UpdateProvider(ctx, p))
			p, err = store.GetProvider(ctx, beta.ID)
			require.NoError(t, err)
			assert.True(t, p.OIDCTrustEmail, "%s: UpdateProvider never writes it", e.name)
			require.NoError(t, store.SetProviderOIDCTrustEmail(ctx, beta.ID, false))
			p, err = store.GetProvider(ctx, beta.ID)
			require.NoError(t, err)
			assert.False(t, p.OIDCTrustEmail)
		})
	}
}
