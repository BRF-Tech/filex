package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestSeedFromEnv_SeedsSettingsAndStorage — a fresh DB seeds branding, trash
// policy, SMTP settings rows and an initial local storage from cfg.Seed.
func TestSeedFromEnv_SeedsSettingsAndStorage(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	cfg := config.Default()
	cfg.Seed.SiteName = "Acme Files"
	cfg.Seed.TrashDays = "30"
	cfg.Seed.SMTP = config.SeedSMTP{
		Host: "smtp.example.com", Port: "587", From: "no-reply@example.com",
		Username: "u", Password: "p", TLS: "starttls",
	}
	cfg.Seed.Storage = config.SeedStorage{
		Driver: "local", Name: "Files", MountPath: "/", Path: "/var/lib/filex/data",
	}

	seedFromEnv(ctx, store, cfg)

	get := func(k string) string { v, _ := store.GetSetting(ctx, k); return v }
	assert.Equal(t, "Acme Files", get("site_name"))
	assert.Equal(t, "30", get("trash.retention_days"))
	assert.Equal(t, "smtp.example.com", get(mailer.KeyHost))
	assert.Equal(t, "587", get(mailer.KeyPort))
	assert.Equal(t, "no-reply@example.com", get(mailer.KeyFrom))
	assert.Equal(t, "starttls", get(mailer.KeyTLS))

	sts, err := store.ListStorages(ctx)
	require.NoError(t, err)
	require.Len(t, sts, 1)
	assert.Equal(t, "local", sts[0].Driver)
	assert.Equal(t, "Files", sts[0].Name)
	assert.Contains(t, string(sts[0].ConfigJSON), "/var/lib/filex/data")
}

// TestSeedFromEnv_OnlyIfAbsent — existing operator values are never clobbered.
func TestSeedFromEnv_OnlyIfAbsent(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertSetting(ctx, "site_name", "Operator Name"))
	_, err := store.CreateStorage(ctx, &model.Storage{
		Name: "Existing", Driver: "local", MountPath: "/",
		ConfigJSON: json.RawMessage(`{"path":"/op/path"}`),
		SyncMode:   model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)

	cfg := config.Default()
	cfg.Seed.SiteName = "Env Name"
	cfg.Seed.Storage = config.SeedStorage{Driver: "local", Path: "/env/path"}

	seedFromEnv(ctx, store, cfg)

	v, _ := store.GetSetting(ctx, "site_name")
	assert.Equal(t, "Operator Name", v, "existing setting must not be clobbered")

	sts, _ := store.ListStorages(ctx)
	require.Len(t, sts, 1, "no new storage when one already exists")
	assert.Equal(t, "Existing", sts[0].Name)
}

// TestSeedDefaultStorage_S3 — an s3 default storage carries the bundled-MinIO
// endpoint + creds into its config blob.
func TestSeedDefaultStorage_S3(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	cfg := config.Default()
	cfg.Seed.Storage = config.SeedStorage{
		Driver: "s3", Bucket: "filex", Prefix: "files",
		Endpoint: "http://minio:9000", Region: "auto",
		AccessKey: "ak", SecretKey: "sk", PathStyle: true,
	}

	seedFromEnv(ctx, store, cfg)

	sts, err := store.ListStorages(ctx)
	require.NoError(t, err)
	require.Len(t, sts, 1)
	assert.Equal(t, "s3", sts[0].Driver)
	blob := string(sts[0].ConfigJSON)
	assert.Contains(t, blob, "filex")
	assert.Contains(t, blob, "minio:9000")
}

// TestSeedDefaultStorage_RawConfig — an existing external storage (sftp) is
// connected via a raw config JSON, for any driver.
func TestSeedDefaultStorage_RawConfig(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	cfg := config.Default()
	cfg.Seed.Storage = config.SeedStorage{
		Driver: "sftp",
		Name:   "NAS",
		Config: `{"host":"nas.example.com","user":"filex","password":"x","root":"/srv/files"}`,
	}

	seedFromEnv(ctx, store, cfg)

	sts, err := store.ListStorages(ctx)
	require.NoError(t, err)
	require.Len(t, sts, 1)
	assert.Equal(t, "sftp", sts[0].Driver)
	assert.Equal(t, "NAS", sts[0].Name)
	assert.Contains(t, string(sts[0].ConfigJSON), "nas.example.com")
}

// TestSeedDefaultStorage_NoneWhenDriverEmpty — no driver → no storage seeded.
func TestSeedDefaultStorage_NoneWhenDriverEmpty(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	seedFromEnv(ctx, store, config.Default())

	sts, err := store.ListStorages(ctx)
	require.NoError(t, err)
	assert.Empty(t, sts)
}
