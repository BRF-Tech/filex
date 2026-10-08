package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const kfBase = `{"v":2,"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="}}`

func TestDiffKeyFiles(t *testing.T) {
	cases := []struct {
		name    string
		before  string
		after   string
		changes []string
		slot    bool
		valid   bool
	}{
		{"a password change", kfBase, `{"v":2,"salt":"bmV3","iter":600000,"verify":"bmV3dg==","fmk":"wrapped","fmk_pw":"bmV3cA==","rk":{"salt":"cms=","blob":"YmxvYg=="}}`, []string{"password"}, true, true},
		{"a re-key: password, recovery key and folder key", kfBase, `{"v":3,"req":["rekey"],"salt":"bmV3","iter":600000,"verify":"bmV3dg==","fmk":"wrapped","fmk_pw":"bmV3cA==","rk":{"salt":"bmV3","blob":"bmV3"},"rekey":{"from":"b2xk","pending":true}}`, []string{"password", "recovery_key", "rekey"}, true, true},
		{"level 1 to level 2: no key slot", kfBase, `{"v":3,"req":["names"],"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="},"names":{"alg":"AES-SIV-512"}}`, []string{"level"}, false, true},
		{"an escrow slot added", kfBase, `{"v":2,"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="},"esc":{"kid":"k"}}`, []string{"escrow"}, false, true},
		{"the same bytes, re-spaced", kfBase, "{\n \"v\": 2, \"salt\": \"c2FsdA==\", \"iter\": 600000, \"verify\": \"dmVy\", \"fmk\": \"wrapped\", \"fmk_pw\": \"cHc=\", \"rk\": {\"salt\": \"cms=\", \"blob\": \"YmxvYg==\"}}", nil, false, true},
		{"a field this server does not know", kfBase, `{"v":2,"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="},"future":1}`, []string{"other"}, false, true},
		{"a conversion begins", kfBase, `{"v":3,"req":["conv"],"conv":{"pending":true},"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="}}`, []string{"conversion"}, false, true},
		{"nothing known before it", "", kfBase, []string{"created"}, false, true},
		{"not a key file", kfBase, `not json`, nil, false, false},
		{"JSON, but no salt", kfBase, `{"v":2}`, nil, false, false},
	}
	for _, c := range cases {
		var before []byte
		if c.before != "" {
			before = []byte(c.before)
		}
		d := DiffKeyFiles(before, []byte(c.after))
		require.Equal(t, c.valid, d.Valid, c.name)
		require.Equal(t, c.changes, d.Changes, c.name)
		require.Equal(t, c.slot, d.KeySlotChanged, c.name)
	}
}

func TestConversionPending(t *testing.T) {
	require.True(t, ConversionPending([]byte(`{"v":3,"req":["conv"],"conv":{"pending":true}}`)))
	require.True(t, ConversionPending([]byte(`{"v":3,"req":["names","conv"],"conv":{"pending":true}}`)))
	require.False(t, ConversionPending([]byte(`{"v":3,"req":["names"],"conv":{"pending":true}}`)), "not required: not a conversion")
	require.False(t, ConversionPending([]byte(`{"v":3,"req":["conv"]}`)))
	require.False(t, ConversionPending([]byte(`{"v":2,"conv":{"pending":true}}`)))
	require.False(t, ConversionPending([]byte(`not json`)))
	require.False(t, ConversionPending(nil))
}

func TestKeepsVaultBlock(t *testing.T) {
	vault := `{"v":3,"req":["vault"],"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","vault":{"v":1,"id":"wpU155hSR3hD1u8bSnGv7w","pack":22}}`
	newPassword := `{"v":3,"req":["vault"],"salt":"bmV3","iter":600000,"verify":"bmV3dg==","fmk":"wrapped","fmk_pw":"bmV3cA==","vault":{"v":1,"id":"wpU155hSR3hD1u8bSnGv7w","pack":22}}`
	otherPack := `{"v":3,"req":["vault"],"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","vault":{"v":1,"id":"wpU155hSR3hD1u8bSnGv7w","pack":24}}`
	level2 := `{"v":3,"req":["names"],"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","names":{"alg":"AES-SIV-512"}}`
	cases := []struct {
		name          string
		before, after []byte
		want          bool
	}{
		{"a vault's new password", []byte(vault), []byte(newPassword), true},
		{"a vault's pack size changed", []byte(vault), []byte(otherPack), false},
		{"a vault turned into level 2", []byte(vault), []byte(level2), false},
		{"a vault's key file replaced by junk", []byte(vault), []byte("{}"), false},
		{"level 2 turned into a vault", []byte(level2), []byte(vault), false},
		{"level 1 turned into a vault", []byte(kfBase), []byte(vault), false},
		{"a vault key file where there was none", nil, []byte(vault), false},
		{"a level-1 key file where there was none", nil, []byte(kfBase), true},
		{"a level-1 password change", []byte(kfBase), []byte(`{"v":2,"salt":"bmV3","iter":600000,"verify":"bmV3dg==","fmk":"wrapped","fmk_pw":"bmV3cA=="}`), true},
		{"level 1 to level 2", []byte(kfBase), []byte(level2), true},
	}
	for _, c := range cases {
		if got := KeepsVaultBlock(c.before, c.after); got != c.want {
			t.Errorf("%s: KeepsVaultBlock = %v, want %v", c.name, got, c.want)
		}
	}
}
