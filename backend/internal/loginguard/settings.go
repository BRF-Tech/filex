package loginguard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

// The settings an administrator edits (Sign-in security page; REST
// /api/admin/login-security). All live in the settings table and are held in
// memory: read at start (Guard.Load) and again every minute (Guard.Tick, so a
// change saved on another instance arrives within a minute), and on every
// save written to the table AND put in force at once (Guard.SaveSetting), so
// a change applies to the next attempt without a restart. A write that
// reaches the table some other way (the generic settings API) is picked up at
// once by Guard.Invalidate.
//
// ⚠ They are instance-wide: one row each, for every tenant. The admin handler
// is supertenant-only for that reason.
const (
	KeyEnabled      = "login.throttle_enabled"
	KeyAccountMax   = "login.account_max_fails"
	KeyIPMax        = "login.ip_max_fails"
	KeyWindow       = "login.window_seconds"
	KeyLockBase     = "login.lock_base_seconds"
	KeyLockMax      = "login.lock_max_seconds"
	KeyIPAllowlist  = "login.ip_allowlist"
	KeyTrustedProxy = "login.trusted_proxies"
)

// settingKeys is every key the guard holds in memory.
var settingKeys = []string{
	KeyEnabled, KeyAccountMax, KeyIPMax, KeyWindow, KeyLockBase, KeyLockMax, KeyIPAllowlist, KeyTrustedProxy,
}

// ActionSettingsUpdate is the audit action of a change to these settings,
// whichever route made it (the Sign-in security page, the generic settings
// API) and whichever door (panel, API key, MCP): the sign-in trail reads it
// beside the `login.` family.
const ActionSettingsUpdate = "login_security.update"

// fieldOfKey names each setting the way the admin surface does - the keys of
// `settings` in GET /api/admin/login-security, and of an audit row's
// `changed_fields`.
var fieldOfKey = map[string]string{
	KeyEnabled:      "enabled",
	KeyAccountMax:   "account_max_fails",
	KeyIPMax:        "ip_max_fails",
	KeyWindow:       "window_seconds",
	KeyLockBase:     "lock_base_seconds",
	KeyLockMax:      "lock_max_seconds",
	KeyIPAllowlist:  "ip_allowlist",
	KeyTrustedProxy: "trusted_proxies",
}

// FieldOf is a setting key's field name (FieldOf(KeyAccountMax) is
// "account_max_fails"); "" for a key that is not one of these settings.
func FieldOf(key string) string { return fieldOfKey[key] }

// IsSettingKey reports whether key is one of the sign-in security settings.
func IsSettingKey(key string) bool {
	for _, k := range settingKeys {
		if k == key {
			return true
		}
	}
	return false
}

var (
	// EnabledSetting is the kill switch. On by default; an operator who runs
	// filex behind something that already limits sign-ins switches it off.
	EnabledSetting = dbsetting.BoolSpec{Key: KeyEnabled, Default: true}
	// AccountMaxSetting is the wrong attempts one account identifier may make
	// inside the window before it is locked.
	AccountMaxSetting = dbsetting.IntSpec{Key: KeyAccountMax, Default: 5, Min: 1, Max: 1000, Unit: "attempts"}
	// IPMaxSetting is the same for one client address.
	IPMaxSetting = dbsetting.IntSpec{Key: KeyIPMax, Default: 10, Min: 1, Max: 10000, Unit: "attempts"}
	// WindowSetting is how long a wrong attempt counts (seconds).
	WindowSetting = dbsetting.IntSpec{Key: KeyWindow, Default: 600, Min: 10, Max: 86400, Unit: "seconds"}
	// LockBaseSetting is the first lock's length; each further lock in a row
	// doubles it (seconds).
	LockBaseSetting = dbsetting.IntSpec{Key: KeyLockBase, Default: 60, Min: 1, Max: 86400, Unit: "seconds"}
	// LockMaxSetting is the ceiling the doubling stops at (seconds).
	LockMaxSetting = dbsetting.IntSpec{Key: KeyLockMax, Default: 900, Min: 1, Max: 86400, Unit: "seconds"}
	// IPAllowlistSetting lists addresses and networks (CIDR) that are exempt
	// from the per-address limit AND may sign in to a locked account.
	IPAllowlistSetting = dbsetting.StringSpec{
		Key: KeyIPAllowlist, Default: "",
		Check: func(v string) error { _, err := ParseAllowlist(v); return err },
	}
	// TrustedProxiesSetting lists the reverse proxies whose X-Forwarded-For /
	// X-Real-IP filex believes: addresses, CIDR networks and the words
	// `auto`, `loopback`, `private`, `link-local` (clientip.ParseList); `none`
	// trusts no proxy. Empty = the environment's FILEX_TRUSTED_PROXIES, and
	// when that is empty too, `auto` (loopback, plus the container networks
	// filex is attached to minus their gateways). A value here wins over the
	// environment.
	TrustedProxiesSetting = dbsetting.StringSpec{
		Key: KeyTrustedProxy, Default: "",
		Check: func(v string) error { _, err := clientip.ParseList(v); return err },
	}
)

// ParseAllowlist reads an IP allow-list: addresses and CIDR networks separated
// by commas, semicolons, spaces or newlines. Blank text is an empty list. Unlike
// the trusted-proxy list it takes no words: an allow-list of client addresses
// that could silently mean "every private address" is a hole.
func ParseAllowlist(text string) (*clientip.Set, error) {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\r' || r == '\t'
	})
	return clientip.ParseEntries(fields)
}

// Config is every number the guard decides with.
type Config struct {
	Enabled    bool
	AccountMax int
	IPMax      int
	Window     time.Duration
	LockBase   time.Duration
	LockMax    time.Duration
	Allow      *clientip.Set
}

// LoadConfig resolves the settings from g.
func LoadConfig(ctx context.Context, g dbsetting.Getter) Config {
	c := Config{
		Enabled:    EnabledSetting.Resolve(ctx, g),
		AccountMax: AccountMaxSetting.Resolve(ctx, g),
		IPMax:      IPMaxSetting.Resolve(ctx, g),
		Window:     time.Duration(WindowSetting.Resolve(ctx, g)) * time.Second,
		LockBase:   time.Duration(LockBaseSetting.Resolve(ctx, g)) * time.Second,
		LockMax:    time.Duration(LockMaxSetting.Resolve(ctx, g)) * time.Second,
	}
	// A ceiling below the first lock would make "escalating" mean "shrinking".
	if c.LockMax < c.LockBase {
		c.LockMax = c.LockBase
	}
	raw, _ := IPAllowlistSetting.ResolveStrict(ctx, g)
	if set, err := ParseAllowlist(raw); err == nil {
		c.Allow = set
	}
	return c
}

// snapshot is the settings in force: the text each key holds in the table (a
// key with no row is absent) and what it resolves to. Replaced whole, never
// changed in place.
type snapshot struct {
	stored  stored
	cfg     Config
	proxies *clientip.Set // login.trusted_proxies; nil = unset (or unusable)
}

// stored is the settings table's text for the guard's keys, as a
// dbsetting.Getter: a key with no row answers sql.ErrNoRows, exactly as the
// table does, so every spec resolves from memory as it would from the table.
type stored map[string]string

// GetSetting implements dbsetting.Getter.
func (s stored) GetSetting(_ context.Context, key string) (string, error) {
	if v, ok := s[key]; ok {
		return v, nil
	}
	return "", sql.ErrNoRows
}

func buildSnapshot(raw stored) *snapshot {
	ctx := context.Background()
	s := &snapshot{stored: raw, cfg: LoadConfig(ctx, raw)}
	if text, err := TrustedProxiesSetting.ResolveStrict(ctx, raw); err == nil {
		if set, perr := clientip.ParseList(text); perr == nil {
			s.proxies = set
		}
	}
	return s
}

// reloadSettings reads the guard's keys from the table into memory. A key the
// table cannot answer (an error other than "no row") keeps the value last
// known — the default when there was none — and the error is returned.
func (g *Guard) reloadSettings(ctx context.Context) error {
	g.setMu.Lock()
	defer g.setMu.Unlock()
	prev := g.settings.Load()
	raw := stored{}
	var errs []error
	for _, k := range settingKeys {
		if g.Store == nil {
			break
		}
		v, err := g.Store.GetSetting(ctx, k)
		switch {
		case err == nil:
			raw[k] = v
		case errors.Is(err, sql.ErrNoRows):
		default:
			errs = append(errs, fmt.Errorf("%s: %w", k, err))
			if prev != nil {
				if v, ok := prev.stored[k]; ok {
					raw[k] = v
				}
			}
		}
	}
	g.settings.Store(buildSnapshot(raw))
	if len(errs) > 0 {
		err := errors.Join(errs...)
		slog.Warn("loginguard: could not read the sign-in security settings; keeping the last known values", slog.Any("err", err))
		return err
	}
	return nil
}

// Config returns the settings in force (from memory).
func (g *Guard) Config(ctx context.Context) Config {
	g.ensureLoaded(ctx)
	if s := g.settings.Load(); s != nil {
		return s.cfg
	}
	return LoadConfig(ctx, nil)
}

// Stored answers the settings table's text for the sign-in security keys as
// the memory holds it — what the admin surface shows as "saved".
func (g *Guard) Stored(ctx context.Context) dbsetting.Getter {
	g.ensureLoaded(ctx)
	if s := g.settings.Load(); s != nil {
		return s.stored
	}
	return stored{}
}

// SaveSetting writes one sign-in security setting to the table and, once the
// table has taken it, puts it in force: the next attempt decides with it. A
// write the table refuses changes nothing and is returned, so the memory and
// the table never disagree about a setting.
func (g *Guard) SaveSetting(ctx context.Context, key, value string) error {
	if !IsSettingKey(key) {
		return fmt.Errorf("loginguard: %q is not a sign-in security setting", key)
	}
	g.ensureLoaded(ctx)
	g.setMu.Lock()
	defer g.setMu.Unlock()
	if g.Store != nil {
		if err := g.Store.UpsertSetting(ctx, key, value); err != nil {
			return err
		}
	}
	raw := stored{}
	if prev := g.settings.Load(); prev != nil {
		for k, v := range prev.stored {
			raw[k] = v
		}
	}
	raw[key] = value
	g.settings.Store(buildSnapshot(raw))
	return nil
}

// Invalidate reads the settings from the table again, now. It is for a write
// that reached the table without SaveSetting (the generic settings API, a
// test); a key the table cannot answer keeps its last known value.
func (g *Guard) Invalidate() {
	_ = g.reloadSettings(context.Background())
}

// TrustedProxySource builds the function clientip.SetSource wants: the
// `login.trusted_proxies` setting in memory when it holds a list, else env
// (the text of FILEX_TRUSTED_PROXIES), else nil (clientip's default: `auto`).
// It is asked once per request and reads memory only; a saved change is in
// force for the next request.
func (g *Guard) TrustedProxySource(env string) func() *clientip.Set {
	envSet, err := clientip.ParseList(env)
	if err != nil {
		envSet = nil
	}
	return func() *clientip.Set {
		g.ensureLoaded(context.Background())
		if s := g.settings.Load(); s != nil && s.proxies != nil {
			return s.proxies
		}
		return envSet
	}
}
