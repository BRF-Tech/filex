package handlers_test

// A save that weakens a protection of a connection while a saved credential is
// kept asks for the credential again (#298, the owner's calls of 2026-11-06):
// SMTP encryption to None, LDAP StartTLS off on ldap://, a new LDAP CA, the SFTP
// host-key check off or another key or key file, FTP without TLS. Measured on
// the real server before the rule: each sent the saved password over the
// weakened connection. And the OpenID redirect address, where no secret goes,
// asks for nothing.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// protectionNeeded is secretNeeded for a weakened protection: the sentence
// names the setting.
func protectionNeeded(t *testing.T, status int, raw []byte, field, setting string) {
	t.Helper()
	assert.Equal(t, http.StatusBadRequest, status, string(raw))
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body), string(raw))
	assert.Equal(t, "SECRET_NEEDED", body["error"])
	assert.Equal(t, field, body["field"])
	assert.Contains(t, body["message"], "("+setting+")")
	assert.Contains(t, body["message"], "type "+field+" again")
}

func TestSMTP_EncryptionOffNeedsThePasswordTypedAgain(t *testing.T) {
	srv, client, store, _, _ := liveServer(t, nil)
	base := srv.URL + "/api/admin/settings"
	stored := func(k string) string { v, _ := store.GetSetting(context.Background(), k); return v }
	status, raw := doReq(t, client, http.MethodPatch, base, map[string]any{
		"smtp.host": "mail.example.com", "smtp.tls": "starttls", "smtp.username": "u", "smtp.password": "saved-pw",
	})
	require.Equal(t, http.StatusOK, status, string(raw))

	status, raw = doReq(t, client, http.MethodPatch, base, map[string]any{"smtp.tls": "none"})
	protectionNeeded(t, status, raw, "smtp.password", "smtp.tls")
	status, raw = doReq(t, client, http.MethodPatch, base, map[string]any{"smtp.tls": "none", "smtp.password": "***"})
	protectionNeeded(t, status, raw, "smtp.password", "smtp.tls")
	status, raw = doReq(t, client, http.MethodPut, base+"/smtp.tls", map[string]any{"value": "none"})
	protectionNeeded(t, status, raw, "smtp.password", "smtp.tls")
	status, raw = doReq(t, client, http.MethodPatch, base, map[string]any{"smtp.tls": ""})
	protectionNeeded(t, status, raw, "smtp.password", "smtp.tls")
	assert.Equal(t, "starttls", stored("smtp.tls"), "a refused save changed the encryption")

	// Encrypted the other way, or another field: nothing asked.
	status, raw = doReq(t, client, http.MethodPatch, base, map[string]any{"smtp.tls": "tls", "smtp.from": "x@example.com"})
	require.Equal(t, http.StatusOK, status, string(raw))
	// Typed, it goes; and back to encrypted asks for nothing.
	status, raw = doReq(t, client, http.MethodPatch, base, map[string]any{"smtp.tls": "none", "smtp.password": "typed"})
	require.Equal(t, http.StatusOK, status, string(raw))
	status, raw = doReq(t, client, http.MethodPatch, base, map[string]any{"smtp.tls": "starttls"})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Equal(t, "typed", stored("smtp.password"))
}

func TestLDAP_WeakeningTLSNeedsTheBindPasswordTypedAgain(t *testing.T) {
	srv, client, _, _, _ := liveServer(t, nil)
	save := func(cfg map[string]any) (int, []byte) {
		return doReq(t, client, http.MethodPatch, srv.URL+"/api/admin/auth-providers/ldap", map[string]any{"enabled": false, "config": cfg})
	}
	test := func(cfg map[string]any) (int, []byte) {
		return doReq(t, client, http.MethodPost, srv.URL+"/api/admin/auth-providers/ldap/test", map[string]any{"config": cfg})
	}
	status, raw := save(map[string]any{"url": "ldap://dir.example.invalid", "base_dn": "dc=example", "start_tls": true, "bind_password": "saved"})
	require.Equal(t, http.StatusOK, status, string(raw))

	status, raw = save(map[string]any{"start_tls": false, "bind_password": ""})
	protectionNeeded(t, status, raw, "bind_password", "start_tls")
	status, raw = test(map[string]any{"start_tls": false, "bind_password": ""})
	protectionNeeded(t, status, raw, "bind_password", "start_tls")
	for _, k := range []string{"ca_pem", "ca_file"} {
		status, raw = save(map[string]any{k: "/etc/attacker-ca.pem", "bind_password": "***"})
		protectionNeeded(t, status, raw, "bind_password", k)
		status, raw = test(map[string]any{k: "/etc/attacker-ca.pem"})
		protectionNeeded(t, status, raw, "bind_password", k)
	}

	// Another field, StartTLS as it is: nothing asked. Typed, it goes, and turning
	// StartTLS back on asks for nothing.
	status, raw = save(map[string]any{"base_dn": "dc=other", "bind_password": ""})
	require.Equal(t, http.StatusOK, status, string(raw))
	status, raw = save(map[string]any{"start_tls": false, "bind_password": "typed"})
	require.Equal(t, http.StatusOK, status, string(raw))
	status, raw = save(map[string]any{"start_tls": true, "bind_password": ""})
	require.Equal(t, http.StatusOK, status, string(raw))

	// On ldaps:// StartTLS means nothing: turning it off asks for nothing.
	status, raw = save(map[string]any{"url": "ldaps://dir.example.invalid", "start_tls": true, "bind_password": "typed2"})
	require.Equal(t, http.StatusOK, status, string(raw))
	status, raw = save(map[string]any{"start_tls": false, "bind_password": ""})
	require.Equal(t, http.StatusOK, status, string(raw))
}

func TestTenantSelf_ANewCANeedsTheBindPasswordTypedAgain(t *testing.T) {
	srv, _, _, _, _ := selfServer(t)
	acmeAdmin := tenantClient(t, srv, "admin@acme.test")
	base := srv.URL + "/api/admin/tenant/auth-providers"
	status, raw := doReq(t, acmeAdmin, http.MethodPost, base, map[string]any{"driver": "ldap", "enabled": false, "config": map[string]any{
		"url": "ldaps://dir.acme.example", "base_dn": "dc=acme", "bind_password": "saved-tenant-pw",
	}})
	require.Equal(t, http.StatusCreated, status, string(raw))

	status, raw = doReq(t, acmeAdmin, http.MethodPatch, base+"/acme-ldap", map[string]any{"enabled": false, "config": map[string]any{"ca_pem": "-----BEGIN CERTIFICATE-----\nattacker", "bind_password": ""}})
	protectionNeeded(t, status, raw, "bind_password", "ca_pem")
	status, raw = doReq(t, acmeAdmin, http.MethodPost, base+"/acme-ldap/test", map[string]any{"config": map[string]any{"ca_pem": "-----BEGIN CERTIFICATE-----\nattacker"}})
	protectionNeeded(t, status, raw, "bind_password", "ca_pem")
}

func TestStorages_WeakeningTheHostKeyCheckNeedsThePasswordTypedAgain(t *testing.T) {
	f := newSecretFixture(t)
	st := f.sftpStorage(t, "kutu-ssh", "sftp")
	path := "/api/admin/storages/" + strconv.FormatInt(st.ID, 10)
	shown := func(extra map[string]any) map[string]any {
		c := map[string]any{"host": "files.example.com", "port": 22, "user": "u", "password": "***", "root": "/srv/files"}
		for k, v := range extra {
			c[k] = v
		}
		return c
	}
	for setting, extra := range map[string]map[string]any{
		"insecure_skip_host_key": {"insecure_skip_host_key": true},
		"host_key":               {"host_key": "ssh-ed25519 AAAAattacker"},
		"known_hosts":            {"known_hosts": "/tmp/attacker_known_hosts"},
	} {
		code, body := f.do(t, http.MethodPatch, path, map[string]any{"config": shown(extra)})
		protectionNeeded(t, code, []byte(body), "password", setting)
		code, body = f.do(t, http.MethodPost, "/api/admin/storages/test", map[string]any{"id": st.ID, "driver": "sftp", "config": shown(extra)})
		protectionNeeded(t, code, []byte(body), "password", setting)
	}
	assert.Equal(t, storagePassword, f.storedPassword(t, st.ID))

	// Typed, it goes; turning the check back on asks for nothing.
	typed := shown(map[string]any{"insecure_skip_host_key": true})
	typed["password"] = "typed"
	code, body := f.do(t, http.MethodPatch, path, map[string]any{"config": typed})
	require.Equal(t, http.StatusOK, code, body)
	code, body = f.do(t, http.MethodPatch, path, map[string]any{"config": shown(map[string]any{"insecure_skip_host_key": false})})
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "typed", f.storedPassword(t, st.ID))
}

func TestStorages_FTPWithoutTLSNeedsThePasswordTypedAgain(t *testing.T) {
	f := newSecretFixture(t)
	cfg, _ := json.Marshal(map[string]any{"host": "ftp.example.com", "user": "u", "password": storagePassword, "root": "/fx", "tls": true})
	st, err := f.store.CreateStorage(context.Background(), &model.Storage{
		Name: "ftpbox", Driver: "ftp", MountPath: "/ftpbox", ConfigJSON: cfg, SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900,
	})
	require.NoError(t, err)
	path := "/api/admin/storages/" + strconv.FormatInt(st.ID, 10)
	shown := map[string]any{"host": "ftp.example.com", "user": "u", "password": "***", "root": "/fx", "tls": false}

	code, body := f.do(t, http.MethodPatch, path, map[string]any{"config": shown})
	protectionNeeded(t, code, []byte(body), "password", "tls")
	delete(shown, "tls")
	code, body = f.do(t, http.MethodPatch, path, map[string]any{"config": shown})
	protectionNeeded(t, code, []byte(body), "password", "tls")

	shown["tls"] = true
	shown["root"] = "/fx2"
	code, body = f.do(t, http.MethodPatch, path, map[string]any{"config": shown})
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, storagePassword, f.storedPassword(t, st.ID))
}

func TestIdentityProviders_ARedirectAddressAsksForNothing(t *testing.T) {
	srv, client, store, _, _ := liveServer(t, nil)
	status, out := patchProvider(t, client, srv.URL, "oidc", map[string]any{"enabled": false, "config": map[string]any{
		"issuer": "https://idp.example.invalid", "client_id": "filex", "client_secret": "saved", "redirect_url": "https://files.example.com/api/auth/oidc/callback",
	}})
	require.Equal(t, http.StatusOK, status, out)

	status, out = patchProvider(t, client, srv.URL, "oidc", map[string]any{"enabled": false, "config": map[string]any{"redirect_url": "https://new.example.com/api/auth/oidc/callback", "client_secret": ""}})
	require.Equal(t, http.StatusOK, status, out)
	st, raw := doReq(t, client, http.MethodPost, srv.URL+"/api/admin/auth-providers/oidc/test", map[string]any{"config": map[string]any{"redirect_url": "https://other.example.com/cb"}})
	assert.NotContains(t, string(raw), "SECRET_NEEDED")
	assert.Equal(t, http.StatusOK, st, string(raw))

	// A tenant's row the same way.
	code, created := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/providers", map[string]any{
		"slug": "acme", "oidc_issuer": "https://id.acme.example", "oidc_client_id": "filex", "oidc_client_secret": "saved-tenant",
		"oidc_redirect_url": "https://acme.example/api/auth/oidc/callback",
	})
	require.Equal(t, http.StatusCreated, code, created)
	id := int64(created["id"].(float64))
	code, got := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/providers/"+itoa64(id), map[string]any{"oidc_redirect_url": "https://new.acme.example/cb", "oidc_client_secret": ""})
	require.Equal(t, http.StatusOK, code, got)
	p, err := store.GetProvider(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "saved-tenant", p.OIDCClientSecret)
}
