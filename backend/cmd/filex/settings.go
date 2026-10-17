package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/server"
)

// ─────────────────── settings ───────────────────
//
// `filex settings reseed` - the explicit way back to the environment (task
// #258). Since 0.57 every setting a variable gives a first value to is a SEED:
// the variable is written on a start where nothing is stored yet, and what an
// administrator saves on the admin panel wins from then on - the external
// services (FILEX_ONLYOFFICE_URL...) included, which used to be written back
// at every start. An operator who changes a variable in compose and wants it
// to take over runs this, then restarts filex.

func settingsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "settings",
		Short: "Stored settings and the environment that seeds them",
	}
	c.AddCommand(settingsReseedCmd())
	return c
}

func settingsReseedCmd() *cobra.Command {
	var all, external bool
	c := &cobra.Command{
		Use:   "reseed [setting key...]",
		Short: "Write the environment's (and config.yaml's) value over the stored settings",
		Long: `Write what the environment - over config.yaml - says over what is stored,
for the settings named by key (for example sync.default_interval_minutes),
every setting (--all), or the external services ONLYOFFICE and draw.io
(--external).

A setting whose variable is not set is left as it is, except the instance
settings (default language, sync interval, the apps' limits...), which go back
to their default. The trusted-proxy list (login.trusted_proxies, from
FILEX_TRUSTED_PROXIES) is taken by a running server within a minute; restart
filex for the others: most of them are read at start.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			setupLogger(cfg)
			drv, err := db.Get(cfg.DB.Driver)
			if err != nil {
				return err
			}
			ctx := context.Background()
			conn, err := drv.Open(ctx, cfg.DB.DSN)
			if err != nil {
				return err
			}
			defer conn.Close()
			// The same schema the server would run with: a reseed before the
			// first start of a new version must not write into an old one.
			if err := db.Migrate(ctx, drv, conn); err != nil {
				return err
			}
			wrote, err := server.ReseedFromConfig(ctx, drv.NewStore(conn), cfg, args, all, external)
			for _, k := range wrote {
				fmt.Fprintln(os.Stdout, "reseeded", k)
			}
			if err != nil {
				return err
			}
			if len(wrote) == 0 {
				fmt.Fprintln(os.Stdout, "nothing to reseed: the environment sets none of these")
				return nil
			}
			fmt.Fprintln(os.Stdout, "Restart filex for the settings read at start to apply.")
			return nil
		},
	}
	c.Flags().BoolVar(&all, "all", false, "every setting the environment or config.yaml seeds")
	c.Flags().BoolVar(&external, "external", false, "the external services (ONLYOFFICE, draw.io)")
	return c
}
