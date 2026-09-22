package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
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
		// Only a 404 may be skipped by the walk; see ErrRemoteNotFound.
		var ae *cliclient.APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %v", filesync.ErrRemoteNotFound, err)
		}
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

func (a apiAdapter) Download(ctx context.Context, remote string, size int64, w io.Writer) (int64, error) {
	return a.c.DownloadSized(ctx, remote, w, size)
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
		syncMoveCmd(),
		syncRemoveCmd(),
		syncRunCmd(opts),
		syncTrashCmd(),
		syncConfirmCmd(),
		syncDiscardCmd(),
	)
	return c
}

func syncAddCmd() *cobra.Command {
	var (
		account string
		isFile  bool
	)
	c := &cobra.Command{
		Use:   "add <local-folder> <adapter://remote/path>",
		Short: "Pair a local folder (or, with --file, a single file) with the server",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := syncStore()
			if err != nil {
				return err
			}
			p, err := st.AddPair(filesync.Pair{Local: args[0], Remote: args[1], Account: account, File: isFile})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  <->  %s\n", p.ID, p.Local, p.Remote)
			fmt.Fprintf(cmd.OutOrStdout(), "Run `filex sync run` to start. The first run merges both sides and deletes nothing.\n")
			return nil
		},
	}
	c.Flags().StringVar(&account, "account", "", "label recording which signed-in server this pair belongs to")
	c.Flags().BoolVar(&isFile, "file", false, "pair a single file instead of a folder")
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
				if p.HoldNew {
					state = fmt.Sprintf("holding %d item(s)", p.Held)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.ID, p.Local, p.Remote, state)
			}
			return w.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print the pairs as JSON")
	return quiet(c)
}

func syncMoveCmd() *cobra.Command {
	return quiet(&cobra.Command{
		Use:   "move <pair-id> <new-local-path>",
		Short: "Repoint a pair at a folder that moved (its sync history moves with it)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := syncStore()
			if err != nil {
				return err
			}
			p, err := st.MovePairLocal(args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  <->  %s\n", p.ID, p.Local, p.Remote)
			fmt.Fprintln(cmd.OutOrStdout(), "History kept - the next run is an ordinary incremental one.")
			return nil
		},
	})
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

// nowFunc is the watcher's clock; tests move it.
var nowFunc = time.Now

func syncRunCmd(opts *clientOpts) *cobra.Command {
	var (
		pairID    string
		account   string
		dryRun    bool
		watch     time.Duration
		quietOut  bool
		transfers int
		limitDown int64
		limitUp   int64
		windowArg string
		watchMax  time.Duration
		fullEvery time.Duration
	)
	c := &cobra.Command{
		Use:   "run",
		Short: "Sync every pair once, or keep syncing with --watch",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A stop request (Ctrl-C, or the desktop app ending its watcher)
			// cancels the run in flight, which flushes its checkpoint; a plain
			// kill lands between two baseline writes.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			win, err := parseSyncWindow(windowArg)
			if err != nil {
				return err
			}
			if limitDown < 0 || limitUp < 0 {
				return errors.New("--limit-down and --limit-up are KiB/s and cannot be negative")
			}
			st, err := syncStore()
			if err != nil {
				return err
			}
			api, err := opts.api(true)
			if err != nil {
				return authHint(err)
			}
			api.DownLimit = cliclient.NewRateLimiter(limitDown * 1024)
			api.UpLimit = cliclient.NewRateLimiter(limitUp * 1024)
			pairs, err := st.LoadPairs()
			if err != nil {
				return err
			}
			if len(pairs) == 0 {
				return fmt.Errorf("no folders are paired; add one with `filex sync add`")
			}

			// ⚠ A 401 is not a bad round, it is the end: the token was revoked
			// or has expired, and retrying it every 30 s only fills the
			// server's log while the person is told nothing. The whole watcher
			// stops — every pair of this process shares the token — with a
			// status the desktop app acts on (exitSignedOut).
			signedOut := func(err error) error {
				if cliclient.IsUnauthorized(err) {
					return &exitError{code: exitSignedOut, err: errSignedOut}
				}
				return nil
			}

			// ⚠ One token cannot speak for two servers. The desktop app runs
			// one process per signed-in account and filters here; without
			// that, pairs belonging to account B would be synced with account
			// A's credentials and fail — or worse, hit a different server's
			// folder of the same name.
			eligible := func(p filesync.Pair) bool {
				return !p.Paused && (pairID == "" || p.ID == pairID) && (account == "" || p.Account == account)
			}

			// runPair runs one pair and reports it. stop is non-nil only when
			// the whole watcher has to end (the token is gone).
			runPair := func(ctx context.Context, p filesync.Pair) (res filesync.Result, runErr, stop error) {
				if dryRun {
					if err := printPlan(ctx, cmd, api, st, p); err != nil {
						if so := signedOut(err); so != nil {
							return res, err, so
						}
						fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", p.ID, err)
						return res, err, nil
					}
					return res, nil, nil
				}
				eng := &filesync.Engine{Pair: p, API: apiAdapter{api}, Store: st, Transfers: transfers}
				// Progress prints even with --quiet. The desktop app starts
				// this command with --quiet and mirrors the LAST stdout line
				// into its panel; without these lines a big first sync spent
				// its whole inventory phase looking dead.
				eng.Progress = func(s string) { fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.ID, s) }
				if !quietOut {
					eng.Log = func(s string) { fmt.Fprintln(cmd.OutOrStdout(), s) }
				}
				res, err := eng.Run(ctx)
				if err != nil {
					if so := signedOut(err); so != nil {
						return res, err, so
					}
					if ctx.Err() == nil { // a stop request or a closing window is not an error
						fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", p.ID, err)
					}
					return res, err, nil
				}
				printResult(cmd, p, res)
				return res, nil, nil
			}

			// A round's context ends when the sync window closes, so a round
			// still busy then stops like a Ctrl-C (its checkpoint flushed) and
			// carries on in the next window.
			withinWindow := func() (context.Context, context.CancelFunc) {
				if win == nil {
					return context.WithCancel(ctx)
				}
				return context.WithDeadline(ctx, win.closesAfter(nowFunc()))
			}
			windowClosed := func(rctx context.Context) {
				if win != nil && ctx.Err() == nil && rctx.Err() != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "sync: the sync window %s closed; the rest continues when it opens\n", win)
				}
			}

			if watch <= 0 {
				if win != nil && !win.contains(nowFunc()) {
					fmt.Fprintf(cmd.OutOrStdout(), "sync: outside the sync window %s; nothing was done\n", win)
					return nil
				}
				rctx, cancel := withinWindow()
				defer cancel()
				for _, p := range pairs {
					if !eligible(p) {
						continue
					}
					if _, _, stop := runPair(rctx, p); stop != nil {
						return stop
					}
					if rctx.Err() != nil {
						break
					}
				}
				windowClosed(rctx)
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Watching %d pair(s); checking every %s. Ctrl-C to stop.\n", len(pairs), watch)
			// Which pairs actually need a run this tick is the planner's call
			// (watch.go): a quiet pair costs one `changes` request and a local
			// walk, not a listing of its whole server tree.
			planner := newWatchPlanner(watch, watchMax, fullEvery)
			tick := func() error {
				rctx, cancel := withinWindow()
				defer cancel()
				seen := map[string]bool{}
				for _, p := range pairs {
					if !eligible(p) {
						continue
					}
					seen[p.ID] = true
					p := p
					d, err := planner.decide(p, nowFunc(),
						func() (string, error) { return filesync.LocalFingerprint(p) },
						func(since string) (string, bool, error) { return api.Changes(rctx, p.Remote, since) })
					if err != nil {
						if so := signedOut(err); so != nil {
							return so
						}
						continue
					}
					if !d.run {
						continue
					}
					res, runErr, stop := runPair(rctx, p)
					if stop != nil {
						return stop
					}
					if rctx.Err() != nil {
						windowClosed(rctx)
						return nil // unfinished: not recorded, the next tick runs it again
					}
					fp := res.LocalFingerprint
					if fp == "" {
						fp, _ = filesync.LocalFingerprint(p)
					}
					planner.ran(p, nowFunc(), d.cursor, res, runErr, fp)
				}
				planner.forget(seen)
				return nil
			}
			waiting := false
			for {
				if win != nil && !win.contains(nowFunc()) {
					if !waiting {
						fmt.Fprintf(cmd.OutOrStdout(), "sync: waiting for the sync window %s\n", win)
						waiting = true
					}
				} else {
					waiting = false
					if err := tick(); err != nil {
						return err
					}
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(watch):
				}
				// Re-read between rounds. pairs.json is edited by OTHER
				// processes — the desktop app writes it while this watcher
				// runs — and the old one-time load meant a pair added after
				// start was silently never synced until the next restart,
				// while a removed one kept going. The file is tiny; the
				// re-read costs nothing.
				if pairs, err = st.LoadPairs(); err != nil {
					return fmt.Errorf("re-read pairs: %w", err)
				}
			}
		},
	}
	c.Flags().StringVar(&pairID, "pair", "", "sync only this pair")
	c.Flags().StringVar(&account, "account", "", "sync only pairs recorded against this account")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would happen without changing anything")
	c.Flags().DurationVar(&watch, "watch", 0, "keep running, re-checking at this interval (e.g. 30s)")
	c.Flags().BoolVar(&quietOut, "quiet", false, "print only the summary line per pair (progress lines still print)")
	c.Flags().IntVar(&transfers, "transfers", 0, "concurrent uploads/downloads per pair (0 = default 4, 1 = fully serial)")
	c.Flags().Int64Var(&limitDown, "limit-down", 0, "cap downloads at this many KiB/s, all transfers together (0 = no limit)")
	c.Flags().Int64Var(&limitUp, "limit-up", 0, "cap uploads at this many KiB/s, all transfers together (0 = no limit)")
	c.Flags().StringVar(&windowArg, "window", "", "only start transfer rounds between these local times, e.g. 22:00-07:00 (may wrap midnight)")
	c.Flags().DurationVar(&watchMax, "watch-max", 5*time.Minute, "with --watch, the longest wait between walks of a quiet pair on a server that cannot report changes")
	c.Flags().DurationVar(&fullEvery, "full-every", 30*time.Minute, "with --watch, walk every pair at least this often even when nothing seems to change (0 = never)")
	return quiet(c)
}

// printPlan is --dry-run. It answers the question people actually ask before
// letting a sync tool near their files: what are you about to do?
func printPlan(ctx context.Context, cmd *cobra.Command, api *cliclient.Client, st *filesync.Store, p filesync.Pair) error {
	local, _, err := filesync.WalkLocal(p.Local)
	if err != nil {
		return err
	}
	remote, err := filesync.WalkRemote(ctx, apiAdapter{api}, p.Remote, nil)
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
	if res.Identical > 0 {
		fmt.Fprintf(out, ", %d already identical", res.Identical)
	}
	if res.Held > 0 {
		fmt.Fprintf(out, ", %d held for a decision", res.Held)
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

// findPair returns the stored pair with this id.
func findPair(st *filesync.Store, id string) (filesync.Pair, error) {
	pairs, err := st.LoadPairs()
	if err != nil {
		return filesync.Pair{}, err
	}
	for _, p := range pairs {
		if p.ID == id {
			return p, nil
		}
	}
	return filesync.Pair{}, fmt.Errorf("no such pair: %s", id)
}

func syncConfirmCmd() *cobra.Command {
	return quiet(&cobra.Command{
		Use:   "confirm <pair-id>",
		Short: "Send the items a pair is holding for a decision to the server",
		Long: "A first run that would upload many files the server does not have, into a\n" +
			"server folder that already has content, holds them instead: with no sync\n" +
			"history, a file that is new here and a file that was deleted on the server\n" +
			"look the same. `confirm` says they are wanted: the next run uploads them.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := syncStore()
			if err != nil {
				return err
			}
			p, err := findPair(st, args[0])
			if err != nil {
				return err
			}
			if !p.HoldNew {
				fmt.Fprintf(cmd.OutOrStdout(), "%s is not holding anything.\n", p.ID)
				return nil
			}
			n, err := st.ConfirmHeld(p.ID)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %d held item(s) go to the server on the next run.\n", p.ID, n)
			return nil
		},
	})
}

func syncDiscardCmd() *cobra.Command {
	return quiet(&cobra.Command{
		Use:   "discard <pair-id>",
		Short: "Move the items a pair is holding into the local sync trash",
		Long: "The other answer to a hold: the files are not wanted on the server (typically\n" +
			"they were cleaned up there, and this machine's copy is stale). They move into\n" +
			"the pair's local sync trash — recoverable with `filex sync trash` for 30 days —\n" +
			"and the next run makes this folder match the server. A file edited after it\n" +
			"was held is left where it is.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := syncStore()
			if err != nil {
				return err
			}
			p, err := findPair(st, args[0])
			if err != nil {
				return err
			}
			if !p.HoldNew {
				fmt.Fprintf(cmd.OutOrStdout(), "%s is not holding anything.\n", p.ID)
				return nil
			}
			moved, kept, err := st.DiscardHeld(p, time.Now())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: moved %d held file(s) to the local sync trash", p.ID, moved)
			if kept > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "; %d changed since and were left in place", kept)
			}
			fmt.Fprintln(cmd.OutOrStdout(), ".")
			return nil
		},
	})
}

func syncTrashCmd() *cobra.Command {
	var (
		pairID  string
		restore string
		asJSON  bool
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
			// The desktop app reads this; parsing the human table would break
			// the moment a column moved.
			if asJSON && restore == "" {
				type row struct {
					Pair    string `json:"pair"`
					Rel     string `json:"rel"`
					Deleted string `json:"deleted"`
					Size    int64  `json:"size"`
				}
				out := []row{}
				for _, p := range pairs {
					if pairID != "" && p.ID != pairID {
						continue
					}
					items, err := st.ListTrash(p.ID)
					if err != nil {
						return err
					}
					for _, it := range items {
						out = append(out, row{p.ID, it.Rel, it.Deleted.Format(time.RFC3339), it.Size})
					}
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(out)
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
						root := p.Local
						if p.File {
							// A file pair's Local IS the file; restores land beside it.
							root = filepath.Dir(p.Local)
						}
						dest := filepath.Join(root, filepath.FromSlash(it.Rel))
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
	c.Flags().BoolVar(&asJSON, "json", false, "print the recoverable items as JSON")
	return quiet(c)
}
