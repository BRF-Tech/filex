// Package instancesettings is the one list of the instance's settings that
// lived in the environment alone until 0.57 and are now rows of the settings
// table, changed on the admin panel (task #258, the owner's grouping of
// 2026-10-15): the default language, the sync interval, the upload staging
// time, the file cache, the apps' size limits, co-editing, the thumbnail
// cache sweep, the webhooks' language and the notifications switch.
//
// # One resolution order
//
//	stored row (settings table)  →  the environment / config.yaml, on a start
//	where no row exists yet  →  the built-in default
//
// It is package dbsetting's order (seed, never override), applied to values
// the CONFIGURATION already holds: config.Load has merged the variable over
// the config.yaml key over the default, so a start that has no row writes what
// the configuration says - only when it says something other than the default,
// so a row never pins a default a later release might change (dbsetting
// seedvalue.go). From then on the row wins and the variable is inert; `filex
// settings reseed` is the explicit way to write the configuration's value
// again (Reseed). A start whose configuration says something the row
// overrules says so in the log, once per setting (Drifts): the variable is
// not read again, and nothing else would tell the operator who changed it.
//
// # Read at start, said on the page
//
// Every setting here is read where the server is wired (Boot writes the value
// in force into the Config every consumer is built from), so a change applies
// at the next start - except the few with a live hook (SetLive). The page is
// told which ones wait (`restart_pending`: the stored value differs from the
// one this process started with), the way the Protection page says it for the
// antivirus switch.
//
// # One list
//
// The admin page draws a card per group from this list (GET/PATCH
// /api/admin/instance-settings), the panel's search finds every row, the docs
// list every variable: a setting added here is added everywhere.
package instancesettings

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

// Group is the admin page whose card draws the setting.
type Group string

const (
	// GroupGeneral: Admin → Settings → General.
	GroupGeneral Group = "general"
	// GroupProtection: Admin → Settings → Protection.
	GroupProtection Group = "protection"
	// GroupApps: Admin → Settings → Plugins (the apps' limits, co-editing).
	GroupApps Group = "apps"
	// GroupNotifications: Admin → Settings → Notifications.
	GroupNotifications Group = "notifications"
	// GroupWebhooks: Admin → Settings → Webhooks.
	GroupWebhooks Group = "webhooks"
)

// Kind is how the page draws the setting.
type Kind string

const (
	KindInt  Kind = "int"
	KindBool Kind = "bool"
	// KindLanguage: a language tag, or "" for the setting's own fallback
	// (the browser's language, the instance's default language).
	KindLanguage Kind = "language"
)

// Setting is one row of the list.
type Setting struct {
	Key   string
	Group Group
	Kind  Kind
	// EnvVar and ConfigKey name where the first value came from - shown on
	// the page and in the docs.
	EnvVar    string
	ConfigKey string
	// Unit names what an int counts ("minutes", "hours", "days", "MB", "GB").
	Unit string
	// Restart: read where the server is wired; a change applies at the next
	// start unless a live hook is registered for the key (SetLive).
	Restart bool

	intSpec    *dbsetting.IntSpec
	boolSpec   *dbsetting.BoolSpec
	stringSpec *dbsetting.StringSpec

	// fromConfig answers the configuration's value in the setting's own
	// type, and whether the configuration SET it (differs from the default).
	fromConfig func(c *config.Config) (any, bool)
	// toConfig writes the value in force into the configuration at start.
	toConfig func(c *config.Config, v any)
}

// languageTag accepts "" or a BCP 47-shaped tag ("en", "tr", "pt-BR"). The
// interface offers the languages it has (and a language pack's); the server
// refuses only what cannot be a language at all.
var languageTag = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

func checkLanguage(v string) error {
	if v == "" || languageTag.MatchString(v) {
		return nil
	}
	return fmt.Errorf("%q is not a language tag such as en or tr", v)
}

func normLanguage(raw string) string { return strings.TrimSpace(raw) }

// ceilDiv rounds n/d up: a configuration's 30 s interval is 1 minute, never 0.
func ceilDiv(n, d int64) int {
	if n <= 0 || d <= 0 {
		return 0
	}
	return int((n + d - 1) / d)
}

const gib = int64(1) << 30

func intSetting(s Setting, spec dbsetting.IntSpec) Setting {
	s.Kind = KindInt
	s.intSpec = &spec
	return s
}

func boolSetting(s Setting, spec dbsetting.BoolSpec) Setting {
	s.Kind = KindBool
	s.boolSpec = &spec
	return s
}

func languageSetting(s Setting, key, env string) Setting {
	s.Kind = KindLanguage
	s.stringSpec = &dbsetting.StringSpec{Key: key, EnvVar: env, Normalize: normLanguage, Check: checkLanguage}
	return s
}

// The settings, in the order each card draws them.
var all = []Setting{
	// ── General ────────────────────────────────────────────────────────
	languageSetting(Setting{
		Key: "instance.default_locale", Group: GroupGeneral, EnvVar: "FILEX_DEFAULT_LOCALE", ConfigKey: "default_locale", Restart: true,
		fromConfig: func(c *config.Config) (any, bool) { v := strings.TrimSpace(c.DefaultLocale); return v, v != "" },
		toConfig:   func(c *config.Config, v any) { c.DefaultLocale = v.(string) },
	}, "instance.default_locale", "FILEX_DEFAULT_LOCALE"),
	intSetting(Setting{
		Key: "sync.default_interval_minutes", Group: GroupGeneral, EnvVar: "FILEX_SYNC_INTERVAL", ConfigKey: "sync.default_interval", Unit: "minutes", Restart: true,
		fromConfig: func(c *config.Config) (any, bool) {
			m := ceilDiv(int64(c.Sync.DefaultInterval), int64(time.Minute))
			return m, m > 0 && m != 15
		},
		toConfig: func(c *config.Config, v any) { c.Sync.DefaultInterval = time.Duration(v.(int)) * time.Minute },
	}, dbsetting.IntSpec{Key: "sync.default_interval_minutes", EnvVar: "FILEX_SYNC_INTERVAL", Default: 15, Min: 1, Max: 1440, Unit: "minutes"}),
	intSetting(Setting{
		Key: "upload.staging_ttl_hours", Group: GroupGeneral, EnvVar: "FILEX_UPLOAD_STAGING_TTL", ConfigKey: "upload.staging_ttl", Unit: "hours", Restart: true,
		fromConfig: func(c *config.Config) (any, bool) {
			h := ceilDiv(int64(c.Upload.StagingTTL), int64(time.Hour))
			return h, h > 0 && h != 24
		},
		toConfig: func(c *config.Config, v any) { c.Upload.StagingTTL = time.Duration(v.(int)) * time.Hour },
	}, dbsetting.IntSpec{Key: "upload.staging_ttl_hours", EnvVar: "FILEX_UPLOAD_STAGING_TTL", Default: 24, Min: 1, Max: 720, Unit: "hours"}),
	intSetting(Setting{
		Key: "cache.max_gb", Group: GroupGeneral, EnvVar: "FILEX_CACHE_MAX_BYTES", ConfigKey: "cache.max_bytes", Unit: "GB", Restart: true,
		fromConfig: func(c *config.Config) (any, bool) {
			g := ceilDiv(c.Cache.MaxBytes, gib)
			return g, g > 0 && g != 20
		},
		toConfig: func(c *config.Config, v any) { c.Cache.MaxBytes = int64(v.(int)) * gib },
	}, dbsetting.IntSpec{Key: "cache.max_gb", EnvVar: "FILEX_CACHE_MAX_BYTES", Default: 20, Min: 1, Max: 10240, Unit: "GB"}),
	boolSetting(Setting{
		Key: "thumbs.sweep", Group: GroupGeneral, EnvVar: "FILEX_THUMBS_SWEEP_INTERVAL", ConfigKey: "thumbs.sweep_interval", Restart: true,
		fromConfig: func(c *config.Config) (any, bool) { on := c.Thumbs.SweepInterval > 0; return on, !on },
		toConfig: func(c *config.Config, v any) {
			if !v.(bool) {
				c.Thumbs.SweepInterval = 0
			} else if c.Thumbs.SweepInterval <= 0 {
				c.Thumbs.SweepInterval = 6 * time.Hour
			}
		},
	}, dbsetting.BoolSpec{Key: "thumbs.sweep", Default: true}),
	intSetting(Setting{
		Key: "thumbs.sweep_interval_minutes", Group: GroupGeneral, EnvVar: "FILEX_THUMBS_SWEEP_INTERVAL", ConfigKey: "thumbs.sweep_interval", Unit: "minutes", Restart: true,
		fromConfig: func(c *config.Config) (any, bool) {
			m := ceilDiv(int64(c.Thumbs.SweepInterval), int64(time.Minute))
			return m, m > 0 && m != 360
		},
		// ⚠ After thumbs.sweep: a sweep switched off stays off.
		toConfig: func(c *config.Config, v any) {
			if c.Thumbs.SweepInterval > 0 {
				c.Thumbs.SweepInterval = time.Duration(v.(int)) * time.Minute
			}
		},
	}, dbsetting.IntSpec{Key: "thumbs.sweep_interval_minutes", EnvVar: "FILEX_THUMBS_SWEEP_INTERVAL", Default: 360, Min: 10, Max: 10080, Unit: "minutes"}),

	// ── Protection ─────────────────────────────────────────────────────
	boolSetting(Setting{
		Key: "versions.on_overwrite", Group: GroupProtection, EnvVar: "FILEX_VERSIONS_ON_OVERWRITE", ConfigKey: "versions_on_overwrite", Restart: true,
		fromConfig: func(c *config.Config) (any, bool) { return c.VersionsOnOverwrite, !c.VersionsOnOverwrite },
		toConfig:   func(c *config.Config, v any) { c.VersionsOnOverwrite = v.(bool) },
	}, dbsetting.BoolSpec{Key: "versions.on_overwrite", Default: true}),

	// ── Apps (Plugins) ─────────────────────────────────────────────────
	appMB("apps.max_input_mb", "FILEX_APP_PLUGIN_MAX_INPUT_MB", "app_plugin_max_input_mb", 256, 16384,
		func(c *config.Config) *int { return &c.AppPluginMaxInputMB }),
	appMB("apps.max_output_mb", "FILEX_APP_PLUGIN_MAX_OUTPUT_MB", "app_plugin_max_output_mb", 512, 16384,
		func(c *config.Config) *int { return &c.AppPluginMaxOutputMB }),
	appMB("apps.max_wasm_mb", "FILEX_APP_PLUGIN_MAX_WASM_MB", "app_plugin_max_wasm_mb", 64, 1024,
		func(c *config.Config) *int { return &c.AppPluginMaxWasmMB }),
	appMB("apps.max_ui_mb", "FILEX_APP_PLUGIN_MAX_UI_MB", "app_plugin_max_ui_mb", 128, 1024,
		func(c *config.Config) *int { return &c.AppPluginMaxUIMB }),
	intSetting(Setting{
		Key: "apps.request_ttl_days", Group: GroupApps, EnvVar: "FILEX_PLUGIN_REQUEST_TTL_DAYS", ConfigKey: "plugin_request_ttl_days", Unit: "days", Restart: true,
		fromConfig: func(c *config.Config) (any, bool) {
			return c.PluginRequestTTLDays, c.PluginRequestTTLDays > 0 && c.PluginRequestTTLDays != 14
		},
		toConfig: func(c *config.Config, v any) { c.PluginRequestTTLDays = v.(int) },
	}, dbsetting.IntSpec{Key: "apps.request_ttl_days", EnvVar: "FILEX_PLUGIN_REQUEST_TTL_DAYS", Default: 14, Min: 1, Max: 365, Unit: "days"}),
	intSetting(Setting{
		Key: "coedit.max_members", Group: GroupApps, EnvVar: "FILEX_COEDIT_MAX_MEMBERS", ConfigKey: "coedit.max_members", Unit: "people", Restart: true,
		fromConfig: func(c *config.Config) (any, bool) {
			return c.CoEdit.MaxMembers, c.CoEdit.MaxMembers > 0 && c.CoEdit.MaxMembers != 20
		},
		toConfig: func(c *config.Config, v any) { c.CoEdit.MaxMembers = v.(int) },
	}, dbsetting.IntSpec{Key: "coedit.max_members", EnvVar: "FILEX_COEDIT_MAX_MEMBERS", Default: 20, Min: 2, Max: 200, Unit: "people"}),
	intSetting(Setting{
		Key: "coedit.unsaved_days", Group: GroupApps, EnvVar: "FILEX_COEDIT_UNSAVED_DAYS", ConfigKey: "coedit.unsaved_days", Unit: "days", Restart: true,
		fromConfig: func(c *config.Config) (any, bool) {
			return c.CoEdit.UnsavedDays, c.CoEdit.UnsavedDays > 0 && c.CoEdit.UnsavedDays != 30
		},
		toConfig: func(c *config.Config, v any) { c.CoEdit.UnsavedDays = v.(int) },
	}, dbsetting.IntSpec{Key: "coedit.unsaved_days", EnvVar: "FILEX_COEDIT_UNSAVED_DAYS", Default: 30, Min: 1, Max: 365, Unit: "days"}),

	// ── Notifications ──────────────────────────────────────────────────
	boolSetting(Setting{
		Key: "notify.enabled", Group: GroupNotifications, EnvVar: "FILEX_NOTIFY_ENABLED", ConfigKey: "notify.enabled", Restart: true,
		fromConfig: func(c *config.Config) (any, bool) { return c.Notify.Enabled, !c.Notify.Enabled },
		toConfig:   func(c *config.Config, v any) { c.Notify.Enabled = v.(bool) },
	}, dbsetting.BoolSpec{Key: "notify.enabled", Default: true}),

	// ── Webhooks ───────────────────────────────────────────────────────
	// Live: the notification service reads it per dispatch (SetLive).
	languageSetting(Setting{
		Key: "notify.webhook_lang", Group: GroupWebhooks, EnvVar: "FILEX_WEBHOOK_LANG", ConfigKey: "notify.webhook_lang",
		fromConfig: func(c *config.Config) (any, bool) { v := strings.TrimSpace(c.Notify.WebhookLang); return v, v != "" },
		toConfig:   func(c *config.Config, v any) { c.Notify.WebhookLang = v.(string) },
	}, "notify.webhook_lang", "FILEX_WEBHOOK_LANG"),
}

func appMB(key, env, yaml string, def, max int, field func(*config.Config) *int) Setting {
	return intSetting(Setting{
		Key: key, Group: GroupApps, EnvVar: env, ConfigKey: yaml, Unit: "MB", Restart: true,
		fromConfig: func(c *config.Config) (any, bool) { v := *field(c); return v, v > 0 && v != def },
		toConfig:   func(c *config.Config, v any) { *field(c) = v.(int) },
	}, dbsetting.IntSpec{Key: key, EnvVar: env, Default: def, Min: 1, Max: max, Unit: "MB"})
}

// All is the list, in order.
func All() []Setting { return append([]Setting(nil), all...) }

// ByKey finds one setting.
func ByKey(key string) (Setting, bool) {
	for _, s := range all {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// Default is the built-in value, in the setting's type.
func (s Setting) Default() any {
	switch s.Kind {
	case KindInt:
		return s.intSpec.Default
	case KindBool:
		return s.boolSpec.Default
	default:
		return s.stringSpec.Default
	}
}

// Bounds are an int's limits; zeros for the other kinds.
func (s Setting) Bounds() (min, max int) {
	if s.intSpec != nil {
		return s.intSpec.Min, s.intSpec.Max
	}
	return 0, 0
}

// Resolve reads the value in force: the stored row, else the default.
func (s Setting) Resolve(ctx context.Context, g dbsetting.Getter) any {
	switch s.Kind {
	case KindInt:
		return s.intSpec.Resolve(ctx, g)
	case KindBool:
		return s.boolSpec.Resolve(ctx, g)
	default:
		return s.stringSpec.Resolve(ctx, g)
	}
}

// text is the one spelling a value is stored and compared in.
func (s Setting) text(v any) string {
	switch t := v.(type) {
	case int:
		return strconv.Itoa(t)
	case bool:
		return dbsetting.FormatBool(t)
	case string:
		return t
	}
	return fmt.Sprint(v)
}

// FieldError is a value the page sent that cannot be stored, with what the
// page needs to say why next to the field.
type FieldError struct {
	Key string
	// Code: "range" (an int outside Min..Max), "type" (not a number / not a
	// switch / not text), "language" (not a language tag), "unknown" (no such
	// setting).
	Code     string
	English  string
	Min, Max int
}

func (e *FieldError) Error() string { return e.Key + ": " + e.English }

// Canonical checks one value the page sent and answers the text to store.
func (s Setting) Canonical(raw json.RawMessage) (string, any, *FieldError) {
	switch s.Kind {
	case KindInt:
		var n int
		if err := json.Unmarshal(raw, &n); err != nil {
			return "", nil, &FieldError{Key: s.Key, Code: "type", English: "must be a whole number", Min: s.intSpec.Min, Max: s.intSpec.Max}
		}
		if err := s.intSpec.Validate(n); err != nil {
			return "", nil, &FieldError{Key: s.Key, Code: "range", English: err.Error(), Min: s.intSpec.Min, Max: s.intSpec.Max}
		}
		return strconv.Itoa(n), n, nil
	case KindBool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return "", nil, &FieldError{Key: s.Key, Code: "type", English: "must be true or false"}
		}
		return dbsetting.FormatBool(b), b, nil
	default:
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return "", nil, &FieldError{Key: s.Key, Code: "type", English: "must be text"}
		}
		c, err := s.stringSpec.Canonical(v)
		if err != nil {
			return "", nil, &FieldError{Key: s.Key, Code: "language", English: err.Error()}
		}
		return c, c, nil
	}
}

// ── what this process started with ─────────────────────────────────────

var (
	bootMu  sync.RWMutex
	booted  = map[string]string{}
	liveMu  sync.RWMutex
	liveFns = map[string]func(context.Context, any){}
)

// SetLive registers what applies a new value of key at once. A setting with
// a live hook is never "waiting for a restart".
func SetLive(key string, fn func(context.Context, any)) {
	liveMu.Lock()
	defer liveMu.Unlock()
	liveFns[key] = fn
}

func liveFor(key string) func(context.Context, any) {
	liveMu.RLock()
	defer liveMu.RUnlock()
	return liveFns[key]
}

// Boot seeds every setting the configuration sets, on a start where its row
// does not exist yet, then writes the value in force into cfg - the one place
// every consumer below reads it from. Call once, after the migrations and
// before anything is built from cfg.
//
// ⚠ A failed seed is not fatal (dbsetting.SeedAll's rule): the default, or
// the configuration's own value when the store cannot be read, stays in force.
func Boot(ctx context.Context, st dbsetting.Store, cfg *config.Config) {
	// Before the seeds: a row a seed writes now holds the configuration's own
	// value and is no drift, a row that was there already may be one.
	for _, d := range Drifts(ctx, st, cfg) {
		dbsetting.WarnStoredWins(d)
	}
	for _, s := range all {
		if v, set := s.fromConfig(cfg); set {
			if err := s.seedValue(ctx, st, v, sourceOf(s)); err != nil {
				slog.Warn("instance settings: seeding failed; the configuration's value applies this start",
					slog.String("key", s.Key), slog.String("err", err.Error()))
			}
		}
	}
	bootMu.Lock()
	defer bootMu.Unlock()
	for _, s := range all {
		v := s.Resolve(ctx, st)
		s.toConfig(cfg, v)
		booted[s.Key] = s.text(v)
	}
}

// Drifts answers the settings whose configuration says something the stored
// row overrules: the variable (or the config.yaml key) is set, the row holds
// a value, and the two differ in the form the row stores. A variable set to
// the default counts too - an operator who wrote FILEX_SYNC_INTERVAL=15m
// expects 15 minutes whatever the row says.
func Drifts(ctx context.Context, g dbsetting.Getter, cfg *config.Config) []dbsetting.Drift {
	var out []dbsetting.Drift
	for _, s := range all {
		_, set := s.fromConfig(cfg)
		if !set && (s.EnvVar == "" || os.Getenv(s.EnvVar) == "") {
			continue
		}
		cur, err := g.GetSetting(ctx, s.Key)
		if err != nil {
			continue
		}
		want, ok := s.configText(cfg)
		if !ok {
			continue
		}
		if want == "" {
			want = s.text(s.Default())
		}
		if dbsetting.Drifted(want, cur) {
			out = append(out, dbsetting.Drift{Key: s.Key, Source: sourceOf(s)})
		}
	}
	return out
}

// configText is the text the configuration's value is stored as: "" when the
// configuration leaves the setting at its default, an int out of range at
// its nearest bound (as a first-start seed does). ok is false for a value
// that cannot be stored at all.
func (s Setting) configText(cfg *config.Config) (string, bool) {
	v, set := s.fromConfig(cfg)
	if !set {
		return "", true
	}
	raw, _ := json.Marshal(v)
	t, _, ferr := s.Canonical(raw)
	if ferr == nil {
		return t, true
	}
	if s.intSpec != nil {
		n, _ := s.intSpec.Clamp(v.(int))
		return strconv.Itoa(n), true
	}
	slog.Warn("instance settings: the configuration's value is not usable; left as it is",
		slog.String("key", s.Key), slog.String("err", ferr.English))
	return "", false
}

func sourceOf(s Setting) string {
	if s.EnvVar != "" && os.Getenv(s.EnvVar) != "" {
		return s.EnvVar
	}
	return "config.yaml " + s.ConfigKey
}

func (s Setting) seedValue(ctx context.Context, st dbsetting.Store, v any, source string) error {
	switch s.Kind {
	case KindInt:
		return s.intSpec.SeedValue(ctx, st, v.(int), source)
	case KindBool:
		return s.boolSpec.SeedValue(ctx, st, v.(bool), source)
	default:
		return s.stringSpec.SeedValueFrom(ctx, st, v.(string), source)
	}
}

// Item is one setting as the page reads it.
type Item struct {
	Key     string `json:"key"`
	Group   Group  `json:"group"`
	Kind    Kind   `json:"kind"`
	Value   any    `json:"value"`
	Default any    `json:"default"`
	Min     *int   `json:"min,omitempty"`
	Max     *int   `json:"max,omitempty"`
	Unit    string `json:"unit,omitempty"`
	// Env is the variable that gave the setting its first value.
	Env string `json:"env,omitempty"`
	// Restart: a change applies at the next start; RestartPending: one is
	// waiting (the stored value is not the one this process started with).
	Restart        bool `json:"restart"`
	RestartPending bool `json:"restart_pending"`
}

// Items reads the settings of one group ("" = every group), as in force.
func Items(ctx context.Context, g dbsetting.Getter, group string) []Item {
	bootMu.RLock()
	defer bootMu.RUnlock()
	out := []Item{}
	for _, s := range all {
		if group != "" && string(s.Group) != group {
			continue
		}
		v := s.Resolve(ctx, g)
		restart := s.Restart && liveFor(s.Key) == nil
		it := Item{
			Key: s.Key, Group: s.Group, Kind: s.Kind, Value: v, Default: s.Default(),
			Unit: s.Unit, Env: s.EnvVar, Restart: restart,
		}
		if s.intSpec != nil {
			min, max := s.intSpec.Min, s.intSpec.Max
			it.Min, it.Max = &min, &max
		}
		if b, ok := booted[s.Key]; ok && restart && b != s.text(v) {
			it.RestartPending = true
		}
		out = append(out, it)
	}
	return out
}

// Write checks every value in patch, and stores them only when all of them
// are good: one bad field refuses the whole save, before anything is written.
// A setting with a live hook is applied at once. It answers the keys written.
func Write(ctx context.Context, st dbsetting.Store, patch map[string]json.RawMessage) ([]string, error) {
	type write struct {
		s    Setting
		text string
		val  any
	}
	var writes []write
	for key, raw := range patch {
		s, ok := ByKey(key)
		if !ok {
			return nil, &FieldError{Key: key, Code: "unknown", English: "no such setting"}
		}
		text, val, ferr := s.Canonical(raw)
		if ferr != nil {
			return nil, ferr
		}
		writes = append(writes, write{s, text, val})
	}
	keys := make([]string, 0, len(writes))
	for _, w := range writes {
		if err := st.UpsertSetting(ctx, w.s.Key, w.text); err != nil {
			return keys, fmt.Errorf("instance settings: write %s: %w", w.s.Key, err)
		}
		keys = append(keys, w.s.Key)
		if fn := liveFor(w.s.Key); fn != nil {
			fn(ctx, w.val)
		}
	}
	return keys, nil
}

// Reseed writes the CONFIGURATION's value of the named settings (every one,
// when keys is empty) over whatever is stored - the explicit way back to what
// the environment or config.yaml says, since a start no longer does it on its
// own (`filex settings reseed`). A setting the configuration leaves at its
// default is reset to the default (its row removed in effect: written as
// empty, which every spec reads as "not set"). It answers what it wrote.
func Reseed(ctx context.Context, st dbsetting.Store, cfg *config.Config, keys []string) ([]string, error) {
	want := map[string]bool{}
	for _, k := range keys {
		if _, ok := ByKey(k); !ok {
			return nil, fmt.Errorf("instance settings: no setting %q", k)
		}
		want[k] = true
	}
	var done []string
	for _, s := range all {
		if len(want) > 0 && !want[s.Key] {
			continue
		}
		text, ok := s.configText(cfg)
		if !ok {
			continue
		}
		if err := st.UpsertSetting(ctx, s.Key, text); err != nil {
			return done, fmt.Errorf("instance settings: reseed %s: %w", s.Key, err)
		}
		done = append(done, s.Key)
	}
	return done, nil
}
