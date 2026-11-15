package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/watchproto"
)

// Config is everything the agent is told. Three sources, each over the one
// before: the JSON file (--config, FILEX_WATCH_CONFIG), the environment
// (FILEX_WATCH_*), the flags. The names are the same everywhere: a field
// `key_file`, a variable FILEX_WATCH_KEY_FILE, a flag --key-file.
//
// The certificate switch keeps the names filex already uses for the same
// thing: a storage's `insecure_skip_tls_verify`, the CLI's
// --insecure-skip-tls-verify, and FILEX_STORAGE_TLS_SKIP_VERIFY's shape for
// the variable (FILEX_WATCH_TLS_SKIP_VERIFY).
type Config struct {
	Server                string   `json:"server"`
	Storage               int64    `json:"storage"`
	Root                  string   `json:"root"`
	KeyFile               string   `json:"key_file"`
	InsecureSkipTLSVerify bool     `json:"insecure_skip_tls_verify"`
	CAFile                string   `json:"ca_file"`
	LogFile               string   `json:"log_file"`
	Quiet                 Duration `json:"quiet"`
	MaxWait               Duration `json:"max_wait"`
	Heartbeat             Duration `json:"heartbeat"`
	SubtreeResync         Duration `json:"subtree_resync"`
	QueueMax              int      `json:"queue_max"`
	Verbose               bool     `json:"verbose"`

	// key is the key itself: FILEX_WATCH_KEY, else the key file's content.
	// Never printed, never logged.
	key string
}

// Duration is a time.Duration read as "1s" / "15m" or as a number of seconds.
type Duration time.Duration

// UnmarshalJSON reads "1s", "1m30s" or 90.
func (d *Duration) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		v, err := parseDuration(s)
		if err != nil {
			return err
		}
		*d = Duration(v)
		return nil
	}
	var secs float64
	if err := json.Unmarshal(b, &secs); err != nil {
		return fmt.Errorf("a duration is \"1s\", \"15m\" or a number of seconds: %w", err)
	}
	*d = Duration(time.Duration(secs * float64(time.Second)))
	return nil
}

// parseDuration reads a Go duration, or a bare number of seconds.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(n * float64(time.Second)), nil
	}
	return time.ParseDuration(s)
}

// defaults are what a setting is when nothing names it.
func defaults() Config {
	return Config{
		Quiet:         Duration(time.Second),
		MaxWait:       Duration(10 * time.Second),
		Heartbeat:     Duration(time.Minute),
		SubtreeResync: Duration(15 * time.Minute),
		QueueMax:      4096,
	}
}

// errHelp is -h: the usage was printed, nothing is wrong.
var errHelp = errors.New("help")

// setting is one setting's three names and how to apply a value to it.
type setting struct {
	flag, env string
	help      string
	isBool    bool
	set       func(c *Config, v string) error
}

func settings() []setting {
	str := func(p func(c *Config) *string) func(*Config, string) error {
		return func(c *Config, v string) error { *p(c) = strings.TrimSpace(v); return nil }
	}
	dur := func(p func(c *Config) *Duration) func(*Config, string) error {
		return func(c *Config, v string) error {
			d, err := parseDuration(v)
			if err != nil {
				return err
			}
			*p(c) = Duration(d)
			return nil
		}
	}
	boolean := func(p func(c *Config) *bool) func(*Config, string) error {
		return func(c *Config, v string) error {
			b, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				return err
			}
			*p(c) = b
			return nil
		}
	}
	return []setting{
		{flag: "server", env: "FILEX_WATCH_SERVER", help: "filex's address", set: str(func(c *Config) *string { return &c.Server })},
		{flag: "storage", env: "FILEX_WATCH_STORAGE", help: "the storage's number", set: func(c *Config, v string) error {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return err
			}
			c.Storage = n
			return nil
		}},
		{flag: "root", env: "FILEX_WATCH_ROOT", help: "the folder to watch", set: str(func(c *Config) *string { return &c.Root })},
		{flag: "key-file", env: "FILEX_WATCH_KEY_FILE", help: "the file holding the key", set: str(func(c *Config) *string { return &c.KeyFile })},
		{flag: "insecure-skip-tls-verify", env: "FILEX_WATCH_TLS_SKIP_VERIFY", help: "do not check filex's certificate", isBool: true, set: boolean(func(c *Config) *bool { return &c.InsecureSkipTLSVerify })},
		{flag: "ca-file", env: "FILEX_WATCH_CA_FILE", help: "also trust these certificates (PEM)", set: str(func(c *Config) *string { return &c.CAFile })},
		{flag: "log-file", env: "FILEX_WATCH_LOG_FILE", help: "write the log here", set: str(func(c *Config) *string { return &c.LogFile })},
		{flag: "quiet", env: "FILEX_WATCH_QUIET", help: "wait this long after the last change", set: dur(func(c *Config) *Duration { return &c.Quiet })},
		{flag: "max-wait", env: "FILEX_WATCH_MAX_WAIT", help: "send a long burst after this long at most", set: dur(func(c *Config) *Duration { return &c.MaxWait })},
		{flag: "heartbeat", env: "FILEX_WATCH_HEARTBEAT", help: "say still here this often", set: dur(func(c *Config) *Duration { return &c.Heartbeat })},
		{flag: "subtree-resync", env: "FILEX_WATCH_SUBTREE_RESYNC", help: "re-read an unwatchable folder this often", set: dur(func(c *Config) *Duration { return &c.SubtreeResync })},
		{flag: "queue-max", env: "FILEX_WATCH_QUEUE_MAX", help: "folders to hold before a full resync", set: func(c *Config, v string) error {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return err
			}
			c.QueueMax = n
			return nil
		}},
		{flag: "verbose", env: "FILEX_WATCH_VERBOSE", help: "log every report", isBool: true, set: boolean(func(c *Config) *bool { return &c.Verbose })},
	}
}

// loadConfig reads the three sources over the defaults, reads the key and
// checks the result. args are the flags (no command name).
func loadConfig(args []string, getenv func(string) string, stderr io.Writer) (*Config, error) {
	all := settings()
	fs := flag.NewFlagSet("filex-watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	configFile := fs.String("config", "", "a JSON file with the settings")
	strFlags := map[string]*string{}
	boolFlags := map[string]*bool{}
	for _, s := range all {
		if s.isBool {
			boolFlags[s.flag] = fs.Bool(s.flag, false, s.help)
			continue
		}
		strFlags[s.flag] = fs.String(s.flag, "", s.help)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q (settings are flags: --root DIR)", fs.Arg(0))
	}
	setByFlag := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setByFlag[f.Name] = true })

	cfg := defaults()
	path := *configFile
	if !setByFlag["config"] {
		path = getenv("FILEX_WATCH_CONFIG")
	}
	if path != "" {
		if err := readConfigFile(path, &cfg); err != nil {
			return nil, err
		}
	}
	for _, s := range all {
		if v := getenv(s.env); v != "" {
			if err := s.set(&cfg, v); err != nil {
				return nil, fmt.Errorf("%s: %w", s.env, err)
			}
		}
	}
	for _, s := range all {
		if !setByFlag[s.flag] {
			continue
		}
		var v string
		if s.isBool {
			v = strconv.FormatBool(*boolFlags[s.flag])
		} else {
			v = *strFlags[s.flag]
		}
		if err := s.set(&cfg, v); err != nil {
			return nil, fmt.Errorf("--%s: %w", s.flag, err)
		}
	}
	if err := cfg.resolveKey(getenv("FILEX_WATCH_KEY")); err != nil {
		return nil, err
	}
	if err := cfg.check(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// readConfigFile reads the JSON file over cfg. An unknown field is refused:
// a misspelt `key_flie` must not be silently a setting that is not there.
func readConfigFile(path string, cfg *Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read the config file: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return fmt.Errorf("the config file %s: %w", path, err)
	}
	return nil
}

// resolveKey is the key: FILEX_WATCH_KEY, else the key file's content. Either
// way it must look like a filex-watch key, so a file holding something else
// (an API key, a path) is said at start, not as a 401 every minute.
func (c *Config) resolveKey(fromEnv string) error {
	key := strings.TrimSpace(fromEnv)
	if key == "" && c.KeyFile != "" {
		b, err := os.ReadFile(c.KeyFile)
		if err != nil {
			return fmt.Errorf("cannot read the key file: %w", err)
		}
		key = strings.TrimSpace(string(b))
	}
	if key == "" {
		return errors.New("no key: set key_file (--key-file, FILEX_WATCH_KEY_FILE) to the file holding it, or FILEX_WATCH_KEY")
	}
	if !strings.HasPrefix(key, watchproto.KeyPrefix) || strings.ContainsAny(key, " \t\r\n") {
		return fmt.Errorf("the key does not look like a filex-watch key (it starts with %s); make one on the storage's page in filex", watchproto.KeyPrefix)
	}
	c.key = key
	return nil
}

// check refuses a configuration that cannot work, each with what to set.
func (c *Config) check() error {
	u, err := url.Parse(strings.TrimRight(c.Server, "/"))
	if c.Server == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("server (--server, FILEX_WATCH_SERVER) must be filex's address, e.g. https://files.example.com")
	}
	c.Server = strings.TrimRight(c.Server, "/")
	if c.Storage <= 0 {
		return errors.New("storage (--storage, FILEX_WATCH_STORAGE) must be the storage's number, shown on its page in filex")
	}
	if c.Root == "" {
		return errors.New("root (--root, FILEX_WATCH_ROOT) must be the folder on this machine that is the storage")
	}
	abs, err := filepath.Abs(filepath.Clean(c.Root))
	if err != nil {
		return fmt.Errorf("root: %w", err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("root %s: %w", abs, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("root %s is not a folder", abs)
	}
	c.Root = abs
	switch {
	case time.Duration(c.Quiet) < 100*time.Millisecond || time.Duration(c.Quiet) > time.Minute:
		return errors.New("quiet must be from 100ms to 1m")
	case time.Duration(c.MaxWait) < time.Duration(c.Quiet) || time.Duration(c.MaxWait) > 10*time.Minute:
		return errors.New("max_wait must be at least quiet and at most 10m")
	case time.Duration(c.Heartbeat) < 10*time.Second || time.Duration(c.Heartbeat) > time.Hour:
		return errors.New("heartbeat must be from 10s to 1h")
	case time.Duration(c.SubtreeResync) < time.Minute || time.Duration(c.SubtreeResync) > 24*time.Hour:
		return errors.New("subtree_resync must be from 1m to 24h")
	case c.QueueMax < 16 || c.QueueMax > 1_000_000:
		return errors.New("queue_max must be from 16 to 1000000")
	}
	return nil
}
