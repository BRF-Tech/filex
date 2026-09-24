package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The Identity providers page shows, beside every provider the environment
// defines, WHERE it is defined — the one place an operator can change it.
func TestLoad_SaysWhereTheSignInProvidersComeFrom(t *testing.T) {
	t.Setenv("FILEX_AUTH_DRIVERS", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.DriversFrom != "default" {
		t.Errorf("default: %q", cfg.Auth.DriversFrom)
	}

	path := filepath.Join(t.TempDir(), "filex.yaml")
	if err := os.WriteFile(path, []byte("auth:\n  drivers: [local, ldap]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "file:" + path; cfg.Auth.DriversFrom != want {
		t.Errorf("file: %q, want %q", cfg.Auth.DriversFrom, want)
	}

	t.Setenv("FILEX_AUTH_DRIVERS", "local,oidc")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.DriversFrom != "FILEX_AUTH_DRIVERS" {
		t.Errorf("env over file: %q", cfg.Auth.DriversFrom)
	}
}
