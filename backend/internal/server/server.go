package server

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/antivirus"
	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	authapitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	authldap "github.com/brf-tech/filex/backend/internal/auth/drivers/ldap"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/multioidc"
	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	authproxyheader "github.com/brf-tech/filex/backend/internal/auth/drivers/proxyheader"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/filecache"
	"github.com/brf-tech/filex/backend/internal/ftpsrv"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/metrics"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/nfssrv"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/replica"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/sftpsrv"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/staging"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/tenantstore"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/update"
	"github.com/brf-tech/filex/backend/internal/version"
	"github.com/brf-tech/filex/backend/internal/versioning"

	// register storage and DB drivers via their init() blocks
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/mysql"
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/postgres"
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"
	_ "github.com/brf-tech/filex/backend/internal/queue/drivers/postgres"
	_ "github.com/brf-tech/filex/backend/internal/queue/drivers/redis"
	_ "github.com/brf-tech/filex/backend/internal/queue/drivers/sqlite"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/ftp"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/s3"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/sftp"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/smb"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/webdav"
)

// Server is the high-level wrapper around HTTP + workers.
type Server struct {
	cfg             config.Config
	store           db.Store
	sqlDB           *sql.DB
	worker          *syncpkg.Worker
	ops             *ops.Service
	queue           queue.Driver
	qpool           *queue.Pool
	notify          notify.Service
	replicaSvc      *replica.Service
	replicaCron     *replica.CronScheduler
	replicaReloader *replica.RulesReloader
	trash           *trash.Service
	versions        *versioning.Service
	quota           *quota.Service
	srv             *http.Server
	idx             *search.Index
	pipeline        *thumb.Pipeline
	resolver        func(int64) (storage.Driver, error)
	mailer          *mailer.Service
	zipWarmer       *sharezip.Warmer
	// sftp is the SFTP endpoint, nil when FILEX_SFTP is off. It owns a TCP
	// listener of its own rather than a route on the HTTP server.
	sftp *sftpsrv.Server
	// ftps is the FTPS endpoint, nil when FILEX_FTPS is off. Like the SFTP
	// one it owns its own listener rather than a route.
	ftps *ftpsrv.Server
	// nfs is the NFSv3 endpoint, nil when FILEX_NFS is off.
	nfs *nfssrv.Server
	// plugins supervises out-of-process storage drivers, nil when
	// FILEX_PLUGINS_DISABLED. Held here so shutdown stops the processes it
	// started — an orphaned plugin would keep a socket and the storage
	// credentials it was handed.
	plugins *plugin.Manager
	// protocolAuth is the shared credential resolver every non-HTTP protocol
	// authenticates through. Held here for the revalidation sweep, which is what
	// makes revoking a credential reach a session that is already open.
	protocolAuth *protocolauth.Resolver
	// stagedUploads owns the staging sweeper. Filled by api.BuildRouter, which
	// constructs the handler the routes use — running the sweeper against a
	// second instance would sweep a different view of the same directory.
	stagedUploads *handlers.StagedUpload

	mu       sync.RWMutex
	storages map[int64]storage.Driver
}

// New constructs and wires a Server but does not Start it.
func New(ctx context.Context, cfg config.Config, embedFS embed.FS) (*Server, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("server: mkdir datadir: %w", err)
	}

	dbDrv, err := db.Get(cfg.DB.Driver)
	if err != nil {
		return nil, err
	}
	sqlDB, err := dbDrv.Open(ctx, cfg.DB.DSN)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, dbDrv, sqlDB); err != nil {
		return nil, fmt.Errorf("server: migrate: %w", err)
	}
	// Every consumer below — handlers, background workers, the trash purge,
	// the CLI — takes the QUOTA-ACCOUNTING store, not the raw one. That
	// wrapper is where users.usage_bytes is kept true: it owns node creation,
	// overwrite and hard delete, so a write surface added later is counted
	// without a line of its own. See internal/quotastore for the rules (and
	// for why the post-write hooks could not host this).
	//
	// ⚠ Wrap HERE, before anything captures `store`. A consumer handed the raw
	// store writes bytes nobody counts — which is precisely the bug this
	// fixes: quota.AddUsage and SetNodeOwner had no callers in the tree at
	// all, so usage_bytes was never incremented, GetNodeOwner always returned
	// nil, and the SubUsage at trash-purge could never fire either.
	accounting := quotastore.New(dbDrv.NewStore(sqlDB))
	accounting.AttachMetrics(quotaMetrics{})
	// The identity wrapper goes OUTSIDE the accounting one so it sees every
	// CreateUser, including those made by the JIT provisioning in the OIDC,
	// LDAP and proxy-header drivers. It names accounts; see identitystore for
	// why that cannot live in the eight call sites (migration 00025).
	var store db.Store = identitystore.New(accounting)

	// Name the accounts that predate migration 00025. Idempotent and cheap
	// (one query plus one UPDATE per unnamed account), so it runs on every
	// start rather than as a one-shot: an account restored from an older dump,
	// or one whose naming lost a race at creation time, is repaired here
	// instead of quietly lacking an SFTP/FTPS login forever.
	if named, err := identitystore.Backfill(ctx, store); err != nil {
		slog.Warn("identity: username backfill failed; accounts without a username can still sign in by e-mail", slog.Any("err", err))
	} else if named > 0 {
		slog.Info("identity: named existing accounts", slog.Int("count", named))
	}

	// Auth drivers — local always present.
	var localDrv auth.LoginDriver
	var oidcDrv auth.OIDCDriver
	enabled := []auth.Driver{}

	for _, name := range cfg.Auth.Drivers {
		switch strings.ToLower(name) {
		case "local":
			d := authlocal.New(store)
			if err := d.Init(ctx, nil); err != nil {
				return nil, fmt.Errorf("auth init local: %w", err)
			}
			enabled = append(enabled, d)
			localDrv = d
		case "oidc":
			d := authoidc.New(store)
			oidcCfg := map[string]any{
				"issuer":        cfg.Auth.OIDC.Issuer,
				"client_id":     cfg.Auth.OIDC.ClientID,
				"client_secret": cfg.Auth.OIDC.ClientSecret,
				"redirect_url":  cfg.Auth.OIDC.RedirectURL,
				"role_claim":    cfg.Auth.OIDC.RoleClaim,
				"admin_group":   cfg.Auth.OIDC.AdminGroup,
			}
			// OIDC discovery often fails transiently when filex and the IdP
			// boot together (compose restart, host reboot). One 502 used to
			// leave SSO offline until a manual `docker restart` — this loop
			// gives the IdP ~60s to come up before we give up.
			oidcErr := initWithBackoff(ctx, "oidc", func(c context.Context) error {
				return d.Init(c, oidcCfg)
			}, []time.Duration{0, 2 * time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second, 30 * time.Second})
			if oidcErr != nil {
				slog.Warn("oidc driver init failed after retries; SSO disabled until restart",
					slog.String("err", oidcErr.Error()))
				continue
			}
			enabled = append(enabled, d)
			oidcDrv = d
		case "ldap":
			d := authldap.New(store)
			if err := d.Init(ctx, map[string]any{
				"url":           cfg.Auth.LDAP.URL,
				"bind_dn":       cfg.Auth.LDAP.BindDN,
				"bind_password": cfg.Auth.LDAP.BindPassword,
				"base_dn":       cfg.Auth.LDAP.BaseDN,
				"user_filter":   cfg.Auth.LDAP.UserFilter,
				"email_attr":    cfg.Auth.LDAP.EmailAttr,
				"start_tls":     cfg.Auth.LDAP.StartTLS,
			}); err != nil {
				slog.Warn("ldap driver init failed", slog.String("err", err.Error()))
				continue
			}
			enabled = append(enabled, d)
		case "proxy-header", "proxyheader", "header_proxy":
			d := authproxyheader.New(store)
			if err := d.Init(ctx, map[string]any{
				"header_user":     "X-Auth-User",
				"header_email":    cfg.Auth.Header.EmailHeader,
				"header_roles":    cfg.Auth.Header.GroupHeader,
				"trusted_proxies": cfg.Auth.Header.TrustedIPs,
				"admin_role":      cfg.Auth.Header.AdminGroup,
			}); err != nil {
				slog.Warn("proxy-header driver init failed", slog.String("err", err.Error()))
				continue
			}
			enabled = append(enabled, d)
		default:
			slog.Warn("unknown auth driver", slog.String("name", name))
		}
	}

	// API-token driver is always enabled (independent of cfg.Auth.Drivers)
	// so AI agents / the work.example.com FilexClient / MCP clients can
	// authenticate against /api/files and /api/ai with X-Filex-Token or a
	// Bearer token. Tokens are minted from /api/admin/ai-tokens.
	{
		atDrv := authapitoken.New(store)
		if err := atDrv.Init(ctx, nil); err != nil {
			return nil, fmt.Errorf("auth init api-token: %w", err)
		}
		enabled = append(enabled, atDrv)
	}
	auth.SetEnabled(enabled)

	// Multi-tenant mode: dispatch OIDC per tenant realm — request host →
	// provider row → that realm's driver (JIT stamps the tenant). Hosts with no
	// tenant OIDC config fall back to the config-file driver above, so the
	// operator's own login keeps working. See docs/MULTI-TENANCY.md.
	if cfg.MultiTenant {
		oidcDrv = multioidc.New(store, oidcDrv)
	}

	// Search index.
	var idx *search.Index
	if cfg.Search.Enabled {
		idx, err = search.Open(cfg.Search.IndexPath)
		if err != nil {
			slog.Warn("search index open failed; falling back to SQL LIKE", slog.String("err", err.Error()))
			idx = nil
		}
	}

	// Capability service.
	caps := capability.New(store)
	caps.SetStaticInventory(
		cfg.Auth.Drivers,
		storage.Names(),
		cfg.DB.Driver,
		cfg.Search.Enabled,
		version.String(),
		"",
		cfg.Demo.Mode,
		cfg.Demo.User,
		cfg.DefaultLocale,
		cfg.Auth.OIDC.AutoRedirect,
	)

	// Sync worker. Bind the search index so every create/update/delete
	// during a sync run also updates Bleve — without this, the in-toolbar
	// search box only sees rows the admin's "Rebuild" button has touched.
	// FILEX_SYNC_INTERVAL reaches the sync worker HERE. Until now it was
	// parsed into config and read by nobody, while loopPoll used a hardcoded
	// 15m that happened to match the default.
	worker := syncpkg.NewWithInterval(store, cfg.Sync.DefaultInterval)
	if idx != nil {
		worker.AttachIndex(idx)
	}

	// Staging area for resumable uploads, plus the byte-source resolver every
	// read surface consults. Both are constructed HERE, before the services
	// that need them, because a staged upload publishes its node the moment the
	// client commits and transfers the bytes afterwards: during that window the
	// driver does not have the object, and a service that asks it directly
	// fails or reads the version being replaced.
	//
	// This one uses the raw (unscoped) store, like every other background
	// service in this function. The router builds its own over the tenant-
	// scoped store — same helper, each bound to the store its layer already
	// uses, so no tenant boundary moves.
	stagingArea := staging.New(cfg.Upload.StagingDir)

	// The local copy filex prepares for a big file on a slow backend. ONE
	// cache for the whole instance, shared by this background resolver and the
	// router's: two caches over the same directory would each enforce the
	// global cap against half the truth, and the cap is the part that must not
	// be wrong.
	fileCacheCfg := filecache.Config{
		Dir:             cfg.Cache.Dir,
		MinSize:         cfg.Cache.MinSize,
		MaxBytes:        cfg.Cache.MaxBytes,
		SlowBytesPerSec: cfg.Cache.SlowBytesPerSec,
	}
	if cfg.Cache.Disabled {
		fileCacheCfg.Dir = ""
	}
	fileCache := filecache.New(fileCacheCfg)
	if fileCache.Enabled() {
		slog.Info("filecache: enabled",
			slog.String("dir", cfg.Cache.Dir),
			slog.Int64("min_size", fileCache.MinSize()),
			slog.Int64("max_bytes", fileCache.MaxBytes()),
			slog.Int64("bytes", fileCache.Bytes()),
			slog.Int("entries", fileCache.Len()))
	} else {
		slog.Info("filecache: disabled (FILEX_CACHE=0)")
	}

	bgBody := filebody.New(store, stagingArea).WithCache(fileCache)

	// Thumbnail pipeline.
	pipelineCaps := thumb.Capabilities{Image: true}
	cap, _ := caps.Get(ctx)
	if cap != nil {
		pipelineCaps.Video = cap.Thumbs.Video
		pipelineCaps.Audio = cap.Thumbs.Audio
		pipelineCaps.PDF = cap.Thumbs.PDF
		pipelineCaps.Office = cap.Thumbs.Office
		pipelineCaps.SVG = cap.Thumbs.SVG
	}
	pipeline := thumb.New(store, cfg.Thumbs.CacheDir, pipelineCaps)
	pipeline.AttachBody(bgBody)

	// Share service.
	shareSvc := share.NewService(store)

	// Storage plugins — drivers that live outside this binary. Started BEFORE
	// the resolver pre-warms storages below: a storage on `plugin:foo` can
	// only open once that plugin has described itself and registered, and a
	// pre-warm that ran first would log "unknown driver" for every one of
	// them. WaitReady bounds that wait; a plugin that is slower than it still
	// registers, and its storages open on first use.
	var pluginMgr *plugin.Manager
	if !cfg.PluginsDisabled {
		pluginMgr, err = plugin.New(plugin.Options{
			Store:       store,
			Dir:         filepath.Join(cfg.DataDir, "plugins"),
			SecretKey:   cfg.SecretKey,
			Log:         slog.Default(),
			Conformance: cfg.PluginConformance,
			TrustedKeys: cfg.PluginTrustedKeys,
			MaxInFlight: cfg.PluginMaxInFlight,
		})
		if err != nil {
			return nil, fmt.Errorf("server: plugins: %w", err)
		}
		if err := pluginMgr.Load(ctx); err != nil {
			// A broken plugin must not stop the server: the whole point of
			// running them out of process is that they cannot take filex
			// down. The failure is logged and visible in the admin list.
			slog.Warn("plugins: load failed; continuing without them", slog.Any("err", err))
		} else {
			pluginMgr.WaitReady(10 * time.Second)
		}
	}

	srvObj := &Server{
		cfg:      cfg,
		plugins:  pluginMgr,
		store:    store,
		sqlDB:    sqlDB,
		worker:   worker,
		idx:      idx,
		pipeline: pipeline,
		storages: map[int64]storage.Driver{},
	}

	// OnlyOffice integration — disabled if no document server URL/secret
	// is configured (the handlers return 503 in that case).
	var ooSvc *onlyoffice.Service
	if cfg.ExternalServices.OnlyOffice.URL != "" && cfg.ExternalServices.OnlyOffice.JWTSecret != "" {
		ooSvc = onlyoffice.New(
			store,
			nil, // resolver wired below once it exists
			cfg.ExternalServices.OnlyOffice.URL,
			cfg.ExternalServices.OnlyOffice.JWTSecret,
			cfg.PublicURL,
			0,
		)
	}

	// Storage resolver — connects API handlers and pipeline to live drivers.
	resolver := func(id int64) (storage.Driver, error) {
		srvObj.mu.RLock()
		drv, ok := srvObj.storages[id]
		srvObj.mu.RUnlock()
		if ok {
			return drv, nil
		}
		st, err := store.GetStorage(ctx, id)
		if err != nil {
			return nil, err
		}
		drv, err = storage.Get(st.Driver)
		if err != nil {
			return nil, err
		}
		cfg := map[string]any{}
		if len(st.ConfigJSON) > 0 {
			_ = jsonDecode(st.ConfigJSON, &cfg)
		}
		if err := drv.Init(ctx, cfg); err != nil {
			return nil, err
		}
		srvObj.mu.Lock()
		srvObj.storages[id] = drv
		srvObj.mu.Unlock()
		pipeline.AttachStorage(id, drv)
		return drv, nil
	}

	// Pre-warm storages so the pipeline knows about them on first access.
	if storages, err := store.ListEnabledStorages(ctx); err == nil {
		for _, st := range storages {
			_, _ = resolver(st.ID)
		}
	}
	srvObj.resolver = resolver

	// Now that resolver exists, fill in dependents that need it.
	caps.AttachStorageResolver(resolver)
	if ooSvc != nil {
		ooSvc.StorageResolver = resolver
	}

	// Async ops queue — DB-backed, restart-safe.
	opsSvc := ops.New(sqlDB, resolver)
	if err := opsSvc.Migrate(ctx); err != nil {
		slog.Warn("ops: migrate", slog.String("err", err.Error()))
	}
	srvObj.ops = opsSvc

	// Driver-based persistent queue. Bound to the same *sql.DB for the
	// sqlite default; postgres/redis open their own connection from
	// cfg.Queue.DSN. The Pool itself starts in Start().
	if cfg.Queue.Enabled {
		qDriverName := cfg.Queue.Driver
		if qDriverName == "" {
			qDriverName = "sqlite"
		}
		qd, err := queue.Get(qDriverName)
		if err != nil {
			slog.Warn("queue: unknown driver, falling back to sqlite",
				slog.String("requested", qDriverName), slog.String("err", err.Error()))
			qd, _ = queue.Get("sqlite")
			qDriverName = "sqlite"
		}
		qcfg := map[string]any{}
		switch qDriverName {
		case "sqlite":
			// Re-use the application *sql.DB so the queue lives in the
			// same file as the metadata store. Also avoids a second
			// migration pipeline — db.Migrate already created ops_queue
			// via 00006_queue.sql.
			qcfg["db"] = sqlDB
		case "postgres", "redis":
			if cfg.Queue.DSN != "" {
				if qDriverName == "redis" {
					qcfg["url"] = cfg.Queue.DSN
				} else {
					qcfg["dsn"] = cfg.Queue.DSN
				}
			}
		}
		if err := qd.Init(ctx, qcfg); err != nil {
			slog.Warn("queue: init failed; persistent queue disabled",
				slog.String("driver", qDriverName), slog.String("err", err.Error()))
		} else {
			// On boot, re-queue any rows left in `running` from a crash.
			if n, err := qd.RecoverOrphans(ctx, 5*time.Minute); err != nil {
				slog.Warn("queue: recover orphans", slog.String("err", err.Error()))
			} else if n > 0 {
				slog.Info("queue: recovered orphan running ops",
					slog.Int64("count", n), slog.String("driver", qDriverName))
			}
			workers := cfg.Queue.Workers
			if workers <= 0 {
				workers = 4
			}
			srvObj.queue = qd
			srvObj.qpool = queue.NewPool(qd, workers)
		}
	}

	// Content extraction ("Bul"): register the async content_index job and
	// hook the search index so every metadata (re)index of an eligible file
	// enqueues extraction — this covers the upload/write handlers AND the
	// sync worker upserts, since both feed Index.IndexNode. Kill-switch:
	// FILEX_SEARCH_CONTENT=0. Best-effort by design — the write path never
	// blocks on (or fails because of) extraction.
	if idx != nil && srvObj.qpool != nil && cfg.Search.Content {
		contentIdx := queue.NewContentIndexer(store, resolver, idx, cfg.Search.ContentMaxBytes)
		contentIdx.AttachBody(bgBody)
		srvObj.qpool.Register(queue.TypeContentIndex, contentIdx.Handle)
		qd := srvObj.queue
		idx.SetContentHook(func(ctx context.Context, n *model.Node) {
			// WithoutCancel: a client disconnect right after a write must
			// not drop the enqueue mid-flight.
			contentIdx.Enqueue(context.WithoutCancel(ctx), qd, n)
		})
	}

	// Notifications subsystem.
	if cfg.Notify.Enabled {
		srvObj.notify = notify.New(store, notify.Config{
			WebhookURL:   cfg.Notify.WebhookURL,
			WebhookToken: cfg.Notify.WebhookToken,
		})
	}

	/* koru:k2 av */
	// Optional ClamAV upload scanning ("Koru" v0.4). Only wired when a
	// binary resolved (FILEX_CLAMAV_BIN > $PATH clamdscan/clamscan,
	// FILEX_CLAMAV=0 kill-switch) AND the persistent queue is up — the
	// scan is an async job (content_index pattern) and must never sit on
	// the upload path. avEnqueue stays nil otherwise, which disables the
	// handlers' emission sink entirely.
	var avEnqueue func(ctx context.Context, n *model.Node)
	if srvObj.qpool != nil {
		if avScanner := antivirus.New(); avScanner.Supports() {
			avJob := queue.NewAntivirusScanner(store, resolver, avScanner, srvObj.notify, idx, antivirus.MaxScanBytes())
			avJob.AttachBody(bgBody)
			srvObj.qpool.Register(queue.TypeAntivirusScan, avJob.Handle)
			qd := srvObj.queue
			avEnqueue = func(ctx context.Context, n *model.Node) {
				avJob.Enqueue(ctx, qd, n)
			}
			slog.Info("antivirus: enabled", slog.String("bin", avScanner.Bin()))
		}
	}

	// Replica orchestration. The wrapper Driver itself is created
	// lazily by the resolver — when a primary storage with a
	// matching replica row exists. v0.1 does not auto-discover the
	// replica pairing; admins set storages.role + replica_of_id via
	// SQL or the (forthcoming) admin UI. This block wires the
	// reconcile + report Service so the queue handler is registered
	// and the cron scheduler comes online; the rules engine runs
	// regardless because it's also consulted by the admin handler
	// (preview rules before saving them).
	{
		_, reloader := replica.NewRulesEngine(store)
		srvObj.replicaReloader = reloader
		// Service is wired with a nil ReplicatedDriver until the
		// admin pairs primary+replica; the queue handler returns a
		// "no replica configured" error in that case. We could lazily
		// look up the wrapper from the resolver but v0.1 skips that
		// and surfaces the missing pair via 503 in the admin UI.
		srvObj.replicaSvc = replica.New(store, nil, srvObj.queue, srvObj.notify)
		srvObj.replicaCron = replica.NewCronScheduler(srvObj.replicaSvc)

		if srvObj.qpool != nil {
			srvObj.qpool.Register(queue.TypeReplicaRetry, srvObj.replicaSvc.HandleRetry)
			srvObj.qpool.Register(queue.TypeReplicaReport, func(ctx context.Context, _ queue.Op) error {
				return srvObj.replicaSvc.GenerateReport(ctx)
			})
			srvObj.qpool.Register(queue.TypeReconcile, func(ctx context.Context, _ queue.Op) error {
				_, err := srvObj.replicaSvc.ReconcileAll(ctx)
				return err
			})
		}
	}

	// Quota service — per-user usage accounting + admin set. Taken FROM the
	// accounting wrapper rather than built again, so the ceiling that is
	// enforced and the counter that is moved are the same object over the
	// same tables.
	quotaSvc := accounting.Quota()
	srvObj.quota = quotaSvc
	// Seed the process gauge from the DB so a restart does not report zero
	// usage until the next upload. One pass over the users table at boot;
	// after this the accounting wrapper moves the gauge by the same deltas it
	// writes, so the two never diverge without the DB also being wrong.
	if users, err := store.ListUsers(ctx); err == nil {
		var total int64
		for _, u := range users {
			total += u.UsageBytes
		}
		metrics.QuotaUsageBytes.Set(float64(total))
	}

	// Trash retention service — handles soft-delete restore + scheduled
	// purge of expired tombstones.
	trashSvc := trash.New(store, resolver, quotaSvc)
	srvObj.trash = trashSvc

	// Versioning service — snapshots before destructive writes; the API
	// layer exposes list/restore/hard-delete via /api/files/versions and
	// /api/admin/versions. Its daily retention loop (versions.keep_n)
	// starts in Start().
	versionsSvc := versioning.New(store, versioning.StorageResolver(resolver))
	versionsSvc.AttachBody(bgBody)
	srvObj.versions = versionsSvc

	// Mailer for invite/share notices — verified periodically in Start().
	srvObj.mailer = mailer.New(store)

	// Handlers see the tenant-scoped store: storage listings are confined to the
	// request's tenant (no-op unless multi-tenant mode is on — the wrapper only
	// diverges when the context carries a tenant scope). Background services above
	// keep the raw `store`; their contexts carry no scope, so they are unaffected.
	scopedStore := tenantstore.New(store)

	// Folder-share ZIP cache (shared by the download handler and the background
	// warmer) + the warmer itself, which every DefaultWarmInterval re-checks
	// each active folder share and pre-builds its zip if the folder changed, so
	// downloads almost always hit a warm cache. Uses the raw (unscoped) store
	// like the other background services.
	//
	// ⚠ It lives under the CACHE directory, not beside the data: every byte in
	// here is regenerable from the folder it archives, and one 15 GB archive of
	// an eleven-minute share once rode into the backups, the off-site restore
	// and the DR mirror three times over before anyone noticed. Backups should
	// exclude <data>/cache entirely; see docs/SHARING.md.
	zipDir := filepath.Join(cfg.Cache.Dir, "sharezips")
	migrateShareZipDir(filepath.Join(cfg.DataDir, "sharezips"), zipDir)
	zipCache := sharezip.New(zipDir)
	zipCache.WarmMaxBytes = cfg.Cache.ShareZipWarmMaxBytes
	zipCache.MaxAge = cfg.Cache.ShareZipMaxAge
	srvObj.zipWarmer = sharezip.NewWarmer(
		zipCache,
		func(ctx context.Context) ([]sharezip.DirShare, error) {
			// ⚠ Paged to completion on purpose. This list is not only what
			// gets pre-built, it is also what the sweeper keeps and what a
			// running build checks itself against — a share truncated off
			// the end of page one would have its archive deleted and its
			// build abandoned while the link still worked.
			const page, maxShares = 500, 100000
			var out []sharezip.DirShare
			complete := false
			for offset := 0; offset < maxShares; offset += page {
				rows, total, err := store.ListAllShares(ctx, nil, true, page, offset)
				if err != nil {
					return nil, err
				}
				for _, sm := range rows {
					if sm.Share == nil || sm.Share.Kind == model.ShareKindDrop {
						continue
					}
					node, nerr := store.GetNode(ctx, sm.Share.NodeID)
					if nerr != nil && !errors.Is(nerr, sql.ErrNoRows) {
						// A database hiccup must not read as "that share
						// is gone": the sweeper would delete a live
						// archive and a running build would abandon
						// itself. Fail the pass; the previous view stands.
						return nil, nerr
					}
					if node == nil || node.Type != model.NodeTypeDirectory {
						continue
					}
					out = append(out, sharezip.DirShare{StorageID: node.StorageID, Path: node.Path, NodeID: node.ID})
				}
				if len(rows) < page || int64(offset+len(rows)) >= total {
					complete = true
					break
				}
			}
			if !complete {
				// Better no answer than half an answer, for the same
				// reason: half an answer deletes live archives.
				return nil, fmt.Errorf("sharezip: more than %d active shares; refusing a partial list", maxShares)
			}
			return out, nil
		},
		resolver,
		sharezip.DefaultWarmInterval,
		func(f string, a ...any) { slog.Info(fmt.Sprintf(f, a...)) },
	)

	// Release awareness. Constructed even when checking is disabled so the
	// admin endpoints can report WHY there is no information, instead of the
	// page looking broken. Nothing is applied here — Run() only learns, and
	// only a policy that allows it (plus a binary install) ever calls Apply.
	updater := update.New(update.Config{
		Enabled:        cfg.Update.Enabled,
		Policy:         update.ParsePolicy(cfg.Update.Policy),
		Channel:        cfg.Update.Channel,
		ManifestURL:    cfg.Update.ManifestURL,
		Window:         update.ParseWindow(cfg.Update.Window),
		Interval:       cfg.Update.Interval,
		StateDir:       cfg.DataDir,
		CurrentVersion: version.Version,
	})
	// Announce a newly published release once, through the same notification
	// pipeline as everything else operational.
	if srvObj.notify != nil {
		updater.OnNewRelease = func(d update.Decision) {
			sev := notify.SeverityInfo
			if d.Target.IsSecurity() {
				sev = notify.SeverityWarning
			}
			_, _ = srvObj.notify.Send(context.Background(), notify.Event{
				Event:    notify.EventUpdateAvailable,
				Severity: sev,
				Title:    "filex " + d.Target.Version + " available",
				Body:     d.Reason,
				Meta: map[string]any{
					"version": d.Target.Version,
					"current": version.Version,
					"step":    string(d.Step),
					"action":  string(d.Action),
					"notes":   d.Target.NotesURL,
				},
			})
		}
	}
	// A self-upgrade must never be the reason a database becomes unrecoverable:
	// snapshot before the binary is swapped, and abort the upgrade if that
	// fails. Down migrations do not exist, so the backup IS the rollback.
	updater.SetPreApply(func(ctx context.Context, target update.Release) error {
		return snapshotBeforeUpgrade(ctx, cfg, target.Version)
	})
	if updater.Enabled() {
		go updater.Run(ctx)
		slog.Info("updates: checking enabled",
			slog.String("policy", cfg.Update.Policy),
			slog.String("mode", string(updater.Mode())))
	} else {
		slog.Info("updates: checking disabled")
	}

	deps := &api.Deps{
		Cfg:             cfg,
		Store:           scopedStore,
		Updater:         updater,
		Worker:          worker,
		Index:           idx,
		Caps:            caps,
		Thumbs:          pipeline,
		Share:           shareSvc,
		OnlyOffice:      ooSvc,
		Ops:             opsSvc,
		Trash:           trashSvc,
		Quota:           quotaSvc,
		Versions:        versionsSvc,
		Queue:           srvObj.queue,
		Notify:          srvObj.notify,
		ReplicaService:  srvObj.replicaSvc,
		ReplicaCron:     srvObj.replicaCron,
		ReplicaReloader: srvObj.replicaReloader,
		StorageResolver: resolver,
		Plugins:         pluginMgr,
		Embed:           embedFS,
		LocalAuth:       localDrv,
		OIDCAuth:        oidcDrv,
		Mailer:          srvObj.mailer,
		ZipCache:        zipCache,
		FileCache:       fileCache,
		AVScan:          avEnqueue, /* koru:k2 av */
	}
	// WebDAV server (/dav/<storage>/<path>, HTTP Basic) — the handler itself
	// is composed inside api.BuildRouter (single Mount line, see
	// internal/dav). FILEX_DAV=0 disables the surface; log the state so the
	// kill switch is visible in boot output.
	if cfg.DAV.Enabled {
		slog.Info("webdav: enabled", slog.String("prefix", "/dav"))
	} else {
		slog.Info("webdav: disabled (FILEX_DAV=0)")
	}

	// The staging directory itself is created here (not lazily in the router)
	// so a broken/unwritable staging path is a boot-time complaint rather than
	// a surprise on the first big upload. The Area value was built at the top,
	// because the background services already needed it.
	if err := os.MkdirAll(cfg.Upload.StagingDir, 0o755); err != nil {
		slog.Warn("uploads: staging dir unavailable",
			slog.String("dir", cfg.Upload.StagingDir), slog.String("err", err.Error()))
	} else {
		slog.Info("uploads: staging enabled",
			slog.String("dir", cfg.Upload.StagingDir),
			slog.String("ttl", cfg.Upload.StagingTTL.String()),
			slog.Int64("chunk", cfg.Upload.ChunkSize))
	}
	deps.Staging = stagingArea

	// ⚠ Set BEFORE BuildRouter but READ later: the listener does not exist yet,
	// and with `:0` the configured address names no port at all. The closure
	// resolves at request time, when it does.
	deps.FTPSAddr = func() string {
		if srvObj.ftps == nil {
			return ""
		}
		return srvObj.ftps.Addr()
	}

	router := api.BuildRouter(deps)
	srvObj.stagedUploads = deps.StagedUploads
	// Filled BY BuildRouter when nil — same reason the protocol listeners below
	// are built after it.
	srvObj.protocolAuth = deps.ProtocolAuth

	// SFTP endpoint (its own TCP listener, not a route). OFF unless asked for:
	// a port nobody requested is not something to open for them.
	//
	// ⚠ Built AFTER BuildRouter, and from `deps`: the credential resolver, the
	// ACL resolver and the read door are singletons with their own caches, and
	// a second set would mean "how long does a revoked password keep working"
	// has two different answers depending on which protocol you ask.
	if cfg.SFTP.Enabled {
		hostKeyDir := cfg.SFTP.HostKeyDir
		if hostKeyDir == "" {
			hostKeyDir = filepath.Join(cfg.DataDir, "ssh")
		}
		sftpSrv, err := sftpsrv.New(sftpsrv.Config{
			Enabled:     true,
			Addr:        cfg.SFTP.Addr,
			HostKeyDir:  hostKeyDir,
			Banner:      cfg.SFTP.Banner,
			MaxSpool:    cfg.SFTP.MaxSpool,
			Store:       deps.Store,
			Auth:        deps.ProtocolAuth,
			ACL:         deps.ACL,
			Resolver:    deps.StorageResolver,
			Body:        deps.Body,
			Quota:       deps.Quota,
			Index:       deps.Index,
			Thumbs:      deps.Thumbs,
			SpoolDir:    cfg.Upload.StagingDir,
			MultiTenant: cfg.MultiTenant,
		})
		if err != nil {
			// A misconfigured host key directory must not take the whole
			// service down: the web app is what most people are here for.
			slog.Error("sftp: not started", slog.String("err", err.Error()))
		} else {
			srvObj.sftp = sftpSrv
		}
	} else {
		slog.Info("sftp: disabled (FILEX_SFTP=0)")
	}

	// FTPS endpoint, same shape as SFTP: its own listener, off unless asked
	// for, and built from `deps` so the credential resolver and the read door
	// are the SAME singletons every other protocol uses.
	//
	// ⚠⚠ AFTER BuildRouter, like the SFTP block above. `deps.ProtocolAuth`,
	// `deps.ACL` and `deps.Body` are filled in BY BuildRouter when they are
	// nil, so a listener constructed before it gets nil for all three — which
	// is exactly what happened here: "ftps: not started … Auth … required",
	// logged at boot while the web app came up fine (2026-08-16, found by
	// pointing curl at the port).
	if cfg.FTPS.Enabled {
		ftpsSrv, err := ftpsrv.New(ftpsrv.Config{
			Enabled:        true,
			Addr:           cfg.FTPS.Addr,
			PublicHost:     cfg.FTPS.PublicHost,
			PassivePortMin: cfg.FTPS.PassivePortMin,
			PassivePortMax: cfg.FTPS.PassivePortMax,
			CertFile:       cfg.FTPS.CertFile,
			KeyFile:        cfg.FTPS.KeyFile,
			CertDir:        filepath.Join(cfg.DataDir, "ftps"),
			Banner:         cfg.FTPS.Banner,
			IdleTimeout:    cfg.FTPS.IdleTimeout,
			Store:          deps.Store,
			Auth:           deps.ProtocolAuth,
			ACL:            deps.ACL,
			Resolver:       deps.StorageResolver,
			Body:           deps.Body,
			Quota:          deps.Quota,
			Index:          deps.Index,
			Thumbs:         deps.Thumbs,
			SpoolDir:       cfg.Upload.StagingDir,
			MultiTenant:    cfg.MultiTenant,
		})
		if err != nil {
			slog.Error("ftps: not started", slog.String("err", err.Error()))
		} else {
			srvObj.ftps = ftpsSrv
		}
	} else {
		slog.Info("ftps: disabled (FILEX_FTPS=0)")
	}

	// NFSv3 endpoint. Same placement rule as the two above: after BuildRouter,
	// built from `deps`.
	if cfg.NFS.Enabled {
		nfsSrv, err := nfssrv.New(nfssrv.Config{
			Enabled:     true,
			Addr:        cfg.NFS.Addr,
			Store:       deps.Store,
			Auth:        deps.ProtocolAuth,
			ACL:         deps.ACL,
			Resolver:    deps.StorageResolver,
			Body:        deps.Body,
			Quota:       deps.Quota,
			Index:       deps.Index,
			Thumbs:      deps.Thumbs,
			SpoolDir:    cfg.Upload.StagingDir,
			MultiTenant: cfg.MultiTenant,
		})
		if err != nil {
			slog.Error("nfs: not started", slog.String("err", err.Error()))
		} else {
			srvObj.nfs = nfsSrv
		}
	} else {
		slog.Info("nfs: disabled (FILEX_NFS=0)")
	}

	srvObj.srv = &http.Server{
		Addr:              cfg.Listen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Seed default rows in external_services so the admin UI has editable
	// cards for OnlyOffice/Drawio/Convert even on fresh installs, then seed
	// settings (SMTP/branding/trash) + an initial storage from env. All are
	// only-if-absent, so operator UI edits are never clobbered.
	seedExternalDefaults(ctx, store, cfg)
	seedFromEnv(ctx, store, cfg)

	return srvObj, nil
}

// migrateShareZipDir moves a pre-existing folder-share ZIP cache out of the
// data directory and into the cache directory, once, at boot.
//
// The archives are regenerable, so the fallback for anything that does not
// move cleanly is to delete them rather than to leave a second cache nobody
// sweeps: an orphaned <data>/sharezips would keep the exact bug this change
// exists to end (files that outlive their share, in the backup set).
func migrateShareZipDir(legacy, dst string) {
	if legacy == "" || dst == "" || legacy == dst {
		return
	}
	if fi, err := os.Stat(legacy); err != nil || !fi.IsDir() {
		return
	}
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err == nil {
			if err := os.Rename(legacy, dst); err == nil {
				slog.Info("sharezip: moved cache out of the data directory",
					slog.String("from", legacy), slog.String("to", dst))
				return
			}
		}
	}
	// Destination already there, or the rename failed (a cross-device data
	// dir, say). Drop the old copy; the warmer rebuilds what is still shared.
	if err := os.RemoveAll(legacy); err != nil {
		slog.Warn("sharezip: could not remove the legacy cache directory",
			slog.String("dir", legacy), slog.String("err", err.Error()))
		return
	}
	slog.Info("sharezip: removed the legacy cache directory (archives are regenerable)",
		slog.String("dir", legacy))
}

// seedExternalDefaults inserts placeholder rows for the three known
// external services if they're missing. We mark them disabled when
// no URL is configured so the capability prober reports "disabled"
// instead of "unreachable" on the next refresh.
func seedExternalDefaults(ctx context.Context, store db.Store, cfg config.Config) {
	type defRow struct {
		name   string
		url    string
		secret string
	}
	defaults := []defRow{
		{name: "onlyoffice", url: cfg.ExternalServices.OnlyOffice.URL, secret: cfg.ExternalServices.OnlyOffice.JWTSecret},
		{name: "drawio", url: cfg.ExternalServices.Drawio.URL, secret: ""},
		{name: "convert", url: cfg.ExternalServices.Convert.URL, secret: ""},
	}
	for _, d := range defaults {
		// Only seed when missing; don't clobber operator-edited rows.
		if cur, _ := store.GetExternalService(ctx, d.name); cur != nil {
			continue
		}
		enabled := d.url != ""
		state := "unconfigured"
		if enabled {
			state = "unknown"
		}
		if err := store.UpsertExternalService(ctx, d.name, enabled, d.url, d.secret, "{}", time.Time{}, state); err != nil {
			slog.Warn("seed external_services row", slog.String("name", d.name), slog.String("err", err.Error()))
		}
	}
}

// Start runs first-run, prints the banner, starts the worker, and serves
// HTTP. Blocks until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	fr, err := FirstRun(ctx, s.store, s.cfg.DataDir, s.cfg.Seed.AdminEmail, s.cfg.Seed.AdminPassword)
	if err != nil {
		return fmt.Errorf("server: first run: %w", err)
	}
	caps, _ := capability.New(s.store).Get(ctx)
	storages, _ := s.store.ListStorages(ctx)
	var capExt map[string]model.ExternalServiceState
	if caps != nil {
		capExt = caps.External
	}
	PrintBanner(os.Stdout, s.cfg, fr, capExt, storages)

	if err := s.worker.Start(ctx); err != nil {
		slog.Warn("sync worker failed to start", slog.String("err", err.Error()))
	}
	if s.ops != nil {
		go s.ops.Run(ctx)
	}
	if s.qpool != nil {
		// Replica retry / reconcile / report handlers are registered
		// in New() before this point; the legacy ops.Service still
		// owns copy/move/delete via its own goroutine.
		s.qpool.Start(ctx)
	}
	if s.zipWarmer != nil {
		s.zipWarmer.Start(ctx)
	}
	// Share max-TTL report: existing links are never shortened by the ceiling
	// (a customer's link minted under the old rule keeps working), so the
	// operator is told how many outlive it — here at boot and in
	// GET /api/admin/protection — and decides by hand.
	go func() {
		svc := share.NewService(s.store)
		if n, err := svc.CountOverMaxTTL(ctx, time.Now()); err == nil && n > 0 {
			slog.Info("share: existing links outlive the max-TTL ceiling (left untouched; see /api/admin/protection)",
				slog.Int("links", n), slog.Int("max_ttl_days", svc.MaxTTLDays(ctx)))
		}
	}()
	// Staging sweeper — an upload area with no GC is a disk incident waiting.
	// Sweeps once at boot, then on the configured interval; every removal is
	// logged with its id, path and staged size.
	if s.stagedUploads != nil {
		go s.stagedUploads.RunSweeper(ctx, s.cfg.Upload.SweepInterval)
	}
	if s.replicaCron != nil {
		s.replicaCron.Start()
		_ = s.replicaCron.Reload(ctx)
	}

	// ⚠⚠ Revocation for the protocols that authenticate ONCE and then stay open.
	// SFTP, FTPS and NFS check a credential at login and never again, so
	// deleting a token, disabling an account or suspending a tenant used to do
	// nothing at all to the session that credential had already opened — the
	// operator's action was true for the next login and false for the
	// connection in flight. This sweep re-checks every registered session and
	// cuts the ones that no longer resolve. Started unconditionally: the
	// registry is empty when no protocol listener is enabled, so the tick costs
	// a map read.
	if s.protocolAuth != nil {
		go s.protocolAuth.RunRevalidator(ctx, protocolauth.DefaultRevalidate)
	}

	// Retention crons ("Koru" v0.4). The trash purge loop existed as
	// trash.Service.RunDailyLoop but was never started anywhere — the
	// documented daily purge (docs/TRASH-VERSIONING.md) only ran via the
	// admin "empty" button. Wire it, plus the new version-retention loop
	// (versions.keep_n; 0 = disabled). Both tick daily, first tick one
	// interval after boot.
	if s.trash != nil {
		go s.trash.RunDailyLoop(ctx, 24*time.Hour)
	}
	if s.versions != nil {
		go s.versions.RunRetentionLoop(ctx, 24*time.Hour)
	}

	// SMTP config verification — run once on boot, then every 5 minutes. The
	// invite/share flow only sends mail while the last verification succeeded;
	// otherwise the UI shows the link / temp password on-screen.
	if s.mailer != nil {
		go func() {
			// Optimistically trust the last-known-good state so sends work
			// immediately after a deploy, before the (slower) re-verify below.
			s.mailer.PrimeFromStore(ctx)
			verify := func() {
				vctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				defer cancel()
				if err := s.mailer.Verify(vctx); err != nil {
					slog.Debug("smtp verify", slog.String("result", err.Error()))
				} else {
					slog.Debug("smtp verify: ok")
				}
			}
			verify()
			t := time.NewTicker(5 * time.Minute)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					verify()
				}
			}
		}()
	}

	go func() {
		<-ctx.Done()
		slog.Info("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutCtx)
		if s.sftp != nil {
			_ = s.sftp.Close()
		}
		if s.ftps != nil {
			_ = s.ftps.Close()
		}
		if s.nfs != nil {
			_ = s.nfs.Close()
		}
		if s.plugins != nil {
			s.plugins.Shutdown()
		}
		s.worker.Stop()
		if s.ops != nil {
			s.ops.Stop()
		}
		if s.qpool != nil {
			s.qpool.Stop()
		}
		if s.queue != nil {
			_ = s.queue.Close()
		}
		if s.replicaCron != nil {
			s.replicaCron.Stop()
		}
		if s.replicaSvc != nil {
			s.replicaSvc.Stop()
		}
		if s.notify != nil {
			s.notify.Stop()
		}
		if s.idx != nil {
			_ = s.idx.Close()
		}
		_ = s.sqlDB.Close()
	}()

	// Optional: thumbnail backfill on boot. Useful for instances that
	// already have nodes in the cache but were running on a binary
	// without the right dependencies — a one-shot run paints the
	// existing rows so the SFC GridView lights up immediately.
	//
	// Setting FILEX_THUMB_BACKFILL_ON_BOOT=once runs the backfill
	// exactly once per process start, in the background, AFTER the
	// HTTP server is listening (so the boot path stays fast). Default
	// off — operators must opt in.
	if mode := strings.ToLower(strings.TrimSpace(os.Getenv("FILEX_THUMB_BACKFILL_ON_BOOT"))); mode == "once" || mode == "true" || mode == "1" {
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					slog.Warn("thumb backfill (boot): panic recovered", slog.Any("recover", rec))
				}
			}()
			// Brief grace so the listener has registered.
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			slog.Info("thumb backfill (boot): starting one-shot backfill")
			res, err := s.BackfillThumbs(ctx, BackfillOptions{})
			if err != nil {
				slog.Warn("thumb backfill (boot): aborted", slog.String("err", err.Error()))
				return
			}
			slog.Info("thumb backfill (boot): done",
				slog.Int("processed", res.Processed),
				slog.Int("ok", res.OK),
				slog.Int("failed", res.Failed),
				slog.Int("skipped", res.Skipped),
			)
		}()
	}

	if s.sftp != nil {
		go func() {
			if err := s.sftp.ListenAndServe(); err != nil {
				// Same rule as above: the SFTP port failing to bind is a
				// complaint, not a reason to stop serving the web app.
				slog.Error("sftp: listener stopped", slog.String("err", err.Error()))
			}
		}()
	}

	if s.ftps != nil {
		go func() {
			if err := s.ftps.ListenAndServe(); err != nil {
				slog.Error("ftps: listener stopped", slog.String("err", err.Error()))
			}
		}()
	}

	if s.nfs != nil {
		go func() {
			if err := s.nfs.ListenAndServe(); err != nil {
				slog.Error("nfs: listener stopped", slog.String("err", err.Error()))
			}
		}()
	}

	slog.Info("filex listening", slog.String("addr", s.cfg.Listen))
	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Store exposes the DB store — used by the CLI subcommands (filex admin
// reset-password, etc.).
func (s *Server) Store() db.Store { return s.store }

// jsonDecode is a tiny wrapper to avoid importing encoding/json everywhere.
func jsonDecode(b []byte, out any) error {
	return json.Unmarshal(b, out)
}

// initWithBackoff retries init() through the given delay slots until it
// succeeds, ctx is cancelled, or every slot has been tried. A 0 first slot
// means "try once immediately, then wait before each retry".
func initWithBackoff(ctx context.Context, driver string, init func(context.Context) error, backoffs []time.Duration) error {
	var err error
	for i, delay := range backoffs {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err = init(ctx); err == nil {
			if i > 0 {
				slog.Info("driver init succeeded after retries",
					slog.String("driver", driver),
					slog.Int("attempts", i+1))
			}
			return nil
		}
		slog.Warn("driver init attempt failed",
			slog.String("driver", driver),
			slog.Int("attempt", i+1),
			slog.Int("remaining", len(backoffs)-i-1),
			slog.String("err", err.Error()))
	}
	return err
}

// quotaMetrics bridges the accounting store to the Prometheus exposition.
// It lives here rather than in internal/quotastore so that package keeps its
// dependencies at auth/db/model/quota and stays testable without a registry.
type quotaMetrics struct{}

// QuotaUsageDelta moves the process-wide usage gauge by the same delta the
// accounting store just wrote, and records the absolute movement so a stalled
// accounting path is visible even when adds and releases cancel out.
func (quotaMetrics) QuotaUsageDelta(_ int64, delta int64) {
	metrics.QuotaUsageBytes.Add(float64(delta))
	if delta > 0 {
		metrics.QuotaAccountedBytes.WithLabelValues("added").Add(float64(delta))
	} else {
		metrics.QuotaAccountedBytes.WithLabelValues("released").Add(float64(-delta))
	}
}
