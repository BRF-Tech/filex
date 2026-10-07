// Package tenancy is the multi-tenant mode switch: where the mode a server
// runs with comes from, and what turning it on or off would mean.
//
// # Three sources, one answer, read once
//
// The mode is decided at start, in this order:
//
//  1. FILEX_MULTI_TENANT, when it is set at all (on or off);
//  2. the config file's `multi_tenant`, when the file names it;
//  3. the `tenancy.multi_tenant` setting, which Admin → Multi-tenant mode
//     writes (PUT /api/admin/tenancy, handlers/tenancy_admin.go);
//  4. off.
//
// The first two PIN the mode: the switch shows their value, locked, and says
// which one set it. While one of them pins it, the setting is kept equal to
// it (Resolve), so taking the variable away later changes nothing until
// somebody flips the switch; an install that ran with FILEX_MULTI_TENANT=1
// does not fall to maintenance mode because a compose line was deleted.
//
// # Why a change waits for a restart
//
// The mode is read ONCE, by server.New, and handed to everything that
// enforces it as a plain value: the HTTP route groups (auth.TenantResolver),
// about thirty handlers, the sign-in providers (authsetup.Live and the
// environment's LDAP and header drivers), the SFTP, FTPS, NFS, WebDAV and S3
// servers, the tenant domain checker, the sign-in handoff store, the apps'
// user scope and the encryption policy's first-start carry. Several of them
// are listeners and drivers built once at start. Flipping some of them live
// and not the others would run a server that enforces tenancy on one door
// and not on the next, so a saved change takes effect at the next start and
// the switch says so ("restart required") until then. The capabilities
// answer (`multi_tenant`) always tells the mode IN FORCE, which is the one
// every screen must follow.
//
// # Turning it off with tenants: maintenance mode, nothing deleted
//
// Off on an install that has tenants besides the platform's own is the
// maintenance mode of docs/MULTI-TENANCY.md: only the platform's own tenant
// signs in (auth.LoginBlockReason), a session or API key of a tenant's
// account stops working (auth.MaintenanceLockout), and no row is touched.
// Turning it back on brings every tenant back as it was. The switch asks
// for the number of tenants before it turns the mode off with tenants
// present, so it cannot be done by a stray click.
package tenancy

import (
	"context"
	"log/slog"
	"strings"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/dbsetting"
	"github.com/brf-tech/filex/backend/internal/model"
)

// SettingKey is the settings-table row the switch writes.
const SettingKey = "tenancy.multi_tenant"

// Setting is the switch as a dbsetting spec: off unless saved on.
var Setting = dbsetting.BoolSpec{Key: SettingKey, Default: false}

// EnvVar is the environment variable that pins the mode.
const EnvVar = "FILEX_MULTI_TENANT"

// Audit actions of the switch (PUT /api/admin/tenancy).
const (
	ActionEnable  = "tenancy.enable"
	ActionDisable = "tenancy.disable"
	// AuditTarget is the target type of both rows.
	AuditTarget = "tenancy"
)

// Who pinned the mode, as the switch reports it (`locked_by`).
const (
	LockedByEnvironment = "environment"
	LockedByConfigFile  = "config_file"
)

// LockedBy says what pinned the mode in cfg: LockedByEnvironment,
// LockedByConfigFile, or "" when nothing did and the switch decides.
func LockedBy(cfg config.Config) string {
	switch {
	case cfg.MultiTenantFrom == EnvVar:
		return LockedByEnvironment
	case strings.HasPrefix(cfg.MultiTenantFrom, "file:"):
		return LockedByConfigFile
	}
	return ""
}

// Resolve settles the mode this start runs with and puts it in
// cfg.MultiTenant, which is what every consumer reads. Call it once, before
// anything reads cfg.MultiTenant.
//
// A mode pinned by the environment or the config file stays what it is, and
// the setting is brought in line with it (written only when it differs). A
// mode nothing pins is the setting's, off when there is none. A store that
// cannot be read leaves an unpinned mode off: the same answer as an install
// that never saved the switch, and the one that never widens what anybody
// can see.
func Resolve(ctx context.Context, st dbsetting.Store, cfg *config.Config) {
	if cfg == nil {
		return
	}
	if LockedBy(*cfg) != "" {
		if st == nil {
			return
		}
		want := dbsetting.FormatBool(cfg.MultiTenant)
		if cur, err := st.GetSetting(ctx, SettingKey); err == nil && strings.TrimSpace(cur) == want {
			return
		}
		if err := st.UpsertSetting(ctx, SettingKey, want); err != nil {
			slog.Warn("tenancy: could not keep the multi-tenant setting in line with "+cfg.MultiTenantFrom,
				slog.Any("err", err))
		}
		return
	}
	cfg.MultiTenant = Setting.Resolve(ctx, st)
}

// Saved is the switch's stored value, and false for ok when there is none
// (never saved, or unreadable).
func Saved(ctx context.Context, g dbsetting.Getter) (on bool, ok bool) {
	if g == nil {
		return false, false
	}
	raw, err := g.GetSetting(ctx, SettingKey)
	if err != nil {
		return false, false
	}
	return dbsetting.ParseBool(raw)
}

// NextStart is the mode the next start will run with, as far as this
// process can tell: a pinned mode stays (the environment and the file are
// read again at start, and this process cannot see a change to them), an
// unpinned one is the saved setting, off when there is none.
func NextStart(ctx context.Context, g dbsetting.Getter, cfg config.Config) bool {
	if LockedBy(cfg) != "" {
		return cfg.MultiTenant
	}
	on, _ := Saved(ctx, g)
	return on
}

// ProviderLister is the one store read CountTenants needs.
type ProviderLister interface {
	ListProviders(ctx context.Context) ([]*model.Provider, error)
}

// CountTenants is how many tenants the install has besides the platform's own
// (the supertenant): the ones maintenance mode would lock out.
func CountTenants(ctx context.Context, st ProviderLister) (int, error) {
	if st == nil {
		return 0, nil
	}
	ps, err := st.ListProviders(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range ps {
		if p != nil && !p.IsSupertenant {
			n++
		}
	}
	return n, nil
}
