package perm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// App permissions: what an installed app declares about ITS OWN actions —
// "Request signatures" for the signing app — so an administrator can let one
// role use that action and not another (the maintainer 2026-09-28: "share.links gibi
// rbac olaylarını applere de getirelim"). Signing a document someone sent you
// needs no permission; ASKING people to sign does.
//
// They are not in the catalogue: the catalogue is a fixed 64-bit set, and
// these come and go with the apps installed. A key is "app.<app>.<id>"
// (app.sign.request), persisted BY NAME in the same three places as the rest:
//
//   - a person's exceptions (model overrides — allow / deny);
//   - a custom role's settings (PermRuleSettings.Apps — allow / deny);
//   - the built-in roles' app decisions (SettingPermissionAppDefaults).
//
// When none of them says anything, the app's own manifest default decides
// (AppDefault). An administrator always may. A key whose app is uninstalled
// is simply never asked.

// AppPrefix starts every app permission key.
const AppPrefix = "app."

var appKeyRE = regexp.MustCompile(`^app\.[a-z0-9][a-z0-9_-]{0,63}\.[a-z0-9][a-z0-9_-]{0,63}$`)

// IsAppKey reports whether k names an app permission ("app.sign.request").
func IsAppKey(k string) bool { return appKeyRE.MatchString(k) }

// AppKey builds the key for permission id of app plugin.
func AppKey(plugin, id string) string { return AppPrefix + plugin + "." + id }

// AppDefault is who holds an app permission nobody has decided: the app's
// manifest picks it, the administrator can change it per role and per person.
type AppDefault string

const (
	// AppDefaultViewer: every account (viewers included).
	AppDefaultViewer AppDefault = "viewer"
	// AppDefaultUser: accounts that can change files — not viewers.
	AppDefaultUser AppDefault = "user"
	// AppDefaultAdmin: administrators only, until granted.
	AppDefaultAdmin AppDefault = "admin"
)

// ValidAppDefault reports whether d is one of the three.
func ValidAppDefault(d string) bool {
	switch AppDefault(d) {
	case AppDefaultViewer, AppDefaultUser, AppDefaultAdmin:
		return true
	}
	return false
}

// SourceAppDefault is an app permission decided by the app's own default.
const SourceAppDefault SourceKind = "app_default"

// AppDefaultFor is, for each built-in role (viewer, user, admin), whether an
// account on it holds an app permission whose manifest default is def when
// nobody has decided it: no exception, no custom role, no decision of the
// built-in role. It is AppAllowed itself, asked of such an account, so the
// catalogue (GET /api/admin/roles/catalogue → apps[].default_for) carries the
// answer and the role editors' "Default (allowed / not allowed)" only reads
// it — 0.49.0's editors kept a copy of the last layer of appDecide.
func AppDefaultFor(def AppDefault) map[string]bool {
	out := make(map[string]bool, 3)
	for _, role := range []string{model.RoleViewer, model.RoleUser, model.RoleAdmin} {
		// With nothing decided anywhere, the key is never found: any will do.
		out[role], _ = Resolve(Input{Role: role}).AppAllowed(AppPrefix+"default.for", def)
	}
	return out
}

// AppAllowed decides the app permission key for this account. def is the
// app's manifest default for it. Layers, highest first: the administrator
// role, the person's own exception, their custom role's decision, the
// built-in role's decision, the app default.
func (r *Result) AppAllowed(key string, def AppDefault) (bool, Source) {
	return r.appDecide(key, def, true)
}

// AppInherited is AppAllowed without the person's own exception: what the
// account would get from its role and the app's default alone — the answer
// a person's "Default" choice stands for on their exceptions editor.
func (r *Result) AppInherited(key string, def AppDefault) (bool, Source) {
	return r.appDecide(key, def, false)
}

func (r *Result) appDecide(key string, def AppDefault, withException bool) (bool, Source) {
	if r == nil {
		return false, Source{}
	}
	if r.admin {
		return true, Source{Kind: SourceRole}
	}
	if withException {
		switch r.overrides[key] {
		case model.PermAllow:
			return true, Source{Kind: SourceOverride}
		case model.PermDeny:
			return false, Source{Kind: SourceOverride}
		}
	}
	if c := r.customRole; c != nil {
		switch c.Settings.Apps[key] {
		case model.PermAllow:
			return true, ruleSource(SourceRule, c)
		case model.PermDeny:
			return false, ruleSource(SourceRule, c)
		}
	}
	switch r.builtinApps[key] {
	case model.PermAllow:
		return true, Source{Kind: SourceBase}
	case model.PermDeny:
		return false, Source{Kind: SourceBase}
	}
	switch def {
	case AppDefaultViewer:
		return r.role == model.RoleUser || r.role == model.RoleViewer, Source{Kind: SourceAppDefault}
	case AppDefaultUser:
		return r.role == model.RoleUser, Source{Kind: SourceAppDefault}
	default:
		return false, Source{Kind: SourceAppDefault}
	}
}

// validateAppEffects checks a map of app decisions: every key an app key, every
// effect allow or deny.
func validateAppEffects(m map[string]string) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !IsAppKey(k) {
			return invalid("%q is not an app permission (app.<app>.<id>)", k)
		}
		if !model.ValidPermEffect(m[k]) {
			return invalid("%q: effect must be %q or %q, got %q", k, model.PermAllow, model.PermDeny, m[k])
		}
	}
	return nil
}

// ValidateAppDecisions is SaveAppDecisions' check without the write.
func ValidateAppDecisions(m map[string]string) error { return validateAppEffects(m) }

// AppDecisions is the built-in roles' app decisions:
// {"user": {"app.sign.request": "deny"}, "viewer": {...}}.
type AppDecisions map[string]map[string]string

// LoadAppDecisions reads the built-in roles' app decisions; none is empty.
func LoadAppDecisions(ctx context.Context, store db.Store) (AppDecisions, error) {
	raw, err := store.GetSetting(ctx, model.SettingPermissionAppDefaults)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && strings.TrimSpace(raw) == "") {
		return AppDecisions{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("perm: app decisions: %w", err)
	}
	out := AppDecisions{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("perm: app decisions: %w", err)
	}
	return out, nil
}

// SaveAppDecisions replaces one built-in role's app decisions ({} clears them).
func SaveAppDecisions(ctx context.Context, store db.Store, role string, m map[string]string) error {
	if role != model.RoleUser && role != model.RoleViewer {
		return invalid("role %q has no editable permissions", role)
	}
	if err := validateAppEffects(m); err != nil {
		return err
	}
	all, err := LoadAppDecisions(ctx, store)
	if err != nil {
		return err
	}
	if len(m) == 0 {
		delete(all, role)
	} else {
		all[role] = m
	}
	b, err := json.Marshal(all)
	if err != nil {
		return err
	}
	if err := store.UpsertSetting(ctx, model.SettingPermissionAppDefaults, string(b)); err != nil {
		return err
	}
	Invalidate()
	return nil
}
