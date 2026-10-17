package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// shareMaxTTLDaysSeed turns FILEX_SHARE_MAX_TTL ("7", "7d", "168h", "0") into
// the whole-day string the setting stores. Anything unparsable seeds nothing,
// so the default applies rather than a garbage value.
func shareMaxTTLDaysSeed(v string) string {
	if v == "" {
		return ""
	}
	d, err := config.ParseDurationDays(v)
	if err != nil || d < 0 {
		slog.Warn("seed setting", slog.String("key", share.SettingKeyMaxTTLDays), slog.String("err", "unparsable FILEX_SHARE_MAX_TTL, ignored"), slog.String("value", v))
		return ""
	}
	days := int((d + 24*time.Hour - 1) / (24 * time.Hour))
	return strconv.Itoa(days)
}

// seedFromEnv applies one-time bootstrap config from env to the DB, only when
// the target record is ABSENT. It lets a fresh `helm install` / `docker
// compose up` come up fully configured (SMTP, branding, trash policy, and an
// initial storage) from env alone, with no admin-UI clicks. Operator UI edits
// are never overwritten — every write is guarded by an existence check, the
// one seed rule of package dbsetting (and, since 0.57, of the external
// services too). Auth/OIDC is env-authoritative already (see
// config.AuthConfig), so it is configured live, not seeded here.
func seedFromEnv(ctx context.Context, store db.Store, cfg config.Config) {
	// First, so a value an operator set on the old field wins over the
	// environment's seed below (both only fill an empty Display name).
	upgradeSiteName(ctx, store)
	seedLegacySettings(ctx, store, cfg, nil, false)
	seedDefaultStorage(ctx, store, cfg)
}

// legacySeed is one setting seedFromEnv gives a first value.
type legacySeed struct{ key, val string }

// legacySeeds are the settings the FILEX_SITE_NAME / FILEX_TRASH_* /
// FILEX_SHARE_MAX_TTL / FILEX_SMTP_* variables (and their config.yaml `seed:`
// block) give a first value, with the value the configuration names.
func legacySeeds(cfg config.Config) []legacySeed {
	out := []legacySeed{
		// FILEX_SITE_NAME seeds the Display name (`branding.name`, Admin →
		// Settings → Branding): since 0.57 the one name of the instance, on its
		// pages and in its mails alike (task #258).
		{brandingNameKey, cfg.Seed.SiteName},
		{"trash.retention_days", cfg.Seed.TrashDays},
		{share.SettingKeyMaxTTLDays, shareMaxTTLDaysSeed(cfg.Seed.ShareMaxTTLDays)},
	}
	// SMTP — the mailer's settings keys, when host/port/from are all set.
	if cfg.Seed.SMTP.Configured() {
		out = append(out,
			legacySeed{mailer.KeyHost, cfg.Seed.SMTP.Host},
			legacySeed{mailer.KeyPort, cfg.Seed.SMTP.Port},
			legacySeed{mailer.KeyUser, cfg.Seed.SMTP.Username},
			legacySeed{mailer.KeyPass, cfg.Seed.SMTP.Password},
			legacySeed{mailer.KeyFrom, cfg.Seed.SMTP.From},
			legacySeed{mailer.KeyTLS, cfg.Seed.SMTP.TLS},
		)
	}
	return out
}

// legacySeedKeys names every key legacySeeds can write, whatever the
// configuration says (for `filex settings reseed <key>`).
var legacySeedKeys = []string{
	brandingNameKey, "trash.retention_days", share.SettingKeyMaxTTLDays,
	mailer.KeyHost, mailer.KeyPort, mailer.KeyUser, mailer.KeyPass, mailer.KeyFrom, mailer.KeyTLS,
}

// seedLegacySettings writes legacySeeds: into an empty row only (the seed
// rule), or - force, `filex settings reseed` - over the stored one. keys
// narrows it (empty: every one). ⚠ No value is logged: one of them is the SMTP
// password. It answers the keys it wrote.
func seedLegacySettings(ctx context.Context, store db.Store, cfg config.Config, keys []string, force bool) []string {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var done []string
	for _, s := range legacySeeds(cfg) {
		if s.val == "" || (len(want) > 0 && !want[s.key]) {
			continue
		}
		if cur, _ := store.GetSetting(ctx, s.key); cur != "" && !force {
			continue // operator / previous value wins
		}
		if err := store.UpsertSetting(ctx, s.key, s.val); err != nil {
			slog.Warn("seed setting", slog.String("key", s.key), slog.String("err", err.Error()))
			continue
		}
		done = append(done, s.key)
	}
	return done
}

// brandingNameKey is the platform's Display name (handlers/branding.go reads
// the `branding.` keys); a tenant's own lives under tenant.<id>.branding.name
// and is never touched here.
const brandingNameKey = "branding.name"

// legacySiteNameKey is the old Settings page's "Site name", which only the
// share-invite mails read. It was merged into the Display name in 0.57.
const legacySiteNameKey = "site_name"

// upgradeSiteName carries an install's old `site_name` into the Display name
// and removes the old row, so the instance has one name. Nothing is lost:
//
//   - `site_name` set, Display name empty → the Display name takes it (the
//     mails keep saying what they said);
//   - both set → the Display name stays (it is what every page already
//     shows), and the old value is logged before it goes;
//   - `site_name` empty or absent → nothing to do.
//
// It runs at every boot and is a no-op once the row is gone, so it needs no
// migration and works the same on every database. ⚠ The tenant layer
// (tenant.<id>.branding.*) is not read or written: `site_name` never had one.
func upgradeSiteName(ctx context.Context, store db.Store) {
	old, err := store.GetSetting(ctx, legacySiteNameKey)
	if err != nil {
		return
	}
	old = strings.TrimSpace(old)
	if old != "" {
		cur, _ := store.GetSetting(ctx, brandingNameKey)
		if strings.TrimSpace(cur) == "" {
			if err := store.UpsertSetting(ctx, brandingNameKey, old); err != nil {
				// Keep the old row: the next boot tries again.
				slog.Warn("site name upgrade", slog.String("err", err.Error()))
				return
			}
			slog.Info("site name carried over to the Display name", slog.String("value", old))
		} else if strings.TrimSpace(cur) != old {
			slog.Info("site name dropped: the Display name is already set",
				slog.String("site_name", old), slog.String("display_name", cur))
		}
	}
	// The store deletes by prefix: make sure the prefix is this one key.
	all, err := store.ListSettings(ctx)
	if err != nil {
		return
	}
	for k := range all {
		if k != legacySiteNameKey && strings.HasPrefix(k, legacySiteNameKey) {
			_ = store.UpsertSetting(ctx, legacySiteNameKey, "")
			return
		}
	}
	if _, ok := all[legacySiteNameKey]; ok {
		if err := store.DeleteSettingsWithPrefix(ctx, legacySiteNameKey); err != nil {
			slog.Warn("site name upgrade", slog.String("err", err.Error()))
		}
	}
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
