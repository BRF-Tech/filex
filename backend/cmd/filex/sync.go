package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/filesync"
)

// apiAdapter bridges the REST client to the narrow interface the sync engine
// asks for. The engine deliberately does not import cliclient — keeping the
// seam here is what lets the engine be tested against an in-memory server.
type apiAdapter struct{ c *cliclient.Client }

func (a apiAdapter) List(ctx context.Context, remote string) (*filesync.Listing, error) {
	res, err := a.c.List(ctx, remote)
	if err != nil {
		return nil, err
	}
	out := &filesync.Listing{Files: make([]filesync.ListedFile, 0, len(res.Files))}
	for _, f := range res.Files {
		out.Files = append(out.Files, filesync.ListedFile{
			Basename:     f.Basename,
			IsDir:        f.Type == "dir",
			Size:         f.Size,
			LastModified: f.LastModified,
		})
	}
	return out, nil
}

func (a apiAdapter) Download(ctx context.Context, remote string, w io.Writer) (int64, error) {
	return a.c.Download(ctx, remote, w)
}

func (a apiAdapter) Upload(ctx context.Context, localPath, remote string) error {
	_, _, err := a.c.Upload(ctx, localPath, remote)
	return err
}

func (a apiAdapter) Mkdir(ctx context.Context, remote string) error {
	_, err := a.c.Mkdir(ctx, remote)
	return err
}

func (a apiAdapter) Remove(ctx context.Context, remote string) error {
	_, err := a.c.Remove(ctx, remote)
	return err
}

func syncStore() (*filesync.Store, error) {
	dir, err := filesync.DefaultStoreDir()
	if err != nil {
		return nil, err
	}
	return &filesync.Store{Dir: dir}, nil
}

// syncCmd builds the `filex sync` tree.
func syncCmd() *cobra.Command {
	opts := &clientOpts{}
	c := &cobra.Command{
		Use:   "sync",
		Short: "Keep a local folder in step with a folder on the server",
		Long: "Pair a folder on this machine with a folder on a filex server and keep\n" +
			"them in step in both directions.\n\n" +
			"Nothing is deleted on the first run of a pair: with no record of a previous\n" +
			"sync there is no way to tell \"you deleted this\" from \"you have not\n" +
			"downloaded it yet\", so the two folders are merged instead. Afterwards\n" +
			"deletes do propagate, and anything removed from this machine is kept in a\n" +
			"local trash for 30 days (`filex sync trash`).\n\n" +
			"When a file changed in both places, both versions are kept: yours keeps its\n" +
			"name and the server's copy lands beside it.",
	}
	c.PersistentFlags().StringVar(&opts.url, "url", "", "filex server URL (default: $FILEX_URL or ~/.filex/cli.yaml)")
	c.PersistentFlags().StringVar(&opts.token, "token", "", "API or session token (default: $FILEX_TOKEN or ~/.filex/cli.yaml)")

	c.AddCommand(
		syncAddCmd(),
		syncListCmd(),
		syncRemoveCmd(),
		syncRunCmd(opts),
		syncTrashCmd(),
	)
	return c
}

func syncAddCmd() *cobra.Command {
	var account string
	c := &cobra.Command{
		Use:   "add <local-folder> <adapter://remote/path>",
		Short: "Pair a local folder with a server folder",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := syncStore()
			if err != nil {
				return err
			}
			p, err := st.AddPair(filesync.Pair{Local: args[0], Remote: args[1], Account: account})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  <->  %s\n", p.ID, p.Local, p.Remote)
			fmt.Fprintf(cmd.OutOrStdout(), "Run `filex sync run` to start. The first run merges both sides and deletes nothing.\n")
			return nil
		},
	}
	c.Flags().StringVar(&account, "account", "", "label recording which signed-in server this pair belongs to")
	return quiet(c)
}

func syncListCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "list",
		Short: "Show the configured folder pairs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := syncStore()
			if err != nil {
				return err
			}
			pairs, err := st.LoadPairs()
			if err != nil {
				return err
			}
			// The desktop app reads this. Parsing the human table would break
			// the moment a column moved, so it gets the real shape.
			if asJSON {
				if pairs == nil {
					pairs = []filesync.Pair{}
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(pairs)
			}
			if len(pairs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No folders are paired yet. Add one with `filex sync add <folder> <adapter://path>`.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tLOCAL\tREMOTE\tSTATE")
			for _, p := range pairs {
				state := "active"
				if p.Paused {
					state = "paused"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.ID, p.Local, p.Remote, state)
			}
			return w.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print the pairs as JSON")
	return quiet(c)
}

func syncRemoveCmd() *cobra.Command {
	return quiet(&cobra.Command{
		Use:   "remove <pair-id>",
		Short: "Stop syncing a pair (files on both sides are left alone)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := syncStore()
			if err != nil {
				return err
			}
			if err := st.RemovePair(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s unpaired. Your files were not touched.\n", args[0])
			return nil
		},
	})
}

func syncRunCmd(opts *clientOpts) *cobra.Command {
	var (
		pairID   string
		account  string
		dryRun   bool
		watch    time.Duration
		quietOut bool
	)
	c := &cobra.Command{
		Use:   "run",
		Short: "Sync every pair once, or keep syncing with --watch",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := syncStore()
			if err != nil {
				return err
			}
			api, err := opts.api(true)
			if err != nil {
				return authHint(err)
			}
			pairs, err := st.LoadPairs()
			if err != nil {
				return err
			}
			if len(pairs) == 0 {
				return fmt.Errorf("no folders are paired; add one with `filex sync add`")
			}

			run := func() error {
				for _, p := range pairs {
					// ⚠ One token cannot speak for two servers. The desktop app
					// runs one process per signed-in account and filters here;
					// without that, pairs belonging to account B would be
					// synced with account A's credentials and fail — or worse,
					// hit a different server's folder of the same name.
					if p.Paused || (pairID != "" && p.ID != pairID) || (account != "" && p.Account != account) {
						continue
					}
					if dryRun {
						if err := printPlan(cmd, api, st, p); err != nil {
							fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", p.ID, err)
						}
						continue
					}
					eng := &filesync.Engine{Pair: p, API: apiAdapter{api}, Store: st}
					if !quietOut {
						eng.Log = func(s string) { fmt.Fprintln(cmd.OutOrStdout(), s) }
					}
					res, err := eng.Run(cmd.Context())
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", p.ID, err)
						continue
					}
					printResult(cmd, p, res)
				}
				return nil
			}

			if watch <= 0 {
				return run()
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Watching %d pair(s); checking every %s. Ctrl-C to stop.\n", len(pairs), watch)
			for {
				if err := run(); err != nil {
					return err
				}
				select {
				case <-cmd.Context().Done():
					return nil
				case <-time.After(watch):
				}
			}
		},
	}
	c.Flags().StringVar(&pairID, "pair", "", "sync only this pair")
	c.Flags().StringVar(&account, "account", "", "sync only pairs recorded against this account")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would happen without changing anything")
	c.Flags().DurationVar(&watch, "watch", 0, "keep running, re-checking at this interval (e.g. 30s)")
	c.Flags().BoolVar(&quietOut, "quiet", false, "print only the summary line per pair")
	return quiet(c)
}

// printPlan is --dry-run. It answers the question people actually ask before
// letting a sync tool near their files: what are you about to do?
func printPlan(cmd *cobra.Command, api *cliclient.Client, st *filesync.Store, p filesync.Pair) error {
	local, _, err := filesync.WalkLocal(p.Local)
	if err != nil {
		return err
	}
	remote, err := filesync.WalkRemote(cmd.Context(), apiAdapter{api}, p.Remote)
	if err != nil {
		return err
	}
	base, had, err := st.LoadBaseline(p.ID)
	if err != nil {
		return err
	}
	actions := filesync.Plan(local, remote, base, filesync.Options{FirstRun: !had, Now: time.Now()})
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s  %s <-> %s\n", p.ID, p.Local, p.Remote)
	if !had {
		fmt.Fprintln(out, "  (first run — both sides are merged and nothing is deleted)")
	}
	if len(actions) == 0 {
		fmt.Fprintln(out, "  already in step")
		return nil
	}
	for _, a := range actions {
		fmt.Fprintf(out, "  %-14s %s  (%s)\n", a.Kind, a.Rel, a.Reason)
	}
	return nil
}

func printResult(cmd *cobra.Command, p filesync.Pair, res filesync.Result) {
	out := cmd.OutOrStdout()
	if res.Planned == 0 {
		fmt.Fprintf(out, "%s: already in step\n", p.ID)
		return
	}
	fmt.Fprintf(out, "%s: %d/%d done — %d up, %d down, %d removed here, %d removed on the server",
		p.ID, res.Applied, res.Planned, res.Uploaded, res.Downloaded, res.DeletedLocal, res.DeletedRemot)
	if res.Conflicts > 0 {
		fmt.Fprintf(out, ", %d kept as both versions", res.Conflicts)
	}
	fmt.Fprintf(out, "  (%s)\n", res.Duration.Round(time.Millisecond))
	// Report what was NOT done rather than letting a summary imply full coverage.
	for _, e := range res.Errors {
		fmt.Fprintf(cmd.ErrOrStderr(), "  ! %s\n", e)
	}
	if n := len(res.Skipped); n > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "  ! skipped %d unreadable or non-regular item(s), e.g. %s\n", n, res.Skipped[0])
	}
	if res.DeletedLocal > 0 {
		fmt.Fprintf(out, "  %d file(s) moved to the local trash — recover with `filex sync trash --pair %s`\n",
			res.DeletedLocal, p.ID)
	}
}

func syncTrashCmd() *cobra.Command {
	var (
		pairID  string
		restore string
	)
	c := &cobra.Command{
		Use:   "trash",
		Short: "List or restore files sync removed from this machine",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := syncStore()
			if err != nil {
				return err
			}
			pairs, err := st.LoadPairs()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			shown := 0
			for _, p := range pairs {
				if pairID != "" && p.ID != pairID {
					continue
				}
				items, err := st.ListTrash(p.ID)
				if err != nil {
					return err
				}
				for _, it := range items {
					if restore != "" {
						if it.Rel != restore {
							continue
						}
						dest := filepath.Join(p.Local, filepath.FromSlash(it.Rel))
						if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
							return err
						}
						if err := os.Rename(it.Path, dest); err != nil {
							return err
						}
						fmt.Fprintf(cmd.OutOrStdout(), "restored %s\n", dest)
						fmt.Fprintln(cmd.OutOrStdout(), "The next sync will treat it as a new file and put it back on the server.")
						return nil
					}
					if shown == 0 {
						fmt.Fprintln(w, "PAIR\tDELETED\tSIZE\tPATH")
					}
					shown++
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", it.PairID,
						it.Deleted.Local().Format("2006-01-02 15:04"), humanSize(it.Size), it.Rel)
				}
			}
			if restore != "" {
				return fmt.Errorf("nothing in the trash matches %q", restore)
			}
			if shown == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing in the sync trash.")
				return nil
			}
			if err := w.Flush(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nKept for %d days. Restore with `filex sync trash --restore <path>`.\n", filesync.TrashRetentionDays)
			return nil
		},
	}
	c.Flags().StringVar(&pairID, "pair", "", "limit to one pair")
	c.Flags().StringVar(&restore, "restore", "", "put this path back into its sync folder")
	return quiet(c)
}
