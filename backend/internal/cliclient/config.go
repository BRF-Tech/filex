package cliclient

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileConfig is the on-disk shape of ~/.filex/cli.yaml. It carries a
// bearer token, so SaveFileConfig always writes it owner-only (0600).
type FileConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
}

// DefaultConfigPath returns the CLI config location: $FILEX_CLI_CONFIG
// when set (tests / unusual homes), otherwise ~/.filex/cli.yaml.
func DefaultConfigPath() (string, error) {
	if p := os.Getenv("FILEX_CLI_CONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".filex", "cli.yaml"), nil
}

// LoadFileConfig reads the CLI config. A missing file is not an error —
// it just yields the zero config so flags/env can still win.
func LoadFileConfig(path string) (FileConfig, error) {
	var cfg FileConfig
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// SaveFileConfig writes the CLI config with owner-only permissions. The
// explicit Chmod after the write covers a pre-existing file whose looser
// mode WriteFile would otherwise keep.
func SaveFileConfig(path string, cfg FileConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// Conn is the resolved server coordinate a client command runs against.
type Conn struct {
	URL   string
	Token string
}

// Resolve merges connection settings by precedence:
//
//	--url/--token flags  >  FILEX_URL/FILEX_TOKEN env  >  config file.
//
// getenv is injected so tests don't have to mutate the process env.
func Resolve(flagURL, flagToken, cfgPath string, getenv func(string) string) (Conn, error) {
	cfg, err := LoadFileConfig(cfgPath)
	if err != nil {
		return Conn{}, err
	}
	conn := Conn{
		URL:   firstNonEmpty(flagURL, getenv("FILEX_URL"), cfg.URL),
		Token: firstNonEmpty(flagToken, getenv("FILEX_TOKEN"), cfg.Token),
	}
	conn.URL = strings.TrimRight(strings.TrimSpace(conn.URL), "/")
	conn.Token = strings.TrimSpace(conn.Token)
	return conn, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
