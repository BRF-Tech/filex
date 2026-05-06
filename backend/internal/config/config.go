// Package config loads filex configuration from a YAML file with
// environment variable overrides (FILEX_*).
//
// Precedence (highest first):
//   1. Environment variables (FILEX_LISTEN, FILEX_DB_DRIVER, …)
//   2. config.yaml (path passed via --config or default ~/.filex/config.yaml)
//   3. Hard-coded defaults
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
	Listen           string         `yaml:"listen"`
	PublicURL        string         `yaml:"public_url"`
	DataDir          string         `yaml:"data_dir"`
	Log              LogConfig      `yaml:"log"`
	DB               DBConfig       `yaml:"db"`
	Auth             AuthConfig     `yaml:"auth"`
	ExternalServices ExtServices    `yaml:"external_services"`
	Sync             SyncConfig     `yaml:"sync"`
	Thumbs           ThumbsConfig   `yaml:"thumbs"`
	Search           SearchConfig   `yaml:"search"`
	CORS             CORSConfig     `yaml:"cors"`
	Queue            QueueConfig    `yaml:"queue"`
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
	Drivers []string             `yaml:"drivers"`
	OIDC    OIDCConfig           `yaml:"oidc"`
	LDAP    LDAPConfig           `yaml:"ldap"`
	Header  HeaderProxyConfig    `yaml:"header_proxy"`
}

// OIDCConfig — Keycloak/Auth0/etc.
type OIDCConfig struct {
	Issuer       string `yaml:"issuer"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	RedirectURL  string `yaml:"redirect_url"`
	RoleClaim    string `yaml:"role_claim"`
	AdminGroup   string `yaml:"admin_group"`
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
			Enabled: true,
		},
		Queue: QueueConfig{
			Driver:  "sqlite",
			Workers: 4,
			Enabled: true,
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
	if v := os.Getenv("FILEX_LOG_LEVEL"); v != "" {
		c.Log.Level = v
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
	if v := os.Getenv("FILEX_AUTH_OIDC_ISSUER"); v != "" {
		c.Auth.OIDC.Issuer = v
	}
	if v := os.Getenv("FILEX_AUTH_OIDC_CLIENT_ID"); v != "" {
		c.Auth.OIDC.ClientID = v
	}
	if v := os.Getenv("FILEX_AUTH_OIDC_CLIENT_SECRET"); v != "" {
		c.Auth.OIDC.ClientSecret = v
	}
	if v := os.Getenv("FILEX_AUTH_OIDC_REDIRECT_URL"); v != "" {
		c.Auth.OIDC.RedirectURL = v
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
}
