package main

import (
	"context"
	"fmt"
	"io"
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
// container or under a package manager: replacing a binary in an image layer
// reverts at the next `up`, and replacing one Homebrew, winget or Snap owns
// leaves the manager recording the old version (then its next upgrade writes
// over ours; a snap is read-only). Those cases fail loudly with the correct
// command instead. `--check` still reports what is available.
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
			fmt.Println(installLine(svc.Install()))
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
				if cmd := svc.Install().UpgradeCommand(); cmd != "" && d.Action != update.ActionNone {
					fmt.Printf("upgrade: %s\n", cmd)
				}
				return nil
			}
			if d.Action == update.ActionNone && to == "" {
				fmt.Println("nothing to do — already up to date")
				return nil
			}
			if err := refuseSelfUpdate(os.Stderr, svc.Install(), d, to); err != nil {
				return err
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
	// A refusal (container, package manager) is a RUNTIME answer, not a
	// misuse: printing the flag list under it reads as "you typed it wrong".
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true // main prints it once, as `filex: ...`
	cmd.Flags().StringVar(&to, "to", "", "install this exact version (e.g. v0.8.0)")
	cmd.Flags().BoolVar(&checkOly, "check", false, "only report what is available")
	cmd.Flags().BoolVar(&force, "force", false, "apply even when the policy would only announce")
	return cmd
}

// installLine is the "install:" line of the report: the mode, and for a
// packaged install who owns it and the command that upgrades it.
func installLine(inst update.Install) string {
	line := "install: " + string(inst.Mode)
	if inst.Mode != update.ModePackage {
		return line
	}
	if name := inst.Manager.Label(); name != "" {
		line += " (" + name
		if cmd := inst.UpgradeCommand(); cmd != "" {
			line += ": " + cmd
		}
		return line + ")"
	}
	return line + " (a package manager)"
}

// refuseSelfUpdate stops a self-update of an install that cannot replace its
// own binary; nil for a plain binary. A packaged install is refused with the
// manager's command in the error itself (main prints it as the last line,
// where a person looks for what went wrong); a container gets the compose
// steps written to w. The refusal does not depend on --force: that flag
// overrides the POLICY, and no policy makes a package manager's file ours to
// replace.
func refuseSelfUpdate(w io.Writer, inst update.Install, d update.Decision, to string) error {
	refusal := inst.Refusal()
	if refusal == nil || inst.Mode == update.ModePackage {
		return refusal
	}
	fmt.Fprintln(w, "\n"+refusal.Error())
	fmt.Fprintln(w, "\nUpgrade the image instead:")
	image := d.Target.Image
	if image == "" {
		image = "ghcr.io/brf-tech/filex:" + firstNonEmpty(to, d.Target.Version)
	}
	fmt.Fprintf(w, "  # docker-compose.yml: image: %s\n  docker compose pull filex\n  docker compose up -d\n", image)
	return fmt.Errorf("cannot self-update in a container")
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
