package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/e2edecrypt"
)

// ─────────────────── encrypt ───────────────────
//
// `filex encrypt` is the command-line twin of "Encrypt with E2EE…": it turns
// a folder into an end-to-end encrypted folder, with the browser's key
// hierarchy and formats (internal/e2edecrypt writes them; the browser's
// lib/e2ecrypto.ts is the other implementation of the same bytes). Two
// forms:
//
//	filex encrypt <folder>            a folder on this machine -> a NEW folder,
//	                                  "<folder>-encrypted", to upload
//	filex encrypt <adapter://folder>  a folder on a filex server, encrypted
//	                                  WHERE IT IS, as the browser does it
//
// Everything is encrypted on this machine; the server only ever receives
// ciphertext and the key file. As with `filex decrypt`, the password is never
// an argument or an environment variable: it is typed without echo, or piped
// as one line with --password-stdin.

// Exit status of a run that left files not encrypted: run it again.
const exitEncryptIncomplete = 6

type encryptOpts struct {
	out             string
	level           int
	fromStdin       bool
	recovery        bool
	recoveryKeyFile string
	escrowKey       string
	keepVersions    bool
	keepTrash       bool
	quiet           bool
	url, token      string
}

func encryptCmd() *cobra.Command {
	o := &encryptOpts{}
	c := &cobra.Command{
		Use:   "encrypt <folder | adapter://folder>",
		Short: "Make a folder an end-to-end encrypted folder (on this machine, or where it is on a filex server)",
		Long: "Encrypts a folder with a password, the way \"Encrypt with E2EE...\" does in the web app:\n" +
			"the same key file, the same file formats, a recovery key shown once. Everything is\n" +
			"encrypted on this machine; the server only ever receives ciphertext.\n\n" +
			"A LOCAL folder is read and left as it is: the encrypted folder is written next to it,\n" +
			"as <folder>-encrypted (or -o). Upload that folder to filex - the web app, or\n" +
			"`filex client upload -r` - and it opens there with the password.\n\n" +
			"A folder on a filex SERVER (adapter://path) is encrypted where it is: its key file is\n" +
			"written first, then every file is replaced by its ciphertext - only if it is still the\n" +
			"file that was listed, and with no plaintext version kept. At level 2 the names are\n" +
			"encrypted after the contents. At the end filex drops the thumbnails and the search text\n" +
			"it held for the folder, and its older versions and trash entries unless --keep-versions\n" +
			"or --keep-trash. The connection is the one `filex client` uses (--url/--token,\n" +
			"FILEX_URL/FILEX_TOKEN, or `filex client login`). The server's encryption policy is\n" +
			"asked first: where it does not let this account encrypt the folder, or wants an\n" +
			"administrator's approval, it says so before any password is asked.\n\n" +
			"Files over 200 MB are written in the streamed format (STREAM), as the browser writes\n" +
			"them; nothing is held in memory but one file up to 200 MB.\n\n" +
			"Stopped half-way (Ctrl-C, a dropped connection, a file that changed meanwhile), it\n" +
			"continues: run the same command again. It asks for the folder password (or, with\n" +
			"--recovery-key, the recovery key) and does only what is left.\n\n" +
			"The password is asked twice on the terminal, without echo (at least 8 characters). To\n" +
			"script it, pipe it as one line and pass --password-stdin. The recovery key is shown\n" +
			"once, as soon as the key file exists; --recovery-key-file writes it to a new file\n" +
			"instead.\n\n" +
			"Exit status: 0 done, 5 wrong password or recovery key, 6 some files are not encrypted\n" +
			"yet (run it again), 7 the folder needs a newer filex, 1 anything else.",
		Example: "  filex encrypt ./Kasa\n" +
			"  filex encrypt ./Kasa --level 2 -o ./Kasa-to-upload\n" +
			"  filex encrypt docs://Kasa\n" +
			"  filex encrypt docs://Kasa --level 2 --keep-versions\n" +
			"  pass show kasa | filex encrypt docs://Kasa --password-stdin --recovery-key-file ./kasa.key",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.level != 1 && o.level != 2 {
				return errors.New("--level is 1 (contents) or 2 (contents and names)")
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			if strings.Contains(args[0], "://") {
				return runEncryptRemote(ctx, cmd, o, args[0])
			}
			return runEncryptLocal(ctx, cmd, o, args[0])
		},
	}
	f := c.Flags()
	f.StringVarP(&o.out, "output", "o", "", "local folder only: where to write the encrypted folder (default: <folder>-encrypted next to it); must not exist")
	f.IntVar(&o.level, "level", 1, "1 = contents only (opens in filex 0.31 and later), 2 = contents and names (0.48 and later)")
	f.BoolVar(&o.fromStdin, "password-stdin", false, "read the password (or, resuming, the recovery key) as one line from stdin")
	f.BoolVar(&o.recovery, "recovery-key", false, "resuming a stopped run: unlock it with the recovery key instead of the password")
	f.StringVar(&o.recoveryKeyFile, "recovery-key-file", "", "write the new recovery key to this file (created, never overwritten) instead of showing it")
	f.StringVar(&o.escrowKey, "escrow-public-key", "", "local folder only: an installation's escrow public key (base64 SPKI, or @file) to seal the folder key to")
	f.BoolVar(&o.keepVersions, "keep-versions", false, "server folder only: keep the older (plaintext) versions of its files")
	f.BoolVar(&o.keepTrash, "keep-trash", false, "server folder only: keep the trash entries that came from it")
	f.BoolVarP(&o.quiet, "quiet", "q", false, "print only warnings and the summary")
	f.StringVar(&o.url, "url", "", "server folder only: filex server URL (default: $FILEX_URL or ~/.filex/cli.yaml)")
	f.StringVar(&o.token, "token", "", "server folder only: API or session token (default: $FILEX_TOKEN, else the saved session)")
	return quiet(c)
}

// encryptExit maps what stopped a run to the documented exit statuses.
func encryptExit(err error) error {
	var ue *e2edecrypt.UnsupportedError
	switch {
	case errors.Is(err, e2edecrypt.ErrWrongPassword):
		return &exitError{code: exitDecryptWrongSecret, err: errors.New("wrong password")}
	case errors.Is(err, e2edecrypt.ErrWrongRecoveryKey):
		return &exitError{code: exitDecryptWrongSecret, err: errors.New("wrong recovery key (or this folder has none)")}
	case errors.As(err, &ue):
		return &exitError{code: exitDecryptUnsupported, err: err}
	}
	return err
}

// newPassword asks for a new folder password: twice on a terminal, once from
// stdin. The rule is the web dialog's.
func newPassword(cmd *cobra.Command, fromStdin bool) (string, error) {
	pw, err := readSecret(cmd, "New folder password", fromStdin)
	if err != nil {
		return "", err
	}
	if p := e2edecrypt.PasswordProblem(pw); p != "" {
		return "", errors.New(p)
	}
	if isTerminal(cmd) && !fromStdin {
		again, err := readSecret(cmd, "The same password again", false)
		if err != nil {
			return "", err
		}
		if again != pw {
			return "", errors.New("the two passwords differ - nothing was written")
		}
	}
	return pw, nil
}

func isTerminal(cmd *cobra.Command) bool {
	f, ok := cmd.InOrStdin().(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// warnBeforePassword is the web dialog's acknowledgement, said before the
// password is asked: the server cannot help with a lost password.
func warnBeforePassword(cmd *cobra.Command, escrowKID string) {
	w := cmd.ErrOrStderr()
	fmt.Fprintln(w, "End-to-end encryption: filex cannot open this folder without its password or its")
	fmt.Fprintln(w, "recovery key. Lose both and the files are gone for good.")
	if escrowKID != "" {
		fmt.Fprintf(w, "This server has key escrow on: its operator holds a second key (%s) that opens\n", escrowKID)
		fmt.Fprintln(w, "this folder too, and using it notifies you.")
	}
}

// showRecoveryKey hands out the recovery key exactly once: into a new file
// (0600), or on the terminal with a confirmation that it was saved, as the
// web dialog asks for a tick before it closes.
func showRecoveryKey(cmd *cobra.Command, o *encryptOpts, key string) error {
	w := cmd.ErrOrStderr()
	if o.recoveryKeyFile != "" {
		err := writeNewFile(o.recoveryKeyFile, key+"\n")
		if err == nil {
			fmt.Fprintf(w, "Recovery key written to %s - keep it apart from the password.\n", o.recoveryKeyFile)
			return nil
		}
		// The key file on the server is already written: the recovery key
		// must not be lost with the file that could not be made.
		fmt.Fprintf(w, "warning: the recovery key could not be written to %s (%v); here it is instead.\n", o.recoveryKeyFile, err)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "RECOVERY KEY - shown once, never stored by filex. It opens the folder without the password:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "    "+key)
	fmt.Fprintln(w)
	if !isTerminal(cmd) || o.fromStdin {
		return nil
	}
	rd := bufio.NewReader(cmd.InOrStdin())
	for {
		fmt.Fprint(w, "Saved it somewhere safe? Type yes to continue: ")
		line, err := rd.ReadString('\n')
		if strings.EqualFold(strings.TrimSpace(line), "yes") {
			fmt.Fprintln(w)
			return nil
		}
		if err != nil {
			return errors.New("the recovery key was not confirmed; the key file is written - run the same command again to continue")
		}
	}
}

// writeNewFile creates p (0600) with content, never overwriting one.
func writeNewFile(p, content string) error {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := io.WriteString(f, content)
	return errors.Join(werr, f.Close())
}

// checkRecoveryKeyFile refuses, before anything is written, a
// --recovery-key-file that is already there.
func checkRecoveryKeyFile(o *encryptOpts) error {
	if o.recoveryKeyFile == "" {
		return nil
	}
	if _, err := os.Lstat(o.recoveryKeyFile); err == nil {
		return fmt.Errorf("%s already exists; --recovery-key-file never overwrites a file", o.recoveryKeyFile)
	}
	return nil
}

func readEscrowFlag(v string) (string, error) {
	if after, ok := strings.CutPrefix(v, "@"); ok {
		b, err := os.ReadFile(after)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	return strings.TrimSpace(v), nil
}

// ── a folder on this machine ─────────────────────────────────────────────

func runEncryptLocal(ctx context.Context, cmd *cobra.Command, o *encryptOpts, in string) error {
	if o.keepVersions || o.keepTrash || o.url != "" || o.token != "" {
		return errors.New("--keep-versions, --keep-trash, --url and --token are for a folder on a server (adapter://path)")
	}
	escrow := ""
	if o.escrowKey != "" {
		var err error
		if escrow, err = readEscrowFlag(o.escrowKey); err != nil {
			return err
		}
	}
	job, err := e2edecrypt.OpenEncrypt(in, e2edecrypt.EncryptTreeOptions{Out: o.out, Names: o.level == 2, EscrowPublicKey: escrow})
	if err != nil {
		return encryptExit(err)
	}
	w, ew := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if job.Resuming() {
		if cmd.Flags().Changed("level") && (o.level == 2) != job.Names() {
			return fmt.Errorf("%s.partial was started at the other level - remove it to start over at --level %d", job.Out(), o.level)
		}
		fmt.Fprintf(ew, "Continuing a stopped run into %s.partial\n", job.Out())
		label := "Folder password"
		if o.recovery {
			label = "Recovery key"
		}
		secret, err := readSecret(cmd, label, o.fromStdin)
		if err != nil {
			return err
		}
		if secret == "" {
			return fmt.Errorf("no %s given", strings.ToLower(label))
		}
		if err := job.Unlock(secret, o.recovery); err != nil {
			return encryptExit(err)
		}
	} else {
		if o.recovery {
			return errors.New("--recovery-key is for continuing a stopped run; this one starts with a new password")
		}
		if err := checkRecoveryKeyFile(o); err != nil {
			return err
		}
		warnBeforePassword(cmd, escrowKIDOf(escrow))
		pw, err := newPassword(cmd, o.fromStdin)
		if err != nil {
			return err
		}
		key, err := job.Create(pw)
		if err != nil {
			return err
		}
		if err := showRecoveryKey(cmd, o, key); err != nil {
			return err
		}
	}
	res, err := job.Run(ctx, func(rel string, size int64) {
		if !o.quiet {
			fmt.Fprintf(ew, "  %s (%s)\n", rel, humanSize(size))
		}
	})
	if err != nil {
		return &exitError{code: exitEncryptIncomplete, err: fmt.Errorf("%w - what is written stays in %s.partial; run the same command again to continue", err, job.Out())}
	}
	for _, warn := range res.Warnings {
		fmt.Fprintln(ew, "warning: "+warn)
	}
	fmt.Fprintf(w, "Encrypted %d file(s) and %d folder(s) into %s", res.Files, res.Dirs, res.Out)
	if res.Kept > 0 {
		fmt.Fprintf(w, " (%d already written by the stopped run)", res.Kept)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Upload it to filex to use it, e.g. filex client upload -r %s <adapter://folder>\n", res.Out)
	return nil
}

// escrowKIDOf is the short name of an escrow key, for the notice; "" when
// none or unreadable (CreateFolder refuses an unreadable one anyway).
func escrowKIDOf(spki string) string {
	if spki == "" {
		return ""
	}
	return e2edecrypt.EscrowKID(spki)
}

// ── a folder on a filex server, where it is ──────────────────────────────

func runEncryptRemote(ctx context.Context, cmd *cobra.Command, o *encryptOpts, remote string) error {
	if o.out != "" || o.escrowKey != "" {
		return errors.New("--output and --escrow-public-key are for a local folder; a server folder is encrypted where it is, with the server's own escrow key when it has one")
	}
	api, err := (&clientOpts{url: o.url, token: o.token}).api(true)
	if err != nil {
		return err
	}
	root, err := cliclient.ParseRemotePath(remote)
	if err != nil {
		return err
	}
	if root.IsRoot() {
		return errors.New("a storage root cannot be encrypted; name a folder in it")
	}
	rootWire := root.String()
	top, err := api.List(ctx, rootWire)
	if err != nil {
		return authHint(err)
	}
	w, ew := cmd.OutOrStdout(), cmd.ErrOrStderr()
	r := &remoteRun{ctx: ctx, api: api, root: root, rootWire: rootWire}

	switch strings.TrimRight(top.E2ERoot, "/") {
	case "":
		if o.recovery {
			return errors.New("--recovery-key is for continuing a stopped run; this folder is not encrypted yet")
		}
		if err := r.start(cmd, o); err != nil {
			return encryptExit(err)
		}
	case strings.TrimRight(rootWire, "/"):
		if err := r.resume(cmd, o); err != nil {
			if errors.Is(err, errNothingToDo) {
				fmt.Fprintln(w, rootWire+" is already an encrypted folder - nothing to do")
				return nil
			}
			return encryptExit(err)
		}
		fmt.Fprintf(ew, "Continuing the encryption of %s\n", rootWire)
	default:
		return fmt.Errorf("%s is inside the encrypted folder %s; encrypted folders cannot be nested", rootWire, top.E2ERoot)
	}
	return r.finish(cmd, o)
}

var errNothingToDo = errors.New("nothing to do")

// remoteRun is one in-place encryption on a server.
type remoteRun struct {
	ctx      context.Context
	api      *cliclient.Client
	root     cliclient.RemotePath
	rootWire string
	marker   []byte
	keys     *e2edecrypt.Keys
	tempDir  string
}

func (r *remoteRun) markerPath() string { return r.root.Join(e2edecrypt.MarkerName).String() }

func (r *remoteRun) writeMarker(data []byte, expect string) error {
	if err := r.api.WriteSmall(r.ctx, r.root, e2edecrypt.MarkerName, data, expect); err != nil {
		return fmt.Errorf("writing the key file: %w", authHint(err))
	}
	r.marker = data
	return nil
}

// start makes a plain folder an encrypted one whose conversion is under way:
// no encrypted folder inside it, the escrow notice, the password, the key file
// FIRST, then the recovery key.
func (r *remoteRun) start(cmd *cobra.Command, o *encryptOpts) error {
	if err := checkRecoveryKeyFile(o); err != nil {
		return err
	}
	nested, err := r.holdsEncryptedFolder()
	if err != nil {
		return authHint(err)
	}
	if nested != "" {
		return fmt.Errorf("%s holds the encrypted folder %s; encrypted folders cannot be nested, so nothing was changed", r.rootWire, nested)
	}
	// Who may encrypt is the server's question (the tenant's encryption
	// policy), asked BEFORE the password: a refusal at the key file would
	// come after somebody typed a password twice for nothing.
	if err := r.mayEncrypt(); err != nil {
		return err
	}
	escrow, err := r.api.E2EEscrowKey(r.ctx)
	if err != nil {
		return fmt.Errorf("reading whether the server has key escrow: %w", authHint(err))
	}
	warnBeforePassword(cmd, escrowKIDOf(escrow))
	pw, err := newPassword(cmd, o.fromStdin)
	if err != nil {
		return err
	}
	made, err := e2edecrypt.CreateFolder(pw, e2edecrypt.CreateOptions{
		EscrowPublicKey: escrow,
		EncryptNames:    o.level == 2,
		// The entries still have their plaintext names until the name pass.
		NamesPending: o.level == 2,
	})
	if err != nil {
		return err
	}
	marker, err := e2edecrypt.StartConversion(made.Marker, time.Now(), &e2edecrypt.Cleanup{Versions: !o.keepVersions, Trash: !o.keepTrash})
	if err != nil {
		return err
	}
	// "none": a key file somebody else wrote meanwhile is not overwritten.
	if err := r.writeMarker(marker, "none"); err != nil {
		return err
	}
	r.keys = made.Keys
	return showRecoveryKey(cmd, o, made.RecoveryKey)
}

// mayEncrypt asks the server whether this account may encrypt the folder
// where it is, and says why not in words before anything is asked or written.
// An answer the CLI does not know reads as allowed: the key file's write is
// asked again by the server, which decides.
func (r *remoteRun) mayEncrypt() error {
	answer, reason, err := r.api.E2EAllowed(r.ctx, r.rootWire, "folder")
	if err != nil {
		return fmt.Errorf("asking whether this account may encrypt %s: %w", r.rootWire, authHint(err))
	}
	switch answer {
	case "request":
		return fmt.Errorf("%s: encrypting this folder needs an administrator's approval first (the encryption policy). "+
			"Ask for it in the web app (the folder's menu: Request encryption...), then run this again; nothing was changed", r.rootWire)
	case "denied":
		return fmt.Errorf("%s: encrypting here is not allowed: %s; nothing was changed", r.rootWire, encryptRefusal(reason))
	}
	return nil
}

// encryptRefusal words the layer of the policy that said no
// (internal/e2epolicy Reason).
func encryptRefusal(reason string) string {
	switch reason {
	case "tenant_disabled":
		return "the platform operator has switched encryption off for this organisation"
	case "policy_off":
		return "an administrator has switched encryption off"
	case "admins_only":
		return "only administrators may encrypt here"
	case "permission":
		return "your role does not allow encrypting here"
	}
	return "the encryption policy does not allow it"
}

// resume unlocks the key file of a folder whose encryption was started (here
// or in the browser) and did not finish.
func (r *remoteRun) resume(cmd *cobra.Command, o *encryptOpts) error {
	data, err := r.api.ReadSmall(r.ctx, r.markerPath(), 1<<20)
	if err != nil {
		return fmt.Errorf("reading the key file: %w", authHint(err))
	}
	m, err := e2edecrypt.ParseMarker(data)
	if err != nil {
		return err
	}
	if !m.ConvPending && !(m.HasNames() && m.Names.Pending) {
		return errNothingToDo
	}
	if cmd.Flags().Changed("level") && (o.level == 2) != m.HasNames() {
		return fmt.Errorf("this folder's encryption was started at level %d; continue it without --level, or at that level", map[bool]int{true: 2, false: 1}[m.HasNames()])
	}
	label := "Folder password"
	if o.recovery {
		label = "Recovery key"
	}
	secret, err := readSecret(cmd, label, o.fromStdin)
	if err != nil {
		return err
	}
	if secret == "" {
		return fmt.Errorf("no %s given", strings.ToLower(label))
	}
	if o.recovery {
		r.keys, err = m.UnlockRecoveryKey(secret)
	} else {
		r.keys, err = m.UnlockPassword(secret)
	}
	if err != nil {
		return err
	}
	r.marker = data
	return nil
}

// holdsEncryptedFolder walks the folder for an encrypted folder inside it.
func (r *remoteRun) holdsEncryptedFolder() (string, error) {
	queue := []string{r.rootWire}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		list, err := r.api.List(r.ctx, dir)
		if err != nil {
			return "", err
		}
		if dir != r.rootWire && list.E2ERoot != "" {
			return list.E2ERoot, nil
		}
		for _, f := range list.Files {
			if f.Type != "dir" {
				continue
			}
			if f.E2E {
				return f.Path, nil
			}
			queue = append(queue, f.Path)
		}
	}
	return "", nil
}

// finish runs what is left: the contents, then (level 2) the names, then the
// key file without `conv` and the cleanup it asked for.
func (r *remoteRun) finish(cmd *cobra.Command, o *encryptOpts) error {
	w, ew := cmd.OutOrStdout(), cmd.ErrOrStderr()
	m, err := e2edecrypt.ParseMarker(r.marker)
	if err != nil {
		return encryptExit(err)
	}
	prog := &e2edecrypt.ConvertProgress{}
	if !o.quiet {
		prog.OnFile = func(p string, size int64) {
			fmt.Fprintf(ew, "  %s (%s)\n", p, humanSize(size))
		}
	}
	if m.ConvPending {
		err := e2edecrypt.RunConversion(r.rootWire, r, prog)
		if r.ctx.Err() != nil || errors.Is(err, e2edecrypt.ErrStopped) {
			return &exitError{code: exitEncryptIncomplete, err: fmt.Errorf("stopped after %d file(s); the rest is still plaintext - run the same command again to continue", prog.Done)}
		}
		if err != nil {
			return err
		}
		if prog.Failed > 0 {
			for _, f := range prog.Failures {
				fmt.Fprintf(ew, "not encrypted: %s: %v\n", f.Path, authHint(f.Err))
			}
			return &exitError{code: exitEncryptIncomplete, err: fmt.Errorf("%d file(s) could not be encrypted (changed meanwhile, or not writable); they are still plaintext - run the same command again to continue", prog.Failed)}
		}
	}
	if m.HasNames() && m.Names.Pending {
		np := &e2edecrypt.NamePassProgress{}
		err := e2edecrypt.RunNamePass(r.keys.Names, r.rootWire, namePassIO{r}, np)
		if r.ctx.Err() != nil || errors.Is(err, e2edecrypt.ErrStopped) {
			return &exitError{code: exitEncryptIncomplete, err: errors.New("stopped while encrypting the names - run the same command again to continue")}
		}
		if err != nil {
			return err
		}
		if np.Failed > 0 {
			for _, f := range np.Failures {
				fmt.Fprintf(ew, "name not encrypted: %s: %v\n", f.Path, f.Err)
			}
			return &exitError{code: exitEncryptIncomplete, err: fmt.Errorf("%d name(s) could not be encrypted - run the same command again to continue", np.Failed)}
		}
		next, err := e2edecrypt.FinishNames(r.marker)
		if err != nil {
			return err
		}
		if err := r.writeMarker(next, ""); err != nil {
			return err
		}
	}
	cleanup := e2edecrypt.ConversionCleanup(r.marker)
	if m.ConvPending {
		next, err := e2edecrypt.FinishConversion(r.marker)
		if err != nil {
			return err
		}
		if err := r.writeMarker(next, ""); err != nil {
			return err
		}
	}
	if !m.ConvPending {
		// Only the names were left (a level change the browser started).
		fmt.Fprintf(w, "Encrypted the names in %s\n", r.rootWire)
		return nil
	}
	fmt.Fprintf(w, "Encrypted %s: %d file(s) now, %d already encrypted", r.rootWire, prog.Done, prog.Skipped)
	if prog.Large > 0 {
		fmt.Fprintf(w, ", %d of them over 200 MB", prog.Large)
	}
	fmt.Fprintln(w)
	versions, trash := true, true
	if cleanup != nil {
		versions, trash = cleanup.Versions, cleanup.Trash
	}
	out, err := r.api.E2ECleanup(r.ctx, r.rootWire, versions, trash)
	if err != nil {
		fmt.Fprintf(ew, "warning: the folder is encrypted, but what the server held from before was not removed: %v\n", authHint(err))
		fmt.Fprintln(ew, "(only the folder's owner or an administrator can remove older versions and trash entries)")
		return nil
	}
	fmt.Fprintf(w, "Removed from the server: %d older version(s), %d trash entr(ies), %d thumbnail(s), %d search text(s)\n",
		out.VersionsDeleted, out.TrashPurged, out.ThumbnailsDropped, out.IndexCleared)
	return nil
}

// ── the conversion's view of the server (e2edecrypt.ConvertIO) ────────────

func rowsOf(list *cliclient.ListResult) []e2edecrypt.ConvertRow {
	rows := make([]e2edecrypt.ConvertRow, 0, len(list.Files))
	for _, f := range list.Files {
		mod := f.LastModified
		if mod <= 0 {
			mod = -1
		}
		rows = append(rows, e2edecrypt.ConvertRow{Path: f.Path, Name: f.Basename, Dir: f.Type == "dir", Size: f.Size, Modified: mod})
	}
	return rows
}

func (r *remoteRun) List(dir string) ([]e2edecrypt.ConvertRow, error) {
	list, err := r.api.List(r.ctx, dir)
	if err != nil {
		return nil, err
	}
	return rowsOf(list), nil
}

func (r *remoteRun) Head(p string) ([]byte, error) {
	b, err := r.api.ReadRange(r.ctx, p, 0, 16)
	if errors.Is(err, io.EOF) {
		return nil, nil // an empty file
	}
	return b, err
}

func (r *remoteRun) Stopped() bool { return r.ctx.Err() != nil }

// Convert downloads the file, encrypts it as it arrives into a temporary
// file (ciphertext only - the plaintext never touches this disk), and sends
// that over the original as a conversion write on the listing's condition.
func (r *remoteRun) Convert(dir string, row e2edecrypt.ConvertRow, expect string) error {
	destDir, err := cliclient.ParseRemotePath(dir)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(r.tempDir, "filex-encrypt-*.part")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	pr, pw := io.Pipe()
	go func() {
		_, derr := r.api.DownloadSized(r.ctx, row.Path, pw, row.Size)
		pw.CloseWithError(derr)
	}()
	encErr := e2edecrypt.EncryptFile(tmp, pr, row.Size, r.keys.FMK, nil)
	_ = pr.CloseWithError(errors.New("encryption stopped"))
	if cerr := tmp.Close(); encErr == nil {
		encErr = cerr
	}
	if encErr != nil {
		return encErr
	}
	_, err = r.api.UploadTo(cliclient.ConversionWrite(r.ctx), tmp.Name(), destDir, row.Name, expect)
	return err
}

// namePassIO is the name pass's view of the server (e2edecrypt.NamePassIO).
type namePassIO struct{ r *remoteRun }

func (n namePassIO) List(dir string) ([]e2edecrypt.ConvertRow, error) { return n.r.List(dir) }
func (n namePassIO) Stopped() bool                                    { return n.r.Stopped() }

func (n namePassIO) ReadSidecar(dir, name string) ([]byte, bool) {
	d, err := cliclient.ParseRemotePath(dir)
	if err != nil {
		return nil, false
	}
	b, err := n.r.api.ReadSmall(n.r.ctx, d.Join(name).String(), 64<<10)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (n namePassIO) WriteSidecar(dir, name, content string) error {
	d, err := cliclient.ParseRemotePath(dir)
	if err != nil {
		return err
	}
	return n.r.api.WriteSmall(n.r.ctx, d, name, []byte(content), "")
}

func (n namePassIO) Rename(dir string, row e2edecrypt.ConvertRow, to string) error {
	item, err := cliclient.ParseRemotePath(row.Path)
	if err != nil {
		return err
	}
	if path.Base(item.Rel) != row.Name {
		return fmt.Errorf("%s: the listing and the path disagree on its name", row.Path)
	}
	return n.r.api.Rename(n.r.ctx, item, to)
}
