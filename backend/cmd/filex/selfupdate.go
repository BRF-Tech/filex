package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/brf-tech/filex/backend/internal/update"
	"github.com/brf-tech/filex/backend/internal/version"
)

// selfUpdateCmd is the operator-facing half of the updater: the server applies
// what its policy allows, this applies what a human asks for.
//
// It deliberately works on installs where the SERVER would refuse to act on
// its own — `--to` accepts a minor or major jump, because a person typing the
// version has read the notes. What it will not do is pretend to work inside a
// container: replacing a binary in an image layer reverts at the next `up`,
// so that case fails loudly with the correct instructions instead.
func selfUpdateCmd() *cobra.Command {
	var (
		to       string
		checkOly bool
		force    bool
	)
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Check for a newer filex and install it",
		Long: "Check the release manifest and install a newer filex.\n\n" +
			"By default only the newest release is considered. Use --to to pin a\n" +
			"specific version (minor/major jumps included — you are the confirmation),\n" +
			"or --check to look without changing anything.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			svc := update.New(update.Config{
				Enabled:        true, // an explicit command overrides the check toggle
				Policy:         update.ParsePolicy(cfg.Update.Policy),
				Channel:        cfg.Update.Channel,
				ManifestURL:    cfg.Update.ManifestURL,
				StateDir:       cfg.DataDir,
				CurrentVersion: version.Version,
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()

			d, err := svc.Check(ctx)
			if err != nil {
				return fmt.Errorf("cannot reach the update server: %w", err)
			}

			fmt.Printf("current: %s\n", version.Version)
			if d.Target.Version != "" {
				fmt.Printf("latest:  %s", d.Target.Version)
				if d.Target.Date != "" {
					fmt.Printf("  (%s)", d.Target.Date)
				}
				fmt.Println()
			}
			fmt.Printf("install: %s\n", svc.Mode())
			if d.Reason != "" {
				fmt.Printf("verdict: %s — %s\n", d.Action, d.Reason)
			}
			for _, r := range d.Skipped {
				line := "  · " + r.Version
				if r.Migrations {
					line += "  [schema change]"
				}
				if r.IsSecurity() {
					line += "  [security]"
				}
				if r.Notes != "" {
					line += " — " + r.Notes
				}
				fmt.Println(line)
			}

			if checkOly {
				return nil
			}
			if d.Action == update.ActionNone && to == "" {
				fmt.Println("nothing to do — already up to date")
				return nil
			}
			if !svc.Mode().CanSelfApply() {
				fmt.Fprintln(os.Stderr, "\n"+update.ErrNotSelfApplicable.Error())
				fmt.Fprintln(os.Stderr, "\nUpgrade the image instead:")
				image := d.Target.Image
				if image == "" {
					image = "ghcr.io/brf-tech/filex:" + firstNonEmpty(to, d.Target.Version)
				}
				fmt.Fprintf(os.Stderr, "  # docker-compose.yml: image: %s\n  docker compose pull filex\n  docker compose up -d\n", image)
				return fmt.Errorf("cannot self-update in a container")
			}

			target := d.Target
			if to != "" {
				t, err := resolveTarget(ctx, svc, cfg.Update.ManifestURL, to)
				if err != nil {
					return err
				}
				target = t
			} else if d.Action == update.ActionInstruct && !force {
				// A major move, or an install below a release's MinVersion.
				// Refuse the implicit path and make the operator name it.
				return fmt.Errorf("%s — rerun with --to %s once you have read the notes%s",
					d.Reason, d.Target.Version, notesSuffix(d.Target.NotesURL))
			}

			fmt.Printf("\ninstalling %s …\n", target.Version)
			if err := svc.Apply(ctx, target); err != nil {
				return err
			}
			fmt.Printf("installed %s\n", target.Version)
			if svc.RestartRequired() {
				fmt.Println("restart filex to activate it (no supervisor was detected)")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "install this exact version (e.g. v0.8.0)")
	cmd.Flags().BoolVar(&checkOly, "check", false, "only report what is available")
	cmd.Flags().BoolVar(&force, "force", false, "apply even when the policy would only announce")
	return cmd
}

// resolveTarget finds an explicitly requested version in the manifest, so a
// typo becomes an error rather than a download of something unexpected.
func resolveTarget(ctx context.Context, svc *update.Service, manifestURL, want string) (update.Release, error) {
	man, err := update.Fetch(ctx, nil, manifestURL, version.Version)
	if err != nil {
		return update.Release{}, err
	}
	wantV, err := update.ParseVersion(want)
	if err != nil {
		return update.Release{}, fmt.Errorf("--to %q is not a version", want)
	}
	for _, r := range man.Releases {
		if v, err := update.ParseVersion(r.Version); err == nil && v.Compare(wantV) == 0 {
			return r, nil
		}
	}
	return update.Release{}, fmt.Errorf("version %s is not in the release manifest", wantV)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func notesSuffix(url string) string {
	if url == "" {
		return ""
	}
	return " (" + url + ")"
}
