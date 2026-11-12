package config

import (
	"os"
	"path/filepath"
	"testing"
)

// GitHub #137: the provider the environment defines can ask for scopes beyond
// openid, profile and email - FILEX_OIDC_EXTRA_SCOPES, its FILEX_AUTH_ twin
// like every other OIDC setting, or auth.oidc.extra_scopes in the config file.
// The value is kept as written; authsetup.ExtraScopes reads it like the
// panel's Scopes box.
func TestOIDCExtraScopes_EnvAndAlias(t *testing.T) {
	cases := []struct {
		env, alias, want string
	}{
		{"", "", ""},
		{"groups", "", "groups"},
		{"groups, offline_access", "", "groups, offline_access"},
		{"", "groups", "groups"},
		{"groups", "roles", "groups"},
	}
	for _, c := range cases {
		t.Setenv("FILEX_OIDC_EXTRA_SCOPES", c.env)
		t.Setenv("FILEX_AUTH_OIDC_EXTRA_SCOPES", c.alias)
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.Auth.OIDC.ExtraScopes; got != c.want {
			t.Errorf("FILEX_OIDC_EXTRA_SCOPES=%q FILEX_AUTH_OIDC_EXTRA_SCOPES=%q: ExtraScopes %q, want %q", c.env, c.alias, got, c.want)
		}
	}
}

func TestOIDCExtraScopes_ConfigFileAndTheEnvironmentOverIt(t *testing.T) {
	t.Setenv("FILEX_OIDC_EXTRA_SCOPES", "")
	t.Setenv("FILEX_AUTH_OIDC_EXTRA_SCOPES", "")
	path := filepath.Join(t.TempDir(), "filex.yaml")
	if err := os.WriteFile(path, []byte("auth:\n  oidc:\n    extra_scopes: \"groups offline_access\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Auth.OIDC.ExtraScopes; got != "groups offline_access" {
		t.Fatalf("auth.oidc.extra_scopes: %q", got)
	}
	t.Setenv("FILEX_OIDC_EXTRA_SCOPES", "roles")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Auth.OIDC.ExtraScopes; got != "roles" {
		t.Fatalf("the environment wins over the file, like every other setting: %q", got)
	}
}
