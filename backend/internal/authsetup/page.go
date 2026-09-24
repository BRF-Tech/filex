package authsetup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// Managed lists the providers the admin page can configure, in the order
// they are added to the chain. `local` and `api-token` are not among them:
// password sign-in is the environment's to decide (see the package comment)
// and API tokens are always on.
var Managed = []string{"oidc", "ldap", "proxy-header"}

// IsManaged reports whether the page may configure a provider.
func IsManaged(name string) bool {
	name = Canonical(name)
	for _, m := range Managed {
		if m == name {
			return true
		}
	}
	return false
}

// FieldKind is how a page field is stored and handed to the driver.
type FieldKind string

// Field kinds.
const (
	FieldText   FieldKind = "text"
	FieldSecret FieldKind = "secret"
	FieldBool   FieldKind = "bool"
)

// Field is one setting a managed provider takes on the page.
type Field struct {
	Key      string    `json:"key"`
	Kind     FieldKind `json:"kind"`
	Required bool      `json:"required,omitempty"`
	// Default is what an absent value means, said so the page can show it.
	Default string `json:"default,omitempty"`
}

// Schema is every field the page may store per provider. A key outside it is
// dropped on save: the settings table is not a scratchpad, and an unknown key
// is either a typo or a client that means something this server does not do.
var Schema = map[string][]Field{
	"oidc": {
		{Key: "issuer", Kind: FieldText, Required: true},
		{Key: "client_id", Kind: FieldText, Required: true},
		{Key: "client_secret", Kind: FieldSecret},
		{Key: "redirect_url", Kind: FieldText},
		{Key: "scopes", Kind: FieldText},
		{Key: "role_claim", Kind: FieldText},
		{Key: "admin_group", Kind: FieldText},
	},
	"ldap": {
		{Key: "url", Kind: FieldText, Required: true},
		{Key: "base_dn", Kind: FieldText, Required: true},
		{Key: "bind_dn", Kind: FieldText},
		{Key: "bind_password", Kind: FieldSecret},
		{Key: "user_filter", Kind: FieldText},
		{Key: "email_attr", Kind: FieldText},
		{Key: "start_tls", Kind: FieldBool},
		{Key: "ca_file", Kind: FieldText},
		{Key: "protocol_login", Kind: FieldBool, Default: "true"},
	},
	"proxy-header": {
		{Key: "trusted_proxies", Kind: FieldText, Required: true},
		{Key: "header_user", Kind: FieldText},
		{Key: "header_email", Kind: FieldText},
		{Key: "header_name", Kind: FieldText},
		{Key: "header_roles", Kind: FieldText},
		{Key: "admin_role", Kind: FieldText},
		{Key: "auto_provision", Kind: FieldBool, Default: "true"},
	},
}

// FieldOf returns a provider's field by key.
func FieldOf(name, key string) (Field, bool) {
	for _, f := range Schema[Canonical(name)] {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// Settings keys. `auth.<name>.<field>` is the shape the page always wrote;
// two control keys sit beside the fields.
const (
	keyEnabled = "enabled"
	// keyLegacy marks a provider whose rows were saved before v0.43.0, when
	// the page applied nothing. They are imported switched OFF and say so.
	keyLegacy = "legacy_unapplied"
	// SchemaSetting records that the one-time upgrade below has run.
	SchemaSetting = "auth.providers.schema"
	schemaV2      = "2"
)

func settingKey(name, field string) string { return "auth." + Canonical(name) + "." + field }

// Stored is what the settings table holds for one managed provider.
type Stored struct {
	Name    string
	Enabled bool
	// Legacy: saved before v0.43.0 and never applied — review and enable.
	Legacy bool
	// Values are the stored field values; a secret stays SEALED here.
	Values map[string]string
	// Exists: at least one row is stored for this provider.
	Exists bool
}

// LoadStored reads every managed provider's rows.
func LoadStored(ctx context.Context, store db.Store) (map[string]*Stored, error) {
	all, err := store.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]*Stored{}
	for _, name := range Managed {
		out[name] = &Stored{Name: name, Values: map[string]string{}}
	}
	for k, v := range all {
		if !strings.HasPrefix(k, "auth.") {
			continue
		}
		rest := strings.TrimPrefix(k, "auth.")
		i := strings.LastIndex(rest, ".")
		if i <= 0 {
			continue
		}
		name, field := Canonical(rest[:i]), rest[i+1:]
		s, ok := out[name]
		if !ok {
			continue
		}
		s.Exists = true
		switch field {
		case keyEnabled:
			s.Enabled = truthy(v)
		case keyLegacy:
			s.Legacy = truthy(v)
		default:
			if _, known := FieldOf(name, field); known {
				s.Values[field] = v
			}
		}
	}
	return out, nil
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ErrSecretKeyRequired: a secret was given and there is no FILEX_SECRET_KEY
// to seal it with. A secret is never stored in the clear.
var ErrSecretKeyRequired = errors.New("a client secret or bind password is stored sealed, and that needs FILEX_SECRET_KEY")

// Change is what a save asks for. Values holds the fields sent; a secret sent
// as "" or "***" keeps the stored one (the page never has it to send back).
type Change struct {
	Enabled *bool
	Values  map[string]any
}

// Merge applies a change to what is stored, without writing anything, and
// returns the next stored state plus the names of the fields that changed
// (never their values — this list goes into the audit row).
func Merge(cur *Stored, ch Change, box *secretbox.Box) (*Stored, []string, error) {
	next := &Stored{Name: cur.Name, Enabled: cur.Enabled, Legacy: cur.Legacy, Exists: true, Values: map[string]string{}}
	for k, v := range cur.Values {
		next.Values[k] = v
	}
	var changed []string
	for k, raw := range ch.Values {
		f, ok := FieldOf(cur.Name, k)
		if !ok || raw == nil {
			continue
		}
		v := stringify(raw)
		switch f.Kind {
		case FieldSecret:
			if v == "" || v == secretMask {
				continue
			}
			if box == nil || !box.Enabled() {
				return nil, nil, ErrSecretKeyRequired
			}
			sealed, err := box.Seal(v)
			if err != nil {
				return nil, nil, err
			}
			next.Values[k] = sealed
			changed = append(changed, k)
		case FieldBool:
			b := "false"
			if truthy(v) {
				b = "true"
			}
			if cur.Values[k] != b {
				next.Values[k] = b
				changed = append(changed, k)
			}
		default:
			v = strings.TrimSpace(v)
			if cur.Values[k] != v {
				next.Values[k] = v
				changed = append(changed, k)
			}
		}
	}
	if ch.Enabled != nil {
		next.Enabled = *ch.Enabled
	}
	sort.Strings(changed)
	return next, changed, nil
}

// secretMask is what a list shows for a stored secret; sent back, it means
// "keep it".
const secretMask = "***"

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case []any:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			parts = append(parts, stringify(x))
		}
		return strings.Join(parts, ", ")
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

// Save writes a provider's next state. A save is a review: it clears the
// "saved before v0.43.0, never applied" mark.
func Save(ctx context.Context, store db.Store, next *Stored) error {
	for k, v := range next.Values {
		if err := store.UpsertSetting(ctx, settingKey(next.Name, k), v); err != nil {
			return err
		}
	}
	if err := store.UpsertSetting(ctx, settingKey(next.Name, keyLegacy), "false"); err != nil {
		return err
	}
	en := "false"
	if next.Enabled {
		en = "true"
	}
	return store.UpsertSetting(ctx, settingKey(next.Name, keyEnabled), en)
}

// DriverConfig turns stored rows into the map the driver's Init reads —
// secrets opened, booleans as booleans, defaults filled — plus whether an
// LDAP provider also judges passwords on the file protocols.
//
// ⚠ The opened map lives in memory only, for the length of a build or a
// test. It is never logged and never sent anywhere but the driver.
func DriverConfig(s *Stored, box *secretbox.Box, opts Options) (map[string]any, bool, error) {
	cfg := map[string]any{}
	for _, f := range Schema[s.Name] {
		v, ok := s.Values[f.Key]
		if !ok || v == "" {
			v = f.Default
		}
		switch f.Kind {
		case FieldSecret:
			if v == "" {
				continue
			}
			plain, err := box.Open(v)
			if err != nil {
				return nil, false, fmt.Errorf("%s cannot be opened: %w", f.Key, err)
			}
			cfg[f.Key] = plain
		case FieldBool:
			cfg[f.Key] = truthy(v)
		default:
			if v != "" {
				cfg[f.Key] = v
			}
		}
	}
	directory := false
	switch s.Name {
	case "oidc":
		if cfg["redirect_url"] == nil && opts.PublicURL != "" {
			cfg["redirect_url"] = strings.TrimRight(opts.PublicURL, "/") + "/api/auth/oidc/callback"
		}
		// The driver always asks for openid, profile and email; the page's
		// scopes box is a space- or comma-separated list of EXTRA ones.
		if sc, _ := cfg["scopes"].(string); sc != "" {
			var extra []string
			for _, x := range strings.FieldsFunc(sc, func(r rune) bool { return r == ' ' || r == ',' }) {
				switch x {
				case "openid", "profile", "email", "":
				default:
					extra = append(extra, x)
				}
			}
			cfg["scopes"] = extra
		}
	case "ldap":
		cfg["multi_tenant"] = opts.MultiTenant
		directory, _ = cfg["protocol_login"].(bool)
	case "proxy-header":
		cfg["multi_tenant"] = opts.MultiTenant
	}
	return cfg, directory, nil
}

// DraftConfig is the configuration "Test now" checks: what is stored with the
// form's unsaved values laid over it. A secret the form left blank, or sent
// back as "***", is the stored one — opened for the test, never returned.
func DraftConfig(s *Stored, draft map[string]any, box *secretbox.Box, opts Options) (map[string]any, error) {
	tmp := &Stored{Name: s.Name, Values: map[string]string{}}
	for k, v := range s.Values {
		tmp.Values[k] = v
	}
	for k, raw := range draft {
		f, ok := FieldOf(s.Name, k)
		if !ok || raw == nil {
			continue
		}
		v := stringify(raw)
		if f.Kind == FieldSecret && (v == "" || v == secretMask) {
			continue
		}
		// A typed secret goes in as it is: Open passes an unsealed value
		// through unchanged, so the driver sees exactly what was typed.
		tmp.Values[k] = v
	}
	cfg, _, err := DriverConfig(tmp, box, opts)
	return cfg, err
}

// UpgradeLegacy runs once per database: rows the page saved before v0.43.0
// were never applied by any server, so turning them on now would switch on,
// on upgrade, a sign-in configuration an operator may have typed long ago
// and forgotten. Each such provider is imported SWITCHED OFF and marked
// "saved before this version, never applied" for the page to show; the
// operator reviews and enables it.
//
// A secret among those rows is sealed with FILEX_SECRET_KEY. With no key it
// cannot be sealed, and it is not kept in the clear either: it is cleared
// (it was never used), the page shows it as not set, and the log says which
// provider and which field — never the value.
func UpgradeLegacy(ctx context.Context, store db.Store, box *secretbox.Box, log *slog.Logger) error {
	if v, err := store.GetSetting(ctx, SchemaSetting); err == nil && v == schemaV2 {
		return nil
	}
	stored, err := LoadStored(ctx, store)
	if err != nil {
		return err
	}
	for _, name := range Managed {
		s := stored[name]
		if !s.Exists {
			continue
		}
		for _, f := range Schema[name] {
			v := s.Values[f.Key]
			if f.Kind != FieldSecret || v == "" || secretbox.IsSealed(v) {
				continue
			}
			if box != nil && box.Enabled() {
				sealed, err := box.Seal(v)
				if err != nil {
					return err
				}
				if err := store.UpsertSetting(ctx, settingKey(name, f.Key), sealed); err != nil {
					return err
				}
				continue
			}
			if err := store.UpsertSetting(ctx, settingKey(name, f.Key), ""); err != nil {
				return err
			}
			log.Warn("auth: a secret saved on the identity providers page before v0.43.0 was cleared: it cannot be stored sealed without FILEX_SECRET_KEY, and it was never used",
				slog.String("provider", name), slog.String("field", f.Key))
		}
		if err := store.UpsertSetting(ctx, settingKey(name, keyEnabled), "false"); err != nil {
			return err
		}
		if err := store.UpsertSetting(ctx, settingKey(name, keyLegacy), "true"); err != nil {
			return err
		}
		log.Warn("auth: identity provider settings saved before v0.43.0 were never applied; imported switched OFF — review them on Admin → Identity providers and enable them there",
			slog.String("provider", name))
	}
	return store.UpsertSetting(ctx, SchemaSetting, schemaV2)
}
