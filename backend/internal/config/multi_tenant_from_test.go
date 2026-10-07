package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Admin → Multi-tenant mode is locked when the environment or the config file
// names the mode, either way, and says which (#167). Before 0.53 an unset
// variable and FILEX_MULTI_TENANT=0 were the same thing, and nothing recorded
// where the mode came from.
func TestLoad_SaysWhatPinsTheMultiTenantMode(t *testing.T) {
	t.Setenv("FILEX_MULTI_TENANT", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MultiTenant || cfg.MultiTenantFrom != "" {
		t.Errorf("nothing set: on=%v from=%q, want off and unpinned (the switch decides)", cfg.MultiTenant, cfg.MultiTenantFrom)
	}

	for _, tc := range []struct {
		value string
		on    bool
	}{
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"0", false},
		{"false", false},
		{"no", false},
	} {
		t.Setenv("FILEX_MULTI_TENANT", tc.value)
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MultiTenant != tc.on || cfg.MultiTenantFrom != "FILEX_MULTI_TENANT" {
			t.Errorf("FILEX_MULTI_TENANT=%q: on=%v from=%q, want on=%v pinned by the variable", tc.value, cfg.MultiTenant, cfg.MultiTenantFrom, tc.on)
		}
	}

	t.Setenv("FILEX_MULTI_TENANT", "")
	dir := t.TempDir()

	// A file that says false pins it off; one that does not mention it pins nothing.
	off := filepath.Join(dir, "off.yaml")
	if err := os.WriteFile(off, []byte("multi_tenant: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(off)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MultiTenant || cfg.MultiTenantFrom != "file:"+off {
		t.Errorf("file says false: on=%v from=%q, want off pinned by the file", cfg.MultiTenant, cfg.MultiTenantFrom)
	}

	silent := filepath.Join(dir, "silent.yaml")
	if err := os.WriteFile(silent, []byte("default_locale: tr\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(silent)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MultiTenantFrom != "" {
		t.Errorf("file silent about the mode: from=%q, want unpinned", cfg.MultiTenantFrom)
	}

	// The variable wins over the file.
	on := filepath.Join(dir, "on.yaml")
	if err := os.WriteFile(on, []byte("multi_tenant: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FILEX_MULTI_TENANT", "0")
	cfg, err = Load(on)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MultiTenant || cfg.MultiTenantFrom != "FILEX_MULTI_TENANT" {
		t.Errorf("variable over file: on=%v from=%q, want off pinned by the variable", cfg.MultiTenant, cfg.MultiTenantFrom)
	}
}
