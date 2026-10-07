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
