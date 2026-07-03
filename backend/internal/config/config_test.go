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
