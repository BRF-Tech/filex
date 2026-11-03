package captcha

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// The providers.
const (
	ProviderNone      = "none"
	ProviderTurnstile = "turnstile"
	ProviderRecaptcha = "recaptcha"
)

// Providers is every value `captcha.provider` takes.
var Providers = []string{ProviderNone, ProviderTurnstile, ProviderRecaptcha}

// What happens when the provider cannot be reached.
const (
	// FallbackLocal: the browser does the local proof of work.
	FallbackLocal = "local"
	// FallbackOff: the form answers 503 instead. For tests only.
	FallbackOff = "off"
)

// The forms filex protects, each on or off in `captcha.forms`.
const (
	// FormLogin is the password sign-in, the recovery sign-in of an SSO
	// installation included.
	FormLogin = "login"
	// FormDemoLogin is a demo's one-click sign-in (FILEX_DEMO).
	FormDemoLogin = "demo_login"
	// FormSharePassword is the PIN a protected link asks for: a share, an
	// app's page, a file request.
	FormSharePassword = "share_password"
	// FormFileRequest is the upload into a file request.
	FormFileRequest = "file_request"
)

// Forms is every form, in the order the settings page lists them. It is also
// what is protected when nothing says otherwise.
var Forms = []string{FormLogin, FormDemoLogin, FormSharePassword, FormFileRequest}

// The settings rows: the shared contract's names.
const (
	KeyProvider = "captcha.provider"
	KeySiteKey  = "captcha.site_key"
	// KeySecret is sealed (internal/secretbox, bound to this row). Its last
	// segment is a secret's name, so the generic settings list masks it.
	KeySecret   = "captcha.secret"
	KeyForms    = "captcha.forms"
	KeyFallback = "captcha.fallback"
	KeyTimeout  = "captcha.verify_timeout_ms"
)

// secretAAD binds the sealed secret to its row.
const secretAAD = "settings:" + KeySecret

// The environment, read when the panel leaves a field empty.
//
// ⚠⚠ Not a seed. filex's other database settings (internal/dbsetting) read
// their variable once, at the first boot, into the row; these are read LIVE
// whenever the row is empty - the order the shared contract sets for every
// product: the panel, then the environment, then off.
const (
	EnvProvider = "CAPTCHA_PROVIDER"
	EnvSiteKey  = "CAPTCHA_SITE_KEY"
	EnvSecret   = "CAPTCHA_SECRET"
	EnvForms    = "CAPTCHA_FORMS"
	EnvFallback = "CAPTCHA_FALLBACK"
	EnvTimeout  = "CAPTCHA_VERIFY_TIMEOUT_MS"
)

// The verify timeout's bounds and default.
const (
	DefaultVerifyTimeout = 4 * time.Second
	MinVerifyTimeoutMS   = 1000
	MaxVerifyTimeoutMS   = 15000
)

// The reasons a configuration cannot be used (Config.Misconfiguration).
const (
	MisSiteKeyMissing   = "site_key_missing"
	MisSecretMissing    = "secret_missing"
	MisSecretUnreadable = "secret_unreadable"
	// The provider's own refusal of the secret (siteverify error codes).
	MisInvalidSecret = "invalid-input-secret"
	MisMissingSecret = "missing-input-secret"
)

// The layer a value came from (Config.Sources).
const (
	SourceDB      = "db"
	SourceEnv     = "env"
	SourceDefault = "default"
)

// Config is the bot check in force: every field resolved panel → environment
// → default.
type Config struct {
	Provider string
	SiteKey  string
	// Secret is the provider's secret, opened. Never leaves the server.
	Secret string
	// SecretUnreadable: the panel holds a secret that does not open (the
	// encryption key changed). It does not fall back to the environment:
	// a broken row must not be hidden.
	SecretUnreadable bool
	Forms            []string
	Fallback         string
	VerifyTimeout    time.Duration
	// Sources names, per field, the layer it came from.
	Sources map[string]string
}

// Enabled reports whether a provider is chosen. A missing key does not turn
// the check off: that is a misconfiguration, answered with the local
// fallback and an alarm.
func (c Config) Enabled() bool {
	return c.Provider == ProviderTurnstile || c.Provider == ProviderRecaptcha
}

// FormEnabled reports whether form is protected.
func (c Config) FormEnabled(form string) bool {
	return c.Enabled() && slices.Contains(c.Forms, form)
}

// Misconfiguration names why a chosen provider cannot be used, or "".
func (c Config) Misconfiguration() string {
	switch {
	case !c.Enabled():
		return ""
	case c.SiteKey == "":
		return MisSiteKeyMissing
	case c.SecretUnreadable:
		return MisSecretUnreadable
	case c.Secret == "":
		return MisSecretMissing
	}
	return ""
}

// SettingStore is the settings table. db.Store satisfies it.
type SettingStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	UpsertSetting(ctx context.Context, key, value string) error
}

// Settings reads and writes the bot check's settings.
type Settings struct {
	Store SettingStore
	// Box seals the secret (FILEX_SECRET_KEY). Nil or keyless: the panel
	// cannot store one; CAPTCHA_SECRET still works.
	Box *secretbox.Box
	// Env reads the environment; nil = os.Getenv.
	Env func(string) string
	// TTL is how long a read is reused; 0 = 5 seconds, negative = not at all
	// (tests). Another instance's change is seen within it.
	TTL time.Duration

	mu     sync.Mutex
	cached *Config
	at     time.Time
}

func (s *Settings) env(k string) string {
	if s.Env != nil {
		return strings.TrimSpace(s.Env(k))
	}
	return strings.TrimSpace(os.Getenv(k))
}

func (s *Settings) row(ctx context.Context, key string) string {
	v, _ := s.rowErr(ctx, key)
	return v
}

// rowErr reads one row; a row that is not there is "" and no error.
func (s *Settings) rowErr(ctx context.Context, key string) (string, error) {
	if s == nil || s.Store == nil {
		return "", nil
	}
	v, err := s.Store.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(v), nil
}

// Invalidate forgets the cached read (after a save on this instance).
func (s *Settings) Invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
}

// Load is the configuration in force.
func (s *Settings) Load(ctx context.Context) Config {
	if s == nil {
		return Config{Provider: ProviderNone, Fallback: FallbackLocal, VerifyTimeout: DefaultVerifyTimeout, Forms: slices.Clone(Forms)}
	}
	ttl := s.TTL
	if ttl < 0 {
		return s.resolve(ctx)
	}
	if ttl == 0 {
		ttl = 5 * time.Second
	}
	s.mu.Lock()
	if s.cached != nil && time.Since(s.at) < ttl {
		c := *s.cached
		s.mu.Unlock()
		return c
	}
	s.mu.Unlock()
	c, err := s.resolveErr(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	// ⚠ A settings table that could not be read is not "no rows": the
	// environment or "off" in its place would switch the protection off for
	// the length of a database hiccup. The last good read stays in force.
	if err != nil {
		slog.Error("captcha: the settings could not be read; the last read stays in force", slog.String("err", err.Error()))
		if s.cached != nil {
			return *s.cached
		}
		return c
	}
	s.cached, s.at = &c, time.Now()
	return c
}

func (s *Settings) resolve(ctx context.Context) Config {
	c, _ := s.resolveErr(ctx)
	return c
}

func (s *Settings) resolveErr(ctx context.Context) (Config, error) {
	var readErr error
	get := func(key string) string {
		v, err := s.rowErr(ctx, key)
		if err != nil && readErr == nil {
			readErr = err
		}
		return v
	}
	c := Config{Sources: map[string]string{}}
	pick := func(field, rowKey, envKey string) string {
		if v := get(rowKey); v != "" {
			c.Sources[field] = SourceDB
			return v
		}
		if v := s.env(envKey); v != "" {
			c.Sources[field] = SourceEnv
			return v
		}
		c.Sources[field] = SourceDefault
		return ""
	}

	c.Provider = strings.ToLower(pick("provider", KeyProvider, EnvProvider))
	if !slices.Contains(Providers, c.Provider) {
		if c.Provider != "" {
			slog.Warn("captcha: unknown provider; the bot check is off", slog.String("provider", c.Provider))
		}
		c.Provider = ProviderNone
	}
	c.SiteKey = pick("site_key", KeySiteKey, EnvSiteKey)

	if sealed := get(KeySecret); sealed != "" {
		c.Sources["secret"] = SourceDB
		secret, err := s.open(sealed)
		if err != nil {
			slog.Error("captcha: the stored secret does not open (did FILEX_SECRET_KEY change?)", slog.String("err", err.Error()))
			c.SecretUnreadable = true
		} else {
			c.Secret = secret
		}
	} else if v := s.env(EnvSecret); v != "" {
		c.Sources["secret"] = SourceEnv
		c.Secret = v
	} else {
		c.Sources["secret"] = SourceDefault
	}

	c.Forms = slices.Clone(Forms)
	if raw := get(KeyForms); raw != "" {
		var list []string
		if json.Unmarshal([]byte(raw), &list) == nil {
			c.Forms, c.Sources["forms"] = knownForms(list), SourceDB
		} else {
			slog.Warn("captcha: the forms row does not parse; every form is protected")
			c.Sources["forms"] = SourceDefault
		}
	} else if raw := s.env(EnvForms); raw != "" {
		c.Forms, c.Sources["forms"] = knownForms(strings.Split(raw, ",")), SourceEnv
	} else {
		c.Sources["forms"] = SourceDefault
	}

	c.Fallback = strings.ToLower(pick("fallback", KeyFallback, EnvFallback))
	if c.Fallback != FallbackOff {
		c.Fallback = FallbackLocal
	}

	c.VerifyTimeout = DefaultVerifyTimeout
	if raw := pick("verify_timeout_ms", KeyTimeout, EnvTimeout); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			ms = min(max(ms, MinVerifyTimeoutMS), MaxVerifyTimeoutMS)
			c.VerifyTimeout = time.Duration(ms) * time.Millisecond
		}
	}
	return c, readErr
}

func (s *Settings) open(sealed string) (string, error) {
	if !secretbox.IsSealed(sealed) {
		return "", errors.New("the row is not sealed")
	}
	if s.Box == nil || !s.Box.Enabled() {
		return "", secretbox.ErrNoKey
	}
	v, err := s.Box.OpenFor(sealed, secretAAD)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(v) == "" {
		return "", errors.New("the stored secret is empty")
	}
	return v, nil
}

// knownForms keeps the known form names of list, once each, in Forms' order.
func knownForms(list []string) []string {
	out := []string{}
	for _, f := range Forms {
		for _, v := range list {
			if strings.EqualFold(strings.TrimSpace(v), f) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// Stored is what the panel holds, field by field ("" / nil = empty: the
// environment or the default applies). The secret is only said to be there.
type Stored struct {
	Provider        string   `json:"provider"`
	SiteKey         string   `json:"site_key"`
	HasSecret       bool     `json:"has_secret"`
	Forms           []string `json:"forms"`
	Fallback        string   `json:"fallback"`
	VerifyTimeoutMS int      `json:"verify_timeout_ms"`
}

// Stored reads the panel's own values.
func (s *Settings) Stored(ctx context.Context) Stored {
	out := Stored{
		Provider: s.row(ctx, KeyProvider),
		SiteKey:  s.row(ctx, KeySiteKey),
		Fallback: s.row(ctx, KeyFallback),
	}
	out.HasSecret = s.row(ctx, KeySecret) != ""
	if raw := s.row(ctx, KeyForms); raw != "" {
		var list []string
		if json.Unmarshal([]byte(raw), &list) == nil {
			out.Forms = knownForms(list)
		}
	}
	if raw := s.row(ctx, KeyTimeout); raw != "" {
		out.VerifyTimeoutMS, _ = strconv.Atoi(raw)
	}
	return out
}

// Patch is a change from the panel. A nil field is left as it is; an empty
// string (an empty list for Forms is a choice, nil is not) clears it, so the
// environment or the default applies again. Secret "" changes nothing;
// ClearSecret removes the stored one.
type Patch struct {
	Provider        *string   `json:"provider,omitempty"`
	SiteKey         *string   `json:"site_key,omitempty"`
	Secret          *string   `json:"secret,omitempty"`
	ClearSecret     bool      `json:"clear_secret,omitempty"`
	Forms           *[]string `json:"forms,omitempty"`
	ClearForms      bool      `json:"clear_forms,omitempty"`
	Fallback        *string   `json:"fallback,omitempty"`
	VerifyTimeoutMS *int      `json:"verify_timeout_ms,omitempty"`
}

// Invalid is a refused value: Field names it.
type Invalid struct {
	Field string
	Min   int
	Max   int
}

func (e *Invalid) Error() string { return "captcha: bad value for " + e.Field }

// ErrNoSecretKey: a secret was given and there is no FILEX_SECRET_KEY to seal
// it with.
var ErrNoSecretKey = errors.New("captcha: no FILEX_SECRET_KEY to seal the secret with")

// Validate checks p without writing anything.
func (p Patch) Validate() error {
	if p.Provider != nil {
		v := strings.ToLower(strings.TrimSpace(*p.Provider))
		if v != "" && !slices.Contains(Providers, v) {
			return &Invalid{Field: "provider"}
		}
	}
	if p.Fallback != nil {
		v := strings.ToLower(strings.TrimSpace(*p.Fallback))
		if v != "" && v != FallbackLocal && v != FallbackOff {
			return &Invalid{Field: "fallback"}
		}
	}
	if p.Forms != nil {
		for _, f := range *p.Forms {
			if !slices.Contains(Forms, strings.TrimSpace(f)) {
				return &Invalid{Field: "forms"}
			}
		}
	}
	if p.VerifyTimeoutMS != nil && *p.VerifyTimeoutMS != 0 &&
		(*p.VerifyTimeoutMS < MinVerifyTimeoutMS || *p.VerifyTimeoutMS > MaxVerifyTimeoutMS) {
		return &Invalid{Field: "verify_timeout_ms", Min: MinVerifyTimeoutMS, Max: MaxVerifyTimeoutMS}
	}
	// A key is one token: a space or a line break inside it is a paste gone
	// wrong, and the provider would refuse it later with less to go on.
	for _, f := range []struct {
		name string
		v    *string
	}{{"site_key", p.SiteKey}, {"secret", p.Secret}} {
		if f.v == nil {
			continue
		}
		if t := strings.TrimSpace(*f.v); len(t) > 512 || strings.ContainsAny(t, "\r\n\t ") {
			return &Invalid{Field: f.name}
		}
	}
	return nil
}

// Apply writes p and answers the names of the fields it changed (never a
// value: the audit row carries names only).
func (s *Settings) Apply(ctx context.Context, p Patch) ([]string, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if s == nil || s.Store == nil {
		return nil, errors.New("captcha: no settings store")
	}
	cur := s.Stored(ctx)
	var changed []string
	set := func(field, key, old string, v *string, lower bool) error {
		if v == nil {
			return nil
		}
		nv := strings.TrimSpace(*v)
		if lower {
			nv = strings.ToLower(nv)
		}
		if nv == old {
			return nil
		}
		if err := s.Store.UpsertSetting(ctx, key, nv); err != nil {
			return fmt.Errorf("captcha: save %s: %w", field, err)
		}
		changed = append(changed, field)
		return nil
	}
	if err := set("provider", KeyProvider, cur.Provider, p.Provider, true); err != nil {
		return changed, err
	}
	if err := set("site_key", KeySiteKey, cur.SiteKey, p.SiteKey, false); err != nil {
		return changed, err
	}
	if err := set("fallback", KeyFallback, cur.Fallback, p.Fallback, true); err != nil {
		return changed, err
	}
	if p.VerifyTimeoutMS != nil {
		nv := ""
		if *p.VerifyTimeoutMS > 0 {
			nv = strconv.Itoa(*p.VerifyTimeoutMS)
		}
		old := ""
		if cur.VerifyTimeoutMS > 0 {
			old = strconv.Itoa(cur.VerifyTimeoutMS)
		}
		if nv != old {
			if err := s.Store.UpsertSetting(ctx, KeyTimeout, nv); err != nil {
				return changed, err
			}
			changed = append(changed, "verify_timeout_ms")
		}
	}
	switch {
	case p.ClearForms:
		if s.row(ctx, KeyForms) != "" {
			if err := s.Store.UpsertSetting(ctx, KeyForms, ""); err != nil {
				return changed, err
			}
			changed = append(changed, "forms")
		}
	case p.Forms != nil:
		b, _ := json.Marshal(knownForms(*p.Forms))
		if string(b) != s.row(ctx, KeyForms) {
			if err := s.Store.UpsertSetting(ctx, KeyForms, string(b)); err != nil {
				return changed, err
			}
			changed = append(changed, "forms")
		}
	}
	switch {
	case p.Secret != nil && strings.TrimSpace(*p.Secret) != "":
		if s.Box == nil || !s.Box.Enabled() {
			return changed, ErrNoSecretKey
		}
		sealed, err := s.Box.SealFor(strings.TrimSpace(*p.Secret), secretAAD)
		if err != nil {
			return changed, err
		}
		if err := s.Store.UpsertSetting(ctx, KeySecret, sealed); err != nil {
			return changed, err
		}
		changed = append(changed, "secret")
	case p.ClearSecret && cur.HasSecret:
		if err := s.Store.UpsertSetting(ctx, KeySecret, ""); err != nil {
			return changed, err
		}
		changed = append(changed, "secret")
	}
	s.Invalidate()
	return changed, nil
}
