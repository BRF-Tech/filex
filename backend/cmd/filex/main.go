// Command filex is the self-hosted file-manager binary.
//
// Default behavior (`filex` with no args) is to start the HTTP server.
// All subcommands accept --config /path/to/config.yaml (or FILEX_CONFIG env).
//
//	filex serve                                 # default
//	filex migrate up | down | status
//	filex admin reset-password [--email]
//	filex admin random-password [--email]
//	filex storage list | add | remove
//	filex thumb backfill [--storage <id|name>] [--limit N] [--retry-failed]
//	filex client login | ls | upload | download | mkdir | rm | mv | search | share
//	filex e2e-escrow keygen                      # install-time E2E key escrow
//	filex decrypt <folder|zip|file> [-o DIR]     # offline: decrypt a downloaded E2E folder
//	filex --version
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/observability"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/server"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/version"

	embedded "github.com/brf-tech/filex/backend/embed"

	// Register driver init() blocks even when the CLI subcommand short-circuits.
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/mysql"
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/postgres"
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"
)

var configPath string

func main() {
	root := &cobra.Command{
		Use:     "filex",
		Short:   "filex - self-hosted file manager",
		Version: version.String(),
	}
	root.PersistentFlags().StringVar(&configPath, "config", os.Getenv("FILEX_CONFIG"), "path to config.yaml (default: $FILEX_CONFIG or ~/.filex/config.yaml)")

	root.AddCommand(
		serveCmd(),
		migrateCmd(),
		adminCmd(),
		storageCmd(),
		thumbCmd(),
		clientCmd(),
		syncCmd(),
		mountCmd(),
		selfUpdateCmd(),
		e2eEscrowCmd(),
		decryptCmd(),
		encryptCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "filex: "+err.Error())
		os.Exit(exitCode(err))
	}
}

func loadConfig() (config.Config, error) {
	path := configPath
	if path == "" {
		home, _ := os.UserHomeDir()
		default_ := home + "/.filex/config.yaml"
		if _, err := os.Stat(default_); err == nil {
			path = default_
		}
	}
	return config.Load(path)
}

func setupLogger(cfg config.Config) {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Log.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	var h slog.Handler
	if strings.ToLower(cfg.Log.Format) == "json" {
		h = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	} else {
		h = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	// Optional Sentry-wire error reporting (GlitchTip). When a DSN is set, tee
	// WARN+ERROR logs to it so operational failures surface centrally.
	if observability.Init(cfg.Sentry.DSN, cfg.Sentry.Environment, version.Version) {
		h = observability.WrapSlog(h)
	}
	slog.SetDefault(slog.New(h))
}

// ─────────────────── serve ───────────────────

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			setupLogger(cfg)
			defer observability.Flush()

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			s, err := server.New(ctx, cfg, embedded.FS)
			if err != nil {
				return err
			}
			return s.Start(ctx)
		},
	}
}

// ─────────────────── migrate ───────────────────

func migrateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "migrate",
		Short: "Apply or roll back DB migrations",
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "up",
			Short: "Apply all pending migrations",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runMigrate("up")
			},
		},
		&cobra.Command{
			Use:   "down",
			Short: "Roll back one migration step",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runMigrate("down")
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show migration status",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runMigrate("status")
			},
		},
	)
	return c
}

func runMigrate(op string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	setupLogger(cfg)
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	drv, err := db.Get(cfg.DB.Driver)
	if err != nil {
		return err
	}
	conn, err := drv.Open(context.Background(), cfg.DB.DSN)
	if err != nil {
		return err
	}
	defer conn.Close()
	switch op {
	case "up":
		return db.Migrate(context.Background(), drv, conn)
	case "down":
		return db.MigrateDown(context.Background(), drv, conn)
	case "status":
		return db.MigrateStatus(context.Background(), drv, conn)
	}
	return fmt.Errorf("unknown migrate op: %s", op)
}

// ─────────────────── admin ───────────────────

func adminCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "admin",
		Short: "Admin user utilities",
	}
	c.AddCommand(adminResetPasswordCmd(), adminRandomPasswordCmd())
	return c
}

func adminResetPasswordCmd() *cobra.Command {
	var email, password string
	c := &cobra.Command{
		Use:   "reset-password",
		Short: "Reset an admin user's password",
		RunE: func(cmd *cobra.Command, args []string) error {
			if email == "" || password == "" {
				return fmt.Errorf("--email and --password required")
			}
			return resetPassword(email, password, "")
		},
	}
	c.Flags().StringVar(&email, "email", "", "user email")
	c.Flags().StringVar(&password, "password", "", "new plaintext password")
	return c
}

func adminRandomPasswordCmd() *cobra.Command {
	var email string
	c := &cobra.Command{
		Use:   "random-password",
		Short: "Generate a random password and set it for the user",
		RunE: func(cmd *cobra.Command, args []string) error {
			if email == "" {
				return fmt.Errorf("--email required")
			}
			pw, err := server.RandomHex(8)
			if err != nil {
				return err
			}
			if err := resetPassword(email, pw, "(random) "); err != nil {
				return err
			}
			fmt.Println("New password for", email+":", pw)
			return nil
		},
	}
	c.Flags().StringVar(&email, "email", "", "user email")
	return c
}

func resetPassword(email, password, label string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	setupLogger(cfg)
	drv, err := db.Get(cfg.DB.Driver)
	if err != nil {
		return err
	}
	conn, err := drv.Open(context.Background(), cfg.DB.DSN)
	if err != nil {
		return err
	}
	defer conn.Close()
	store := drv.NewStore(conn)
	user, err := store.GetUserByEmail(context.Background(), strings.ToLower(email))
	if err != nil {
		// auto-create as admin if user did not exist.
		hash, _ := local.HashPassword(password)
		_, err := store.CreateUser(context.Background(), strings.ToLower(email), hash, model.RoleAdmin, "en", model.TimezoneUnset)
		if err != nil {
			return err
		}
		fmt.Println(label+"created", email)
		return nil
	}
	hash, err := local.HashPassword(password)
	if err != nil {
		return err
	}
	if err := store.UpdateUserPassword(context.Background(), user.ID, hash); err != nil {
		return err
	}
	fmt.Println(label+"reset password for", email)
	return nil
}

// ─────────────────── storage ───────────────────

func storageCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "storage",
		Short: "Manage storage backends",
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List configured storages",
			RunE: func(cmd *cobra.Command, args []string) error {
				return storageList()
			},
		},
		storageAddCmd(),
		storageRemoveCmd(),
		storageScanCollisionsCmd(),
	)
	return c
}

// storageScanCollisionsCmd reports names that exist as BOTH a file and a
// folder — damage that predates the write guards (storage.ErrKindConflict).
//
// It only REPORTS. Fixing means choosing which of the two to keep, and that is
// a judgement call about someone's data: the stray file may be the accident, or
// the folder may be. Printing them is what lets a human decide.
func storageScanCollisionsCmd() *cobra.Command {
	var name, root string
	c := &cobra.Command{
		Use:   "scan-collisions",
		Short: "Report paths that exist as both a file and a folder",
		Long: "An object store accepts `X` and `X/…` side by side; a directory-backed\n" +
			"mirror (MinIO) cannot represent it, so the prefix stops listing and the\n" +
			"objects under it silently lose their backup. This finds those names.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return storageScanCollisions(name, root)
		},
	}
	c.Flags().StringVar(&name, "storage", "", "storage name (default: every enabled storage)")
	c.Flags().StringVar(&root, "path", "", "subtree to scan (default: storage root)")
	return c
}

func storageScanCollisions(only, root string) error {
	ctx := context.Background()
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	dbDrv, err := db.Get(cfg.DB.Driver)
	if err != nil {
		return err
	}
	conn, err := dbDrv.Open(ctx, cfg.DB.DSN)
	if err != nil {
		return err
	}
	defer conn.Close()
	store := dbDrv.NewStore(conn)

	list, err := store.ListEnabledStorages(ctx)
	if err != nil {
		return err
	}
	total := 0
	for _, st := range list {
		if only != "" && st.Name != only {
			continue
		}
		drv, err := storage.Get(st.Driver)
		if err != nil {
			// ⚠ A plugin driver only exists inside a RUNNING server: the
			// manager starts the plugin process and registers it. A CLI
			// process has no manager, so "unknown driver" here is expected
			// and says nothing useful on its own — say the useful thing.
			if strings.HasPrefix(st.Driver, plugin.DriverPrefix) {
				fmt.Printf("%-20s  SKIP (%s is provided by a plugin, which only runs inside the server - use the admin API or the web UI for this storage)\n", st.Name, st.Driver)
				continue
			}
			fmt.Printf("%-20s  SKIP (%v)\n", st.Name, err)
			continue
		}
		scfg := map[string]any{}
		if len(st.ConfigJSON) > 0 {
			_ = json.Unmarshal(st.ConfigJSON, &scfg)
		}
		if err := drv.Init(ctx, scfg); err != nil {
			fmt.Printf("%-20s  SKIP (init: %v)\n", st.Name, err)
			continue
		}
		hits, err := storage.ScanKindCollisions(ctx, drv, root)
		if err != nil {
			fmt.Printf("%-20s  ERROR %v\n", st.Name, err)
			continue
		}
		if len(hits) == 0 {
			fmt.Printf("%-20s  clean\n", st.Name)
			continue
		}
		for _, h := range hits {
			fmt.Printf("%-20s  COLLISION  %s\n", st.Name, h.Path)
		}
		total += len(hits)
	}
	if total > 0 {
		fmt.Printf("\n%d collision(s). Each name is both a file and a folder;\n"+
			"decide which one to keep, then remove the other.\n", total)
	}
	return nil
}

func storageList() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	drv, err := db.Get(cfg.DB.Driver)
	if err != nil {
		return err
	}
	conn, err := drv.Open(context.Background(), cfg.DB.DSN)
	if err != nil {
		return err
	}
	defer conn.Close()
	store := drv.NewStore(conn)
	list, err := store.ListStorages(context.Background())
	if err != nil {
		return err
	}
	for _, st := range list {
		fmt.Printf("%4d  %-10s  %-20s  %s\n", st.ID, st.Driver, st.Name, st.MountPath)
	}
	return nil
}

// validateStorageInput runs `filex storage add` through the same gates as
// POST /api/admin/storages: known driver, parseable config, and a
// non-root mount point (storage.ValidateNonRootPath, which reads the
// field the driver's descriptor flags as its root).
//
// Missing required fields are reported as a warning rather than an error:
// a descriptor marks what a form should ask for, and an operator scripting
// this command may legitimately lean on ambient credentials (an S3
// instance role fills access_key/secret_key with nothing in the config).
func validateStorageInput(driver, configJSON string) error {
	if driver == "" {
		return fmt.Errorf("--driver is required (one of: %s)", strings.Join(storage.Names(), ", "))
	}
	if _, err := storage.Get(driver); err != nil {
		return fmt.Errorf("unknown driver %q (registered: %s)", driver, strings.Join(storage.Names(), ", "))
	}
	cfgMap := map[string]any{}
	if strings.TrimSpace(configJSON) != "" {
		if err := json.Unmarshal([]byte(configJSON), &cfgMap); err != nil {
			return fmt.Errorf("--config is not a JSON object: %w", err)
		}
	}
	if err := storage.ValidateNonRootPath(driver, cfgMap); err != nil {
		return err
	}
	if d, ok := storage.DescriptorFor(driver); ok {
		if missing := d.MissingRequired(cfgMap); len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "warning: %s config is missing %s - the driver will fail to start unless it can pick them up elsewhere\n",
				driver, strings.Join(missing, ", "))
		}
	}
	return nil
}

// storageAddSync works out the sync settings `filex storage add` writes: the
// mode (validated as the admin API validates it - `lazy` for local storages
// only), the scan interval, and the lazy catalogue's settings, written into
// the storage's config under the keys the admin form writes (lazy_fill,
// lazy_max_watches, lazy_watch_ttl) and held to the same bounds. lazy carries
// only the lazy flags that were given; one given without `--sync-mode lazy` is
// refused rather than stored for a mode that never reads it.
func storageAddSync(driver, mode string, intervalS int, configJSON string, lazy map[string]any) (model.SyncMode, int, string, error) {
	m := model.SyncMode(strings.TrimSpace(mode))
	if m == "" {
		m = model.SyncModePoll
	}
	if err := model.ValidateSyncModeFor(m, driver); err != nil {
		return "", 0, "", err
	}
	if intervalS <= 0 {
		return "", 0, "", fmt.Errorf("--sync-interval must be a positive number of seconds")
	}
	if len(lazy) > 0 && m != model.SyncModeLazy {
		return "", 0, "", fmt.Errorf("--lazy-fill, --lazy-max-watches and --lazy-watch-ttl apply to --sync-mode lazy only")
	}
	if m != model.SyncModeLazy {
		return m, intervalS, configJSON, nil
	}
	cfg := map[string]any{}
	if strings.TrimSpace(configJSON) != "" {
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return "", 0, "", fmt.Errorf("--config is not a JSON object: %w", err)
		}
	}
	for k, v := range lazy {
		cfg[k] = v
	}
	if err := storage.ValidateLazyConfig(cfg); err != nil {
		return "", 0, "", err
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return "", 0, "", err
	}
	return m, intervalS, string(out), nil
}

func storageAddCmd() *cobra.Command {
	var name, driver, mount, configJSON, syncMode, lazyFill string
	var syncInterval, lazyMaxWatches, lazyWatchTTL int
	c := &cobra.Command{
		Use:   "add",
		Short: "Add a new storage row",
		RunE: func(cmd *cobra.Command, args []string) error {
			// This command writes the storages row straight into the DB,
			// so it used to skip every check the admin API runs — a
			// storage added here could sit at the bucket root, or name a
			// driver that does not exist, and nothing said a word until
			// the sync worker tripped over it. Same validation as the API
			// now, from the same driver descriptors.
			if err := validateStorageInput(driver, configJSON); err != nil {
				return err
			}
			lazy := map[string]any{}
			if cmd.Flags().Changed("lazy-fill") {
				lazy[storage.LazyFillKey] = lazyFill
			}
			if cmd.Flags().Changed("lazy-max-watches") {
				lazy[storage.LazyMaxWatchesKey] = lazyMaxWatches
			}
			if cmd.Flags().Changed("lazy-watch-ttl") {
				lazy[storage.LazyWatchTTLKey] = lazyWatchTTL
			}
			mode, interval, cfgJSON, err := storageAddSync(driver, syncMode, syncInterval, configJSON, lazy)
			if err != nil {
				return err
			}
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			drv, err := db.Get(cfg.DB.Driver)
			if err != nil {
				return err
			}
			conn, err := drv.Open(context.Background(), cfg.DB.DSN)
			if err != nil {
				return err
			}
			defer conn.Close()
			store := drv.NewStore(conn)
			st := &model.Storage{
				Name:          name,
				Driver:        driver,
				MountPath:     mount,
				ConfigJSON:    []byte(cfgJSON),
				SyncMode:      mode,
				SyncIntervalS: interval,
				Enabled:       true,
			}
			created, err := store.CreateStorage(context.Background(), st)
			if err != nil {
				return err
			}
			fmt.Println("created storage", created.ID, created.Name)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "logical name")
	c.Flags().StringVar(&driver, "driver", "", "driver: "+strings.Join(storage.Names(), " | "))
	c.Flags().StringVar(&mount, "mount", "/", "logical mount path")
	c.Flags().StringVar(&configJSON, "config", "{}", "JSON object with driver-specific options")
	c.Flags().StringVar(&syncMode, "sync-mode", string(model.SyncModePoll), "how the catalogue follows the storage: poll | fsnotify | ondemand | lazy (lazy: local storages only)")
	c.Flags().IntVar(&syncInterval, "sync-interval", 900, "seconds between two scans")
	c.Flags().StringVar(&lazyFill, "lazy-fill", "", "lazy: "+storage.LazyFillBackground+" (catalogue the rest in the background) | "+storage.LazyFillOnOpen+" (only the folders people open)")
	c.Flags().IntVar(&lazyMaxWatches, "lazy-max-watches", storage.LazyMaxWatchesDefault, "lazy: the most visited folders watched at once")
	c.Flags().IntVar(&lazyWatchTTL, "lazy-watch-ttl", storage.LazyWatchTTLDefault, "lazy: minutes a visited folder stays watched after it was last opened")
	return c
}

func storageRemoveCmd() *cobra.Command {
	var name string
	c := &cobra.Command{
		Use:   "remove",
		Short: "Remove a storage by name",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			drv, err := db.Get(cfg.DB.Driver)
			if err != nil {
				return err
			}
			conn, err := drv.Open(context.Background(), cfg.DB.DSN)
			if err != nil {
				return err
			}
			defer conn.Close()
			store := drv.NewStore(conn)
			st, err := store.GetStorageByName(context.Background(), name)
			if err != nil {
				return err
			}
			if err := store.DeleteStorage(context.Background(), st.ID); err != nil {
				return err
			}
			fmt.Println("removed", name)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "storage name")
	return c
}

// ─────────────────── thumb ───────────────────

// thumbCmd groups thumbnail-related maintenance utilities.
func thumbCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "thumb",
		Short: "Thumbnail maintenance (backfill, retry, …)",
	}
	c.AddCommand(thumbBackfillCmd())
	return c
}

// thumbBackfillCmd walks the file nodes in scope and draws the thumbnails
// that are missing, left pending or stale (the file changed after its
// render). Useful after deploying a new image with extra deps (e.g. ffmpeg /
// ghostscript) or connecting OnlyOffice, so existing rows produce thumbs. It is the same
// walk as Admin → Tools → Thumbnail repair (server.BackfillThumbs).
//
//	filex thumb backfill                          — every enabled storage
//	filex thumb backfill --storage local          — single storage by name
//	filex thumb backfill --storage 2              — single storage by id
//	filex thumb backfill --storage 2 --path /Pics — one folder (and everything in it) or one file
//	filex thumb backfill --limit 100              — first 100 files (across all storages)
//	filex thumb backfill --retry-failed           — re-run rows in state=failed
//	filex thumb backfill --rebuild                — draw every file again
//	filex thumb backfill --concurrency 8          — wider worker pool
func thumbBackfillCmd() *cobra.Command {
	var (
		storageRef    string
		limit         int
		retryFailed   bool
		retrySkipped  bool
		stale         bool
		rebuild       bool
		pathRef       string
		concurrency   int
		progressEvery int
	)
	c := &cobra.Command{
		Use:   "backfill",
		Short: "Generate thumbnails for every existing file node",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bleve's embedded boltdb backend takes an exclusive file
			// lock when the index is opened. A running `filex serve`
			// instance already holds that lock, so server.New() below
			// would block indefinitely on search.Open(). Backfill never
			// touches the search index — disable it for this command so
			// the spin-up stays under a second even when the server is
			// live. Operator can override with FILEX_SEARCH_ENABLED=true
			// if running on a stopped node.
			if os.Getenv("FILEX_SEARCH_ENABLED") == "" {
				_ = os.Setenv("FILEX_SEARCH_ENABLED", "false")
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			setupLogger(cfg)

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			// Spin up a full Server (boot the pipeline + storage resolver
			// + driver init), then call BackfillThumbs synchronously.
			// We do NOT call Start() — no HTTP server needed.
			s, err := server.New(ctx, cfg, embedded.FS)
			if err != nil {
				return err
			}

			opts := server.BackfillOptions{
				Limit:         limit,
				RetryFailed:   retryFailed,
				RetrySkipped:  retrySkipped,
				Stale:         stale,
				All:           rebuild,
				Path:          pathRef,
				Concurrency:   concurrency,
				ProgressEvery: progressEvery,
				OnProgress: func(st server.BackfillStats) {
					fmt.Fprintf(os.Stdout, "thumb backfill: processed=%d ok=%d failed=%d skipped=%d\n",
						st.Processed, st.OK, st.Failed, st.Skipped)
				},
			}

			// Optional --storage filter — accept ID or name.
			if storageRef != "" {
				store := s.Store()
				if id, perr := strconv.ParseInt(storageRef, 10, 64); perr == nil {
					if _, gerr := store.GetStorage(ctx, id); gerr != nil {
						return fmt.Errorf("--storage %s: %w", storageRef, gerr)
					}
					opts.StorageIDs = []int64{id}
				} else {
					st, gerr := store.GetStorageByName(ctx, storageRef)
					if gerr != nil {
						return fmt.Errorf("--storage %s: %w", storageRef, gerr)
					}
					opts.StorageIDs = []int64{st.ID}
				}
			}

			stats, err := s.BackfillThumbs(ctx, opts)
			fmt.Fprintf(os.Stdout, "{processed: %d, ok: %d, failed: %d, skipped: %d}\n",
				stats.Processed, stats.OK, stats.Failed, stats.Skipped)
			return err
		},
	}
	// A refused storage (server.ErrNotIndexed) is a RUNTIME answer, not a
	// misuse: printing the flag list above it reads as "you typed it wrong".
	c.SilenceUsage = true
	c.SilenceErrors = true // main prints it once, as `filex: ...`
	c.Flags().StringVar(&storageRef, "storage", "", "limit to a single storage (id or name); empty = every enabled storage")
	c.Flags().IntVar(&limit, "limit", 0, "stop after N files (0 = unlimited)")
	c.Flags().BoolVar(&retryFailed, "retry-failed", false, "re-run thumbnails currently in state=failed")
	c.Flags().BoolVar(&retrySkipped, "retry-skipped", false, "re-run thumbnails currently in state=skipped (use after the pipeline gains coverage for previously-skipped kinds)")
	c.Flags().BoolVar(&stale, "stale", true, "also draw again thumbnails whose file changed after they were drawn (--stale=false to skip them)")
	c.Flags().BoolVar(&rebuild, "rebuild", false, "draw every file in scope again, whatever state its thumbnail is in")
	c.Flags().StringVar(&pathRef, "path", "", "limit to one file, or one folder and everything in it (storage-relative, e.g. /Photos/2024); needs --storage")
	c.Flags().IntVar(&concurrency, "concurrency", 4, "worker pool size")
	c.Flags().IntVar(&progressEvery, "progress-every", 25, "emit a progress line every N processed files (0 = silent)")
	return c
}
