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
	"strings"
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

// Upload sends one file with the engine's precondition and reports the file as
// the server listed it afterwards.
//
// The multipart upload answers with a listing of the folder it wrote into —
// the same shape as `index` — so the new server-side signature normally
// costs nothing. The staged path (large files) answers with a commit receipt
// instead; then the folder is listed once, right here, so the engine records
// the result of ITS write and not whatever the folder holds a whole pass
// later.
func (a apiAdapter) Upload(ctx context.Context, localPath, remote, expect string) (*filesync.ListedFile, error) {
	rp, err := cliclient.ParseRemotePath(remote)
	if err != nil {
		return nil, err
	}
	raw, err := a.c.UploadTo(ctx, localPath, rp.Dir(), rp.Base(), expect)
	if err != nil {
		if errors.Is(err, cliclient.ErrPreconditionFailed) {
			return nil, fmt.Errorf("%w (%v)", filesync.ErrRemoteChanged, err)
		}
		return nil, err
	}
	if f := listedIn(raw, rp.Base()); f != nil {
		return f, nil
	}
	l, err := a.List(ctx, rp.Dir().String())
	if err != nil {
		return nil, nil // the engine lists the folder again at the end of the pass
	}
	for _, f := range l.Files {
		if f.Basename == rp.Base() && !f.IsDir {
			f := f
			return &f, nil
		}
	}
	return nil, nil
}

// listedIn finds name in an upload answer that carries a folder listing.
func listedIn(raw []byte, name string) *filesync.ListedFile {
	var res cliclient.ListResult
	if json.Unmarshal(raw, &res) != nil {
		return nil
	}
	for _, f := range res.Files {
		if f.Basename == name && f.Type != "dir" {
			return &filesync.ListedFile{Basename: f.Basename, Size: f.Size, LastModified: f.LastModified}
		}
	}
	return nil
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

// nowFunc is the sync window's clock; tests move it.
var nowFunc = time.Now

func syncRunCmd(opts *clientOpts) *cobra.Command {
	var (
		pairID    string
		account   string
		dryRun    bool
		watch     time.Duration
		quietOut  bool
		transfers int
		live      bool
		limitDown int64
		limitUp   int64
		windowArg string
		watchMax  time.Duration
		fullEvery time.Duration
	)
	c := &cobra.Command{
		Use:   "run",
		Short: "Sync every pair once, or keep syncing with --watch",
		Long: "Sync every pair once. With --watch, keep running: changes on either\n" +
			"side are synced as they happen — the server announces its changes over\n" +
			"the same live stream the web explorer uses, and the local folders are\n" +
			"watched by the file system. The --watch interval is the safety net: it\n" +
			"asks the server's change log what changed while the stream was down,\n" +
			"walks a pair whose local tree changed unseen, retries what failed, and\n" +
			"walks every pair at least every --full-every.\n\n" +
			"The live state is printed as `live: connected|polling|offline — …`.\n" +
			"A token the server refuses stops the command with exit status 3.\n\n" +
			"One engine per pair on this computer: a pair another filex is already\n" +
			"syncing (the desktop app, a second copy of it, another terminal) is\n" +
			"left alone and reported as `<pair>: lock: busy — …`. A single run skips\n" +
			"it, syncs the rest and exits with status 4; --watch takes the pair over\n" +
			"once the other process stops (`<pair>: lock: acquired`).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A stop request (Ctrl-C, SIGTERM) cancels the pass in flight, which
			// still writes its ledger; a plain kill lands between two
			// checkpoints.
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
			api.DownLimit = cliclient.NewRateLimiter(kibPerSecond(limitDown))
			api.UpLimit = cliclient.NewRateLimiter(kibPerSecond(limitUp))
			// ⚠ One token cannot speak for two servers. The desktop app runs
			// one process per signed-in account and filters here; without
			// that, pairs belonging to account B would be synced with account
			// A's credentials and fail — or worse, hit a different server's
			// folder of the same name.
			selected := func(all []filesync.Pair) []filesync.Pair {
				var out []filesync.Pair
				for _, p := range all {
					if p.Paused || (pairID != "" && p.ID != pairID) || (account != "" && p.Account != account) {
						continue
					}
					out = append(out, p)
				}
				return out
			}
			pairs, err := st.LoadPairs()
			if err != nil {
				return err
			}
			if len(pairs) == 0 {
				return fmt.Errorf("no folders are paired; add one with `filex sync add`")
			}
			// ⚠ A 401 is not a bad pass, it is the end: the token was revoked
			// or has expired, and retrying it only fills the server's log while
			// the person is told nothing. Every pair of this process shares the
			// token, so the whole command stops, with a status the desktop app
			// acts on (exitSignedOut).
			signedOut := func(err error) error {
				if cliclient.IsUnauthorized(err) {
					return &exitError{code: exitSignedOut, err: errSignedOut}
				}
				return nil
			}

			// The pair locks --watch holds (synclock.go). A one-shot run holds
			// none here: each pass takes its own pair's lock.
			locks := newPairLocks(st)
			engineFor := func(p filesync.Pair) *filesync.Engine {
				eng := &filesync.Engine{Pair: p, API: apiAdapter{api}, Store: st, Transfers: transfers,
					StopOn: cliclient.IsUnauthorized, Lock: locks.get(p.ID)}
				// Progress prints even with --quiet. The desktop app starts
				// this command with --quiet and mirrors the LAST stdout line
				// into its panel; without these lines a big first sync spent
				// its whole inventory phase looking dead.
				eng.Progress = func(s string) { fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.ID, s) }
				if !quietOut {
					eng.Log = func(s string) { fmt.Fprintln(cmd.OutOrStdout(), s) }
				}
				return eng
			}
			// runPass is one engine pass; dirs == nil is a full pass.
			reporter := newPassReporter(cmd.OutOrStdout(), cmd.ErrOrStderr())
			runPass := func(ctx context.Context, p filesync.Pair, dirs []string) (filesync.Result, error) {
				eng := engineFor(p)
				var res filesync.Result
				var err error
				if dirs == nil {
					res, err = eng.Run(ctx)
				} else {
					res, err = eng.RunDirs(ctx, dirs)
				}
				if err != nil && ctx.Err() != nil && !cliclient.IsUnauthorized(err) {
					// A stop request or a closing sync window is not a failure
					// of the pair; the pass wrote its ledger and says so.
					reporter.pass(p, dirs != nil, res, nil)
					return res, err
				}
				reporter.pass(p, dirs != nil, res, err)
				return res, err
			}

			if dryRun {
				for _, p := range selected(pairs) {
					if err := printPlan(ctx, cmd, api, st, p); err != nil {
						if so := signedOut(err); so != nil {
							return so
						}
						fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", p.ID, err)
					}
				}
				return nil
			}
			if watch <= 0 {
				if win != nil && !win.contains(nowFunc()) {
					fmt.Fprintf(cmd.OutOrStdout(), "sync: outside the sync window %s; nothing was done\n", win)
					return nil
				}
				rctx, cancel := context.WithCancel(ctx)
				if win != nil {
					rctx, cancel = context.WithDeadline(ctx, win.closesAfter(nowFunc()))
				}
				defer cancel()
				var busy []string
				for _, p := range selected(pairs) {
					if _, err := runPass(rctx, p, nil); err != nil {
						if so := signedOut(err); so != nil {
							return so
						}
						if errors.Is(err, filesync.ErrPairBusy) {
							busy = append(busy, p.ID)
						}
					}
					if rctx.Err() != nil {
						break
					}
				}
				if win != nil && ctx.Err() == nil && rctx.Err() != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "sync: the sync window %s closed; the rest continues when it opens\n", win)
				}
				if len(busy) > 0 {
					return &exitError{code: exitPairBusy, err: errPairsBusy(busy)}
				}
				return nil
			}
			return runLive(ctx, cmd, st, api, locks, selected, runPass, watchSettings{
				interval: watch, watchMax: watchMax, fullEvery: fullEvery, window: win, live: live,
			})
		},
	}
	c.Flags().StringVar(&pairID, "pair", "", "sync only this pair")
	c.Flags().StringVar(&account, "account", "", "sync only pairs recorded against this account")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would happen without changing anything")
	c.Flags().DurationVar(&watch, "watch", 0, "keep running; the safety-net check of every pair runs at this interval (e.g. 30s)")
	c.Flags().BoolVar(&live, "live", true, "with --watch: sync changes as they happen (server change stream + local file-system events); false = interval checks only")
	c.Flags().BoolVar(&quietOut, "quiet", false, "print only the summary line per pair (progress lines still print)")
	c.Flags().IntVar(&transfers, "transfers", 0, "concurrent uploads/downloads per pair (0 = default 4, 1 = fully serial)")
	c.Flags().Int64Var(&limitDown, "limit-down", 0, "cap downloads at this many KiB/s, all transfers together (0 = no limit)")
	c.Flags().Int64Var(&limitUp, "limit-up", 0, "cap uploads at this many KiB/s, all transfers together (0 = no limit)")
	c.Flags().StringVar(&windowArg, "window", "", "only sync between these local times, e.g. 22:00-07:00 (may wrap midnight)")
	c.Flags().DurationVar(&watchMax, "watch-max", 5*time.Minute, "with --watch, the longest wait between walks of a quiet pair on a server that cannot report changes")
	c.Flags().DurationVar(&fullEvery, "full-every", 30*time.Minute, "with --watch, walk every pair at least this often even when nothing seems to change (0 = never)")
	return quiet(c)
}

// maxKiBPerSecond keeps a --limit-* value from overflowing when it is turned
// into bytes: anything above it (≈ 8 EiB/s) is no limit anyway.
const maxKiBPerSecond = int64(1) << 53

// kibPerSecond is a --limit-* value in bytes per second (0 = no limit).
func kibPerSecond(kib int64) int64 {
	if kib <= 0 || kib > maxKiBPerSecond {
		return 0
	}
	return kib * 1024
}

// watchSettings is what `sync run --watch` was asked for.
type watchSettings struct {
	interval, watchMax, fullEvery time.Duration
	window                        *syncWindow
	// live switches the change stream and the file-system watcher on; off,
	// only the interval's checks find changes.
	live bool
}

// runLive is `sync run --watch` (synclive.go). It holds the lock of every pair
// it syncs in locks, which runPass hands to each pass (synclock.go).
func runLive(ctx context.Context, cmd *cobra.Command, st *filesync.Store, api *cliclient.Client,
	locks *pairLocks,
	selected func([]filesync.Pair) []filesync.Pair,
	runPass func(context.Context, filesync.Pair, []string) (filesync.Result, error),
	ws watchSettings) error {
	out := cmd.OutOrStdout()
	loop := &liveLoop{
		locker: locks,
		errOut: cmd.ErrOrStderr(),
		loadPairs: func() ([]filesync.Pair, error) {
			all, err := st.LoadPairs()
			if err != nil {
				return nil, err
			}
			return selected(all), nil
		},
		runPass: runPass,
		localChanged: func(p filesync.Pair, dir string) bool {
			changed, err := (&filesync.Engine{Pair: p, Store: st}).LocalDirChanged(dir)
			return err != nil || changed
		},
		changes: func(ctx context.Context, p filesync.Pair, since string) (string, bool, error) {
			return api.Changes(ctx, p.WatchRoot(), since)
		},
		localFP:  filesync.LocalFingerprint,
		planner:  newWatchPlanner(ws.interval, ws.watchMax, ws.fullEvery),
		window:   ws.window,
		clock:    nowFunc,
		interval: ws.interval,
		out:      out,
	}
	if ws.live {
		loop.stream = &cliclient.ChangeStream{
			Client:         api,
			OnChange:       loop.noteRemote,
			OnState:        loop.streamState,
			OnConnected:    loop.checkAll,
			OnUnauthorized: func(err error) { loop.signedOut(err) },
		}
		applyWatchBudgetEnv(os.Getenv)
		if lw, err := newLocalWatcher(st.Dir, loop.noteLocal, loop.notePairsFile, loop.localState); err == nil {
			loop.local = lw
		} else {
			// No watcher at all: every pair says so, under its own folder.
			loop.localDown = err
		}
		fmt.Fprintf(out, "Watching: changes on either side are synced as they happen; a safety-net check every %s. Ctrl-C to stop.\n", ws.interval)
	} else {
		fmt.Fprintf(out, "Watching; checking every %s. Ctrl-C to stop.\n", ws.interval)
	}
	return loop.Run(ctx)
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

// passReporter prints what each pass did. Every line names its pair: the
// desktop app keeps one status per pair (desktop/src/syncstatus.ts), and a
// terminal running several pairs is easier to read that way too.
//
// failing remembers which pairs' LAST pass failed. A targeted (live) pass that
// found nothing to do — typically the echo of this engine's own upload — stays
// silent, because "already in step" for every echo would bury the lines that
// matter; EXCEPT right after a failure: that clean summary is what tells the
// desktop the failure is over. Without it the stale error stood under the
// folder until the next full check.
type passReporter struct {
	out, errOut io.Writer
	failing     map[string]bool
}

func newPassReporter(out, errOut io.Writer) *passReporter {
	return &passReporter{out: out, errOut: errOut, failing: map[string]bool{}}
}

func (r *passReporter) pass(p filesync.Pair, targeted bool, res filesync.Result, err error) {
	if errors.Is(err, filesync.ErrPairBusy) {
		// Not a failure of the pair: another process on this computer is
		// syncing it, and this pass touched nothing. Its own line, so the
		// desktop app says so instead of showing an error (synclock.go).
		fmt.Fprintf(r.errOut, "%s: %s\n", p.ID, lockBusyLine(err))
		return
	}
	if err != nil {
		r.failing[p.ID] = true
		fmt.Fprintf(r.errOut, "%s: %v\n", p.ID, err)
		return
	}
	failed := len(res.Errors) > 0
	quiet := targeted && res.Planned == 0 && !failed && !r.failing[p.ID]
	r.failing[p.ID] = failed
	if !quiet {
		writeResult(r.out, r.errOut, p, res)
	}
}

// errPairsBusy is the one-shot run's closing error when it skipped pairs
// another process on this computer holds (exitPairBusy).
func errPairsBusy(ids []string) error {
	what, it := "1 pair was", "it"
	if len(ids) > 1 {
		what, it = fmt.Sprintf("%d pairs were", len(ids)), "them"
	}
	return fmt.Errorf("%s not synced (%s): another filex on this computer is syncing %s — "+
		"run again once that one has stopped (in the desktop app: Pause sync, or quit it)",
		what, strings.Join(ids, ", "), it)
}

// printResult is writeResult on a command's own streams.
func printResult(cmd *cobra.Command, p filesync.Pair, res filesync.Result) {
	writeResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), p, res)
}

// writeResult reports one pass.
//
// ⚠ The summary says ", N failed" when the pass had errors. The desktop clears
// a pair's error on its next clean summary, and the error TEXT arrives on
// stderr — a different pipe, read in no guaranteed order against stdout. Only
// a summary that carries the verdict itself cannot clear the error it came
// with.
func writeResult(out, errOut io.Writer, p filesync.Pair, res filesync.Result) {
	if res.Planned == 0 && len(res.Errors) == 0 && res.Held == 0 {
		fmt.Fprintf(out, "%s: already in step\n", p.ID)
	} else {
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
		if n := len(res.Errors); n > 0 {
			fmt.Fprintf(out, ", %d failed", n)
		}
		fmt.Fprintf(out, "  (%s)\n", res.Duration.Round(time.Millisecond))
	}
	// Report what was NOT done rather than letting a summary imply full coverage.
	for _, e := range res.Errors {
		fmt.Fprintf(errOut, "%s: ! %s\n", p.ID, e)
	}
	// Not errors: the other side moved while this pass ran, nothing was
	// overwritten, and the next pass keeps both versions.
	for _, rc := range res.Raced {
		fmt.Fprintf(out, "%s: ~ %s — both versions are kept on the next pass\n", p.ID, rc)
	}
	// Worth reading, not a failure: a symlink in a synced folder is reported
	// on every full check for as long as it is there.
	if n := len(res.Skipped); n > 0 {
		fmt.Fprintf(errOut, "%s: note: skipped %d unreadable or non-regular item(s), e.g. %s\n", p.ID, n, res.Skipped[0])
	}
	if res.DeletedLocal > 0 {
		fmt.Fprintf(out, "%s: %d file(s) moved to the local trash — recover with `filex sync trash --pair %s`\n",
			p.ID, res.DeletedLocal, p.ID)
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
