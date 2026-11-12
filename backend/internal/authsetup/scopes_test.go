package authsetup

import (
	"reflect"
	"testing"
)

// GitHub #137: the panel's Scopes box and FILEX_OIDC_EXTRA_SCOPES are read by
// one function, so a provider asks the IdP for the same scopes wherever it
// was set up.
func TestExtraScopes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"groups", []string{"groups"}},
		{"groups offline_access", []string{"groups", "offline_access"}},
		{"groups,offline_access", []string{"groups", "offline_access"}},
		{" groups ,  offline_access,, ", []string{"groups", "offline_access"}},
		{"openid profile email", nil},
		{"openid groups email profile", []string{"groups"}},
		{"groups groups,groups offline_access groups", []string{"groups", "offline_access"}},
		{"groups\toffline_access\nroles", []string{"groups", "offline_access", "roles"}},
	}
	for _, c := range cases {
		if got := ExtraScopes(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ExtraScopes(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

// The environment's provider shows its scopes on the Identity providers page
// the way the box takes them, not as a Go list.
func TestNewEnvEntry_ShowsTheScopesAsTheBoxWritesThem(t *testing.T) {
	e := NewEnvEntry("oidc", "FILEX_AUTH_DRIVERS", map[string]any{
		"issuer":        "https://idp.example.com",
		"client_id":     "filex",
		"client_secret": "not-shown",
		"scopes":        []string{"groups", "offline_access"},
	}, nil, nil, false)
	if got := e.Display["scopes"]; got != "groups offline_access" {
		t.Fatalf("Display[scopes] = %#v, want %q", got, "groups offline_access")
	}
	if _, shown := e.Display["client_secret"]; shown {
		t.Fatal("the client secret is never shown")
	}
}
