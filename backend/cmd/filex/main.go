// Command filex is the self-hosted file-manager binary.
//
// Default behavior (`filex` with no args) is to start the HTTP server.
// All subcommands accept --config /path/to/config.yaml (or FILEX_CONFIG env).
//
//	filex serve                       # default
//	filex migrate up | down | status
//	filex admin reset-password [--email]
//	filex admin random-password [--email]
//	filex storage list | add | remove
//	filex --version
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"gitlab.com/brftech/filemanager/backend/internal/auth/drivers/local"
	"gitlab.com/brftech/filemanager/backend/internal/config"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/model"
	"gitlab.com/brftech/filemanager/backend/internal/server"
	"gitlab.com/brftech/filemanager/backend/internal/version"

	embedded "gitlab.com/brftech/filemanager/backend/embed"

	// Register driver init() blocks even when the CLI subcommand short-circuits.
	_ "gitlab.com/brftech/filemanager/backend/internal/db/drivers/mysql"
	_ "gitlab.com/brftech/filemanager/backend/internal/db/drivers/postgres"
	_ "gitlab.com/brftech/filemanager/backend/internal/db/drivers/sqlite"
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
	c.Flags().StringVar(&driver, "driver", "", "driver: local | s3 | sftp | webdav")
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
