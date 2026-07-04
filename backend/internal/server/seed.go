package server

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// seedFromEnv applies one-time bootstrap config from env to the DB, only when
// the target record is ABSENT. It lets a fresh `helm install` / `docker
// compose up` come up fully configured (SMTP, branding, trash policy, and an
// initial storage) from env alone, with no admin-UI clicks. Operator UI edits
// are never overwritten — every write is guarded by an existence check,
// mirroring seedExternalDefaults. Auth/OIDC is env-authoritative already (see
// config.AuthConfig), so it is configured live, not seeded here.
func seedFromEnv(ctx context.Context, store db.Store, cfg config.Config) {
	seedSetting := func(key, val string) {
		if val == "" {
			return
		}
		if cur, _ := store.GetSetting(ctx, key); cur != "" {
			return // operator / previous value wins
		}
		if err := store.UpsertSetting(ctx, key, val); err != nil {
			slog.Warn("seed setting", slog.String("key", key), slog.String("err", err.Error()))
		}
	}

	// Branding + trash retention.
	seedSetting("site_name", cfg.Seed.SiteName)
	seedSetting("trash.retention_days", cfg.Seed.TrashDays)

	// SMTP — seed the mailer's settings keys when host/port/from are all set.
	if cfg.Seed.SMTP.Configured() {
		seedSetting(mailer.KeyHost, cfg.Seed.SMTP.Host)
		seedSetting(mailer.KeyPort, cfg.Seed.SMTP.Port)
		seedSetting(mailer.KeyUser, cfg.Seed.SMTP.Username)
		seedSetting(mailer.KeyPass, cfg.Seed.SMTP.Password)
		seedSetting(mailer.KeyFrom, cfg.Seed.SMTP.From)
		seedSetting(mailer.KeyTLS, cfg.Seed.SMTP.TLS)
	}

	seedDefaultStorage(ctx, store, cfg)
}

// seedDefaultStorage creates one storage row from env when NO storage exists
// yet, so a fresh install already has somewhere to put files. Supports the
// `local` and `s3` drivers (the two a packaged deployment bundles); other
// drivers are added via the admin UI.
func seedDefaultStorage(ctx context.Context, store db.Store, cfg config.Config) {
	s := cfg.Seed.Storage
	if s.Driver == "" {
		return
	}
	existing, err := store.ListStorages(ctx)
	if err != nil {
		slog.Warn("seed storage: list", slog.String("err", err.Error()))
		return
	}
	if len(existing) > 0 {
		return // operator already configured storage
	}

	var cfgMap map[string]any
	if s.Config != "" {
		// Raw JSON config for ANY driver (sftp/webdav/ftp, or advanced s3) —
		// this is how an existing external storage is connected from env.
		if err := json.Unmarshal([]byte(s.Config), &cfgMap); err != nil {
			slog.Warn("seed storage: FILEX_DEFAULT_STORAGE_CONFIG is not valid JSON", slog.String("err", err.Error()))
			return
		}
	} else {
		switch s.Driver {
		case "local":
			if s.Path == "" {
				slog.Warn("seed storage: local driver needs FILEX_DEFAULT_STORAGE_PATH")
				return
			}
			cfgMap = map[string]any{"path": s.Path}
		case "s3":
			if s.Bucket == "" || s.Prefix == "" {
				slog.Warn("seed storage: s3 driver needs FILEX_DEFAULT_STORAGE_S3_BUCKET and _PREFIX")
				return
			}
			cfgMap = map[string]any{
				"bucket":     s.Bucket,
				"prefix":     s.Prefix,
				"endpoint":   s.Endpoint,
				"region":     s.Region,
				"access_key": s.AccessKey,
				"secret_key": s.SecretKey,
				"path_style": s.PathStyle,
			}
		default:
			slog.Warn("seed storage: set FILEX_DEFAULT_STORAGE_CONFIG (raw JSON) for this driver",
				slog.String("driver", s.Driver))
			return
		}
	}

	if err := storage.ValidateNonRootPath(s.Driver, cfgMap); err != nil {
		slog.Warn("seed storage: invalid path", slog.String("err", err.Error()))
		return
	}
	cfgJSON, err := json.Marshal(cfgMap)
	if err != nil {
		slog.Warn("seed storage: marshal config", slog.String("err", err.Error()))
		return
	}

	name := s.Name
	if name == "" {
		name = "Files"
	}
	mount := s.MountPath
	if mount == "" {
		mount = "/"
	}
	st := &model.Storage{
		Name:          name,
		Driver:        s.Driver,
		MountPath:     mount,
		ConfigJSON:    json.RawMessage(cfgJSON),
		SyncMode:      model.SyncModePoll,
		SyncIntervalS: 900,
		Enabled:       true,
	}
	if _, err := store.CreateStorage(ctx, st); err != nil {
		slog.Warn("seed storage: create", slog.String("err", err.Error()))
		return
	}
	slog.Info("seeded default storage from env",
		slog.String("driver", s.Driver), slog.String("name", name))
}
