// Package config loads filex configuration from a YAML file with
// environment variable overrides (FILEX_*).
//
// Precedence (highest first):
//  1. Environment variables (FILEX_LISTEN, FILEX_DB_DRIVER, …)
//  2. config.yaml (path passed via --config or default ~/.filex/config.yaml)
//  3. Hard-coded defaults
//
// Some settings live in the DB (settings table) instead of config.yaml —
// e.g. instance branding, default thumbnail policy. Those are read by
// individual services via db.Store.GetSetting and are NOT modeled here.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level runtime configuration object.
type Config struct {
	Listen        string `yaml:"listen"`
	PublicURL     string `yaml:"public_url"`
	DataDir       string `yaml:"data_dir"`
	DefaultLocale string `yaml:"default_locale"`
	// CookieDomain sets the Domain attribute on the filex_session cookie
	// (e.g. ".example.com" to share the session across subdomains). Empty =
	// host-only cookie, the historical behavior. Applied on both set and
	// clear so logout removes the same cookie it created.
	CookieDomain string `yaml:"cookie_domain"`
	// MultiTenant turns on native multi-tenancy (host-resolved provider =
	// tenant, per-provider storage confinement, scoped user directory). OFF by
	// default — a single-tenant install behaves exactly as before. See
	// docs/MULTI-TENANCY.md.
	MultiTenant      bool         `yaml:"multi_tenant"`
	Log              LogConfig    `yaml:"log"`
	DB               DBConfig     `yaml:"db"`
	Auth             AuthConfig   `yaml:"auth"`
	ExternalServices ExtServices  `yaml:"external_services"`
	Sync             SyncConfig   `yaml:"sync"`
	Thumbs           ThumbsConfig `yaml:"thumbs"`
	Search           SearchConfig `yaml:"search"`
	CORS             CORSConfig   `yaml:"cors"`
	Queue            QueueConfig  `yaml:"queue"`
	Notify           NotifyConfig `yaml:"notify"`
	Demo             DemoConfig   `yaml:"demo"`
	Sentry           SentryConfig `yaml:"sentry"`
	Seed             SeedConfig   `yaml:"seed"`
	DAV              DAVConfig    `yaml:"dav"`
}

// DAVConfig — the WebDAV server surface at /dav (v0.3 "Bağlan"). ON by
// default; FILEX_DAV=0 is the kill switch (the handler then answers 404).
type DAVConfig struct {
	Enabled bool `yaml:"enabled"`
}

// SeedConfig holds one-time bootstrap values applied to the DB on first boot,
// only when the target record is ABSENT (operator UI edits are never
// clobbered). It lets a fresh `helm install` / `docker compose up` come up
// fully configured from env alone — admin user, SMTP, branding, trash policy
// and an initial storage — with zero admin-UI clicks. Consumed by
// internal/server/seed.go. (Auth/OIDC is env-authoritative already via
// AuthConfig, so it is not duplicated here.)
type SeedConfig struct {
	AdminEmail    string      `yaml:"admin_email"`
	AdminPassword string      `yaml:"admin_password"`
	SiteName      string      `yaml:"site_name"`
	TrashDays     string      `yaml:"trash_retention_days"`
	SMTP          SeedSMTP    `yaml:"smtp"`
	Storage       SeedStorage `yaml:"storage"`
}

// SeedSMTP mirrors the mailer's smtp.* settings keys.
type SeedSMTP struct {
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
	TLS      string `yaml:"tls"` // starttls | tls | none
}

// Configured reports whether enough SMTP fields are set to seed a row.
func (s SeedSMTP) Configured() bool { return s.Host != "" && s.Port != "" && s.From != "" }

// SeedStorage describes an initial storage row to create when no storage
// exists yet. Driver "" (default) seeds nothing.
//
// For local + s3 the ergonomic fields below are enough. To seed ANY other
// driver (sftp, webdav, ftp) — i.e. connect an EXISTING external storage — set
// Driver plus Config to the driver's raw config JSON; it is used verbatim.
type SeedStorage struct {
	Driver    string `yaml:"driver"` // local | s3 | sftp | webdav | ftp | "" (none)
	Name      string `yaml:"name"`
	MountPath string `yaml:"mount_path"`
	// Config is a raw JSON object used verbatim as the storage's driver config.
	// Set it for sftp/webdav/ftp (or advanced s3); overrides the fields below.
	Config string `yaml:"config"`
	Path   string `yaml:"path"` // local driver on-disk root
	// s3 driver:
	Bucket    string `yaml:"bucket"`
	Prefix    string `yaml:"prefix"`
	Endpoint  string `yaml:"endpoint"`
	Region    string `yaml:"region"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	PathStyle bool   `yaml:"path_style"`
}

// SentryConfig — optional Sentry-wire error reporting (self-hosted GlitchTip at
// errors.brf.sh). An empty DSN disables it entirely (default build reports
// nothing). Environment tags events (e.g. production / demo) so one project can
// serve multiple deployments.
type SentryConfig struct {
	DSN         string `yaml:"dsn"`
	Environment string `yaml:"environment"`
}

// DemoConfig — public-demo affordances. When Mode=true the login page
// renders an "Open the demo" CTA that auto-submits the supplied
// credentials, plus a feature-tour card above the form.
type DemoConfig struct {
	// Mode flips the UI into the demo presentation. Backend itself
	// stays a normal install — auth still happens against the local
	// driver, the demo creds are just a regular user.
	Mode bool `yaml:"mode"`
	// User + Pass are the credentials the "Open the demo" CTA submits.
	// Defaults: demo@demo.com / demo (operators must keep DB in sync).
	User string `yaml:"user"`
	Pass string `yaml:"pass"`
}

// NotifyConfig — webhook + in-app channel configuration. Both are
// optional; leaving WebhookURL empty disables outbound delivery while
// the in-app bell continues to record events.
type NotifyConfig struct {
	// Enabled toggles the entire subsystem. When false the API returns
	// 503 from /api/notifications/... and Service.Send is a no-op.
	Enabled bool `yaml:"enabled"`
	// WebhookURL receives a generic JSON POST per event.
	WebhookURL string `yaml:"webhook_url"`
	// WebhookToken — optional Authorization: Bearer <token>.
	WebhookToken string `yaml:"webhook_token"`
}

// QueueConfig — persistent op queue. Driver "sqlite" (default) shares
// the application DB; "postgres" / "redis" can be wired for production
// or HA setups (see internal/queue/drivers/{postgres,redis}).
type QueueConfig struct {
	// Driver selects the queue backend: sqlite | postgres | redis.
	// Empty defaults to "sqlite".
	Driver string `yaml:"driver"`
	// DSN is the connection string for postgres ("postgres://...") or
	// redis ("redis://..."). For sqlite the application DB handle is
	// shared automatically and DSN is ignored.
	DSN string `yaml:"dsn"`
	// Workers controls Pool size. <=0 → 4.
	Workers int `yaml:"workers"`
	// Enabled lets operators turn the persistent queue off entirely
	// (the legacy ops.Service still handles copy/move/delete in that
	// case). Default: true.
	Enabled bool `yaml:"enabled"`
}

// LogConfig — slog level + format.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // text, json
}

// DBConfig — driver and DSN.
type DBConfig struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

// AuthConfig — enabled drivers and per-driver options.
type AuthConfig struct {
	Drivers []string          `yaml:"drivers"`
	OIDC    OIDCConfig        `yaml:"oidc"`
	LDAP    LDAPConfig        `yaml:"ldap"`
	Header  HeaderProxyConfig `yaml:"header_proxy"`
}

// OIDCConfig — Keycloak/Auth0/etc.
type OIDCConfig struct {
	Issuer       string `yaml:"issuer"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	RedirectURL  string `yaml:"redirect_url"`
	RoleClaim    string `yaml:"role_claim"`
	AdminGroup   string `yaml:"admin_group"`
	// AutoRedirect makes the login page start the OIDC flow immediately
	// (SSO-first installs). The password form stays reachable via ?local=1
	// for break-glass/admin logins. OFF by default — unchanged behavior.
	AutoRedirect bool `yaml:"auto_redirect"`
}

// LDAPConfig — directory bind.
type LDAPConfig struct {
	URL          string `yaml:"url"`
	BindDN       string `yaml:"bind_dn"`
	BindPassword string `yaml:"bind_password"`
	BaseDN       string `yaml:"base_dn"`
	UserFilter   string `yaml:"user_filter"`
	EmailAttr    string `yaml:"email_attr"`
	StartTLS     bool   `yaml:"start_tls"`
}

// HeaderProxyConfig — accept Cloudflare Access / Authelia headers.
type HeaderProxyConfig struct {
	EmailHeader string   `yaml:"email_header"`
	GroupHeader string   `yaml:"group_header"`
	TrustedIPs  []string `yaml:"trusted_ips"`
	AdminGroup  string   `yaml:"admin_group"`
}

// ExtServices — plug-and-play.
type ExtServices struct {
	OnlyOffice OnlyOfficeConfig `yaml:"onlyoffice"`
	Drawio     DrawioConfig     `yaml:"drawio"`
	Convert    ConvertConfig    `yaml:"convert"`
}

// OnlyOfficeConfig — Document Server URL + JWT secret.
type OnlyOfficeConfig struct {
	URL       string `yaml:"url"`
	JWTSecret string `yaml:"jwt_secret"`
}

// DrawioConfig — embed URL.
type DrawioConfig struct {
	URL string `yaml:"url"`
}

// ConvertConfig — universal converter (p2r3/convert fork) embed URL.
type ConvertConfig struct {
	URL string `yaml:"url"`
}

// Mermaid needs no external service — diagrams render client-side in the
// browser via the bundled `mermaid` library, so there is no MermaidConfig.

// SyncConfig — worker settings.
type SyncConfig struct {
	DefaultInterval time.Duration `yaml:"default_interval"`
	Workers         int           `yaml:"workers"`
}

// ThumbsConfig — generation policy.
type ThumbsConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Formats  []string `yaml:"formats"`
	CacheDir string   `yaml:"cache_dir"`
}

// SearchConfig — bleve index.
type SearchConfig struct {
	Enabled   bool   `yaml:"enabled"`
	IndexPath string `yaml:"index_path"`
	// Content toggles async file-content extraction into the index ("Bul"
	// wave). ON by default; FILEX_SEARCH_CONTENT=0 is the kill-switch.
	Content bool `yaml:"content"`
	// ContentMaxBytes caps the SOURCE file size eligible for extraction
	// (FILEX_SEARCH_CONTENT_MAX). <=0 falls back to 5 MiB. The extracted
	// text itself is always capped at 200 KiB.
	ContentMaxBytes int64 `yaml:"content_max_bytes"`
}

// CORSConfig — origin allowlist.
type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins"`
	AllowedMethods []string `yaml:"allowed_methods"`
	AllowedHeaders []string `yaml:"allowed_headers"`
}

// Default returns a Config populated with sensible defaults.
func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Listen:    "0.0.0.0:5212",
		PublicURL: "http://localhost:5212",
		DataDir:   filepath.Join(home, ".filex"),
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		DB: DBConfig{
			Driver: "sqlite",
			DSN:    "", // resolved at boot if empty
		},
		Auth: AuthConfig{
			Drivers: []string{"local"},
		},
		Sync: SyncConfig{
			DefaultInterval: 15 * time.Minute,
			Workers:         4,
		},
		Thumbs: ThumbsConfig{
			Enabled: true,
			Formats: []string{"image", "video", "pdf", "office"},
		},
		Search: SearchConfig{
			Enabled:         true,
			Content:         true,
			ContentMaxBytes: 5 << 20,
		},
		Queue: QueueConfig{
			Driver:  "sqlite",
			Workers: 4,
			Enabled: true,
		},
		Notify: NotifyConfig{
			Enabled: true,
		},
		DAV: DAVConfig{
			Enabled: true,
		},
		Demo: DemoConfig{
			Mode: false,
			User: "demo@demo.com",
			Pass: "demo",
		},
		CORS: CORSConfig{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
			AllowedHeaders: []string{"Authorization", "Content-Type", "X-Filex-Pin"},
		},
	}
}

// Load reads a YAML file and applies environment overrides. Pass empty
// path for defaults + env only.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		expanded := expandHome(path)
		if data, err := os.ReadFile(expanded); err == nil {
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return Config{}, fmt.Errorf("config: yaml: %w", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("config: read %s: %w", expanded, err)
		}
	}
	applyEnv(&cfg)
	// Default the OIDC redirect to <public_url>/api/auth/oidc/callback so an
	// issuer + client id/secret are enough to stand up SSO (no need to also
	// spell out the callback URL).
	if cfg.Auth.OIDC.Issuer != "" && cfg.Auth.OIDC.RedirectURL == "" {
		cfg.Auth.OIDC.RedirectURL = strings.TrimRight(cfg.PublicURL, "/") + "/api/auth/oidc/callback"
	}
	cfg.DataDir = expandHome(cfg.DataDir)
	if cfg.DB.Driver == "sqlite" && cfg.DB.DSN == "" {
		cfg.DB.DSN = filepath.Join(cfg.DataDir, "instance.sqlite")
	}
	if cfg.Search.IndexPath == "" {
		cfg.Search.IndexPath = filepath.Join(cfg.DataDir, "search.bleve")
	}
	if cfg.Thumbs.CacheDir == "" {
		cfg.Thumbs.CacheDir = filepath.Join(cfg.DataDir, "thumbs")
	}
	return cfg, nil
}

// getenvFirst returns the value of the first non-empty env var.
// Used by applyEnv to honor both the short FILEX_OIDC_* prefix
// (current convention) and the legacy FILEX_AUTH_OIDC_* prefix.
func getenvFirst(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// applyEnv overrides the config from FILEX_* environment variables.
func applyEnv(c *Config) {
	if v := os.Getenv("FILEX_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("FILEX_PUBLIC_URL"); v != "" {
		c.PublicURL = v
	}
	if v := os.Getenv("FILEX_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("FILEX_DEFAULT_LOCALE"); v != "" {
		c.DefaultLocale = v
	}
	if v := os.Getenv("FILEX_MULTI_TENANT"); v == "1" || v == "true" {
		c.MultiTenant = true
	}
	if v := os.Getenv("FILEX_COOKIE_DOMAIN"); v != "" {
		c.CookieDomain = v
	}
	if v := os.Getenv("FILEX_LOG_LEVEL"); v != "" {
		c.Log.Level = v
	}
	if v := os.Getenv("FILEX_SENTRY_DSN"); v != "" {
		c.Sentry.DSN = v
	}
	if v := os.Getenv("FILEX_SENTRY_ENVIRONMENT"); v != "" {
		c.Sentry.Environment = v
	}
	if v := os.Getenv("FILEX_LOG_FORMAT"); v != "" {
		c.Log.Format = v
	}
	if v := os.Getenv("FILEX_DB_DRIVER"); v != "" {
		c.DB.Driver = v
	}
	if v := os.Getenv("FILEX_DB_DSN"); v != "" {
		c.DB.DSN = v
	}
	// OIDC env mapping accepts both prefixes:
	//   FILEX_OIDC_*       (deploy/.env.example + docs)
	//   FILEX_AUTH_OIDC_*  (legacy from earlier draft of this file)
	// The shorter form wins when both are set, matching the convention
	// used in deploy/demo-fm.brf.sh.compose.yml + plan files.
	if v := os.Getenv("FILEX_AUTH_DRIVERS"); v != "" {
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			c.Auth.Drivers = out
		}
	}
	if v := getenvFirst("FILEX_OIDC_ISSUER", "FILEX_AUTH_OIDC_ISSUER"); v != "" {
		c.Auth.OIDC.Issuer = v
	}
	if v := getenvFirst("FILEX_OIDC_CLIENT_ID", "FILEX_AUTH_OIDC_CLIENT_ID"); v != "" {
		c.Auth.OIDC.ClientID = v
	}
	if v := getenvFirst("FILEX_OIDC_CLIENT_SECRET", "FILEX_AUTH_OIDC_CLIENT_SECRET"); v != "" {
		c.Auth.OIDC.ClientSecret = v
	}
	if v := getenvFirst("FILEX_OIDC_REDIRECT_URL", "FILEX_AUTH_OIDC_REDIRECT_URL"); v != "" {
		c.Auth.OIDC.RedirectURL = v
	}
	if v := getenvFirst("FILEX_OIDC_ROLE_CLAIM", "FILEX_AUTH_OIDC_ROLE_CLAIM"); v != "" {
		c.Auth.OIDC.RoleClaim = v
	}
	if v := getenvFirst("FILEX_OIDC_ADMIN_GROUP", "FILEX_AUTH_OIDC_ADMIN_GROUP"); v != "" {
		c.Auth.OIDC.AdminGroup = v
	}
	if v := getenvFirst("FILEX_OIDC_AUTO_REDIRECT", "FILEX_AUTH_OIDC_AUTO_REDIRECT"); v != "" {
		c.Auth.OIDC.AutoRedirect = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_ONLYOFFICE_URL"); v != "" {
		c.ExternalServices.OnlyOffice.URL = v
	}
	if v := os.Getenv("FILEX_ONLYOFFICE_JWT"); v != "" {
		c.ExternalServices.OnlyOffice.JWTSecret = v
	}
	if v := os.Getenv("FILEX_DRAWIO_URL"); v != "" {
		c.ExternalServices.Drawio.URL = v
	}
	if v := os.Getenv("FILEX_CONVERT_URL"); v != "" {
		c.ExternalServices.Convert.URL = v
	}
	if v := os.Getenv("FILEX_SYNC_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Sync.DefaultInterval = d
		}
	}
	if v := os.Getenv("FILEX_SYNC_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Sync.Workers = n
		}
	}
	if v := os.Getenv("FILEX_THUMBS_ENABLED"); v != "" {
		c.Thumbs.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_SEARCH_ENABLED"); v != "" {
		c.Search.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_SEARCH_CONTENT"); v != "" {
		c.Search.Content = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_SEARCH_CONTENT_MAX"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			c.Search.ContentMaxBytes = n
		}
	}
	if v := os.Getenv("FILEX_CORS_ALLOWED_ORIGINS"); v != "" {
		c.CORS.AllowedOrigins = strings.Split(v, ",")
	}
	if v := os.Getenv("FILEX_QUEUE_DRIVER"); v != "" {
		c.Queue.Driver = v
	}
	if v := os.Getenv("FILEX_QUEUE_DSN"); v != "" {
		c.Queue.DSN = v
	}
	if v := os.Getenv("FILEX_QUEUE_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Queue.Workers = n
		}
	}
	if v := os.Getenv("FILEX_QUEUE_ENABLED"); v != "" {
		c.Queue.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_NOTIFY_ENABLED"); v != "" {
		c.Notify.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_DAV"); v != "" {
		c.DAV.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_WEBHOOK_URL"); v != "" {
		c.Notify.WebhookURL = v
	}
	if v := os.Getenv("FILEX_WEBHOOK_TOKEN"); v != "" {
		c.Notify.WebhookToken = v
	}
	if v := os.Getenv("FILEX_DEMO_MODE"); v != "" {
		c.Demo.Mode = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_DEMO_USER"); v != "" {
		c.Demo.User = v
	}
	if v := os.Getenv("FILEX_DEMO_PASS"); v != "" {
		c.Demo.Pass = v
	}

	// LDAP directory bind (previously YAML-only). Enable with
	// FILEX_AUTH_DRIVERS=local,ldap.
	if v := os.Getenv("FILEX_LDAP_URL"); v != "" {
		c.Auth.LDAP.URL = v
	}
	if v := os.Getenv("FILEX_LDAP_BIND_DN"); v != "" {
		c.Auth.LDAP.BindDN = v
	}
	if v := os.Getenv("FILEX_LDAP_BIND_PASSWORD"); v != "" {
		c.Auth.LDAP.BindPassword = v
	}
	if v := os.Getenv("FILEX_LDAP_BASE_DN"); v != "" {
		c.Auth.LDAP.BaseDN = v
	}
	if v := os.Getenv("FILEX_LDAP_USER_FILTER"); v != "" {
		c.Auth.LDAP.UserFilter = v
	}
	if v := os.Getenv("FILEX_LDAP_EMAIL_ATTR"); v != "" {
		c.Auth.LDAP.EmailAttr = v
	}
	if v := os.Getenv("FILEX_LDAP_START_TLS"); v != "" {
		c.Auth.LDAP.StartTLS = v == "1" || strings.EqualFold(v, "true")
	}

	// Reverse-proxy header auth (previously YAML-only). Enable with
	// FILEX_AUTH_DRIVERS=proxy_header.
	if v := os.Getenv("FILEX_HEADER_EMAIL"); v != "" {
		c.Auth.Header.EmailHeader = v
	}
	if v := os.Getenv("FILEX_HEADER_GROUP"); v != "" {
		c.Auth.Header.GroupHeader = v
	}
	if v := os.Getenv("FILEX_HEADER_TRUSTED_IPS"); v != "" {
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		c.Auth.Header.TrustedIPs = out
	}
	if v := os.Getenv("FILEX_HEADER_ADMIN_GROUP"); v != "" {
		c.Auth.Header.AdminGroup = v
	}

	// ── Boot seeds (env → DB rows on first boot, only-if-absent) ──────
	if v := os.Getenv("FILEX_ADMIN_EMAIL"); v != "" {
		c.Seed.AdminEmail = v
	}
	if v := os.Getenv("FILEX_ADMIN_PASSWORD"); v != "" {
		c.Seed.AdminPassword = v
	}
	if v := os.Getenv("FILEX_SITE_NAME"); v != "" {
		c.Seed.SiteName = v
	}
	if v := os.Getenv("FILEX_TRASH_RETENTION_DAYS"); v != "" {
		c.Seed.TrashDays = v
	}
	if v := os.Getenv("FILEX_SMTP_HOST"); v != "" {
		c.Seed.SMTP.Host = v
	}
	if v := os.Getenv("FILEX_SMTP_PORT"); v != "" {
		c.Seed.SMTP.Port = v
	}
	if v := os.Getenv("FILEX_SMTP_USERNAME"); v != "" {
		c.Seed.SMTP.Username = v
	}
	if v := os.Getenv("FILEX_SMTP_PASSWORD"); v != "" {
		c.Seed.SMTP.Password = v
	}
	if v := os.Getenv("FILEX_SMTP_FROM"); v != "" {
		c.Seed.SMTP.From = v
	}
	if v := os.Getenv("FILEX_SMTP_TLS"); v != "" {
		c.Seed.SMTP.TLS = v
	}
	if v := os.Getenv("FILEX_DEFAULT_STORAGE_DRIVER"); v != "" {
		c.Seed.Storage.Driver = v
	}
	if v := os.Getenv("FILEX_DEFAULT_STORAGE_NAME"); v != "" {
		c.Seed.Storage.Name = v
	}
	if v := os.Getenv("FILEX_DEFAULT_STORAGE_MOUNT"); v != "" {
		c.Seed.Storage.MountPath = v
	}
	if v := os.Getenv("FILEX_DEFAULT_STORAGE_PATH"); v != "" {
		c.Seed.Storage.Path = v
	}
	if v := os.Getenv("FILEX_DEFAULT_STORAGE_CONFIG"); v != "" {
		c.Seed.Storage.Config = v
	}
	if v := os.Getenv("FILEX_DEFAULT_STORAGE_S3_BUCKET"); v != "" {
		c.Seed.Storage.Bucket = v
	}
	if v := os.Getenv("FILEX_DEFAULT_STORAGE_S3_PREFIX"); v != "" {
		c.Seed.Storage.Prefix = v
	}
	if v := os.Getenv("FILEX_DEFAULT_STORAGE_S3_ENDPOINT"); v != "" {
		c.Seed.Storage.Endpoint = v
	}
	if v := os.Getenv("FILEX_DEFAULT_STORAGE_S3_REGION"); v != "" {
		c.Seed.Storage.Region = v
	}
	if v := os.Getenv("FILEX_DEFAULT_STORAGE_S3_ACCESS_KEY"); v != "" {
		c.Seed.Storage.AccessKey = v
	}
	if v := os.Getenv("FILEX_DEFAULT_STORAGE_S3_SECRET_KEY"); v != "" {
		c.Seed.Storage.SecretKey = v
	}
	if v := os.Getenv("FILEX_DEFAULT_STORAGE_S3_PATH_STYLE"); v != "" {
		c.Seed.Storage.PathStyle = v == "1" || strings.EqualFold(v, "true")
	}
}
