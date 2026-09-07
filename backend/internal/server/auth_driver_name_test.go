package server

import "testing"

// The spelling of an auth driver name is not cosmetic: an entry the loader
// does not recognise produces one `slog.Warn("unknown auth driver")` line and
// then nothing — the driver is simply absent, and an operator who followed the
// documentation has reverse-proxy SSO that looks configured and is not.
//
// Four places tell people to write `proxy_header` with an underscore
// (docs/CONFIGURATION.md twice, docs/ARCHITECTURE.md, the Helm chart's
// values.yaml), and only docs/LDAP.md used the hyphen the loader wanted. This
// pins the underscore spelling as valid so those documents stay true.
func TestNormalizeDriverName(t *testing.T) {
	cases := map[string]string{
		"proxy_header": "proxy-header", // what every doc but one says
		"proxy-header": "proxy-header",
		"PROXY_HEADER": "proxy-header",
		"  ldap  ":     "ldap",
		"OIDC":         "oidc",
		"local":        "local",
		"api_token":    "api-token",
	}
	for in, want := range cases {
		if got := normalizeDriverName(in); got != want {
			t.Errorf("normalizeDriverName(%q) = %q, want %q", in, got, want)
		}
	}
}
