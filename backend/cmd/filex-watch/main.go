// Command filex-watch tells filex which folders changed on the machine that
// owns a storage's disk (docs/FILEX-WATCH.md).
//
// filex keeps a catalogue of every storage and keeps it current by watching
// the disk (Live watching) or by scanning it. A watch only sees what its own
// machine writes: when the disk is somewhere else - a NAS share filex reaches
// over the network, the folder an SFTP storage points at - a file written
// there reaches filex at the next scan. filex-watch runs THERE, watches the
// folder, and reports each folder whose contents changed to
// POST /api/storages/{id}/watch/events with a key made on the storage's page.
// filex then looks at those folders alone.
//
//	filex-watch [run] --config /etc/filex-watch/config.json
//	filex-watch check --config /etc/filex-watch/config.json
//	filex-watch version
//
// Settings come from a JSON file, FILEX_WATCH_* environment variables and
// flags, later ones winning (config.go). The key is read from a file
// (key_file) or FILEX_WATCH_KEY, never from a flag: a flag is on the process
// list for every user of the machine to read.
//
// Its own release line: filex-watch is versioned and tagged apart from filex
// (`filex-watch-v<version>`, watchproto.AgentVersion), and speaks the
// protocol of internal/watchproto, which filex's door speaks too.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/brf-tech/filex/backend/internal/watchproto"
)

// version is the release this binary is; the release workflow stamps it
// with -ldflags "-X main.version=<version>" and refuses a tag that disagrees
// with watchproto.AgentVersion.
var version = watchproto.AgentVersion

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run is the whole program, with its world passed in so a test can drive it.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	cmd := "run"
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-version":
			fmt.Fprintln(stdout, "filex-watch", version)
			return 0
		case "run", "check":
			cmd, args = args[0], args[1:]
		case "help", "--help", "-h", "-help":
			fmt.Fprint(stdout, usage)
			return 0
		}
	}
	cfg, err := loadConfig(args, getenv, stderr)
	if err != nil {
		if errors.Is(err, errHelp) {
			return 0
		}
		fmt.Fprintln(stderr, "filex-watch:", err)
		return 2
	}
	logger, closeLog, err := newLogger(cfg, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "filex-watch:", err)
		return 2
	}
	defer closeLog()

	if cmd == "check" {
		return check(context.Background(), cfg, stdout)
	}

	work := func(ctx context.Context) error { return runAgent(ctx, cfg, logger) }
	if isWindowsService() {
		if err := runService(work); err != nil {
			logger.Error("the Windows service stopped with an error", slog.String("err", err.Error()))
			return 1
		}
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := work(ctx); err != nil {
		logger.Error("stopped", slog.String("err", err.Error()))
		return 1
	}
	return 0
}

// newLogger writes to log_file (appended, readable by its owner only) or to
// stderr, at debug level with verbose.
func newLogger(cfg *Config, stderr io.Writer) (*slog.Logger, func(), error) {
	level := slog.LevelInfo
	if cfg.Verbose {
		level = slog.LevelDebug
	}
	out, closeFn := stderr, func() {}
	if cfg.LogFile != "" {
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot open the log file: %w", err)
		}
		out, closeFn = f, func() { _ = f.Close() }
	}
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})), closeFn, nil
}

// check sends one empty report - a heartbeat - and says what filex answered:
// the quickest way to see that the address, the storage and the key agree
// before installing the service.
func check(ctx context.Context, cfg *Config, stdout io.Writer) int {
	cl, err := newClient(cfg)
	if err != nil {
		fmt.Fprintln(stdout, "filex-watch:", err)
		return 2
	}
	ans, err := cl.send(ctx, watchproto.Report{Protocol: watchproto.Protocol, Agent: agentInfo(cfg)})
	if err != nil {
		fmt.Fprintln(stdout, "filex-watch: check failed:", err)
		return 1
	}
	fmt.Fprintf(stdout, "filex-watch: ok - storage %q takes this key's reports (%s, storage %d)\n", ans.Storage, cfg.Server, cfg.Storage)
	return 0
}

const usage = `filex-watch - tells filex which folders changed on this machine

usage:
  filex-watch [run] [flags]   watch the folder and report changes (the service)
  filex-watch check [flags]   send one report and say what filex answered
  filex-watch version

flags (each also FILEX_WATCH_<NAME> in the environment, or a field of --config):
  --config FILE                  a JSON file with the settings below
  --server URL                   filex's address, e.g. https://files.example.com
  --storage ID                   the storage's number (its page in the admin panel)
  --root DIR                     the folder on this machine that is the storage
  --key-file FILE                the file holding the key (FILEX_WATCH_KEY: the key itself)
  --insecure-skip-tls-verify     do not check filex's certificate (a self-signed one)
  --ca-file FILE                 also trust the certificates in this PEM file
  --log-file FILE                write the log here instead of to stderr
  --quiet DURATION               wait this long after the last change (1s)
  --max-wait DURATION            send a long burst after this long at most (10s)
  --heartbeat DURATION           say "still here" this often when nothing changes (60s)
  --subtree-resync DURATION      ask filex to re-read a folder this machine cannot watch this often (15m)
  --queue-max N                  folders to hold before asking for a full resync (4096)
  --verbose                      log every report

docs: https://docs.filex.sh/FILEX-WATCH
`
