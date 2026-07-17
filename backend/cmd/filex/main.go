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
//	filex --version
package main

import (
	"context"
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
	"github.com/brf-tech/filex/backend/internal/server"
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
		Short:   "filex — self-hosted file manager",
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
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "filex: "+err.Error())
		os.Exit(1)
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
		_, err := store.CreateUser(context.Background(), strings.ToLower(email), hash, model.RoleAdmin, "en", "UTC")
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
	)
	return c
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

func storageAddCmd() *cobra.Command {
	var name, driver, mount, configJSON string
	c := &cobra.Command{
		Use:   "add",
		Short: "Add a new storage row",
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
			st := &model.Storage{
				Name:          name,
				Driver:        driver,
				MountPath:     mount,
				ConfigJSON:    []byte(configJSON),
				SyncMode:      model.SyncModePoll,
				SyncIntervalS: 900,
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
	c.Flags().StringVar(&driver, "driver", "", "driver: local | s3 | sftp | ftp | webdav")
	c.Flags().StringVar(&mount, "mount", "/", "logical mount path")
	c.Flags().StringVar(&configJSON, "config", "{}", "JSON object with driver-specific options")
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

// thumbBackfillCmd walks every persisted file node and (re)dispatches the
// thumbnail pipeline. Useful after deploying a new image with extra deps
// (e.g. ffmpeg / ghostscript / libreoffice) so existing rows produce thumbs.
//
//	filex thumb backfill                      — every enabled storage
//	filex thumb backfill --storage local      — single storage by name
//	filex thumb backfill --storage 2          — single storage by id
//	filex thumb backfill --limit 100          — first 100 files (across all storages)
//	filex thumb backfill --retry-failed       — re-run rows in state=failed
//	filex thumb backfill --concurrency 8      — wider worker pool
func thumbBackfillCmd() *cobra.Command {
	var (
		storageRef    string
		limit         int
		retryFailed   bool
		retrySkipped  bool
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
	c.Flags().StringVar(&storageRef, "storage", "", "limit to a single storage (id or name); empty = every enabled storage")
	c.Flags().IntVar(&limit, "limit", 0, "stop after N files (0 = unlimited)")
	c.Flags().BoolVar(&retryFailed, "retry-failed", false, "re-run thumbnails currently in state=failed")
	c.Flags().BoolVar(&retrySkipped, "retry-skipped", false, "re-run thumbnails currently in state=skipped (use after the pipeline gains coverage for previously-skipped kinds)")
	c.Flags().IntVar(&concurrency, "concurrency", 4, "worker pool size")
	c.Flags().IntVar(&progressEvery, "progress-every", 25, "emit a progress line every N processed files (0 = silent)")
	return c
}
