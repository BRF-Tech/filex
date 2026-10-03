// client_files.go - the `filex client` commands that reach the operations
// queue and the per-file extras: cp (and mv's multi-source form), trash,
// versions, tag, actions/run, archive, and share ls|rm. The wire calls live in
// internal/cliclient; this file is cobra plumbing and rendering.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

// ─────────────────── cp / mv ───────────────────

func clientCpCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "cp <adapter://source>... <adapter://target>",
		Short: "Copy remote items (target: existing folder, or full new path); across storages too",
		Long: "Copy a file or a folder on the server. The target is an existing folder (or ends in /),\n" +
			"which receives the item under its own name, or the copy's full new path. Source and target\n" +
			"may be on different storages. Nothing is ever replaced: a copy into a folder that already\n" +
			"holds the name is saved beside it as name-copy.ext, and a full target path that is taken is\n" +
			"refused before anything is sent. With several sources the target must be an existing folder.\n" +
			"The command waits for the server's operation and fails when it did.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTransfer(cmd, opts, "copy", args)
		},
	}
	return quiet(c)
}

// runTransfer drives cp and mv: one queued operation per source, each followed
// to its end before the next starts, so an error names the item it is about
// and everything before it is done.
func runTransfer(cmd *cobra.Command, opts *clientOpts, verb string, args []string) error {
	api, err := opts.api(true)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	srcs, dst := args[:len(args)-1], args[len(args)-1]
	if len(srcs) > 1 {
		isDir, err := api.IsDir(ctx, dst)
		if err != nil {
			return err
		}
		if !isDir {
			return fmt.Errorf("%s: with several sources the target must be an existing folder", dst)
		}
		if !strings.HasSuffix(dst, "/") {
			dst += "/"
		}
	}
	word, done := "cp", "Copied"
	if verb == "move" {
		word, done = "mv", "Moved"
	}
	for _, src := range srcs {
		var t *cliclient.Transfer
		if verb == "move" {
			t, err = api.Move(ctx, src, dst)
		} else {
			t, err = api.Copy(ctx, src, dst)
		}
		if err != nil {
			return authHint(fmt.Errorf("%s %s: %w", word, src, err))
		}
		if opts.json {
			fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(string(t.Raw)))
			continue
		}
		if t.Into {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s into %s\n", done, src, t.Folder.String())
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s -> %s\n", done, src, t.To.String())
		}
	}
	return nil
}

// ─────────────────── trash ───────────────────

func clientTrashCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "trash",
		Short: "List the trash and restore what `rm` put there",
	}
	c.AddCommand(clientTrashLsCmd(opts), clientTrashRestoreCmd(opts))
	return c
}

func clientTrashLsCmd(opts *clientOpts) *cobra.Command {
	var storageID int64
	var limit, offset int
	c := &cobra.Command{
		Use:   "ls",
		Short: "List the items in the trash you may see, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			page, err := api.Trash(cmd.Context(), storageID, limit, offset)
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(page.Raw))
				return nil
			}
			if len(page.Entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "The trash is empty.")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tDELETED\tSIZE\tBY\tLOCATION")
			for _, e := range page.Entries {
				by := e.DeletedByName
				if e.DeletedBySelf {
					by = "you"
				}
				if by == "" {
					by = "-"
				}
				loc := e.Location()
				if e.E2eRoot != "" {
					loc += "  (encrypted folder " + e.E2eRoot + ")"
				}
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", e.ID, fmtTime(e.DeletedAt), humanSize(e.Size), by, loc)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if shown := page.Offset + len(page.Entries); shown < page.Total {
				fmt.Fprintf(cmd.OutOrStdout(), "%d-%d of %d; --offset %d for the next page\n", page.Offset+1, shown, page.Total, shown)
			}
			return nil
		},
	}
	c.Flags().Int64Var(&storageID, "storage-id", 0, "only this storage's trash (0 = every storage)")
	c.Flags().IntVar(&limit, "limit", 50, "entries per page (at most 500)")
	c.Flags().IntVar(&offset, "offset", 0, "skip this many entries")
	return quiet(c)
}

func clientTrashRestoreCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "restore <id>...",
		Short: "Put trash entries back where they were deleted from (ids from `trash ls`)",
		Long: "Restore trash entries to their original place. A place that is taken again is refused\n" +
			"(409 EXISTS) and the entry stays in the trash: nothing is replaced.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			for _, id := range ids {
				raw, err := api.RestoreTrash(cmd.Context(), id)
				if err != nil {
					return authHint(fmt.Errorf("restore %d: %w", id, err))
				}
				if opts.json {
					fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(string(raw)))
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Restored %d\n", id)
			}
			return nil
		},
	}
	return quiet(c)
}

// ─────────────────── versions ───────────────────

func clientVersionsCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "versions",
		Short: "List a file's versions and restore one",
	}
	c.AddCommand(clientVersionsLsCmd(opts), clientVersionsRestoreCmd(opts))
	return c
}

// nodeIDArg is a file's catalogue id: --id when given, else looked up from its
// path's folder listing.
func nodeIDArg(cmd *cobra.Command, api *cliclient.Client, id int64, remote string) (int64, error) {
	if id > 0 {
		return id, nil
	}
	if remote == "" {
		return 0, errors.New("name the file (adapter://path) or pass --id")
	}
	return api.NodeID(cmd.Context(), remote)
}

func clientVersionsLsCmd(opts *clientOpts) *cobra.Command {
	var id int64
	c := &cobra.Command{
		Use:   "ls <adapter://file>",
		Short: "List the recorded versions of a file, newest first",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			nodeID, err := nodeIDArg(cmd, api, id, firstArg(args))
			if err != nil {
				return authHint(err)
			}
			list, err := api.Versions(cmd.Context(), nodeID)
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(list.Raw))
				return nil
			}
			if len(list.Versions) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No versions recorded.")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "VERSION\tID\tSIZE\tRECORDED")
			for _, v := range list.Versions {
				fmt.Fprintf(tw, "%d\t%d\t%s\t%s\n", v.VersionN, v.ID, humanSize(v.Size), fmtTime(v.CreatedAt))
			}
			return tw.Flush()
		},
	}
	c.Flags().Int64Var(&id, "id", 0, "the file's id instead of its path")
	return quiet(c)
}

func clientVersionsRestoreCmd(opts *clientOpts) *cobra.Command {
	var id int64
	c := &cobra.Command{
		Use:   "restore <adapter://file> <version-id>",
		Short: "Make a recorded version the file's content (the current content is kept as a version)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			remote, verArg := "", args[len(args)-1]
			if len(args) == 2 {
				remote = args[0]
			}
			verID, err := strconv.ParseInt(verArg, 10, 64)
			if err != nil || verID <= 0 {
				return fmt.Errorf("bad version id %q: use the ID column of `versions ls`", verArg)
			}
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			nodeID, err := nodeIDArg(cmd, api, id, remote)
			if err != nil {
				return authHint(err)
			}
			raw, err := api.RestoreVersion(cmd.Context(), nodeID, verID)
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(string(raw)))
				return nil
			}
			what := remote
			if what == "" {
				what = "file " + strconv.FormatInt(nodeID, 10)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Restored version %d of %s\n", verID, what)
			return nil
		},
	}
	c.Flags().Int64Var(&id, "id", 0, "the file's id instead of its path (then give only the version id)")
	return quiet(c)
}

// ─────────────────── tag ───────────────────

func clientTagCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "tag",
		Short: "Read and change tags (personal or team)",
		Long: "A personal tag is yours alone, like a star; a team tag is seen by everyone in your\n" +
			"organisation who can see the file, and changing one needs edit permission on it.",
	}
	c.AddCommand(clientTagLsCmd(opts), clientTagAddCmd(opts), clientTagRmCmd(opts), clientTagFilesCmd(opts))
	return c
}

func clientTagLsCmd(opts *clientOpts) *cobra.Command {
	var id int64
	c := &cobra.Command{
		Use:   "ls [adapter://path]",
		Short: "A file's tags (no argument: every tag you can see)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			var items []cliclient.TagItem
			var raw []byte
			if len(args) == 0 && id == 0 {
				items, raw, err = api.AllTags(cmd.Context())
				if err != nil {
					return authHint(err)
				}
			} else {
				nodeID, err := nodeIDArg(cmd, api, id, firstArg(args))
				if err != nil {
					return authHint(err)
				}
				tags, err := api.TagsOf(cmd.Context(), nodeID)
				if err != nil {
					return authHint(err)
				}
				items, raw = tags.Items, tags.Raw
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return nil
			}
			renderTags(cmd.OutOrStdout(), items)
			return nil
		},
	}
	c.Flags().Int64Var(&id, "id", 0, "the file's id instead of its path")
	return quiet(c)
}

func renderTags(w io.Writer, items []cliclient.TagItem) {
	if len(items) == 0 {
		fmt.Fprintln(w, "No tags.")
		return
	}
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TAG\tKIND")
	for _, it := range items {
		fmt.Fprintf(tw, "%s\t%s\n", it.Name, it.Kind)
	}
	_ = tw.Flush()
}

// tagKind reads --team / --personal into a kind ("" = neither given).
func tagKind(team, personal bool) (string, error) {
	switch {
	case team && personal:
		return "", errors.New("--team and --personal are each other's opposite: give one")
	case team:
		return cliclient.TagTeam, nil
	case personal:
		return cliclient.TagPersonal, nil
	}
	return "", nil
}

func clientTagAddCmd(opts *clientOpts) *cobra.Command {
	var team bool
	c := &cobra.Command{
		Use:   "add <adapter://path> <tag>...",
		Short: "Add tags to a file (personal; --team for team tags)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := cliclient.TagPersonal
			if team {
				kind = cliclient.TagTeam
			}
			return editTags(cmd, opts, args[0], cliclient.TagEdit{Add: args[1:], Kind: kind})
		},
	}
	c.Flags().BoolVar(&team, "team", false, "add team tags (seen by everyone who can see the file; needs edit permission)")
	return quiet(c)
}

func clientTagRmCmd(opts *clientOpts) *cobra.Command {
	var team, personal bool
	c := &cobra.Command{
		Use:   "rm <adapter://path> <tag>...",
		Short: "Remove tags from a file (both kinds unless --team or --personal)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := tagKind(team, personal)
			if err != nil {
				return err
			}
			return editTags(cmd, opts, args[0], cliclient.TagEdit{Remove: args[1:], Kind: kind})
		},
	}
	c.Flags().BoolVar(&team, "team", false, "remove only team tags of that name")
	c.Flags().BoolVar(&personal, "personal", false, "remove only your personal tags of that name")
	return quiet(c)
}

func editTags(cmd *cobra.Command, opts *clientOpts, remote string, edit cliclient.TagEdit) error {
	api, err := opts.api(true)
	if err != nil {
		return err
	}
	nodeID, err := api.NodeID(cmd.Context(), remote)
	if err != nil {
		return authHint(err)
	}
	tags, err := api.EditTags(cmd.Context(), nodeID, edit)
	if err != nil {
		return authHint(err)
	}
	if opts.json {
		fmt.Fprintln(cmd.OutOrStdout(), string(tags.Raw))
		return nil
	}
	renderTags(cmd.OutOrStdout(), tags.Items)
	return nil
}

func clientTagFilesCmd(opts *clientOpts) *cobra.Command {
	var team, personal bool
	var limit int
	c := &cobra.Command{
		Use:   "files <tag>",
		Short: "The files carrying a tag, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := tagKind(team, personal)
			if err != nil {
				return err
			}
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			files, raw, err := api.TaggedFiles(cmd.Context(), args[0], kind, limit)
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return nil
			}
			if len(files) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No files carry this tag.")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "TYPE\tSIZE\tPATH")
			for _, f := range files {
				size := "-"
				if f.Type == "file" {
					size = humanSize(f.Size)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", f.Type, size, f.Location())
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&team, "team", false, "only the team tag of that name")
	c.Flags().BoolVar(&personal, "personal", false, "only your personal tag of that name")
	c.Flags().IntVar(&limit, "limit", 500, "maximum number of files (at most 1000)")
	return quiet(c)
}

// ─────────────────── actions / run ───────────────────

func clientActionsCmd(opts *clientOpts) *cobra.Command {
	var lang string
	c := &cobra.Command{
		Use:   "actions",
		Short: "List the app actions you may run (for `filex client run`)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			actions, raw, err := api.AppActions(cmd.Context())
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return nil
			}
			if len(actions) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No app actions are available to you.")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "APP\tACTION\tFILES\tLABEL")
			for _, a := range actions {
				applies := a.Applies.Kind
				if applies == "" {
					applies = "file"
				}
				if len(a.Applies.Ext) > 0 {
					applies += " ." + strings.Join(a.Applies.Ext, " .")
				}
				label := a.LabelIn(lang)
				if a.View != "" {
					label += " (form: --param)"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.Plugin, a.ID, applies, label)
			}
			return tw.Flush()
		},
	}
	c.Flags().StringVar(&lang, "lang", "en", "language of the labels")
	return quiet(c)
}

func clientRunCmd(opts *clientOpts) *cobra.Command {
	var params, jsonParams []string
	c := &cobra.Command{
		Use:   "run <app> <action> <adapter://path>...",
		Short: "Run an app's action on files (e.g. Convert) and wait for its result",
		Example: "  filex client run convert convert docs://data/table.csv --param target=xlsx\n" +
			"  filex client run sign request docs://contracts/nda.pdf --param-json signers='[\"ada@example.com\"]'",
		Long: "Run an action an installed app offers in the explorer's file menu (`filex client actions`\n" +
			"lists them). The files are on one storage. --param key=value passes a text value,\n" +
			"--param-json key=<json> a number, a list or an object. An action that asks for its input\n" +
			"in a form needs its fields as parameters. The command waits for the job and prints the\n" +
			"files it wrote.",
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := parseParams(params, jsonParams)
			if err != nil {
				return err
			}
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			op, raw, err := api.RunAction(cmd.Context(), args[0], args[1], args[2:], p)
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(string(raw)))
				return nil
			}
			msg := op.Message
			if msg == "" {
				msg = "done"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s/%s: %s\n", args[0], args[1], msg)
			for _, o := range op.Outputs {
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", o.Path)
			}
			return nil
		},
	}
	c.Flags().StringArrayVar(&params, "param", nil, "an action parameter as key=value (text; repeatable)")
	c.Flags().StringArrayVar(&jsonParams, "param-json", nil, "an action parameter as key=<json> (repeatable)")
	return quiet(c)
}

// parseParams builds an action's params from --param and --param-json. nil
// when none was given, so an action with a form can say it needs input.
func parseParams(text, raw []string) (map[string]any, error) {
	if len(text) == 0 && len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(text)+len(raw))
	for _, kv := range text {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("bad --param %q: want key=value", kv)
		}
		out[strings.TrimSpace(k)] = v
	}
	for _, kv := range raw {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("bad --param-json %q: want key=<json>", kv)
		}
		var val any
		if err := json.Unmarshal([]byte(v), &val); err != nil {
			return nil, fmt.Errorf("bad --param-json %s: %v", k, err)
		}
		out[strings.TrimSpace(k)] = val
	}
	return out, nil
}

// ─────────────────── archive ───────────────────

func clientArchiveCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "archive",
		Short: "Create and extract archives on the server (ZIP, 7z, TAR; passwords)",
		Long: "The server does the work: nothing is downloaded. Which formats are allowed, and the\n" +
			"size limits, are the administrator's (Settings -> Archives; docs/ARCHIVES.md). A password\n" +
			"is read from standard input with --password-stdin, never from the command line.",
	}
	c.AddCommand(clientArchiveCreateCmd(opts), clientArchiveExtractCmd(opts))
	return c
}

// archivePassword reads one line from stdin (without echo on a terminal).
func archivePassword(cmd *cobra.Command) (string, error) {
	pw, err := readPassword(cmd, bufio.NewReader(cmd.InOrStdin()))
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if pw == "" {
		return "", errors.New("empty password")
	}
	return pw, nil
}

func clientArchiveCreateCmd(opts *clientOpts) *cobra.Command {
	var o cliclient.ArchiveCreateOptions
	var pwStdin bool
	c := &cobra.Command{
		Use:     "create <adapter://archive> <adapter://source>...",
		Short:   "Pack files and folders into a new archive (format from the name, or --format)",
		Example: "  filex client archive create docs://out/report.7z docs://reports/2026 --password-stdin < pw.txt",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if pwStdin {
				pw, err := archivePassword(cmd)
				if err != nil {
					return err
				}
				o.Password = pw
			}
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			_, raw, err := api.CreateArchive(cmd.Context(), args[0], args[1:], o)
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(string(raw)))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", args[0])
			return nil
		},
	}
	c.Flags().StringVar(&o.Format, "format", "", "zip, 7z, tar, tar.gz, tar.bz2, tar.xz or tar.zst (default: from the archive's name)")
	c.Flags().BoolVar(&pwStdin, "password-stdin", false, "read the archive's password from standard input (zip and 7z)")
	c.Flags().BoolVar(&o.EncryptNames, "encrypt-names", false, "7z: encrypt the file names too")
	c.Flags().IntVar(&o.Compression, "compression", 0, "compression level 1-9 (default: the server's)")
	return quiet(c)
}

func clientArchiveExtractCmd(opts *clientOpts) *cobra.Command {
	var dest string
	var pwStdin bool
	var members []string
	c := &cobra.Command{
		Use:   "extract <adapter://archive> [adapter://folder]",
		Short: "Extract an archive (default: into its own folder; same storage)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 {
				dest = args[1]
			}
			pw := ""
			if pwStdin {
				var err error
				if pw, err = archivePassword(cmd); err != nil {
					return err
				}
			}
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			_, raw, err := api.ExtractArchive(cmd.Context(), args[0], dest, pw, members)
			if err != nil {
				if errors.Is(err, cliclient.ErrArchivePassword) && !pwStdin {
					return fmt.Errorf("%w - give it with --password-stdin", err)
				}
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(string(raw)))
				return nil
			}
			where := dest
			if where == "" {
				where = "its folder"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Extracted %s into %s\n", args[0], where)
			return nil
		},
	}
	c.Flags().BoolVar(&pwStdin, "password-stdin", false, "read the archive's password from standard input")
	c.Flags().StringArrayVar(&members, "member", nil, "extract only this entry (repeatable; the path inside the archive)")
	return quiet(c)
}

// ─────────────────── share ls / rm ───────────────────

func clientShareLsCmd(opts *clientOpts) *cobra.Command {
	var active bool
	var limit, offset int
	c := &cobra.Command{
		Use:   "ls",
		Short: "List the public links you created, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			page, err := api.MyShares(cmd.Context(), active, limit, offset)
			if err != nil {
				return authHint(err)
			}
			if opts.json {
				fmt.Fprintln(cmd.OutOrStdout(), string(page.Raw))
				return nil
			}
			if len(page.Items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No links.")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tKIND\tPIN\tEXPIRES\tDOWNLOADS\tITEM\tURL")
			for _, s := range page.Items {
				pin := "-"
				if s.Share.HasPin {
					pin = "yes"
				}
				exp := "never"
				if s.Share.ExpiresAt != nil {
					exp = fmtTime(*s.Share.ExpiresAt)
				}
				dl := strconv.Itoa(s.Share.DownloadCount)
				if s.Share.MaxDownloads != nil {
					dl += "/" + strconv.Itoa(*s.Share.MaxDownloads)
				}
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Share.ID, s.Share.Kind, pin, exp, dl, s.Location(), s.URL)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if shown := int64(offset + len(page.Items)); shown < page.Total {
				fmt.Fprintf(cmd.OutOrStdout(), "%d-%d of %d; --offset %d for the next page\n", offset+1, shown, page.Total, shown)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&active, "active", false, "leave out expired and used-up links")
	c.Flags().IntVar(&limit, "limit", 50, "links per page (at most 500)")
	c.Flags().IntVar(&offset, "offset", 0, "skip this many links")
	return quiet(c)
}

func clientShareRmCmd(opts *clientOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "rm <id>...",
		Short: "Revoke public links (ids from `share ls`); their URLs stop working at once",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			api, err := opts.api(true)
			if err != nil {
				return err
			}
			for _, id := range ids {
				raw, err := api.Unshare(cmd.Context(), id)
				if err != nil {
					return authHint(fmt.Errorf("revoke %d: %w", id, err))
				}
				if opts.json {
					if s := strings.TrimSpace(string(raw)); s != "" {
						fmt.Fprintln(cmd.OutOrStdout(), s)
					}
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Revoked %d\n", id)
			}
			return nil
		},
	}
	return quiet(c)
}

// ─────────────────── helpers ───────────────────

func parseIDs(args []string) ([]int64, error) {
	ids := make([]int64, 0, len(args))
	for _, a := range args {
		n, err := strconv.ParseInt(strings.TrimSpace(a), 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("bad id %q: want a number from the ID column", a)
		}
		ids = append(ids, n)
	}
	return ids, nil
}

func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// fmtTime renders a server time as local "YYYY-MM-DD HH:MM" ("-" for zero).
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}
