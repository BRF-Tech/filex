package storage

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

// The one rule, as the SMTP settings, the identity providers and a tenant's
// OIDC call it.
func TestSecretMoved(t *testing.T) {
	prev := map[string]any{"url": "ldaps://dir.example.com/", "base_dn": "dc=example"}

	err := SecretMoved([]string{"bind_password"}, map[string]any{"url": "ldaps://attacker.example", "base_dn": "dc=example"}, prev)
	var named *SecretAddressError
	require.ErrorAs(t, err, &named, "a kept secret, a new address")
	assert.Equal(t, "bind_password", named.Field)
	assert.False(t, named.CheckOff)

	assert.NoError(t, SecretMoved(nil, map[string]any{"url": "ldaps://attacker.example"}, prev), "nothing kept: a typed secret goes anywhere")
	assert.NoError(t, SecretMoved([]string{"bind_password"}, map[string]any{"url": "LDAPS://dir.example.com", "base_dn": "dc=other"}, prev),
		"the same address however it is written, another field changed")

	err = SecretMoved([]string{"client_secret"}, map[string]any{"issuer": "https://attacker.example"}, map[string]any{"issuer": "https://idp.example.com"})
	require.ErrorAs(t, err, &named, "an OpenID issuer is an address")
	assert.Equal(t, "client_secret", named.Field)

	err = SecretMoved([]string{"z", "a"}, map[string]any{"host": "b"}, map[string]any{"host": "a"})
	require.ErrorAs(t, err, &named)
	assert.Equal(t, "a", named.Field, "the first in order is named, whatever order they came in")
}

// The owner, 2026-11-06: an OpenID redirect address is not where a secret
// goes, so changing it asks for nothing; the issuer still does.
func TestSecretMoved_ARedirectAddressAsksForNothing(t *testing.T) {
	kept := []string{"client_secret"}
	prev := map[string]any{"issuer": "https://idp.example.com", "redirect_url": "https://files.example.com/cb"}

	assert.NoError(t, SecretMoved(kept, map[string]any{"issuer": "https://idp.example.com", "redirect_url": "https://new.example.com/cb"}, prev))
	assert.NoError(t, SecretMoved([]string{"oidc_client_secret"},
		map[string]any{"oidc_issuer": "https://idp.example.com", "oidc_redirect_url": "https://new.example.com/cb"},
		map[string]any{"oidc_issuer": "https://idp.example.com", "oidc_redirect_url": "https://files.example.com/cb"}))
	assert.Error(t, SecretMoved(kept, map[string]any{"issuer": "https://attacker.example", "redirect_url": "https://files.example.com/cb"}, prev))
}

// The exemption list holds only keys the words would catch (otherwise the entry
// is dead and says nothing), and never one a secret is sent to.
func TestNotWhereSecretsGo(t *testing.T) {
	for k, why := range notWhereSecretsGo {
		assert.True(t, caughtByAddressWords(k), "%s is not caught by the address words: the entry is dead", k)
		assert.False(t, isAddressKey(k), k)
		assert.NotEmpty(t, why, "%s says why", k)
	}
	for _, k := range []string{"url", "issuer", "host", "endpoint", "oidc_issuer", "server", "address", "uri", "hostname"} {
		assert.True(t, isAddressKey(k), "%s is where a secret goes", k)
	}
}

// Each protection, both ways: weakened while a secret is kept, it asks;
// strengthened, kept as it is, or with nothing kept, it does not.
func TestSecretMoved_AWeakenedProtectionNeedsTheCredential(t *testing.T) {
	cases := []struct {
		key        string
		prev, next map[string]any
	}{
		{"insecure_skip_tls_verify", map[string]any{"url": "https://d"}, map[string]any{"url": "https://d", "insecure_skip_tls_verify": true}},
		{"insecure_skip_host_key", map[string]any{"host": "h"}, map[string]any{"host": "h", "insecure_skip_host_key": true}},
		{"tls", map[string]any{"host": "h", "tls": true}, map[string]any{"host": "h", "tls": false}},
		{"tls", map[string]any{"host": "h", "tls": true}, map[string]any{"host": "h"}},
		{"start_tls", map[string]any{"url": "ldap://dir", "start_tls": true}, map[string]any{"url": "ldap://dir", "start_tls": false}},
		{"ca_pem", map[string]any{"url": "ldaps://dir"}, map[string]any{"url": "ldaps://dir", "ca_pem": "-----BEGIN CERTIFICATE-----x"}},
		{"ca_pem", map[string]any{"url": "ldaps://dir", "ca_pem": "a"}, map[string]any{"url": "ldaps://dir", "ca_pem": "b"}},
		{"ca_file", map[string]any{"url": "ldaps://dir"}, map[string]any{"url": "ldaps://dir", "ca_file": "/etc/ca.pem"}},
		{"host_key", map[string]any{"host": "h", "host_key": "ssh-ed25519 AAAA1"}, map[string]any{"host": "h", "host_key": "ssh-ed25519 AAAA2"}},
		{"host_key", map[string]any{"host": "h", "host_key": "ssh-ed25519 AAAA1"}, map[string]any{"host": "h"}},
		{"known_hosts", map[string]any{"host": "h"}, map[string]any{"host": "h", "known_hosts": "/tmp/kh"}},
	}
	for _, c := range cases {
		err := SecretMoved([]string{"password"}, c.next, c.prev)
		var named *SecretAddressError
		require.ErrorAs(t, err, &named, "%s: %v -> %v", c.key, c.prev, c.next)
		assert.True(t, named.CheckOff, c.key)
		assert.Equal(t, c.key, named.Protection)
		assert.Equal(t, "password", named.Field)

		assert.NoError(t, SecretMoved(nil, c.next, c.prev), "%s: nothing kept, a typed secret goes", c.key)
		// A trust anchor changes both ways; taking one away counts as well.
		if !map[string]bool{"ca_pem": true, "ca_file": true, "host_key": true, "known_hosts": true}[c.key] {
			assert.NoError(t, SecretMoved([]string{"password"}, c.prev, c.next), "%s: the other way round strengthens it", c.key)
		}
		assert.NoError(t, SecretMoved([]string{"password"}, c.next, c.next), "%s: kept as it is", c.key)
	}

	// StartTLS means something only on a plain ldap:// address.
	assert.NoError(t, SecretMoved([]string{"bind_password"},
		map[string]any{"url": "ldaps://dir", "start_tls": false}, map[string]any{"url": "ldaps://dir", "start_tls": true}))
	// A trust anchor written the same way is the same anchor.
	assert.NoError(t, SecretMoved([]string{"password"}, map[string]any{"ca_file": " /etc/ca.pem "}, map[string]any{"ca_file": "/etc/ca.pem"}))
	// Only a bool turns a switch: that is what the drivers read.
	assert.NoError(t, SecretMoved([]string{"password"}, map[string]any{"insecure_skip_host_key": "true"}, map[string]any{}))
}

// The owner's call of 2026-11-06: the check going off is the same threat as a
// new address. Whoever may edit a storage but not read its password must not be
// able to hand that password to a server filex no longer checks.
func TestKeepSecrets_TurningTheCheckOffNeedsTheCredentialTypedAgain(t *testing.T) {
	off := json.RawMessage(`{"url":"https://dav.example.com/","user":"u","password":"real"}`)
	offSaid := json.RawMessage(`{"url":"https://dav.example.com/","user":"u","password":"real","insecure_skip_tls_verify":false}`)
	on := json.RawMessage(`{"url":"https://dav.example.com/","user":"u","password":"real","insecure_skip_tls_verify":true}`)
	const masked = `{"url":"https://dav.example.com/","user":"u","password":"***","insecure_skip_tls_verify":true}`

	for name, prev := range map[string]json.RawMessage{"never set": off, "said false": offSaid} {
		_, err := KeepSecrets("webdav", json.RawMessage(masked), "webdav", prev)
		require.Error(t, err, name)
		assert.ErrorIs(t, err, ErrSecretAddressChanged, name)
		var named *SecretAddressError
		require.ErrorAs(t, err, &named, name)
		assert.Equal(t, "password", named.Field, name)
		assert.True(t, named.CheckOff, "%s: the words have to say why, the address did not change", name)
	}

	typed := keep(t, "webdav", json.RawMessage(`{"url":"https://dav.example.com/","user":"u","password":"typed","insecure_skip_tls_verify":true}`), "webdav", off)
	assert.Equal(t, "typed", typed["password"], "typed by somebody who knows it, it goes")
	assert.Equal(t, true, typed["insecure_skip_tls_verify"])

	back := keep(t, "webdav", json.RawMessage(`{"url":"https://dav.example.com/","user":"u","password":"***","insecure_skip_tls_verify":false}`), "webdav", on)
	assert.Equal(t, "real", back["password"], "turning the check back on asks for nothing")

	dropped := keep(t, "webdav", json.RawMessage(`{"url":"https://dav.example.com/","user":"u","password":"***"}`), "webdav", on)
	assert.Equal(t, "real", dropped["password"], "nor does dropping the key")

	stays := keep(t, "webdav", json.RawMessage(`{"url":"https://dav.example.com/","user":"v","password":"***","insecure_skip_tls_verify":true}`), "webdav", on)
	assert.Equal(t, "real", stays["password"], "editing something else while it is off asks for nothing")
	assert.Equal(t, "v", stays["user"])

	stillChecked := keep(t, "webdav", json.RawMessage(`{"url":"https://dav.example.com/","user":"v","password":"***"}`), "webdav", off)
	assert.Equal(t, "real", stillChecked["password"], "and neither does an edit that leaves the check on")
}

// Every credential the save leaves masked is held to it, not the first alone.
func TestKeepSecrets_TheCheckGoingOffHoldsEveryMaskedCredential(t *testing.T) {
	prev := json.RawMessage(`{"endpoint":"https://s3.example.com","access_key":"AK","secret_key":"SK"}`)

	_, err := KeepSecrets("s3", json.RawMessage(`{"endpoint":"https://s3.example.com","access_key":"typed","secret_key":"***","insecure_skip_tls_verify":true}`), "s3", prev)

	var named *SecretAddressError
	require.ErrorAs(t, err, &named)
	assert.Equal(t, "secret_key", named.Field)
}

// Only what a driver reads turns the check off: a string does nothing there.
func TestKeepSecrets_OnlyABoolTurnsTheCheckOff(t *testing.T) {
	prev := json.RawMessage(`{"url":"https://dav.example.com/","password":"real"}`)

	got := keep(t, "webdav", json.RawMessage(`{"url":"https://dav.example.com/","password":"***","insecure_skip_tls_verify":"true"}`), "webdav", prev)

	assert.Equal(t, "real", got["password"])
}

// Nothing saved, nothing to protect: a mask on a create is dropped as before.
func TestKeepSecrets_ACreateWithTheCheckOffKeepsNoMask(t *testing.T) {
	got := keep(t, "webdav", json.RawMessage(`{"url":"https://dav.example.com/","password":"***","insecure_skip_tls_verify":true}`), "", nil)

	assert.NotContains(t, got, "password")
	assert.Equal(t, true, got["insecure_skip_tls_verify"])
}

func TestMaskSecrets_CredentialsAreNeverShown(t *testing.T) {
	cases := []struct {
		driver string
		cfg    string
		masked []string
		shown  map[string]any
	}{
		{"s3", `{"bucket":"b","endpoint":"https://s3","access_key":"AKIA","secret_key":"s3cr3t"}`,
			[]string{"access_key", "secret_key"}, map[string]any{"bucket": "b", "endpoint": "https://s3"}},
		{"smb", `{"host":"u1.your-storagebox.de","share":"backup","user":"u1","password":"pw"}`,
			[]string{"password"}, map[string]any{"host": "u1.your-storagebox.de", "user": "u1"}},
		{"sftp", `{"host":"h","user":"u","password":"pw","private_key":"-----BEGIN","key_path":"/k"}`,
			[]string{"password", "private_key"}, map[string]any{"key_path": "/k"}},
		{"plugin:mystery", `{"endpoint":"x","api_token":"t","client_secret":"c"}`,
			[]string{"api_token", "client_secret"}, map[string]any{"endpoint": "x"}},
	}
	for _, c := range cases {
		t.Run(c.driver, func(t *testing.T) {
			got := decode(t, MaskSecrets(c.driver, json.RawMessage(c.cfg)))
			for _, k := range c.masked {
				assert.Equal(t, SecretMask, got[k], "%s went out in clear", k)
			}
			for k, v := range c.shown {
				assert.Equal(t, v, got[k])
			}
		})
	}
	empty := decode(t, MaskSecrets("smb", json.RawMessage(`{"host":"h","password":""}`)))
	assert.Equal(t, "", empty["password"], "an empty secret says none is set; masked it would look set")
}

func keep(t *testing.T, driver string, next json.RawMessage, prevDriver string, prev json.RawMessage) map[string]any {
	t.Helper()
	out, err := KeepSecrets(driver, next, prevDriver, prev)
	require.NoError(t, err)
	return decode(t, out)
}

func TestKeepSecrets_TheMaskSentBackIsTheStoredValue(t *testing.T) {
	prev := json.RawMessage(`{"host":"h","password":"real","user":"u"}`)
	shown := MaskSecrets("smb", prev)
	edited := decode(t, shown)
	edited["user"] = "u2"
	back, _ := json.Marshal(edited)

	got := keep(t, "smb", back, "smb", prev)
	assert.Equal(t, "real", got["password"], "saving what the page showed replaced the password with the mask")
	assert.Equal(t, "u2", got["user"])

	changed := keep(t, "smb", json.RawMessage(`{"host":"h","password":"new"}`), "smb", prev)
	assert.Equal(t, "new", changed["password"], "a new password was ignored")

	fresh := keep(t, "smb", json.RawMessage(`{"host":"h","password":"***"}`), "", nil)
	_, has := fresh["password"]
	assert.False(t, has, "a mask with nothing behind it was stored as the password")
}

// A kept credential goes only where it was saved. Otherwise anybody who may
// edit a storage but not read its password - an admin API key, an MCP agent -
// could point it at a server of their own and have filex send the password.
func TestKeepSecrets_AKeptCredentialStaysAtItsAddress(t *testing.T) {
	prev := json.RawMessage(`{"host":"files.example.com","endpoint":"https://s3.example.com/","password":"real"}`)
	for name, next := range map[string]string{
		"another host":     `{"host":"attacker.example","endpoint":"https://s3.example.com","password":"***"}`,
		"another endpoint": `{"host":"files.example.com","endpoint":"https://attacker.example","password":"***"}`,
		"the host dropped": `{"endpoint":"https://s3.example.com","password":"***"}`,
	} {
		_, err := KeepSecrets("smb", json.RawMessage(next), "smb", prev)
		require.Error(t, err, name)
		assert.ErrorIs(t, err, ErrSecretAddressChanged, name)
		var named *SecretAddressError
		require.ErrorAs(t, err, &named)
		assert.Equal(t, "password", named.Field)
	}
	_, err := KeepSecrets("sftp", json.RawMessage(`{"host":"files.example.com","endpoint":"https://s3.example.com","password":"***"}`), "smb", prev)
	assert.ErrorIs(t, err, ErrSecretAddressChanged, "another driver is another address")

	// The same address however it is written (case, a trailing slash) is
	// the same; and a new password typed in goes anywhere.
	same := keep(t, "smb", json.RawMessage(`{"host":"FILES.example.com","endpoint":"https://s3.example.com","password":"***"}`), "smb", prev)
	assert.Equal(t, "real", same["password"])
	typed := keep(t, "smb", json.RawMessage(`{"host":"elsewhere.example","password":"typed"}`), "smb", prev)
	assert.Equal(t, "typed", typed["password"])
}
