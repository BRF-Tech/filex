package config

import (
	"os"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Listen == "" {
		t.Fatal("default listen empty")
	}
	if cfg.DB.Driver == "" {
		t.Fatal("default db.driver empty")
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("FILEX_LISTEN", "127.0.0.1:9999")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:9999" {
		t.Fatalf("env override failed: %s", cfg.Listen)
	}
	_ = os.Setenv // keep env import path
}

// TestOIDCRedirectDefault — issuer + public URL are enough; the callback URL
// defaults automatically.
func TestOIDCRedirectDefault(t *testing.T) {
	t.Setenv("FILEX_PUBLIC_URL", "https://files.example.com")
	t.Setenv("FILEX_OIDC_ISSUER", "https://id.example.com/realms/main")
	t.Setenv("FILEX_OIDC_CLIENT_ID", "filex")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://files.example.com/api/auth/oidc/callback"
	if cfg.Auth.OIDC.RedirectURL != want {
		t.Fatalf("redirect default: got %q want %q", cfg.Auth.OIDC.RedirectURL, want)
	}
}

// TestSeedAndAuthEnv — the new seed + LDAP env vars land on the config.
func TestSeedAndAuthEnv(t *testing.T) {
	t.Setenv("FILEX_ADMIN_EMAIL", "boss@example.com")
	t.Setenv("FILEX_SMTP_HOST", "smtp.example.com")
	t.Setenv("FILEX_DEFAULT_STORAGE_DRIVER", "local")
	t.Setenv("FILEX_DEFAULT_STORAGE_PATH", "/srv/files")
	t.Setenv("FILEX_LDAP_URL", "ldaps://ldap.example.com")
	t.Setenv("FILEX_HEADER_EMAIL", "X-Auth-Email")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Seed.AdminEmail != "boss@example.com" {
		t.Fatalf("admin email: %q", cfg.Seed.AdminEmail)
	}
	if cfg.Seed.SMTP.Host != "smtp.example.com" {
		t.Fatalf("smtp host: %q", cfg.Seed.SMTP.Host)
	}
	if cfg.Seed.Storage.Driver != "local" || cfg.Seed.Storage.Path != "/srv/files" {
		t.Fatalf("storage seed: %+v", cfg.Seed.Storage)
	}
	if cfg.Auth.LDAP.URL != "ldaps://ldap.example.com" {
		t.Fatalf("ldap url: %q", cfg.Auth.LDAP.URL)
	}
	if cfg.Auth.Header.EmailHeader != "X-Auth-Email" {
		t.Fatalf("header email: %q", cfg.Auth.Header.EmailHeader)
	}
}
