package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadWith loads the config with exactly these two variables in effect (an
// empty value is "unset"), so a FILEX_* left in the developer's shell cannot
// decide the outcome.
func loadWith(t *testing.T, publicURL, basePath string) (Config, error) {
	t.Helper()
	t.Setenv("FILEX_PUBLIC_URL", publicURL)
	t.Setenv("FILEX_BASE_PATH", basePath)
	t.Setenv("FILEX_OIDC_ISSUER", "")
	t.Setenv("FILEX_AUTH_OIDC_ISSUER", "")
	return Load("")
}

// The default is the root, exactly as before the setting existed: no base,
// and the public URL untouched.
func TestBasePathDefaultsToTheRoot(t *testing.T) {
	cfg, err := loadWith(t, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BasePath != "" || cfg.BasePathFrom != "" {
		t.Fatalf("default base: got %q from %q, want the root", cfg.BasePath, cfg.BasePathFrom)
	}
	if cfg.PublicURL != DefaultPublicURL || cfg.PublicURLSet {
		t.Fatalf("default public URL changed: %q (set=%v)", cfg.PublicURL, cfg.PublicURLSet)
	}

	cfg, err = loadWith(t, "https://files.example.com/", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BasePath != "" || cfg.PublicURL != "https://files.example.com/" {
		t.Fatalf("a root public URL must stay a root deployment: base %q, url %q", cfg.BasePath, cfg.PublicURL)
	}
}

// The public URL's path IS the base when nothing else says so — one setting.
func TestBasePathFromPublicURL(t *testing.T) {
	for _, u := range []string{"https://example.com/filex", "https://example.com/filex/"} {
		cfg, err := loadWith(t, u, "")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BasePath != "/filex" || cfg.BasePathFrom != "FILEX_PUBLIC_URL" {
			t.Fatalf("%s: got base %q from %q", u, cfg.BasePath, cfg.BasePathFrom)
		}
	}
}

// FILEX_BASE_PATH alone puts the base on the public URL, so every link built
// from it carries the prefix — and the default OIDC callback lives under it.
func TestBasePathExplicit(t *testing.T) {
	t.Setenv("FILEX_OIDC_CLIENT_ID", "filex")
	t.Setenv("FILEX_PUBLIC_URL", "https://example.com")
	t.Setenv("FILEX_BASE_PATH", "/filex")
	t.Setenv("FILEX_OIDC_ISSUER", "https://id.example.com/realms/main")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BasePath != "/filex" || cfg.BasePathFrom != "FILEX_BASE_PATH" {
		t.Fatalf("got base %q from %q", cfg.BasePath, cfg.BasePathFrom)
	}
	if cfg.PublicURL != "https://example.com/filex" {
		t.Fatalf("public URL must carry the base: %q", cfg.PublicURL)
	}
	if want := "https://example.com/filex/api/auth/oidc/callback"; cfg.Auth.OIDC.RedirectURL != want {
		t.Fatalf("OIDC callback: got %q want %q", cfg.Auth.OIDC.RedirectURL, want)
	}

	// Both saying the same thing is fine, and does not double the prefix.
	cfg, err = loadWith(t, "https://example.com/filex/", "/filex")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BasePath != "/filex" || cfg.PublicURL != "https://example.com/filex/" {
		t.Fatalf("agreeing settings: base %q, url %q", cfg.BasePath, cfg.PublicURL)
	}

	// The default address gets the base too, but stays a GUESS.
	cfg, err = loadWith(t, "", "/filex")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != DefaultPublicURL+"/filex" || cfg.PublicURLSet {
		t.Fatalf("default URL under a base: %q (set=%v)", cfg.PublicURL, cfg.PublicURLSet)
	}

	// "/" is the root, said out loud.
	cfg, err = loadWith(t, "https://example.com", "/")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BasePath != "" || cfg.BasePathFrom != "" || cfg.PublicURL != "https://example.com" {
		t.Fatalf(`"/" must be the root: base %q from %q, url %q`, cfg.BasePath, cfg.BasePathFrom, cfg.PublicURL)
	}
}

// A bad value stops the server at startup with a message that says what to
// write instead — never a half-working prefix.
func TestBasePathRefusesBadValues(t *testing.T) {
	cases := []struct {
		url, base, want string
	}{
		{"", "filex", "must start with a slash"},
		{"", "/filex/", `use "/filex"`},
		{"", "/../filex", `".." segment`},
		{"", "/a//b", "empty segment"},
		{"", "/fi lex", "may only use"},
		{"https://example.com/files", "/filex", "make the two agree"},
		{"https://example.com/filex", "/", "make the two agree"},
		{"https://example.com/fi%20lex", "", "percent-encoded"},
		{"https://example.com/a/../b", "", `".." segment`},
	}
	for _, c := range cases {
		_, err := loadWith(t, c.url, c.base)
		if err == nil {
			t.Fatalf("url %q base %q: loaded, want a refusal", c.url, c.base)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("url %q base %q: %v, want it to say %q", c.url, c.base, err, c.want)
		}
	}
}

// `base_path:` in the config file works like the variable, and the variable
// wins over the file.
func TestBasePathFromTheConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("public_url: https://example.com\nbase_path: /files\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FILEX_PUBLIC_URL", "")
	t.Setenv("FILEX_BASE_PATH", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BasePath != "/files" || cfg.BasePathFrom != "base_path" || cfg.PublicURL != "https://example.com/files" {
		t.Fatalf("from the file: base %q from %q, url %q", cfg.BasePath, cfg.BasePathFrom, cfg.PublicURL)
	}

	t.Setenv("FILEX_BASE_PATH", "/filex")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BasePath != "/filex" || cfg.BasePathFrom != "FILEX_BASE_PATH" {
		t.Fatalf("the variable must win: base %q from %q", cfg.BasePath, cfg.BasePathFrom)
	}
}
