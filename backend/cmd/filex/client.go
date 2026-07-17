// client.go wires the `filex client` subcommand family — a CLI consumer
// of a REMOTE filex server's REST API (the rest of the binary manages a
// local instance). Connection resolution, wire calls and mv semantics
// live in internal/cliclient; this file is cobra plumbing + rendering.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

// clientOpts carries the persistent `filex client` flags into each leaf.
type clientOpts struct {
	url   string
	token string
	json  bool
}

// api resolves the connection (flags > env > ~/.filex/cli.yaml) and
// returns a ready client. Leaf commands that talk to the server call
// this; login has its own token-less path.
func (o *clientOpts) api(requireToken bool) (*cliclient.Client, error) {
	cfgPath, err := cliclient.DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	conn, err := cliclient.Resolve(o.url, o.token, cfgPath, os.Getenv)
	if err != nil {
		return nil, err
	}
	if conn.URL == "" {
		return nil, errors.New("no server URL — pass --url, set FILEX_URL, or run `filex client login`")
	}
	if requireToken && conn.Token == "" {
		return nil, errors.New("no token — run `filex client login` or set FILEX_TOKEN")
	}
	return cliclient.New(conn), nil
}

// authHint appends the login hint to 401 errors so an expired session
// tells the user exactly what to do next.
func authHint(err error) error {
	if cliclient.IsUnauthorized(err) {
		return fmt.Errorf("%w — token missing/expired; run `filex client login`", err)
	}
	return err
}

// clientCmd builds the `filex client` tree:
//
//	filex client login|ls|upload|download|mkdir|rm|mv|search|share
func clientCmd() *cobra.Command {
	opts := &clientOpts{}
	c := &cobra.Command{
		Use:   "client",
		Short: "Talk to a remote filex server over its REST API",
		Long: "Connect to a remote filex server and manage files from the terminal.\n" +
			"Connection resolution order: --url/--token flags, then FILEX_URL/FILEX_TOKEN\n" +
			"environment variables, then ~/.filex/cli.yaml (written by `filex client login`).\n" +
			"Remote paths use the adapter://relative/path form, e.g. docs://reports/2026.",
	}
	c.PersistentFlags().StringVar(&opts.url, "url", "", "filex server URL (default: $FILEX_URL or ~/.filex/cli.yaml)")
	c.PersistentFlags().StringVar(&opts.token, "token", "", "API or session token (default: $FILEX_TOKEN or ~/.filex/cli.yaml)")
	c.PersistentFlags().BoolVar(&opts.json, "json", false, "print raw JSON responses instead of tables")

	c.AddCommand(
		clientLoginCmd(opts),
		clientLsCmd(opts),
		clientUploadCmd(opts),
		clientDownloadCmd(opts),
		clientMkdirCmd(opts),
		clientRmCmd(opts),
		clientMvCmd(opts),
		clientSearchCmd(opts),
		clientShareCmd(opts),
	)
	return c
}

// quiet marks a leaf command as CLI-clean: no usage dump and no double
// error print on failure (main() already writes `filex: <err>` + exit 1).
func quiet(c *cobra.Command) *cobra.Command {
	c.SilenceUsage = true
	c.SilenceErrors = true
	return c
}

// ─────────────────── login ───────────────────

func clientLoginCmd(opts *clientOpts) *cobra.Command {
	var email, totp string
	c := &cobra.Command{
		Use:   "login",
		Short: "Sign in and store the session token in ~/.filex/cli.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := cliclient.DefaultConfigPath()
			if err != nil {
				return err
			}
			conn, err := cliclient.Resolve(opts.url, "", cfgPath, os.Getenv)
			if err != nil {
				return err
			}
			if conn.URL == "" {
				return errors.New("login needs a server URL — pass --url or set FILEX_URL")
			}

			// One shared reader: prompting email and piping the password
			// through the same stdin must not swallow each other's lines.
			rd := bufio.NewReader(cmd.InOrStdin())
			if email == "" {
				fmt.Fprint(cmd.ErrOrStderr(), "E-mail: ")
				line, err := rd.ReadString('\n')
				if err != nil && line == "" {
					return fmt.Errorf("read e-mail: %w", err)
				}
				email = strings.TrimSpace(line)
			}
			if email == "" {
				return errors.New("empty e-mail")
			}
			password, err := readPassword(cmd, rd)
			if err != nil {
				return fmt.Errorf("read password: %w", err)
			}
			if password == "" {
				return errors.New("empty password")
			}

			api := cliclient.New(cliclient.Conn{URL: conn.URL})
			lr, err := api.Login(cmd.Context(), email, password, totp)
			if err != nil {
				return err
			}
			if err := cliclient.SaveFileConfig(cfgPath, cliclient.FileConfig{URL: conn.URL, Token: lr.Token}); err != nil {
				return fmt.Errorf("save %s: %w", cfgPath, err)
			}
			if opts.json {
				fmt.Fprintf(cmd.OutOrStdout(), "{\"ok\":true,\"url\":%q,\"config\":%q}\n", conn.URL, cfgPath)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Logged in as %s on %s\nToken saved to %s (0600)\n", email, conn.URL, cfgPath)
			return nil
		},
	}
	c.Flags().StringVar(&email, "email", "", "account e-mail (prompted when omitted)")
	c.Flags().StringVar(&totp, "totp", "", "two-factor code (accounts with TOTP enabled)")
	return quiet(c)
}

// readPassword reads the password without echo when stdin is a terminal,
// and as a plain line when piped (CI / `echo pass | filex client login`).
func readPassword(cmd *cobra.Command, rd *bufio.Reader) (string, error) {
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(cmd.ErrOrStderr(), "Password: ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		return string(b), err
	}
	line, err := rd.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// ─────────────────── ls ───────────────────

func clientLsCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "ls [adapter://path]",
		Short: "List a remote directory (no argument: list storages)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			remote := ""
			if len(args) == 1 {
				remote = args[0]
			}
			res, err := api.List(cmd.Context(), remote)
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(res.Raw))
				return nil
			}
			if remote == "" {
				// Storage overview — the caller didn't pick an adapter yet.
				for _, s := range res.Storages {
					fmt.Fprintf(cmd.OutOrStdout(), "%s://\n", s)
				}
				return nil
			}
			renderListing(cmd.OutOrStdout(), res)
			return nil
		},
	}
	return quiet(c)
}

// renderListing prints the aligned human table for one directory.
func renderListing(w io.Writer, res *cliclient.ListResult) {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tSIZE\tMODIFIED\tNAME")
	for _, f := range res.Files {
		size := "-"
		if f.Type == "file" {
			size = humanSize(f.Size)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", f.Type, size, fmtMillis(f.LastModified), f.Basename)
	}
	_ = tw.Flush()
}

// ─────────────────── upload / download ───────────────────

func clientUploadCmd(opts *clientOpts) *cobra.Command {
	var recursive bool
	c := &cobra.Command{
		Use:   "upload <local-path> <adapter://path>",
		Short: "Upload a local file, or a whole folder with -r (destination: folder, or full path to rename)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			if recursive {
				return runUploadTree(cmd, api, opts, args[0], args[1])
			}
			dest, raw, err := api.Upload(cmd.Context(), args[0], args[1])
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Uploaded %s -> %s\n", args[0], dest.String())
			return nil
		},
	}
	c.Flags().BoolVarP(&recursive, "recursive", "r", false,
		"upload a directory recursively (remote folders are created; symlinks are skipped)")
	return quiet(c)
}

// runUploadTree drives the recursive upload: stream per-file progress,
// then a summary. Any per-item error makes the command exit non-zero —
// scripts must not mistake a half-uploaded tree for success.
func runUploadTree(cmd *cobra.Command, api *cliclient.Client, opts *clientOpts, local, remote string) error {
	out := cmd.OutOrStdout()
	errw := cmd.ErrOrStderr()
	progress := func(ev cliclient.TreeEvent) {
		if opts.json {
			return
		}
		switch ev.Kind {
		case cliclient.TreeFile:
			fmt.Fprintf(out, "Uploaded %s -> %s\n", ev.Local, ev.Remote.String())
		case cliclient.TreeSymlink:
			fmt.Fprintf(errw, "warning: skipping symlink %s\n", ev.Local)
		case cliclient.TreeErr:
			fmt.Fprintf(errw, "error: %s: %v\n", ev.Local, ev.Err)
		}
	}
	rep, err := api.UploadTree(cmd.Context(), local, remote, progress)
	if err != nil {
		return authHint(err)
	}
	if opts.json {
		type jsonErr struct {
			Path  string `json:"path"`
			Error string `json:"error"`
		}
		summary := struct {
			Local           string    `json:"local"`
			Remote          string    `json:"remote"`
			Files           int       `json:"files"`
			Dirs            int       `json:"dirs"`
			SkippedSymlinks []string  `json:"skipped_symlinks,omitempty"`
			Errors          []jsonErr `json:"errors,omitempty"`
		}{Local: local, Remote: remote, Files: rep.Files, Dirs: rep.Dirs, SkippedSymlinks: rep.Symlinks}
		for _, e := range rep.Errors {
			summary.Errors = append(summary.Errors, jsonErr{Path: e.Local, Error: e.Err.Error()})
		}
		b, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(b))
	} else {
		fmt.Fprintf(out, "Done: %d file(s), %d folder(s), %d error(s)\n", rep.Files, rep.Dirs, len(rep.Errors))
		for _, e := range rep.Errors {
			fmt.Fprintf(errw, "  failed: %s: %v\n", e.Local, e.Err)
		}
	}
	if n := len(rep.Errors); n > 0 {
		return fmt.Errorf("recursive upload finished with %d error(s)", n)
	}
	return nil
}

func clientDownloadCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "download <adapter://path> [local-target]",
		Short: "Download a remote file (default: basename in the current dir; `-` = stdout)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			remote := args[0]
			rp, err := cliclient.ParseRemotePath(remote)
			if err != nil {
				return err
			}

			target := ""
			if len(args) == 2 {
				target = args[1]
			}
			if target == "-" {
				_, err := api.Download(cmd.Context(), remote, cmd.OutOrStdout())
				return authHint(err)
			}
			if target == "" {
				target = rp.Base()
			} else if fi, err := os.Stat(target); err == nil && fi.IsDir() {
				target = filepath.Join(target, rp.Base())
			}

			out, err := os.Create(target)
			if err != nil {
				return err
			}
			n, err := api.Download(cmd.Context(), remote, out)
			cerr := out.Close()
			if err != nil {
				_ = os.Remove(target) // don't leave a partial file behind
				return authHint(err)
			}
			if cerr != nil {
				return cerr
			}
			if opts.json {
				fmt.Fprintf(cmd.OutOrStdout(), "{\"remote\":%q,\"local\":%q,\"bytes\":%d}\n", remote, target, n)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Downloaded %s -> %s (%s)\n", remote, target, humanSize(n))
			return nil
		},
	}
	return quiet(c)
}

// ─────────────────── mkdir / rm / mv ───────────────────

func clientMkdirCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "mkdir <adapter://path>",
		Short: "Create a remote folder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			raw, err := api.Mkdir(cmd.Context(), args[0])
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", args[0])
			return nil
		},
	}
	return quiet(c)
}

func clientRmCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "rm <adapter://path> [more...]",
		Short: "Move remote items to the server-side trash",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			var raw []byte
			for _, remote := range args {
				if raw, err = api.Remove(cmd.Context(), remote); err != nil {
					return authHint(fmt.Errorf("rm %s: %w", remote, err))
				}
				if !opts.json {
					fmt.Fprintf(cmd.OutOrStdout(), "Trashed %s\n", remote)
				}
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			}
			return nil
		},
	}
	return quiet(c)
}

func clientMvCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "mv <adapter://source> <adapter://target>",
		Short: "Move or rename a remote item (target: existing dir, or full new path)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			dest, raw, err := api.Move(cmd.Context(), args[0], args[1])
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Moved %s -> %s\n", args[0], dest.String())
			return nil
		},
	}
	return quiet(c)
}

// ─────────────────── search / share ───────────────────

func clientSearchCmd(opts *clientOpts) *cobra.Command {
	var scope string
	var storageID int64
	var limit int
	c := &cobra.Command{
		Use:   "search <query>",
		Short: "Search file names and indexed content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			res, err := api.Search(cmd.Context(), args[0], scope, storageID, limit)
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(res.Raw))
				return nil
			}
			if len(res.Results) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No matches.")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "PATH\tMATCHED\tSNIPPET")
			for _, hit := range res.Results {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", hit.Path, hit.Matched, truncateLine(hit.Snippet, 80))
			}
			return tw.Flush()
		},
	}
	c.Flags().StringVar(&scope, "scope", "all", "search scope: name | content | all")
	c.Flags().Int64Var(&storageID, "storage-id", 0, "limit to one storage id (0 = all)")
	c.Flags().IntVar(&limit, "limit", 50, "maximum number of results")
	return quiet(c)
}

func clientShareCmd(opts *clientOpts) *cobra.Command {
	var pin bool
	var expiresDays int
	c := &cobra.Command{
		Use:   "share <adapter://path>",
		Short: "Create a public download link (folders are served as ZIP)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			res, err := api.Share(cmd.Context(), args[0], pin, expiresDays)
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(res.Raw))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "URL:     %s\n", res.URL)
			if res.PIN != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "PIN:     %s\n", res.PIN)
			}
			if res.ExpiresAt != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Expires: %s\n", res.ExpiresAt.Local().Format("2006-01-02 15:04"))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&pin, "pin", false, "protect the link with a server-generated PIN")
	c.Flags().IntVar(&expiresDays, "expires-days", 0, "expire the link after N days (0 = never)")
	return quiet(c)
}

// ─────────────────── rendering helpers ───────────────────

// humanSize formats a byte count for the table view (1 decimal, IEC-ish
// thousands of 1024).
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// fmtMillis renders a Unix-millisecond stamp as local "YYYY-MM-DD HH:MM"
// ("-" for unknown).
func fmtMillis(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04")
}

// truncateLine collapses a snippet onto one line and caps it at max
// runes (ellipsis when cut) so the search table never wraps.
func truncateLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}
