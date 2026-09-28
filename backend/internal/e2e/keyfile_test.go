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
