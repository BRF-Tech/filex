package db_test

// The tenant realm (migration 00073), on every engine.
//
// Two halves, because an upgrade and a fresh install are two different
// programs: the backfill has to give the tenants that ALREADY exist their slug
// of today (and leave the platform's own tenant without one), and the store has
// to give a new tenant its realm once — and never write it again.

import (
	"context"
	"fmt"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// versionBeforeRealm is the last migration that predates the realm column.
const versionBeforeRealm = 72

func TestProviderRealmBackfillOnEveryEngine(t *testing.T) {
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
			sqlDB, err := drv.Open(context.Background(), e.fresh(t, admin))
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })

			goose.SetBaseFS(drv.MigrationsFS())
			defer goose.SetBaseFS(nil)
			require.NoError(t, goose.SetDialect(drv.Dialect()))
			require.NoError(t, goose.UpToContext(context.Background(), sqlDB, ".", versionBeforeRealm))

			// Tenants of a running install: one with a mixed-case slug, which
			// the realm folds, as every realm comparison does.
			for _, slug := range []string{"Acme", "beta-co"} {
				_, err := sqlDB.ExecContext(context.Background(), fmt.Sprintf(
					`INSERT INTO providers (slug, name, is_supertenant, enabled) VALUES ('%s','%s',%s,%s)`,
					slug, slug, boolLit(e.driver, false), boolLit(e.driver, true)))
				require.NoError(t, err, "%s: seeding a pre-upgrade tenant", e.name)
			}

			require.NoError(t, db.Migrate(context.Background(), drv, sqlDB))

			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			def, err := store.GetProviderBySlug(ctx, model.DefaultProviderSlug)
			require.NoError(t, err)
			require.NotNil(t, def)
			require.Empty(t, def.Realm, "%s: the platform's own tenant has no realm", e.name)

			acme, err := store.GetProviderBySlug(ctx, "Acme")
			require.NoError(t, err)
			require.Equal(t, "acme", acme.Realm, "%s: an existing tenant gets its slug, lower-cased", e.name)
			beta, err := store.GetProviderByRealm(ctx, "BETA-CO")
			require.NoError(t, err)
			require.NotNil(t, beta, "%s: a realm is found whatever case it is typed in", e.name)
			require.Equal(t, "beta-co", beta.Slug)

			// The index is unique: a second tenant cannot take a realm.
			_, err = sqlDB.ExecContext(ctx, fmt.Sprintf(
				`INSERT INTO providers (slug, name, realm, is_supertenant, enabled) VALUES ('dup','dup','acme',%s,%s)`,
				boolLit(e.driver, false), boolLit(e.driver, true)))
			require.Error(t, err, "%s: a duplicate realm was accepted", e.name)
		})
	}
}

func TestProviderRealmStoreOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			// No realm given: the slug, lower-cased.
			a, err := store.CreateProvider(ctx, &model.Provider{Slug: "Acme", Name: "Acme", Enabled: true})
			require.NoError(t, err)
			require.Equal(t, "acme", a.Realm)

			// A realm given: that one, folded.
			b, err := store.CreateProvider(ctx, &model.Provider{Slug: "b-slug", Realm: " Beta ", Name: "Beta", Enabled: true})
			require.NoError(t, err)
			require.Equal(t, "beta", b.Realm)

			// Created as the platform's own tenant: no realm.
			s, err := store.CreateProvider(ctx, &model.Provider{Slug: "platform2", Realm: "ignored", Name: "P", IsSupertenant: true, Enabled: true})
			require.NoError(t, err)
			require.Empty(t, s.Realm)

			got, err := store.GetProviderByRealm(ctx, "BETA")
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, b.ID, got.ID)
			none, err := store.GetProviderByRealm(ctx, "")
			require.NoError(t, err)
			require.Nil(t, none, "an empty realm is nobody's")
			none, err = store.GetProviderByRealm(ctx, "nobody")
			require.NoError(t, err)
			require.Nil(t, none)

			// ⚠ The realm never changes: an update that carries another one —
			// and another slug — leaves it exactly as it was.
			b.Realm = "changed"
			b.Slug = "b-renamed"
			require.NoError(t, store.UpdateProvider(ctx, b))
			again, err := store.GetProvider(ctx, b.ID)
			require.NoError(t, err)
			require.Equal(t, "b-renamed", again.Slug)
			require.Equal(t, "beta", again.Realm, "UpdateProvider must not write the realm")
			gone, err := store.GetProviderByRealm(ctx, "changed")
			require.NoError(t, err)
			require.Nil(t, gone)

			// Two tenants cannot share a realm.
			_, err = store.CreateProvider(ctx, &model.Provider{Slug: "other", Realm: "acme", Name: "Other", Enabled: true})
			require.Error(t, err, "a duplicate realm was accepted")

			list, err := store.ListProviders(ctx)
			require.NoError(t, err)
			realms := map[string]string{}
			for _, p := range list {
				realms[p.Slug] = p.Realm
			}
			require.Equal(t, "acme", realms["Acme"])
			require.Equal(t, "", realms[model.DefaultProviderSlug])
		})
	}
}
