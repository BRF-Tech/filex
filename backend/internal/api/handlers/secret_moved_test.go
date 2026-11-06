package handlers_test

// A saved credential goes only to the address it was saved for, on every page
// that keeps one (#298): the SMTP settings, the instance's identity providers
// (their save and their Test), a tenant's own providers and a tenant's OIDC.
// Measured on the real server before the rule: each of them sent the saved
// password or client secret to a new address.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// listener is a directory nobody should reach with a saved password: it keeps
// what it is sent.
type listener struct {
	addr string
	mu   sync.Mutex
	got  []byte
}

func newListener(t *testing.T) *listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	l := &listener{addr: ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				b, _ := io.ReadAll(c)
				l.mu.Lock()
				l.got = append(l.got, b...)
				l.mu.Unlock()
			}()
		}
	}()
	return l
}

func (l *listener) saw(s string) bool {
	time.Sleep(300 * time.Millisecond)
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Contains(string(l.got), s)
}

func secretNeeded(t *testing.T, status int, raw []byte, field string) {
	t.Helper()
	assert.Equal(t, http.StatusBadRequest, status, string(raw))
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body), string(raw))
	assert.Equal(t, "SECRET_NEEDED", body["error"])
	assert.Equal(t, field, body["field"])
	assert.Contains(t, body["message"], "type "+field+" again")
}

func TestSMTP_ANewHostNeedsThePasswordTypedAgain(t *testing.T) {
	srv, client, store, _, _ := liveServer(t, nil)
	base := srv.URL + "/api/admin/settings"
	stored := func(k string) string { v, _ := store.GetSetting(context.Background(), k); return v }
	status, raw := doReq(t, client, http.MethodPatch, base, map[string]any{
		"smtp.host": "mail.example.com", "smtp.port": "587", "smtp.username": "u", "smtp.password": "saved-pw",
	})
	require.Equal(t, http.StatusOK, status, string(raw))

	// A new host and the password not sent (the form), masked, or alone.
	status, raw = doReq(t, client, http.MethodPatch, base, map[string]any{"smtp.host": "attacker.example"})
	secretNeeded(t, status, raw, "smtp.password")
	status, raw = doReq(t, client, http.MethodPatch, base, map[string]any{"smtp.host": "attacker.example", "smtp.password": "***"})
	secretNeeded(t, status, raw, "smtp.password")
	status, raw = doReq(t, client, http.MethodPut, base+"/smtp.host", map[string]any{"value": "attacker.example"})
	secretNeeded(t, status, raw, "smtp.password")
	assert.Equal(t, "mail.example.com", stored("smtp.host"), "a refused save changed the host")
	assert.Equal(t, "saved-pw", stored("smtp.password"))

	// The same host however it is written, and another field: nothing asked.
	status, raw = doReq(t, client, http.MethodPatch, base, map[string]any{"smtp.host": "MAIL.example.com", "smtp.from": "x@example.com"})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Equal(t, "saved-pw", stored("smtp.password"))

	// Typed with the new host, it goes.
	status, raw = doReq(t, client, http.MethodPatch, base, map[string]any{"smtp.host": "relay.example.com", "smtp.password": "typed-pw"})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Equal(t, "relay.example.com", stored("smtp.host"))
	assert.Equal(t, "typed-pw", stored("smtp.password"))

	// Cleared with it, nothing is kept: nothing to ask for.
	status, raw = doReq(t, client, http.MethodPatch, base, map[string]any{"smtp.host": "open-relay.example.com", "smtp.password": ""})
	require.Equal(t, http.StatusOK, status, string(raw))
	status, raw = doReq(t, client, http.MethodPut, base+"/smtp.host", map[string]any{"value": "third.example.com"})
	require.Equal(t, http.StatusOK, status, string(raw))
}

func TestIdentityProviders_ANewAddressNeedsTheSecretTypedAgain(t *testing.T) {
	srv, client, _, _, _ := liveServer(t, nil)
	dir := newListener(t)
	ldap := "ldap://" + dir.addr
	status, out := patchProvider(t, client, srv.URL, "ldap", map[string]any{"enabled": false, "config": map[string]any{
		"url": "ldap://dir.example.invalid:389", "base_dn": "dc=example,dc=com", "bind_dn": "cn=svc,dc=example,dc=com", "bind_password": "saved-bind-pw",
	}})
	require.Equal(t, http.StatusOK, status, out)

	// Test with a new address and the password blank: refused before it connects.
	status, raw := doReq(t, client, http.MethodPost, srv.URL+"/api/admin/auth-providers/ldap/test",
		map[string]any{"config": map[string]any{"url": ldap, "bind_password": ""}})
	secretNeeded(t, status, raw, "bind_password")

	// Saved with a new address: blank, masked, or not sent at all. The save tests
	// what it is about to store, so a kept password would have gone out here.
	for name, cfg := range map[string]map[string]any{
		"blank":    {"url": ldap, "bind_password": ""},
		"masked":   {"url": ldap, "bind_password": "***"},
		"not sent": {"url": ldap},
	} {
		status, raw = doReq(t, client, http.MethodPatch, srv.URL+"/api/admin/auth-providers/ldap", map[string]any{"enabled": false, "config": cfg})
		secretNeeded(t, status, raw, "bind_password")
		_ = name
	}
	assert.False(t, dir.saw("saved-bind-pw"), "the saved bind password reached the new address")

	// Typed, it goes; and the address the same, another field asks for nothing.
	status, out = patchProvider(t, client, srv.URL, "ldap", map[string]any{"enabled": false, "config": map[string]any{"url": ldap, "bind_password": "typed-bind-pw"}})
	require.Equal(t, http.StatusOK, status, out)
	assert.True(t, dir.saw("typed-bind-pw"), "the save's test did not run: the listener proves nothing")
	status, out = patchProvider(t, client, srv.URL, "ldap", map[string]any{"enabled": false, "config": map[string]any{"base_dn": "dc=other", "bind_password": ""}})
	require.Equal(t, http.StatusOK, status, out)
	assert.False(t, dir.saw("saved-bind-pw"))
}

func TestIdentityProviders_ANewIssuerNeedsTheClientSecretTypedAgain(t *testing.T) {
	srv, client, _, _, _ := liveServer(t, nil)
	status, out := patchProvider(t, client, srv.URL, "oidc", map[string]any{"enabled": false, "config": map[string]any{
		"issuer": "https://idp.example.invalid", "client_id": "filex", "client_secret": "saved-client-secret",
	}})
	require.Equal(t, http.StatusOK, status, out)

	status, raw := doReq(t, client, http.MethodPost, srv.URL+"/api/admin/auth-providers/oidc/test",
		map[string]any{"config": map[string]any{"issuer": "https://attacker.example", "client_secret": ""}})
	secretNeeded(t, status, raw, "client_secret")
	status, raw = doReq(t, client, http.MethodPatch, srv.URL+"/api/admin/auth-providers/oidc",
		map[string]any{"enabled": false, "config": map[string]any{"issuer": "https://attacker.example", "client_secret": ""}})
	secretNeeded(t, status, raw, "client_secret")

	status, out = patchProvider(t, client, srv.URL, "oidc", map[string]any{"enabled": false, "config": map[string]any{"issuer": "https://idp2.example.invalid", "client_secret": "typed"}})
	require.Equal(t, http.StatusOK, status, out)
	status, out = patchProvider(t, client, srv.URL, "oidc", map[string]any{"enabled": false, "config": map[string]any{"scopes": "groups", "client_secret": ""}})
	require.Equal(t, http.StatusOK, status, out)
}

func TestTenantSelf_ANewAddressNeedsTheBindPasswordTypedAgain(t *testing.T) {
	srv, store, _, _, _ := selfServer(t)
	acmeAdmin := tenantClient(t, srv, "admin@acme.test")
	base := srv.URL + "/api/admin/tenant/auth-providers"
	status, raw := doReq(t, acmeAdmin, http.MethodPost, base, map[string]any{"driver": "ldap", "enabled": false, "config": map[string]any{
		"url": "ldaps://dir.acme.example", "base_dn": "dc=acme", "bind_dn": "cn=svc,dc=acme", "bind_password": "saved-tenant-pw",
	}})
	require.Equal(t, http.StatusCreated, status, string(raw))

	status, raw = doReq(t, acmeAdmin, http.MethodPost, base+"/acme-ldap/test", map[string]any{"config": map[string]any{"url": "ldaps://attacker.example", "bind_password": ""}})
	secretNeeded(t, status, raw, "bind_password")
	status, raw = doReq(t, acmeAdmin, http.MethodPatch, base+"/acme-ldap", map[string]any{"enabled": false, "config": map[string]any{"url": "ldaps://attacker.example", "bind_password": ""}})
	secretNeeded(t, status, raw, "bind_password")
	row, err := store.GetAuthInstanceBySlug(context.Background(), "acme-ldap")
	require.NoError(t, err)
	assert.Contains(t, row.ConfigJSON, "ldaps://dir.acme.example", "a refused save changed the address")

	status, raw = doReq(t, acmeAdmin, http.MethodPatch, base+"/acme-ldap", map[string]any{"enabled": false, "config": map[string]any{"url": "ldaps://dir2.acme.example", "bind_password": "typed"}})
	require.Equal(t, http.StatusOK, status, string(raw))
	status, raw = doReq(t, acmeAdmin, http.MethodPatch, base+"/acme-ldap", map[string]any{"enabled": false, "config": map[string]any{"base_dn": "dc=other", "bind_password": ""}})
	require.Equal(t, http.StatusOK, status, string(raw))
}

func TestTenantOIDC_ANewIssuerNeedsTheClientSecretTypedAgain(t *testing.T) {
	srv, client, store, _, _ := liveServer(t, nil)
	code, created := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/providers", map[string]any{
		"slug": "acme", "oidc_issuer": "https://id.acme.example", "oidc_client_id": "filex", "oidc_client_secret": "saved-tenant-secret",
	})
	require.Equal(t, http.StatusCreated, code, created)
	id := int64(created["id"].(float64))
	path := srv.URL + "/api/admin/providers/" + itoa64(id)
	secretOf := func() (string, string) {
		p, err := store.GetProvider(context.Background(), id)
		require.NoError(t, err)
		return p.OIDCIssuer, p.OIDCClientSecret
	}

	for _, body := range []map[string]any{
		{"oidc_issuer": "https://attacker.example", "oidc_client_secret": ""},
		{"oidc_issuer": "https://attacker.example"},
	} {
		status, raw := doReq(t, client, http.MethodPatch, path, body)
		secretNeeded(t, status, raw, "oidc_client_secret")
	}
	issuer, secret := secretOf()
	assert.Equal(t, "https://id.acme.example", issuer)
	assert.Equal(t, "saved-tenant-secret", secret)

	code, out := doJSON(t, client, http.MethodPatch, path, map[string]any{"oidc_issuer": "https://id2.acme.example", "oidc_client_secret": "typed"})
	require.Equal(t, http.StatusOK, code, out)
	code, out = doJSON(t, client, http.MethodPatch, path, map[string]any{"admin_group": "admins", "oidc_client_secret": ""})
	require.Equal(t, http.StatusOK, code, out)
	_, secret = secretOf()
	assert.Equal(t, "typed", secret)
}

// The page's own unit, without a server: Merge and DraftConfig hold every kept
// secret to its address.
func TestAuthsetup_AKeptSecretGoesNowhereNew(t *testing.T) {
	b, err := secretbox.New(liveTestKey)
	require.NoError(t, err)
	sealed, err := b.Seal("saved")
	require.NoError(t, err)
	cur := &authsetup.Stored{Name: "ldap", Exists: true, Values: map[string]string{"url": "ldaps://dir.example.com", "bind_password": sealed}}

	_, _, err = authsetup.Merge(cur, authsetup.Change{Values: map[string]any{"url": "ldaps://attacker.example"}}, b)
	assert.Error(t, err, "a new address, the password kept")
	_, err = authsetup.DraftConfig(cur, map[string]any{"url": "ldaps://attacker.example", "bind_password": "***"}, b, authsetup.Options{})
	assert.Error(t, err, "a Test at a new address, the password kept")

	next, _, err := authsetup.Merge(cur, authsetup.Change{Values: map[string]any{"url": "ldaps://new.example.com", "bind_password": "typed"}}, b)
	require.NoError(t, err)
	assert.NotEqual(t, sealed, next.Values["bind_password"])
	_, _, err = authsetup.Merge(cur, authsetup.Change{Values: map[string]any{"base_dn": "dc=other", "bind_password": ""}}, b)
	assert.NoError(t, err)
	_, err = authsetup.DraftConfig(cur, map[string]any{"url": "LDAPS://dir.example.com/"}, b, authsetup.Options{})
	assert.NoError(t, err, "the same address however it is written")

	empty := &authsetup.Stored{Name: "ldap", Values: map[string]string{}}
	_, _, err = authsetup.Merge(empty, authsetup.Change{Values: map[string]any{"url": "ldaps://dir.example.com"}}, b)
	assert.NoError(t, err, "no secret stored, nothing to keep")
}
