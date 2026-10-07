package tenancy_test

import (
	"context"
	"testing"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenancy"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// The switch decides the mode only where nothing pins it (#167).
func TestResolve_TheSwitchDecidesWhereNothingPinsIt(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)

	cfg := config.Default()
	tenancy.Resolve(ctx, store, &cfg)
	if cfg.MultiTenant {
		t.Fatal("never saved, nothing pinned: the mode is off")
	}

	if err := store.UpsertSetting(ctx, tenancy.SettingKey, "true"); err != nil {
		t.Fatal(err)
	}
	cfg = config.Default()
	tenancy.Resolve(ctx, store, &cfg)
	if !cfg.MultiTenant {
		t.Fatal("saved on, nothing pinned: the next start runs multi-tenant")
	}
	if !tenancy.NextStart(ctx, store, cfg) {
		t.Fatal("NextStart follows the saved switch")
	}
}

// FILEX_MULTI_TENANT (or the config file) wins over the switch, and the
// setting is kept in line with it, so taking the variable away later changes
// nothing until somebody flips the switch.
func TestResolve_APinnedModeWinsAndTheSettingFollowsIt(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	if err := store.UpsertSetting(ctx, tenancy.SettingKey, "false"); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.MultiTenant, cfg.MultiTenantFrom = true, tenancy.EnvVar
	tenancy.Resolve(ctx, store, &cfg)
	if !cfg.MultiTenant {
		t.Fatal("the variable says on: the saved off must not win")
	}
	if got := tenancy.LockedBy(cfg); got != tenancy.LockedByEnvironment {
		t.Fatalf("LockedBy = %q, want %q", got, tenancy.LockedByEnvironment)
	}
	on, ok := tenancy.Saved(ctx, store)
	if !ok || !on {
		t.Fatalf("the setting follows the variable: saved=%v ok=%v", on, ok)
	}

	// The variable is gone at the next start: the mode stays on.
	next := config.Default()
	tenancy.Resolve(ctx, store, &next)
	if !next.MultiTenant {
		t.Fatal("removing FILEX_MULTI_TENANT=1 must not drop the install into maintenance mode")
	}

	file := config.Default()
	file.MultiTenantFrom = "file:/etc/filex/config.yaml"
	if got := tenancy.LockedBy(file); got != tenancy.LockedByConfigFile {
		t.Fatalf("LockedBy(file) = %q, want %q", got, tenancy.LockedByConfigFile)
	}
}

// Off on an install with tenants is maintenance mode: the count the switch
// asks for is every tenant but the platform's own.
func TestCountTenants_EveryTenantButThePlatformsOwn(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)

	n, err := tenancy.CountTenants(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("a fresh install has only the platform's own tenant: got %d", n)
	}
	for _, slug := range []string{"acme", "beta"} {
		if _, err := store.CreateProvider(ctx, &model.Provider{Slug: slug, Name: slug, AuthType: model.AuthTypeLocal, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err = tenancy.CountTenants(ctx, store); err != nil || n != 2 {
		t.Fatalf("CountTenants = %d, %v; want 2", n, err)
	}
}
