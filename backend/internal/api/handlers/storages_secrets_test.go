package handlers_test

// A storage's credentials never go out (#186 follow-up, the second leak).
//
// ⚠ Every read of a storage - the Storages page, a tenant administrator's
// storage settings, an admin API key on /api/ai/admin, the admin_storages_*
// MCP tools - answered with its configuration as stored: an S3 secret key, an
// SMB or SFTP password, a private key, in clear. They are masked now ("***").
// A save that sends the mask back keeps the stored value - but only while the
// credential would go where it was saved: pointing a storage at a new host
// with the password kept would let anybody who may edit a storage, but not
// read its password, have filex send it to a server of their own.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const storagePassword = "pw-never-shown"

// probeSeen records the password a "Test connection" probe was given.
var probeSeen sync.Map // host -> password

type secretProbeDriver struct {
	storage.Driver
	host string
}

func init() {
	storage.Register("secretprobe-test", func() storage.Driver {
		d, _ := storage.Get("local")
		return &secretProbeDriver{Driver: d}
	})
}

func (d *secretProbeDriver) Name() string { return "secretprobe-test" }
func (d *secretProbeDriver) Init(_ context.Context, cfg map[string]any) error {
	d.host, _ = cfg["host"].(string)
	pw, _ := cfg["password"].(string)
	probeSeen.Store(d.host, pw)
	return nil
}
func (d *secretProbeDriver) List(context.Context, string) ([]storage.Object, error) { return nil, nil }

type secretFixture struct {
	srv    string
	client *http.Client
	store  interface {
		GetStorage(ctx context.Context, id int64) (*model.Storage, error)
		CreateStorage(ctx context.Context, st *model.Storage) (*model.Storage, error)
		GetUserByEmail(ctx context.Context, email string) (*model.User, error)
		UpdateUserLocale(ctx context.Context, id int64, locale, tz string) error
	}
	email string
}

func newSecretFixture(t *testing.T) *secretFixture {
	t.Helper()
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	return &secretFixture{srv: srv.URL, client: client, store: store, email: email}
}

func (f *secretFixture) do(t *testing.T, method, path string, body any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, f.srv+path, rd)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (f *secretFixture) sftpStorage(t *testing.T, name, driver string) *model.Storage {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{
		"host": "files.example.com", "port": 22, "user": "u", "password": storagePassword, "root": "/srv/files",
	})
	st, err := f.store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: driver, MountPath: "/" + name, ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: false,
	})
	require.NoError(t, err)
	return st
}

func (f *secretFixture) storedPassword(t *testing.T, id int64) string {
	t.Helper()
	st, err := f.store.GetStorage(context.Background(), id)
	require.NoError(t, err)
	cfg := map[string]any{}
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	pw, _ := cfg["password"].(string)
	return pw
}

func TestStorages_CredentialsNeverGoOut(t *testing.T) {
	f := newSecretFixture(t)
	st := f.sftpStorage(t, "uzak", "sftp")

	code, body := f.do(t, http.MethodGet, "/api/admin/storages", nil)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, storagePassword, "the storage list sent the password")
	assert.Contains(t, body, "files.example.com", "the rest of the configuration is still shown")

	code, body = f.do(t, http.MethodGet, "/api/admin/storages/"+strconv.FormatInt(st.ID, 10), nil)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, storagePassword, "the storage sent its password")
	assert.Contains(t, body, `"password":"***"`)

	code, body = f.do(t, http.MethodPost, "/api/admin/storages", map[string]any{
		"name": "yeni", "driver": "sftp", "enabled": false, "mount_path": "/yeni",
		"config": map[string]any{"host": "h", "user": "u", "password": "typed-once", "root": "/srv/x"},
	})
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, "typed-once", "the create answer sent the password back")
}

func TestStorages_TheMaskSentBackKeepsTheSecretWhereItWasSaved(t *testing.T) {
	f := newSecretFixture(t)
	st := f.sftpStorage(t, "uzak", "sftp")
	path := "/api/admin/storages/" + strconv.FormatInt(st.ID, 10)

	// What the form was shown, with the root changed: the password is kept.
	shown := map[string]any{"host": "files.example.com", "port": 22, "user": "u", "password": "***", "root": "/srv/other"}
	code, body := f.do(t, http.MethodPatch, path, map[string]any{"config": shown})
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, storagePassword, f.storedPassword(t, st.ID), "saving what the form showed replaced the password with the mask")
	assert.NotContains(t, body, storagePassword, "the save answer sent the password")

	// The same password pointed at a new host: refused, nothing changes.
	moved := map[string]any{"host": "attacker.example", "port": 22, "user": "u", "password": "***", "root": "/srv/other"}
	code, body = f.do(t, http.MethodPatch, path, map[string]any{"config": moved})
	assert.Equal(t, http.StatusBadRequest, code, "a saved password would have been sent to a new host")
	assert.Contains(t, body, "SECRET_NEEDED")
	assert.Equal(t, storagePassword, f.storedPassword(t, st.ID))
	again, err := f.store.GetStorage(context.Background(), st.ID)
	require.NoError(t, err)
	assert.Contains(t, string(again.ConfigJSON), "files.example.com", "the refused save changed the host")

	// Typed again, it goes.
	moved["password"] = "new-pw"
	code, _ = f.do(t, http.MethodPatch, path, map[string]any{"config": moved})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "new-pw", f.storedPassword(t, st.ID))
}

// The four ways a save can meet the certificate switch, on the API a form, an
// admin key and an MCP agent all reach.
func TestStorages_TurningTheCheckOffNeedsThePasswordTypedAgain(t *testing.T) {
	f := newSecretFixture(t)
	st := f.sftpStorage(t, "kutu", "sftp")
	path := "/api/admin/storages/" + strconv.FormatInt(st.ID, 10)
	config := func(password string, skip any) map[string]any {
		c := map[string]any{"host": "files.example.com", "port": 22, "user": "u", "password": password, "root": "/srv/files"}
		if skip != nil {
			c[storage.SkipTLSVerifyKey] = skip
		}
		return c
	}
	switchOf := func() any {
		row, err := f.store.GetStorage(context.Background(), st.ID)
		require.NoError(t, err)
		cfg := map[string]any{}
		require.NoError(t, json.Unmarshal(row.ConfigJSON, &cfg))
		return cfg[storage.SkipTLSVerifyKey]
	}

	// Off to on, the password masked: refused, in words that say why, and
	// nothing changes.
	code, body := f.do(t, http.MethodPatch, path, map[string]any{"config": config("***", true)})
	assert.Equal(t, http.StatusBadRequest, code, "a saved password would have gone to a server nobody checks")
	assert.Contains(t, body, "SECRET_NEEDED")
	assert.Contains(t, body, `"field":"password"`)
	assert.Contains(t, body, "certificate check")
	assert.Nil(t, switchOf(), "the refused save turned the check off")
	assert.Equal(t, storagePassword, f.storedPassword(t, st.ID))

	// Off to on, the password typed: it goes.
	code, body = f.do(t, http.MethodPatch, path, map[string]any{"config": config("typed-again", true)})
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, true, switchOf())
	assert.Equal(t, "typed-again", f.storedPassword(t, st.ID))

	// On and staying on, another field edited: nothing asked.
	still := config("***", true)
	still["root"] = "/srv/other"
	code, body = f.do(t, http.MethodPatch, path, map[string]any{"config": still})
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "typed-again", f.storedPassword(t, st.ID))

	// On to off: nothing asked.
	code, body = f.do(t, http.MethodPatch, path, map[string]any{"config": config("***", false)})
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, false, switchOf())
	assert.Equal(t, "typed-again", f.storedPassword(t, st.ID))
}

func TestStorages_TheRefusalIsInTheReadersLanguage(t *testing.T) {
	f := newSecretFixture(t)
	st := f.sftpStorage(t, "dil", "sftp")
	// The reader is the account: its language comes before Accept-Language.
	me, err := f.store.GetUserByEmail(context.Background(), f.email)
	require.NoError(t, err)
	require.NoError(t, f.store.UpdateUserLocale(context.Background(), me.ID, "tr", "UTC"))

	code, body := f.do(t, http.MethodPatch, "/api/admin/storages/"+strconv.FormatInt(st.ID, 10), map[string]any{"config": map[string]any{
		"host": "files.example.com", "port": 22, "user": "u", "password": "***", "root": "/srv/files", storage.SkipTLSVerifyKey: true,
	}})

	require.Equal(t, http.StatusBadRequest, code, body)
	assert.Contains(t, body, "Sertifika denetimini kapatmak için password alanını yeniden yazın")
}

// Test connection would have sent the password all the same, and saved nothing.
func TestStorageTest_TurningTheCheckOffDoesNotSendTheSavedPassword(t *testing.T) {
	f := newSecretFixture(t)
	st := f.sftpStorage(t, "denetim", "secretprobe-test")

	code, body := f.do(t, http.MethodPost, "/api/admin/storages/test", map[string]any{
		"id": st.ID, "driver": "secretprobe-test",
		"config": map[string]any{"host": "files.example.com", "user": "u", "password": "***", "root": "/srv/files", storage.SkipTLSVerifyKey: true},
	})

	assert.Equal(t, http.StatusBadRequest, code, body)
	assert.Contains(t, body, "SECRET_NEEDED")
	_, reached := probeSeen.Load("files.example.com")
	assert.False(t, reached, "the probe was opened with the saved password and the check off")
}

func TestStorageTest_AKeptPasswordIsTheSavedOneAndStaysAtItsAddress(t *testing.T) {
	f := newSecretFixture(t)
	st := f.sftpStorage(t, "sinama", "secretprobe-test")

	code, body := f.do(t, http.MethodPost, "/api/admin/storages/test", map[string]any{
		"id": st.ID, "driver": "secretprobe-test",
		"config": map[string]any{"host": "files.example.com", "user": "u", "password": "***", "root": "/srv/files"},
	})
	require.Equal(t, http.StatusOK, code, body)
	seen, _ := probeSeen.Load("files.example.com")
	assert.Equal(t, storagePassword, seen, "Test connection tried the mask as the password")

	code, body = f.do(t, http.MethodPost, "/api/admin/storages/test", map[string]any{
		"id": st.ID, "driver": "secretprobe-test",
		"config": map[string]any{"host": "attacker.example", "user": "u", "password": "***", "root": "/srv/files"},
	})
	assert.Equal(t, http.StatusBadRequest, code, "Test connection sent a saved password to a new host")
	assert.Contains(t, body, "SECRET_NEEDED")
	_, reached := probeSeen.Load("attacker.example")
	assert.False(t, reached, "the probe was opened against the new host")
}

func TestStorages_TheTokenDoorsMaskToo(t *testing.T) {
	srv, client, store, uid := adminFixture(t)
	cfg, _ := json.Marshal(map[string]any{"host": "h", "user": "u", "password": storagePassword, "root": "/srv/files"})
	_, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "sftp-depo", Driver: "sftp", MountPath: "/sftp-depo", ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: false,
	})
	require.NoError(t, err)

	tok := issueToken(t, store, uid, "admin", nil)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/ai/admin/storages", nil)
	req.Header.Set("X-Filex-Token", tok)
	resp, err := client.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	assert.Contains(t, string(raw), "sftp-depo")
	assert.NotContains(t, string(raw), storagePassword, "an admin API key read the storage's password")

	mcpTok := issueToken(t, store, uid, "mcp,admin", nil)
	code, body := mcpPost(t, client, srv.URL+"/api/ai/mcp", mcpTok,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin_storages_list","arguments":{}}}`)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "sftp-depo", body)
	assert.NotContains(t, body, storagePassword, "an MCP client read the storage's password")
}
