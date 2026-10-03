package config

import (
	"strings"
	"testing"
)

// FILEX_TRUSTED_PROXIES is read from the environment and validated at start:
// a list that is silently ignored would leave an operator believing the proxy
// they named is the one whose X-Forwarded-For is believed.
func TestTrustedProxiesFromEnv(t *testing.T) {
	t.Setenv("FILEX_TRUSTED_PROXIES", "203.0.113.0/24, loopback, private, link-local")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrustedProxies != "203.0.113.0/24, loopback, private, link-local" {
		t.Fatalf("TrustedProxies = %q", cfg.TrustedProxies)
	}

	t.Setenv("FILEX_TRUSTED_PROXIES", "")
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrustedProxies != "" {
		t.Fatalf("unset must stay empty (the default: auto), got %q", cfg.TrustedProxies)
	}

	// `auto` is a word of the list like the classes.
	t.Setenv("FILEX_TRUSTED_PROXIES", "auto, 192.0.2.10")
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrustedProxies != "auto, 192.0.2.10" {
		t.Fatalf("TrustedProxies = %q", cfg.TrustedProxies)
	}
}

func TestTrustedProxiesBadEntryStopsTheServer(t *testing.T) {
	t.Setenv("FILEX_TRUSTED_PROXIES", "10.0.0.0/8, caddy")
	_, err := Load("")
	if err == nil {
		t.Fatal("a bad entry must be an error")
	}
	if !strings.Contains(err.Error(), "FILEX_TRUSTED_PROXIES") || !strings.Contains(err.Error(), "caddy") {
		t.Fatalf("the error must name the variable and the entry: %v", err)
	}
}
