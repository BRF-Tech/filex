package cliclient

import (
	"errors"
	"fmt"
	"net/url"
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
	// Realm is the tenant realm the session was signed in to, on a
	// multi-tenant server; it belongs to URL and is used again only by a
	// login at that address (Resolve). Omitted when empty, so a file without
	// one reads the same to an older CLI.
	Realm string `yaml:"realm,omitempty"`
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
	// Realm is the realm saved with URL in the config file ("" when the
	// resolved URL is another server's, or none was saved).
	Realm string
	// SavedURL is set when the config file holds a session that was NOT used
	// because it belongs to another address (this one): the caller says so
	// instead of a bare "no token".
	SavedURL string
}

// Resolve merges connection settings by precedence:
//
//	--url/--token flags  >  FILEX_URL/FILEX_TOKEN env  >  config file.
//
// ⚠ What the config file holds belongs to the address it was saved with - the
// session token above all: a --url or FILEX_URL that names another address
// gets neither the saved token nor the saved realm (sameServer). Sending it
// there would hand one server's session to another. A token given on purpose
// (--token, FILEX_TOKEN) goes to whichever address it is given with.
//
// getenv is injected so tests don't have to mutate the process env.
func Resolve(flagURL, flagToken, cfgPath string, getenv func(string) string) (Conn, error) {
	cfg, err := LoadFileConfig(cfgPath)
	if err != nil {
		return Conn{}, err
	}
	conn := Conn{
		URL:   firstNonEmpty(flagURL, getenv("FILEX_URL"), cfg.URL),
		Token: firstNonEmpty(flagToken, getenv("FILEX_TOKEN")),
	}
	conn.URL = strings.TrimRight(strings.TrimSpace(conn.URL), "/")
	conn.Token = strings.TrimSpace(conn.Token)
	home := sameServer(conn.URL, cfg.URL)
	if saved := strings.TrimSpace(cfg.Token); conn.Token == "" && saved != "" {
		if home {
			conn.Token = saved
		} else {
			conn.SavedURL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
		}
	}
	if realm := strings.TrimSpace(cfg.Realm); realm != "" && home {
		conn.Realm = realm
	}
	return conn, nil
}

// sameServer reports whether two server URLs name the same address: the same
// scheme, the same host (case and a final dot aside), the same port (the
// scheme's default one written or not) and the same base path (a trailing
// slash aside). Another name for the same machine - its IP, a short name - is
// another address: nothing here can prove it is the same server.
func sameServer(a, b string) bool {
	ka, okA := serverKey(a)
	kb, okB := serverKey(b)
	return okA && okB && ka == kb
}

func serverKey(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if host == "" {
		return "", false
	}
	return scheme + "://" + host + " " + port + " " + strings.TrimRight(u.EscapedPath(), "/"), true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
