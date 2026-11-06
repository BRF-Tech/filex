package server

// FILEX_STORAGE_TLS_SKIP_VERIFY after 0.58.2: one start moves it to the
// connections, then it does nothing (#298). Real store, real rows.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/ftp"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/s3"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/sftp"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/webdav"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// A secret key with characters JSON may spell two ways, and a number that does
// not survive a float: what must come back the bytes it was.
const s3Row = `{"bucket":"b","prefix":"p","endpoint":"https://s3.example.com:9000","access_key":"AK","secret_key":"s&<k>","attempt_timeout_s":12345678901234567}`

func tlsRow(t *testing.T, store db.Store, name, driver, cfg string) *model.Storage {
	t.Helper()
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: driver, MountPath: "/" + name, ConfigJSON: json.RawMessage(cfg),
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	return st
}

func tlsTarget(t *testing.T, store db.Store, name, driver, cfg string) *model.ReplicationTarget {
	t.Helper()
	rt, err := store.CreateReplicationTarget(context.Background(), &model.ReplicationTarget{
		Name: name, Driver: driver, ConfigJSON: json.RawMessage(cfg), Enabled: true,
	})
	require.NoError(t, err)
	return rt
}

func storedConfig(t *testing.T, store db.Store, id int64) map[string]json.RawMessage {
	t.Helper()
	st, err := store.GetStorage(context.Background(), id)
	require.NoError(t, err)
	var cfg map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	return cfg
}

func targetConfig(t *testing.T, store db.Store, id int64) map[string]json.RawMessage {
	t.Helper()
	rt, err := store.GetReplicationTarget(context.Background(), id)
	require.NoError(t, err)
	var cfg map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rt.ConfigJSON, &cfg))
	return cfg
}

func TestSeedStorageTLS_OnTheFirstStartEveryConnectionWithTheSwitchGetsIt(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	s3 := tlsRow(t, store, "s3", "s3", s3Row)
	dav := tlsRow(t, store, "dav", "webdav", `{"url":"https://dav.example.com/","user":"u","password":"p","root":"fx"}`)
	ftps := tlsRow(t, store, "ftps", "ftp", `{"host":"h","user":"u","password":"p","root":"/fx","tls":true}`)
	plainFTP := tlsRow(t, store, "plainftp", "ftp", `{"host":"h","user":"u","password":"p","root":"/fx"}`)
	sftp := tlsRow(t, store, "sftp", "sftp", `{"host":"h","user":"u","password":"p","root":"/fx"}`)
	disk := tlsRow(t, store, "disk", "local", `{"path":"/srv/disk"}`)
	offRow := tlsRow(t, store, "disabled", "s3", s3Row)
	offRow.Enabled = false
	require.NoError(t, store.UpdateStorage(ctx, offRow))
	target := tlsTarget(t, store, "backup", "s3", s3Row)
	localTarget := tlsTarget(t, store, "backup-disk", "local", `{"path":"/srv/backup"}`)

	assert.True(t, seedStorageTLS(ctx, store, true))

	on := json.RawMessage("true")
	assert.Equal(t, on, storedConfig(t, store, s3.ID)[storage.SkipTLSVerifyKey])
	assert.Equal(t, on, storedConfig(t, store, dav.ID)[storage.SkipTLSVerifyKey])
	assert.Equal(t, on, storedConfig(t, store, ftps.ID)[storage.SkipTLSVerifyKey])
	assert.Equal(t, on, storedConfig(t, store, offRow.ID)[storage.SkipTLSVerifyKey], "a storage switched off today is switched on tomorrow")
	assert.Equal(t, on, targetConfig(t, store, target.ID)[storage.SkipTLSVerifyKey])
	assert.NotContains(t, storedConfig(t, store, plainFTP.ID), storage.SkipTLSVerifyKey, "without FTPS the switch means nothing")
	assert.NotContains(t, storedConfig(t, store, sftp.ID), storage.SkipTLSVerifyKey)
	assert.NotContains(t, storedConfig(t, store, disk.ID), storage.SkipTLSVerifyKey)
	assert.NotContains(t, targetConfig(t, store, localTarget.ID), storage.SkipTLSVerifyKey)
}

// A row keeps everything else it held, to the byte: a stored credential above
// all, whatever it is written in.
func TestSeedStorageTLS_LeavesTheRestOfTheConfigurationAlone(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	st := tlsRow(t, store, "s3", "s3", s3Row)
	var before map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(s3Row), &before))

	seedStorageTLS(ctx, store, true)

	after := storedConfig(t, store, st.ID)
	delete(after, storage.SkipTLSVerifyKey)
	assert.Equal(t, before, after)
	got, err := store.GetStorage(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, st.Name, got.Name)
	assert.Equal(t, st.MountPath, got.MountPath)
	assert.Equal(t, st.Enabled, got.Enabled)
	assert.Equal(t, st.UID, got.UID)
}

func TestSeedStorageTLS_ASecondStartChangesNothing(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	kept := tlsRow(t, store, "kept", "s3", s3Row)
	require.True(t, seedStorageTLS(ctx, store, true))

	// The administrator turned one switch off, and a storage came after it.
	cfg := storedConfig(t, store, kept.ID)
	cfg[storage.SkipTLSVerifyKey] = json.RawMessage("false")
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	row, err := store.GetStorage(ctx, kept.ID)
	require.NoError(t, err)
	row.ConfigJSON = raw
	require.NoError(t, store.UpdateStorage(ctx, row))
	later := tlsRow(t, store, "later", "s3", s3Row)

	assert.False(t, seedStorageTLS(ctx, store, true), "the variable is still set: it does nothing now")

	assert.Equal(t, json.RawMessage("false"), storedConfig(t, store, kept.ID)[storage.SkipTLSVerifyKey])
	assert.NotContains(t, storedConfig(t, store, later.ID), storage.SkipTLSVerifyKey)
}

// Without the variable on the first start there is nothing to move, and that
// start counts: set later, it must not take the check away from every storage.
func TestSeedStorageTLS_TheFirstStartCountsWithTheVariableOff(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	st := tlsRow(t, store, "s3", "s3", s3Row)

	assert.False(t, seedStorageTLS(ctx, store, false))
	assert.NotContains(t, storedConfig(t, store, st.ID), storage.SkipTLSVerifyKey)

	assert.False(t, seedStorageTLS(ctx, store, true))
	assert.NotContains(t, storedConfig(t, store, st.ID), storage.SkipTLSVerifyKey)
}

// The variable's move is nobody editing anything: it does not go through
// KeepSecrets and asks for no password, and a later edit of a moved row keeps
// the saved one, because the switch stays on.
func TestSeedStorageTLS_IsNotAnEditAndAsksForNoPassword(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	st := tlsRow(t, store, "dav", "webdav", `{"url":"https://dav.example.com/","user":"u","password":"real","root":"fx"}`)

	require.True(t, seedStorageTLS(ctx, store, true))

	row, err := store.GetStorage(ctx, st.ID)
	require.NoError(t, err)
	assert.Contains(t, string(row.ConfigJSON), `"password":"real"`)
	kept, err := storage.KeepSecrets("webdav", storage.MaskSecrets("webdav", row.ConfigJSON), "webdav", row.ConfigJSON)
	require.NoError(t, err, "the form shows a moved row with the mask and the switch on, and saving it asks for nothing")
	assert.Contains(t, string(kept), `"password":"real"`)
}

// A configuration that already answers is the administrator's.
func TestWithSkipTLSVerify_AnAnswerIsKept(t *testing.T) {
	raw := json.RawMessage(`{"bucket":"b","insecure_skip_tls_verify":false}`)

	out, changed, err := withSkipTLSVerify("s3", raw)

	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, raw, out)
}

func TestWithSkipTLSVerify_UnknownDriverAndBrokenRow(t *testing.T) {
	out, changed, err := withSkipTLSVerify("no-such-driver", json.RawMessage(`not json`))
	require.NoError(t, err, "a driver that has no switch has nothing to read")
	assert.False(t, changed)
	assert.Equal(t, json.RawMessage(`not json`), out)

	_, _, err = withSkipTLSVerify("s3", json.RawMessage(`not json`))
	assert.Error(t, err)
}

// One row that cannot be read leaves every other row as it was, and the next
// start does all of it again.
func TestSeedStorageTLS_AFailureChangesNothing(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	good := tlsRow(t, store, "good", "s3", s3Row)
	tlsTarget(t, store, "broken", "s3", `[1,2]`)

	assert.True(t, seedStorageTLS(ctx, store, true), "the storage seeded later in this start still wants the answer")

	assert.NotContains(t, storedConfig(t, store, good.ID), storage.SkipTLSVerifyKey)
	done, _ := store.GetSetting(ctx, settingStorageTLSSeeded)
	assert.Empty(t, done)
}

// A fresh install: the variable and a storage from the environment, which is
// made after the rows above were looked at.
func TestSeedDefaultStorage_TakesTheSwitchOnTheStartTheVariableIsRead(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	cfg := config.Default()
	cfg.Seed.Storage = config.SeedStorage{Driver: "s3", Bucket: "b", Prefix: "p", Endpoint: "https://s3.example.com"}
	cfg.StorageTLS.SkipVerify = seedStorageTLS(ctx, store, true)

	seedDefaultStorage(ctx, store, cfg)

	rows, err := store.ListStorages(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Contains(t, string(rows[0].ConfigJSON), `"insecure_skip_tls_verify":true`)
}

func TestSeedDefaultStorage_NoSwitchOnAnyOtherStart(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	seedStorageTLS(ctx, store, false)
	cfg := config.Default()
	cfg.Seed.Storage = config.SeedStorage{Driver: "s3", Bucket: "b", Prefix: "p", Endpoint: "https://s3.example.com"}
	cfg.StorageTLS.SkipVerify = seedStorageTLS(ctx, store, true)

	seedDefaultStorage(ctx, store, cfg)

	rows, err := store.ListStorages(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.NotContains(t, string(rows[0].ConfigJSON), storage.SkipTLSVerifyKey)
}
