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
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level runtime configuration object.
type Config struct {
	Listen    string `yaml:"listen"`
	PublicURL string `yaml:"public_url"`
	// PublicURLSet reports whether the operator actually chose PublicURL,
	// rather than inheriting DefaultPublicURL.
	//
	// ⚠ It matters because the default is a GUESS and filex hands it out as
	// fact: the realtime ticket derives `ws_url` from it, so an instance on any
	// other port told every browser to connect to :5212, the connection was
	// refused, and the client fell back to 12-second polling with no error
	// raised anywhere. Not serialised -- it describes how the config was
	// loaded, not the deployment.
	PublicURLSet  bool   `yaml:"-"`
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
	MultiTenant bool `yaml:"multi_tenant"`
	// PluginsDisabled (FILEX_PLUGINS_DISABLED=1) turns the storage-plugin
	// subsystem off: nothing under <data-dir>/plugins is launched, no remote
	// plugin is contacted, and the admin API answers 503.
	//
	// ⚠ DEMO MODE FLIPS THE DEFAULT: with FILEX_DEMO_MODE on, plugins are off
	// unless this variable explicitly says otherwise, because a demo hands an
	// admin account to strangers and installing a plugin runs a program on the
	// host. Set FILEX_PLUGINS_DISABLED=0 to override that deliberately.
	//
	// Otherwise ON by default,
	// because a plugin is only ever installed by an admin — but an operator
	// hardening a shared instance may not want the admin role to include
	// "run a program on the server". See docs/PLUGINS.md.
	PluginsDisabled bool `yaml:"plugins_disabled"`
	// PluginConformance (FILEX_PLUGIN_CONFORMANCE) is enforce | warn | off.
	//
	// enforce (default): a plugin that fails a probe of a capability IT
	// DECLARED is refused, and a storage cannot be saved on it. The reason a
	// half-working plugin must not be usable is that the user cannot tell
	// whose fault it is: they meet an upload button that fails and read it as
	// filex being broken. warn registers it anyway and keeps the report (for
	// somebody writing a plugin); off skips the probes.
	PluginConformance string `yaml:"plugin_conformance"`
	// PluginTrustedKeys (FILEX_PLUGIN_TRUSTED_KEYS, comma-separated) are
	// ed25519 public keys, hex or base64. Set ANY key and filex refuses a
	// plugin binary that is not signed by one of them.
	//
	// ⚠ The sha256 an install already records proves only that the file has
	// not changed since it arrived — never that it came from somebody you
	// trust. This is the setting that answers the second question, and until
	// it is set nothing is asked.
	PluginTrustedKeys []string `yaml:"plugin_trusted_keys"`
	// PluginMaxInFlight (FILEX_PLUGIN_MAX_INFLIGHT) bounds concurrent
	// operations per plugin. 0 keeps the default; a plugin at its ceiling
	// refuses new work rather than queueing it, which is what stops one slow
	// plugin from becoming one slow filex.
	PluginMaxInFlight int `yaml:"plugin_max_in_flight"`
	// SecretKey (FILEX_SECRET_KEY) encrypts the secrets filex has to be able to
	// read back rather than merely compare — today the S3 access keys, because
	// SigV4 derives an HMAC chain from the secret and so cannot work off a
	// one-way hash. See internal/secretbox for what this does and does not buy.
	//
	// ⚠ Losing it makes every credential sealed under it unusable; they must be
	// re-issued. It is not a password to rotate casually — treat it like the
	// database, because losing one is as bad as losing the other.
	SecretKey        string       `yaml:"secret_key"`
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
	S3               S3Config     `yaml:"s3"`
	SFTP             SFTPConfig   `yaml:"sftp"`
	FTPS             FTPSConfig   `yaml:"ftps"`
	NFS              NFSConfig    `yaml:"nfs"`
	Update           UpdateConfig `yaml:"update"`
	Upload           UploadConfig `yaml:"upload"`
	Cache            CacheConfig  `yaml:"cache"`
	/* kimlik:e3 cloud */
	Cloud CloudConfig `yaml:"cloud"`
	// VersionsOnOverwrite installs the pre-write versioning guard. Default on.
	// FILEX_VERSIONS_ON_OVERWRITE=0 turns it off -- the escape hatch for a
	// deployment where a broken or full object store would otherwise refuse
	// writes -- and =1 turns it back on, including over a config.yaml that set
	// versions_on_overwrite: false (env wins; see applyEnv).
	// NOTE: versions.keep_n=0 does NOT do this (0 means "unlimited"; the
	// snapshot path falls back to DefaultRetention).
	VersionsOnOverwrite bool `yaml:"versions_on_overwrite"`
	// VersionsFailOpen lets a snapshot failure fall through to the write
	// instead of refusing it. Default false -- Service.Snapshot's own contract
	// is that a caller must NOT proceed past a failed snapshot. It exists
	// because the fail-closed default has a sharp edge: if the object store
	// fills up, snapshots start failing and EVERY overwrite on the instance is
	// refused (desktop sync, WebDAV, OnlyOffice saves, the browser) and the
	// only other lever is an env var plus a restart.
	VersionsFailOpen bool `yaml:"versions_fail_open"`
}

// CacheConfig — the local copy filex prepares for a big file that lives on a
// slow backend (internal/filecache). See docs/CONFIGURATION.md.
type CacheConfig struct {
	// Dir is where prepared copies live. Defaults to <data_dir>/cache.
	Dir string `yaml:"dir"`
	// MinSize is the smallest file worth preparing (FILEX_CACHE_MIN_SIZE,
	// default 64 MiB). Below it the preparation costs more than the transfer
	// it saves.
	MinSize int64 `yaml:"min_size"`
	// MaxBytes is the GLOBAL ceiling on the cache directory
	// (FILEX_CACHE_MAX_BYTES, default 20 GiB), enforced with LRU eviction. It
	// is never unlimited: a per-file cache with no global cap is a disk
	// incident, and this project has had one (29 GB of leaked multipart temp
	// files, v0.13.4).
	MaxBytes int64 `yaml:"max_bytes"`
	// SlowBytesPerSec is the measured throughput below which a storage counts
	// as slow (FILEX_CACHE_SLOW_BPS, default 10 MiB/s).
	SlowBytesPerSec int64 `yaml:"slow_bytes_per_sec"`
	// Disabled turns the whole thing off (FILEX_CACHE=0). With no slow
	// storage configured it never does anything anyway.
	Disabled bool `yaml:"disabled"`

	// ShareZipWarmMaxBytes is the largest folder (sum of file sizes) the
	// folder-share ZIP warmer pre-builds (FILEX_SHAREZIP_WARM_MAX_BYTES,
	// default 2 GiB; 0 = no ceiling). Bigger folders are still zipped — on
	// demand, when a visitor clicks download — they are just not built
	// speculatively. See docs/SHARING.md.
	ShareZipWarmMaxBytes int64 `yaml:"sharezip_warm_max_bytes"`
	// ShareZipMaxAge is how long a cached folder-share ZIP may sit on disk
	// before the sweeper removes it even though its share is still live
	// (FILEX_SHAREZIP_MAX_AGE, default 7d; 0 = keep for the share's life).
	// Accepts Go durations plus a `d` suffix ("7d", "36h").
	ShareZipMaxAge time.Duration `yaml:"sharezip_max_age"`
}

// UploadConfig — staged (resumable, driver-agnostic) uploads. See
// docs/UPLOADS.md.
type UploadConfig struct {
	// StagingDir is where in-flight upload parts live. Defaults to
	// <data_dir>/uploads. Must be on a filesystem with room for the largest
	// upload you expect — the whole object passes through it.
	StagingDir string `yaml:"staging_dir"`
	// ChunkSize is the part size handed to clients that do not ask for one.
	// Clients may request a different size; the server's answer is binding.
	ChunkSize int64 `yaml:"chunk_size"`
	// StagingTTL is how long a staging directory may sit with no activity
	// before the sweeper removes it. FILEX_UPLOAD_STAGING_TTL.
	StagingTTL time.Duration `yaml:"staging_ttl"`
	// SweepInterval is how often the sweeper runs.
	SweepInterval time.Duration `yaml:"sweep_interval"`
}

// UpdateConfig — release awareness and (where the install owns its binary)
// self-upgrade. See docs/UPDATES.md.
//
// The default is "check and tell me, change nothing": Enabled=true with
// Policy="manual". Nothing moves on its own until the operator opts in with
// AUTO_UPGRADE=true (= policy "patch"). Checking can be turned off entirely
// with FILEX_UPDATE_CHECK=0, which stops all outbound requests.
type UpdateConfig struct {
	// Enabled controls the periodic check. False = no outbound request at all.
	Enabled bool `yaml:"enabled"`
	// Policy: off | manual | patch | minor. Which part of x.y.z may be applied
	// without asking. "patch" is what AUTO_UPGRADE=true selects.
	Policy string `yaml:"policy"`
	// Channel selects the manifest flavor ("stable").
	Channel string `yaml:"channel"`
	// ManifestURL overrides the default release index (mirrors, forks,
	// air-gapped installs that publish their own).
	ManifestURL string `yaml:"manifest_url"`
	// Window is a daily maintenance window for automatic upgrades,
	// "03:00-05:00" in the server's local time. Empty = any time.
	Window string `yaml:"window"`
	// Interval between checks. Zero → 24h; anything under an hour is raised to
	// an hour (a self-hosted install has no reason to poll harder).
	Interval time.Duration `yaml:"interval"`
}

// CloudConfig — self-serve cloud/SaaS PREPARATION (v0.7 "Kimlik" E3, see
// docs/CLOUD.md). Master-gated by Enabled (FILEX_CLOUD): while it is false —
// the default — nothing in the cloud package is constructed, no /api/cloud
// route registers, capabilities carry no cloud field and the migration-00021
// columns stay untouched. This is a skeleton for a FUTURE hosted offering,
// not a live service.
type CloudConfig struct {
	// Enabled is the master flag (FILEX_CLOUD=1). Default false.
	Enabled bool `yaml:"enabled"`
	// PlansJSON is the config-driven plan catalog (FILEX_CLOUD_PLANS), a JSON
	// array of {id,name,price_monthly,stripe_price_id,limits:{storage_bytes,
	// max_users}}. Empty → a single built-in "free" skeleton plan.
	PlansJSON string `yaml:"plans"`
	// StripeSecret is the Stripe API secret key (STRIPE_SECRET /
	// FILEX_STRIPE_SECRET). Empty → billing endpoints answer 503
	// "not configured".
	StripeSecret string `yaml:"stripe_secret"`
	// BaseHost, when set (FILEX_CLOUD_BASE_HOST, e.g. "filex.cloud"), derives
	// each signed-up tenant's host as <slug>.<BaseHost>. Empty → the tenant is
	// provisioned without a host (operator assigns one later).
	BaseHost string `yaml:"base_host"`
}

// DAVConfig — the WebDAV server surface at /dav (v0.3 "Bağlan" (Connect)). ON by
// default; FILEX_DAV=0 is the kill switch (the handler then answers 404).
type DAVConfig struct {
	Enabled bool `yaml:"enabled"`
}

// S3Config controls the S3-compatible endpoint (internal/s3api).
type S3Config struct {
	// Enabled — FILEX_S3 kill switch. ON by default like /dav: the endpoint
	// refuses every unsigned request anyway, and an access key has to be
	// minted before anything can reach it.
	Enabled bool `yaml:"enabled"`
	// Domain enables virtual-hosted-style addressing (bucket.s3.example.com).
	// Empty leaves path-style only, which is what restic and the older SDKs
	// use — so an install without a wildcard DNS record still works, it just
	// needs `path_style` in the client config.
	//
	// ⚠ Setting it requires a wildcard A record AND a wildcard certificate for
	// *.<domain>; without both, current SDKs (which default to virtual-hosted)
	// fail at TLS rather than with anything that names the cause.
	Domain string `yaml:"domain"`
}

// SFTPConfig controls the SFTP endpoint (internal/sftpsrv).
type SFTPConfig struct {
	// Enabled — FILEX_SFTP. OFF by default, unlike /dav and S3: this surface
	// opens a TCP port of its own, and a port nobody asked for is not something
	// to switch on for them.
	Enabled bool `yaml:"enabled"`
	// Addr is the listen address. 2022 by convention — sftpgo and
	// `rclone serve sftp` both use it, while 2222 means "SSH in a container".
	Addr string `yaml:"addr"`
	// HostKeyDir holds the server's host keys.
	//
	// ⚠ It must survive a rebuild. Regenerating host keys gives every user
	// REMOTE HOST IDENTIFICATION HAS CHANGED — indistinguishable from an attack,
	// and usually "fixed" by deleting the known_hosts line, which trains people
	// to ignore the one warning that matters. Empty puts them in the data dir.
	HostKeyDir string `yaml:"host_key_dir"`
	// Banner is shown before authentication. Empty sends none.
	Banner string `yaml:"banner"`
	// MaxSpool caps one upload's spool file. 0 uses the package default.
	MaxSpool int64 `yaml:"max_spool"`
}

// FTPSConfig controls the FTPS endpoint (internal/ftpsrv).
//
// ⚠⚠ There is no "plain FTP" switch and there will not be one. Plain FTP sends
// the password in the clear and the file after it; a flag that turned TLS off
// would be a flag that publishes a user's credentials to anything on the path.
type FTPSConfig struct {
	// Enabled — FILEX_FTPS. OFF by default: this one opens a control port AND
	// a range of data ports.
	Enabled bool `yaml:"enabled"`
	// Addr is the control channel (2121 by convention; 21 needs root).
	Addr string `yaml:"addr"`
	// PublicHost is what passive-mode replies advertise.
	//
	// ⚠⚠ The setting FTP deployments get wrong, and it fails as a HANG rather
	// than an error: the server answers PASV with an address the client cannot
	// route to and the client waits until it times out. Behind NAT or in Docker
	// it must be the address the CLIENT would use.
	PublicHost string `yaml:"public_host"`
	// PassivePortMin/Max bound the data ports. They must be open in the
	// firewall and, under Docker, published one-for-one — a range open on the
	// server and closed at the edge is the same hang.
	PassivePortMin int `yaml:"passive_port_min"`
	PassivePortMax int `yaml:"passive_port_max"`
	// CertFile/KeyFile are the TLS material. Empty generates a self-signed pair
	// into the data dir — encrypted but unverified, and the guide says so.
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	// Banner is the greeting line.
	Banner string `yaml:"banner"`
	// IdleTimeout in seconds; 0 uses the package default.
	IdleTimeout int `yaml:"idle_timeout"`
}

// NFSConfig controls the NFSv3 endpoint (internal/nfssrv).
//
// ⚠⚠ NFSv3 is unencrypted and unauthenticated on the wire. filex binds identity
// to the EXPORT PATH (32 bytes of entropy, see model.NFSExport) rather than
// trusting the uid a client asserts — but the traffic itself is still readable
// by anything on the path. This is the NAS-on-the-LAN protocol; `filex mount`
// (FUSE over HTTPS) is the answer for anything off-LAN.
type NFSConfig struct {
	// Enabled — FILEX_NFS. OFF by default, and more emphatically than the
	// others: switching it on is a decision about the network it sits on.
	Enabled bool `yaml:"enabled"`
	// Addr is the listen address. 2049 is the registered port and what clients
	// assume; anything else needs `-o port=` on every mount.
	Addr string `yaml:"addr"`
}

// SeedConfig holds one-time bootstrap values applied to the DB on first boot,
// only when the target record is ABSENT (operator UI edits are never
// clobbered). It lets a fresh `helm install` / `docker compose up` come up
// fully configured from env alone — admin user, SMTP, branding, trash policy
// and an initial storage — with zero admin-UI clicks. Consumed by
// internal/server/seed.go. (Auth/OIDC is env-authoritative already via
// AuthConfig, so it is not duplicated here.)
type SeedConfig struct {
	AdminEmail    string `yaml:"admin_email"`
	AdminPassword string `yaml:"admin_password"`
	SiteName      string `yaml:"site_name"`
	TrashDays     string `yaml:"trash_retention_days"`
	// ShareMaxTTLDays seeds the `share.max_ttl_days` setting — the longest
	// life a NEW share link may be given (FILEX_SHARE_MAX_TTL, e.g. "7" or
	// "30d"; "0" = unlimited). Like the other seeds it only applies when the
	// setting has never been written; after that the admin UI owns it.
	ShareMaxTTLDays string      `yaml:"share_max_ttl_days"`
	SMTP            SeedSMTP    `yaml:"smtp"`
	Storage         SeedStorage `yaml:"storage"`
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
// errors.example.com). An empty DSN disables it entirely (default build reports
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
	// CAFile is a PEM bundle holding the CA that signed the directory's
	// certificate. Empty verifies against the system trust store, which is what
	// the driver always did — and what forced installs with a private/internal
	// CA to rebuild the container's /etc/ssl/certs/ca-certificates.crt just to
	// reach their own AD. Appended to the system pool, not substituted for it.
	CAFile string `yaml:"ca_file"`
	// ProtocolLogin lets directory accounts authenticate over the non-HTTP
	// protocols (WebDAV, SFTP, FTPS, S3, NFS) with their directory password.
	// ON by default: with it off, an account that can sign in to the web UI is
	// answered 401 everywhere else, which is indistinguishable from a wrong
	// password. Turn it off to keep the directory password on the login form
	// only and require an API token everywhere else.
	//
	// ⚠ Accounts with TOTP enabled are refused on those protocols regardless,
	// exactly as local passwords are — none of them has a second-factor
	// channel. See internal/protocolauth.
	ProtocolLogin bool `yaml:"protocol_login"`
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

// SyncConfig — storage sync settings.
//
// ⚠ There is deliberately no `Workers` here any more. FILEX_SYNC_WORKERS was
// parsed into a field that nothing read, while CONFIGURATION.md advertised it
// as "concurrent storage sync workers" — there is no pool to size: the sync
// worker runs one goroutine per enabled storage (internal/sync.Worker.startOne)
// and always has. Inventing a pool to justify the variable would be a
// redesign; documenting a knob that does nothing is worse than either, so the
// knob is gone and the doc says what actually happens.
type SyncConfig struct {
	// DefaultInterval is the poll cadence for storages with no
	// sync_interval_s of their own. FILEX_SYNC_INTERVAL.
	DefaultInterval time.Duration `yaml:"default_interval"`
}

// ThumbsConfig — generation policy.
type ThumbsConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Formats  []string `yaml:"formats"`
	CacheDir string   `yaml:"cache_dir"`
	// SweepInterval is how often the cached JPEGs are reconciled against the
	// node catalogue, so a file whose node no longer exists stops occupying
	// disk (FILEX_THUMBS_SWEEP_INTERVAL, default 6h, 0 = never).
	//
	// ⚠ The sweeper is deliberately independent of Enabled: an operator who
	// turns thumbnails OFF still has whatever the cache accumulated while they
	// were on, and that is exactly when nobody is watching it.
	SweepInterval time.Duration `yaml:"sweep_interval"`
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
	// AutoRebuild lets filex repair an index written by an older document
	// schema, in the background, at startup. ON by default;
	// FILEX_SEARCH_AUTO_REBUILD=0 is the kill-switch.
	//
	// It is on by default because the alternative shipped once and did not
	// work: v0.29.0 improved filename matching for everyone EXCEPT the
	// installations that already had an index, told them so in an admin
	// endpoint nobody reads, and the issue reporter upgraded and correctly
	// said nothing had changed.
	AutoRebuild bool `yaml:"auto_rebuild"`
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
		PublicURL: DefaultPublicURL,
		DataDir:   filepath.Join(home, ".filex"),
		// Fail-closed by default -- see the field docs on Config.
		VersionsOnOverwrite: true,
		VersionsFailOpen:    false,
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
			LDAP: LDAPConfig{
				ProtocolLogin: true,
			},
		},
		Sync: SyncConfig{
			DefaultInterval: 15 * time.Minute,
		},
		Thumbs: ThumbsConfig{
			Enabled:       true,
			Formats:       []string{"image", "video", "pdf", "office"},
			SweepInterval: 6 * time.Hour,
		},
		Search: SearchConfig{
			Enabled:         true,
			Content:         true,
			ContentMaxBytes: 5 << 20,
			AutoRebuild:     true,
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
		S3: S3Config{
			Enabled: true,
		},
		Update: UpdateConfig{
			Enabled:  true,
			Policy:   "manual",
			Channel:  "stable",
			Interval: 24 * time.Hour,
		},
		Upload: UploadConfig{
			ChunkSize:     8 << 20,
			StagingTTL:    24 * time.Hour,
			SweepInterval: time.Hour,
		},
		Cache: CacheConfig{
			ShareZipWarmMaxBytes: 2 << 30,
			ShareZipMaxAge:       7 * 24 * time.Hour,
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

// ParseDurationDays is time.ParseDuration plus a `d` (day) suffix, which Go's
// parser lacks and which is the unit people actually write retention in.
// "7d" = 168h. A bare number is also accepted as days, so FILEX_SHARE_MAX_TTL=7
// and =7d mean the same thing.
func ParseDurationDays(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, errors.New("empty duration")
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	if strings.HasSuffix(v, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(v, "d"), 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(v)
}

// DefaultPublicURL is the address filex assumes when nobody says otherwise.
// ⚠ It is a guess, and anything derived from it must be able to tell that it
// is one -- see Config.PublicURLSet.
const DefaultPublicURL = "http://localhost:5212"

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
	// Set by either source -- a YAML `public_url` counts as much as the env
	// var. Comparing against the default rather than tracking each writer
	// keeps this true no matter how many ways there are to set it; an operator
	// who spells out the default gets the default, which is what they asked
	// for.
	cfg.PublicURLSet = cfg.PublicURL != DefaultPublicURL
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
	if cfg.Upload.StagingDir == "" {
		cfg.Upload.StagingDir = filepath.Join(cfg.DataDir, "uploads")
	}
	if cfg.Cache.Dir == "" {
		cfg.Cache.Dir = filepath.Join(cfg.DataDir, "cache")
	}
	if cfg.Upload.ChunkSize <= 0 {
		cfg.Upload.ChunkSize = 8 << 20
	}
	if cfg.Upload.StagingTTL <= 0 {
		cfg.Upload.StagingTTL = 24 * time.Hour
	}
	if cfg.Upload.SweepInterval <= 0 {
		cfg.Upload.SweepInterval = time.Hour
	}
	// ⚠ Only a NEGATIVE value falls back to the default here. Zero is the
	// operator saying "never sweep the thumbnail cache", and a <= 0 test would
	// silently turn that kill switch back on.
	if cfg.Thumbs.SweepInterval < 0 {
		cfg.Thumbs.SweepInterval = 6 * time.Hour
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
	pluginsSpoken := false
	if v, ok := os.LookupEnv("FILEX_PLUGINS_DISABLED"); ok && v != "" {
		pluginsSpoken = true
		c.PluginsDisabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_PLUGIN_CONFORMANCE"); v != "" {
		c.PluginConformance = strings.ToLower(strings.TrimSpace(v))
	}
	if v := os.Getenv("FILEX_PLUGIN_TRUSTED_KEYS"); v != "" {
		c.PluginTrustedKeys = nil
		for _, k := range strings.Split(v, ",") {
			if k = strings.TrimSpace(k); k != "" {
				c.PluginTrustedKeys = append(c.PluginTrustedKeys, k)
			}
		}
	}
	if v := os.Getenv("FILEX_PLUGIN_MAX_INFLIGHT"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			c.PluginMaxInFlight = n
		}
	}
	if v := os.Getenv("FILEX_COOKIE_DOMAIN"); v != "" {
		c.CookieDomain = v
	}
	if v := os.Getenv("FILEX_UPLOAD_STAGING_DIR"); v != "" {
		c.Upload.StagingDir = v
	}
	if v := os.Getenv("FILEX_UPLOAD_CHUNK_SIZE"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			c.Upload.ChunkSize = n
		}
	}
	if v := os.Getenv("FILEX_UPLOAD_STAGING_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.Upload.StagingTTL = d
		}
	}
	if v := os.Getenv("FILEX_CACHE"); v != "" {
		c.Cache.Disabled = v == "0" || strings.EqualFold(v, "false")
	}
	if v := os.Getenv("FILEX_CACHE_DIR"); v != "" {
		c.Cache.Dir = v
	}
	if v := os.Getenv("FILEX_CACHE_MIN_SIZE"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			c.Cache.MinSize = n
		}
	}
	if v := os.Getenv("FILEX_CACHE_MAX_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			c.Cache.MaxBytes = n
		}
	}
	if v := os.Getenv("FILEX_CACHE_SLOW_BPS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			c.Cache.SlowBytesPerSec = n
		}
	}
	if v := os.Getenv("FILEX_SHAREZIP_WARM_MAX_BYTES"); v != "" {
		// 0 is a legal value here (no ceiling), unlike the sizes above.
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n >= 0 {
			c.Cache.ShareZipWarmMaxBytes = n
		}
	}
	if v := os.Getenv("FILEX_SHAREZIP_MAX_AGE"); v != "" {
		if d, err := ParseDurationDays(v); err == nil && d >= 0 {
			c.Cache.ShareZipMaxAge = d
		}
	}
	if v := os.Getenv("FILEX_SHARE_MAX_TTL"); v != "" {
		c.Seed.ShareMaxTTLDays = v
	}
	/* kimlik:e3 cloud */
	if v := os.Getenv("FILEX_CLOUD"); v != "" {
		c.Cloud.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_CLOUD_PLANS"); v != "" {
		c.Cloud.PlansJSON = v
	}
	if v := getenvFirst("STRIPE_SECRET", "FILEX_STRIPE_SECRET"); v != "" {
		c.Cloud.StripeSecret = v
	}
	if v := os.Getenv("FILEX_CLOUD_BASE_HOST"); v != "" {
		c.Cloud.BaseHost = v
	}
	// Both directions, like every other boolean here: an operator who set
	// `versions_on_overwrite: false` in config.yaml must be able to turn it
	// back on from the environment, not only off. docs/CONFIGURATION.md states
	// that as the rule for the whole file.
	if v := os.Getenv("FILEX_VERSIONS_ON_OVERWRITE"); v != "" {
		c.VersionsOnOverwrite = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_VERSIONS_FAIL_OPEN"); v != "" {
		c.VersionsFailOpen = v == "1" || strings.EqualFold(v, "true")
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
	// used in deploy/demo-fm.example.com.compose.yml + plan files.
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
		} else {
			// Silently keeping the default is how a typo becomes "the setting
			// does not work". Say so once, at boot.
			slog.Warn("config: FILEX_SYNC_INTERVAL is not a Go duration; keeping default",
				slog.String("value", v), slog.String("default", c.Sync.DefaultInterval.String()))
		}
	}
	// FILEX_SYNC_WORKERS is intentionally NOT parsed — see SyncConfig.
	if v := os.Getenv("FILEX_THUMBS_SWEEP_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			c.Thumbs.SweepInterval = d
		} else {
			slog.Warn("config: FILEX_THUMBS_SWEEP_INTERVAL is not a Go duration; keeping default",
				slog.String("value", v))
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
	if v := os.Getenv("FILEX_SEARCH_AUTO_REBUILD"); v != "" {
		c.Search.AutoRebuild = v == "1" || strings.EqualFold(v, "true")
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
	if v := os.Getenv("FILEX_SECRET_KEY"); v != "" {
		c.SecretKey = v
	}
	if v := os.Getenv("FILEX_S3"); v != "" {
		c.S3.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_S3_DOMAIN"); v != "" {
		c.S3.Domain = strings.ToLower(strings.TrimSpace(v))
	}
	if v := os.Getenv("FILEX_SFTP"); v != "" {
		c.SFTP.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_SFTP_ADDR"); v != "" {
		c.SFTP.Addr = strings.TrimSpace(v)
	}
	if v := os.Getenv("FILEX_SFTP_HOST_KEY_DIR"); v != "" {
		c.SFTP.HostKeyDir = strings.TrimSpace(v)
	}
	if v := os.Getenv("FILEX_SFTP_BANNER"); v != "" {
		c.SFTP.Banner = v
	}
	if v := os.Getenv("FILEX_FTPS"); v != "" {
		c.FTPS.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_FTPS_ADDR"); v != "" {
		c.FTPS.Addr = strings.TrimSpace(v)
	}
	if v := os.Getenv("FILEX_FTPS_PUBLIC_HOST"); v != "" {
		c.FTPS.PublicHost = strings.TrimSpace(v)
	}
	if v := os.Getenv("FILEX_FTPS_PASV_MIN"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			c.FTPS.PassivePortMin = n
		}
	}
	if v := os.Getenv("FILEX_FTPS_PASV_MAX"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			c.FTPS.PassivePortMax = n
		}
	}
	if v := os.Getenv("FILEX_FTPS_CERT"); v != "" {
		c.FTPS.CertFile = strings.TrimSpace(v)
	}
	if v := os.Getenv("FILEX_FTPS_KEY"); v != "" {
		c.FTPS.KeyFile = strings.TrimSpace(v)
	}
	if v := os.Getenv("FILEX_FTPS_BANNER"); v != "" {
		c.FTPS.Banner = v
	}
	if v := os.Getenv("FILEX_NFS"); v != "" {
		c.NFS.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_NFS_ADDR"); v != "" {
		c.NFS.Addr = strings.TrimSpace(v)
	}
	// ── updates ──
	// AUTO_UPGRADE is the friendly shorthand people reach for; it selects the
	// patch policy (z moves by itself, y and x are announced). An explicit
	// FILEX_UPDATE_POLICY set afterwards wins, so the precise knob always
	// overrides the shorthand.
	if v := os.Getenv("AUTO_UPGRADE"); v != "" {
		if v == "1" || strings.EqualFold(v, "true") {
			c.Update.Policy = "patch"
		} else {
			c.Update.Policy = "manual"
		}
	}
	if v := os.Getenv("FILEX_UPDATE_CHECK"); v != "" {
		c.Update.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("FILEX_UPDATE_POLICY"); v != "" {
		c.Update.Policy = v
	}
	if v := os.Getenv("FILEX_UPDATE_CHANNEL"); v != "" {
		c.Update.Channel = v
	}
	if v := os.Getenv("FILEX_UPDATE_MANIFEST_URL"); v != "" {
		c.Update.ManifestURL = v
	}
	if v := os.Getenv("FILEX_UPDATE_WINDOW"); v != "" {
		c.Update.Window = v
	}
	if v := os.Getenv("FILEX_UPDATE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Update.Interval = d
		}
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
	// ⚠⚠ A demo instance hands an ADMIN account to strangers — that is what it
	// is for. Installing a storage plugin makes filex run an uploaded program
	// on the host, and the plugin API is admin-only, which on a demo means
	// "anybody". So demo mode turns the subsystem OFF unless the operator says
	// otherwise in so many words.
	//
	// Measured on the public demo 2026-08-19: the published credentials logged
	// in as role=admin and GET /api/admin/plugins answered 200. Nothing was
	// installed, but nothing was stopping it either. A default that depends on
	// the operator noticing this is not a default.
	if c.Demo.Mode && !pluginsSpoken {
		c.PluginsDisabled = true
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
	if v := os.Getenv("FILEX_LDAP_CA_FILE"); v != "" {
		c.Auth.LDAP.CAFile = v
	}
	if v := os.Getenv("FILEX_LDAP_PROTOCOL_LOGIN"); v != "" {
		c.Auth.LDAP.ProtocolLogin = v == "1" || strings.EqualFold(v, "true")
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
