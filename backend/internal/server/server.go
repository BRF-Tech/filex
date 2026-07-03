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
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth"
	authapitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	authldap "github.com/brf-tech/filex/backend/internal/auth/drivers/ldap"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	authproxyheader "github.com/brf-tech/filex/backend/internal/auth/drivers/proxyheader"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/replica"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/trash"
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
	quota           *quota.Service
	srv             *http.Server
	idx             *search.Index
	pipeline        *thumb.Pipeline
	resolver        func(int64) (storage.Driver, error)
	mailer          *mailer.Service

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
	store := dbDrv.NewStore(sqlDB)

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
	)

	// Sync worker. Bind the search index so every create/update/delete
	// during a sync run also updates Bleve — without this, the in-toolbar
	// search box only sees rows the admin's "Rebuild" button has touched.
	worker := syncpkg.New(store)
	if idx != nil {
		worker.AttachIndex(idx)
	}

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

	// Share service.
	shareSvc := share.NewService(store)

	srvObj := &Server{
		cfg:      cfg,
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

	// Notifications subsystem.
	if cfg.Notify.Enabled {
		srvObj.notify = notify.New(store, notify.Config{
			WebhookURL:   cfg.Notify.WebhookURL,
			WebhookToken: cfg.Notify.WebhookToken,
		})
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

	// Quota service — per-user usage accounting + admin set.
	quotaSvc := quota.New(store)
	srvObj.quota = quotaSvc

	// Trash retention service — handles soft-delete restore + scheduled
	// purge of expired tombstones.
	trashSvc := trash.New(store, resolver, quotaSvc)
	srvObj.trash = trashSvc

	// Versioning service — snapshots before destructive writes; the API
	// layer exposes list/restore/hard-delete via /api/files/versions and
	// /api/admin/versions.
	versionsSvc := versioning.New(store, versioning.StorageResolver(resolver))

	// Mailer for invite/share notices — verified periodically in Start().
	srvObj.mailer = mailer.New(store)

	deps := &api.Deps{
		Cfg:             cfg,
		Store:           store,
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
		Embed:           embedFS,
		LocalAuth:       localDrv,
		OIDCAuth:        oidcDrv,
		Mailer:          srvObj.mailer,
	}
	router := api.BuildRouter(deps)

	srvObj.srv = &http.Server{
		Addr:              cfg.Listen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Seed default rows in external_services so the admin UI has
	// editable cards for OnlyOffice/Drawio/Mermaid even on fresh
	// installs. UpsertExternalService is no-op when the row already
	// exists with the same shape (sqlite + postgres drivers).
	seedExternalDefaults(ctx, store, cfg)

	return srvObj, nil
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
		{name: "mermaid", url: cfg.ExternalServices.Mermaid.URL, secret: ""},
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
	fr, err := FirstRun(ctx, s.store, s.cfg.DataDir)
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
	if s.replicaCron != nil {
		s.replicaCron.Start()
		_ = s.replicaCron.Reload(ctx)
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
