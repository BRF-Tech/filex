package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/e2edecrypt"
)

// ─────────────────── decrypt ───────────────────
//
// `filex decrypt` opens an end-to-end encrypted folder OFFLINE: a folder (or a
// .zip of one, or a single file) downloaded from filex, decrypted with the
// folder password or its recovery key into a plain folder with the real
// names. No server, no config, no database — it is the way out of filex for
// someone who wants their files back in the clear.
//
// The secret is never an argument or an environment variable: those end up
// in shell history, `ps` output and CI logs. It is read from the terminal
// without echo, or as one line from stdin.
//
// A vault (encryption level 3) is read the same way from a copy, and also
// straight from a server - `filex decrypt docs://Kasa` - over the API, with no
// lock: `state`, the index, and the byte ranges of the packs it needs.
//
// There is deliberately no escrow-key option. The escrow unlock in the web UI
// announces itself to the folder's owner before it opens anything; an offline
// escrow decryptor would be a silent one (docs/E2E-ENCRYPTION.md → "What
// escrow can and cannot do").

// Exit statuses, so a script can tell a typo from a broken folder.
const (
	exitDecryptWrongSecret = 5
	exitDecryptCorrupt     = 6
	exitDecryptUnsupported = 7
)

func decryptCmd() *cobra.Command {
	var out, marker, url, token string
	var recovery, fromStdin, noWarnings bool
	var generation uint64
	c := &cobra.Command{
		Use:   "decrypt <folder | folder.zip | file | file.fxe | adapter://vault>",
		Short: "Decrypt a downloaded end-to-end encrypted folder or file, offline - or a vault on a server",
		Long: "Decrypts an end-to-end encrypted folder downloaded from filex - the folder\n" +
			"itself, a .zip of it, or a single encrypted file - into a plain folder with\n" +
			"the real file and folder names. Works offline: no server, no config.\n\n" +
			"A single encrypted file (.fxe) carries its own password and recovery key\n" +
			"and needs no key file: it is decrypted to its ORIGINAL name next to it, or\n" +
			"to the file -o names. One .fxe at a time - each has its own password.\n\n" +
			"The folder password is asked for on the terminal (no echo). With\n" +
			"--recovery-key, the recovery key shown when the folder was created is asked\n" +
			"for instead. Neither is ever taken from an argument or an environment\n" +
			"variable; to script it, pipe the secret as one line and pass\n" +
			"--password-stdin.\n\n" +
			"Nothing is written unless everything decrypts: the output is assembled in\n" +
			"<out>.partial-* and renamed into place at the end. A wrong password, a\n" +
			"wrong recovery key, a single damaged file or a truncated, reordered or\n" +
			"extended large file leaves no output at all.\n\n" +
			"The input needs the folder's key file, .filex-e2e.json, at its root. A\n" +
			"subfolder or a single file downloaded on its own does not have one: pass\n" +
			"the encrypted folder's marker with --marker.\n\n" +
			"A vault (encryption level 3) is decrypted whole: from a copy of the vault\n" +
			"folder (or a .zip of it), or straight from the server - adapter://path, with\n" +
			"the connection `filex client` uses (--url/--token, FILEX_URL/FILEX_TOKEN, or\n" +
			"the saved session). Reading a vault takes no lock: somebody may be writing\n" +
			"meanwhile. It is the latest generation that verifies; --generation N takes\n" +
			"an older one the vault still keeps.\n\n" +
			"The operator escrow key is not accepted here on purpose: escrow use in the\n" +
			"web UI notifies the folder's owner, and an offline tool cannot.\n\n" +
			"Exit status: 0 done, 5 wrong password or recovery key, 6 a damaged file\n" +
			"or name, 7 the folder needs a newer filex, 1 anything else.",
		Example: "  filex decrypt ~/Downloads/Kasa.zip\n" +
			"  filex decrypt ./Kasa -o ./Kasa-plain\n" +
			"  filex decrypt ./Kasa --recovery-key\n" +
			"  filex decrypt ./sub --marker ./Kasa/.filex-e2e.json\n" +
			"  filex decrypt ./Rapor.pdf.fxe\n" +
			"  filex decrypt ./encrypted-3fa2c1d0.fxe -o ./Rapor.pdf\n" +
			"  filex decrypt docs://Kasa -o ./Kasa-plain              # a vault, from the server\n" +
			"  filex decrypt docs://Kasa --generation 41 -o ./Kasa-41\n" +
			"  pass show kasa | filex decrypt ./Kasa --password-stdin",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.Contains(args[0], "://") {
				if marker != "" {
					return errors.New("--marker is for a copy on this disk; a vault on a server carries its own key file")
				}
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return runDecryptRemoteVault(ctx, cmd, args[0], remoteDecrypt{
					out: out, url: url, token: token, generation: generation,
					recovery: recovery, fromStdin: fromStdin, quiet: noWarnings,
				})
			}
			if url != "" || token != "" {
				return errors.New("--url and --token are for a vault on a server (adapter://path); a copy on this disk needs no server")
			}
			job, err := e2edecrypt.Open(args[0], e2edecrypt.OpenOptions{Out: out, MarkerPath: marker, Generation: generation})
			if err != nil {
				return decryptExit(err)
			}
			defer job.Close()

			label := "Folder password"
			if job.IsFxe() {
				label = "File password"
			}
			if recovery {
				label = "Recovery key"
			}
			secret, err := readSecret(cmd, label, fromStdin)
			if err != nil {
				return fmt.Errorf("reading the %s: %w", strings.ToLower(label), err)
			}
			if secret == "" {
				return fmt.Errorf("no %s given", strings.ToLower(label))
			}
			if err := job.Unlock(secret, recovery); err != nil {
				return decryptExit(err)
			}
			res, err := job.Run()
			if err != nil {
				return decryptExit(err)
			}
			w := cmd.OutOrStdout()
			if !noWarnings {
				for _, warn := range res.Warnings {
					fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+warn)
				}
			}
			if job.IsFxe() {
				fmt.Fprintf(w, "Decrypted into %s", res.Out)
			} else {
				fmt.Fprintf(w, "Decrypted %d file(s) and %d folder(s) into %s", res.Files, res.Dirs, res.Out)
			}
			if n := len(res.Warnings); n > 0 {
				fmt.Fprintf(w, " (%d warning(s))", n)
			}
			fmt.Fprintln(w)
			return nil
		},
	}
	c.Flags().StringVarP(&out, "output", "o", "", "output directory (default: <input>-decrypted next to the input), or for a .fxe the file to write (default: its original name next to it); must not exist")
	c.Flags().StringVar(&marker, "marker", "", "the encrypted folder's .filex-e2e.json, when the input does not carry it")
	c.Flags().BoolVar(&recovery, "recovery-key", false, "unlock with the recovery key instead of the password")
	c.Flags().BoolVar(&fromStdin, "password-stdin", false, "read the password (or recovery key) as one line from stdin")
	c.Flags().BoolVarP(&noWarnings, "quiet", "q", false, "do not print warnings, only the summary")
	c.Flags().Uint64Var(&generation, "generation", 0, "a vault only: decrypt this older generation (one the vault still keeps) instead of the latest")
	c.Flags().StringVar(&url, "url", "", "a vault on a server only: filex server URL (default: $FILEX_URL or ~/.filex/cli.yaml)")
	c.Flags().StringVar(&token, "token", "", "a vault on a server only: API or session token (default: $FILEX_TOKEN, else the saved session)")
	return c
}

// remoteDecrypt are the flags of `filex decrypt adapter://vault`.
type remoteDecrypt struct {
	out, url, token string
	generation      uint64
	recovery        bool
	fromStdin       bool
	quiet           bool
}

// runDecryptRemoteVault decrypts a vault straight from a server, all or
// nothing, like a copy: no lock, nothing written on the server.
func runDecryptRemoteVault(ctx context.Context, cmd *cobra.Command, remote string, o remoteDecrypt) error {
	api, err := (&clientOpts{url: o.url, token: o.token}).api(true)
	if err != nil {
		return err
	}
	rp, err := cliclient.ParseRemotePath(remote)
	if err != nil {
		return err
	}
	if rp.IsRoot() {
		return errors.New("a vault is a folder: name it, like docs://Kasa")
	}
	out := o.out
	if out == "" {
		out = rp.Base() + "-decrypted"
	}
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	if _, err := os.Lstat(out); err == nil {
		return fmt.Errorf("%s: %w", out, e2edecrypt.ErrOutputExists)
	}
	wire := rp.String()
	m, err := readRemoteVaultMarker(ctx, api, wire)
	if err != nil {
		return decryptExit(err)
	}
	keys, err := unlockVaultMarker(cmd, m, o.recovery, o.fromStdin)
	if err != nil {
		return decryptExit(err)
	}
	vk, err := e2edecrypt.NewVaultKeys(keys.FMK, m.Vault.ID)
	clear(keys.FMK)
	if err != nil {
		return err
	}
	defer vk.Wipe()
	src := &remoteVaultSource{api: api, remote: wire}
	res, err := e2edecrypt.DecryptVault(ctx, src, vk, m.Vault.PackLog2, e2edecrypt.VaultDecryptOptions{Out: out, Generation: o.generation})
	if err != nil {
		return decryptExit(authHint(err))
	}
	if !o.quiet {
		for _, warn := range res.Warnings {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+warn)
		}
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Decrypted %d file(s) and %d folder(s) into %s", res.Files, res.Dirs, res.Out)
	if n := len(res.Warnings); n > 0 {
		fmt.Fprintf(w, " (%d warning(s))", n)
	}
	fmt.Fprintln(w)
	return nil
}

// readSecret reads the password or recovery key: from the terminal without
// echo, or as the first line of stdin when asked to or when stdin is a pipe.
func readSecret(cmd *cobra.Command, label string, fromStdin bool) (string, error) {
	in := cmd.InOrStdin()
	if f, ok := in.(*os.File); ok && !fromStdin && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(cmd.ErrOrStderr(), label+": ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		return string(b), err
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// decryptExit maps the decryptor's errors to the exit statuses above, with a
// message that says what to do.
func decryptExit(err error) error {
	var ce *e2edecrypt.CorruptError
	var ue *e2edecrypt.UnsupportedError
	switch {
	case errors.Is(err, e2edecrypt.ErrWrongPassword):
		return &exitError{code: exitDecryptWrongSecret, err: errors.New("wrong password - nothing was written")}
	case errors.Is(err, e2edecrypt.ErrWrongRecoveryKey):
		return &exitError{code: exitDecryptWrongSecret, err: errors.New("wrong recovery key (or this folder has none) - nothing was written")}
	case errors.As(err, &ue):
		return &exitError{code: exitDecryptUnsupported, err: err}
	case errors.As(err, &ce):
		return &exitError{code: exitDecryptCorrupt, err: fmt.Errorf("%w - nothing was written", err)}
	case errors.Is(err, e2edecrypt.ErrNotMarker):
		return &exitError{code: exitDecryptCorrupt, err: err}
	}
	return err
}
