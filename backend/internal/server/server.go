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

	"gitlab.com/brftech/filemanager/backend/internal/api"
	"gitlab.com/brftech/filemanager/backend/internal/auth"
	authldap "gitlab.com/brftech/filemanager/backend/internal/auth/drivers/ldap"
	authlocal "gitlab.com/brftech/filemanager/backend/internal/auth/drivers/local"
	authoidc "gitlab.com/brftech/filemanager/backend/internal/auth/drivers/oidc"
	"gitlab.com/brftech/filemanager/backend/internal/capability"
	"gitlab.com/brftech/filemanager/backend/internal/config"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/model"
	"gitlab.com/brftech/filemanager/backend/internal/search"
	"gitlab.com/brftech/filemanager/backend/internal/share"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
	syncpkg "gitlab.com/brftech/filemanager/backend/internal/sync"
	"gitlab.com/brftech/filemanager/backend/internal/thumb"

	// register storage and DB drivers via their init() blocks
	_ "gitlab.com/brftech/filemanager/backend/internal/db/drivers/mysql"
	_ "gitlab.com/brftech/filemanager/backend/internal/db/drivers/postgres"
	_ "gitlab.com/brftech/filemanager/backend/internal/db/drivers/sqlite"
	_ "gitlab.com/brftech/filemanager/backend/internal/storage/drivers/local"
	_ "gitlab.com/brftech/filemanager/backend/internal/storage/drivers/s3"
	_ "gitlab.com/brftech/filemanager/backend/internal/storage/drivers/sftp"
	_ "gitlab.com/brftech/filemanager/backend/internal/storage/drivers/webdav"
)

// Server is the high-level wrapper around HTTP + workers.
type Server struct {
	cfg    config.Config
	store  db.Store
	sqlDB  *sql.DB
	worker *syncpkg.Worker
	srv    *http.Server
	idx    *search.Index

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
			if err := d.Init(ctx, map[string]any{
				"issuer":        cfg.Auth.OIDC.Issuer,
				"client_id":     cfg.Auth.OIDC.ClientID,
				"client_secret": cfg.Auth.OIDC.ClientSecret,
				"redirect_url":  cfg.Auth.OIDC.RedirectURL,
				"role_claim":    cfg.Auth.OIDC.RoleClaim,
				"admin_group":   cfg.Auth.OIDC.AdminGroup,
			}); err != nil {
				slog.Warn("oidc driver init failed", slog.String("err", err.Error()))
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
		default:
			slog.Warn("unknown auth driver", slog.String("name", name))
		}
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

	// Sync worker.
	worker := syncpkg.New(store)

	// Thumbnail pipeline.
	pipelineCaps := thumb.Capabilities{Image: true}
	cap, _ := caps.Get(ctx)
	if cap != nil {
		pipelineCaps.Video = cap.Thumbs.Video
		pipelineCaps.PDF = cap.Thumbs.PDF
		pipelineCaps.Office = cap.Thumbs.Office
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
		storages: map[int64]storage.Driver{},
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

	deps := &api.Deps{
		Cfg:             cfg,
		Store:           store,
		Worker:          worker,
		Index:           idx,
		Caps:            caps,
		Thumbs:          pipeline,
		Share:           shareSvc,
		StorageResolver: resolver,
		Embed:           embedFS,
		LocalAuth:       localDrv,
		OIDCAuth:        oidcDrv,
	}
	router := api.BuildRouter(deps)

	srvObj.srv = &http.Server{
		Addr:              cfg.Listen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srvObj, nil
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

	go func() {
		<-ctx.Done()
		slog.Info("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutCtx)
		s.worker.Stop()
		if s.idx != nil {
			_ = s.idx.Close()
		}
		_ = s.sqlDB.Close()
	}()

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
