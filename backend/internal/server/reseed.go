package server

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/antivirus"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/dbsetting"
	"github.com/brf-tech/filex/backend/internal/drafts"
	"github.com/brf-tech/filex/backend/internal/instancesettings"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/usage"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// dbSeeders is every setting a variable seeds through package dbsetting, in
// ONE list: the boot seeds them (server.go) and `filex settings reseed`
// writes the environment's value over them on request (ReseedFromConfig). A
// spec a package adds to its own list (antivirus.Settings, search.Settings,
// usage.SettingSpecs) arrives here by itself.
func dbSeeders() []dbsetting.Seeder {
	out := append([]dbsetting.Seeder{}, antivirus.Settings()...)
	out = append(out, usage.SettingSpecs()...)
	out = append(out, search.Settings()...)
	out = append(out,
		drafts.LimitSetting,
		// The thumbnail settings (thumb/svglimits.go, thumb/office.go),
		// edited on Admin → Settings → General.
		thumb.SVGMaxMBSetting, thumb.SVGTimeoutSetting, thumb.FolderPreviewsSetting,
		thumb.OfficeMaxMBSetting, thumb.OfficeSlotsSetting,
		// Whether an app granted files:e2e-plaintext may edit a file of an
		// encrypted folder (wasmplugin/e2eplain.go, filex 0.56).
		wasmplugin.E2EPlaintextSetting,
	)
	return out
}

// ReseedFromConfig writes the CONFIGURATION's value - the environment over
// config.yaml - over what is stored, for the settings named: the explicit way
// back to the environment since 0.57 (task #258), when a start stopped writing
// it over an administrator's edits on its own. Every setting a variable gives
// a first value to is covered by one of five lists: the instance settings
// (internal/instancesettings), the dbsetting family (dbSeeders), the
// bootstrap seeds (seedFromEnv: the Display name, trash, share links, SMTP),
// the trusted-proxy list (loginguard proxyseed.go) and, with external, the
// external services.
//
// keys names settings by their key; all takes every one. A setting the
// configuration leaves unset is left as it is - except an instance setting,
// which goes back to its default (its variable's absence is a value too).
// The trusted-proxy list is never dropped to `auto` that way: a list typed on
// the page stays when the configuration names none.
// It answers what it wrote, sorted; nothing is written when a key is unknown.
func ReseedFromConfig(ctx context.Context, store db.Store, cfg config.Config, keys []string, all, external bool) ([]string, error) {
	if !all && !external && len(keys) == 0 {
		return nil, fmt.Errorf("name a setting, or give --all or --external")
	}
	known := map[string]string{}
	for _, s := range instancesettings.All() {
		known[s.Key] = "instance"
	}
	for _, s := range dbSeeders() {
		known[s.SettingKey()] = "dbsetting"
	}
	for _, k := range legacySeedKeys {
		known[k] = "legacy"
	}
	known[loginguard.KeyTrustedProxy] = "proxy"
	byKind := map[string][]string{}
	var unknown []string
	for _, k := range keys {
		kind, ok := known[k]
		if !ok {
			unknown = append(unknown, k)
			continue
		}
		byKind[kind] = append(byKind[kind], k)
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("no such setting: %s", strings.Join(unknown, ", "))
	}

	var wrote []string
	if all || len(byKind["instance"]) > 0 {
		var only []string
		if !all {
			only = byKind["instance"]
		}
		done, err := instancesettings.Reseed(ctx, store, &cfg, only)
		wrote = append(wrote, done...)
		if err != nil {
			return wrote, err
		}
	}
	if all || len(byKind["dbsetting"]) > 0 {
		var only []string
		if !all {
			only = byKind["dbsetting"]
		}
		wrote = append(wrote, dbsetting.Reseed(ctx, store, only, dbSeeders()...)...)
	}
	if all || len(byKind["legacy"]) > 0 {
		var only []string
		if !all {
			only = byKind["legacy"]
		}
		wrote = append(wrote, seedLegacySettings(ctx, store, cfg, only, true)...)
	}
	if all || len(byKind["proxy"]) > 0 {
		ok, err := loginguard.ReseedTrustedProxies(ctx, store, cfg.TrustedProxies)
		if err != nil {
			return wrote, err
		}
		if ok {
			wrote = append(wrote, loginguard.KeyTrustedProxy)
		}
	}
	if all || external {
		for _, name := range applyExternalFromConfig(ctx, store, cfg, true) {
			wrote = append(wrote, "external:"+name)
		}
	}
	sort.Strings(wrote)
	return wrote, nil
}
