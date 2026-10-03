package handlers

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// What the demo masking calls an address - and what it leaves alone.
func TestMaskAddressesInText(t *testing.T) {
	const m = demoMaskedIP
	for in, want := range map[string]string{
		"203.0.113.7":                    m,
		"203.0.113.7:51234":              m,
		"2001:db8::45":                   m,
		"[2001:db8::45]:443":             m,
		"fe80::1%eth0":                   m,
		"fe80::":                         m,
		"::1":                            m,
		"198.51.100.0/24":                m,
		"192.0.2.77, 198.51.100.0/24":    m + ", " + m,
		"loopback, private, 10.9.8.7":    "loopback, private, " + m,
		"connect to 10.0.0.1: refused":   "connect to " + m + ": refused",
		"(10.0.0.1)":                     "(" + m + ")",
		"ada@example.com":                "ada@example.com",
		"203.0.113.7@example.com":        "203.0.113.7@example.com",
		"login.unlocked":                 "login.unlocked",
		"2026-10-01T10:00:00Z":           "2026-10-01T10:00:00Z",
		"account":                        "account",
		"":                               "",
		"http://203.0.113.7:8080/x":      "http://203.0.113.7:8080/x",
		"ip_allowlist, trusted_proxies":  "ip_allowlist, trusted_proxies",
		"203.0.113.7\n198.51.100.9;none": m + "\n" + m + ";none",
	} {
		require.Equal(t, want, maskAddressesInText(in), "%q", in)
	}
}

func TestMaskAddressesInValueCopies(t *testing.T) {
	in := map[string]any{
		"subject": "203.0.113.7",
		"nested":  map[string]any{"list": []any{"203.0.113.8", 5, "ok"}},
		"fields":  []string{"ip_allowlist"},
		"n":       3.0,
	}
	out := maskAddressesInValue(in).(map[string]any)
	require.Equal(t, demoMaskedIP, out["subject"])
	require.Equal(t, []any{demoMaskedIP, 5, "ok"}, out["nested"].(map[string]any)["list"])
	require.Equal(t, []string{"ip_allowlist"}, out["fields"])
	require.Equal(t, 3.0, out["n"])
	require.Equal(t, "203.0.113.7", in["subject"], "the original is never changed")
}

func TestMaskAddressList(t *testing.T) {
	require.Nil(t, maskAddressList(nil))
	in := []string{"loopback", "10.1.0.0/16", "203.0.113.7", "none"}
	out := maskAddressList(in)
	require.Equal(t, []string{"loopback", demoMaskedIP, demoMaskedIP, "none"}, out)
	require.Equal(t, "10.1.0.0/16", in[1], "a new list")
}

// A typed name is an account's - and stays readable on a demo - when it is
// one of the instance's e-mails or usernames, however it was typed: any case,
// with a realm in front, with an operating system's DOMAIN\ prefix.
func TestDemoNamesKnowTheInstancesAccounts(t *testing.T) {
	n := demoNames{"known@example.com": {}, "known": {}}
	for typed, shown := range map[string]bool{
		"known@example.com":           true,
		"Known@Example.com":           true,
		"known":                       true,
		"acme/known@example.com":      true,
		`CORP\known`:                  true,
		"stranger@example.org":        false,
		"acme/stranger@example.org":   false,
		"known@example.com.evil.test": false,
		"":                            true,
		demoMaskedIP:                  true,
	} {
		want := typed
		if !shown {
			want = demoMaskedIP
		}
		require.Equal(t, want, n.mask(typed), "%q", typed)
	}
	require.Equal(t, demoMaskedIP, demoNames{}.mask("anybody"), "no account list: every typed name is hidden")
}
