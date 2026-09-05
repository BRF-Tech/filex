package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

// ─────────────────── e2e-escrow ───────────────────
//
// `filex e2e-escrow keygen` mints the installation escrow keypair.
//
// It deliberately does NOT write anything, talk to the database, or read
// the config: the private half must not exist on the server, and the
// command is meant to be runnable anywhere — an operator's laptop is a
// perfectly good place to generate it.

func e2eEscrowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "e2e-escrow",
		Short: "End-to-end encryption key escrow (install-time)",
		Long: "Key escrow gives the operator a second way into E2E-encrypted folders.\n" +
			"It is OFF unless " + e2e.EnvEscrowKey + " is set, and it is fixed for the\n" +
			"life of an installation — see docs/E2E-ENCRYPTION.md.",
	}
	cmd.AddCommand(e2eEscrowKeygenCmd())
	return cmd
}

func e2eEscrowKeygenCmd() *cobra.Command {
	var bits int
	var quiet bool
	c := &cobra.Command{
		Use:   "keygen",
		Short: "Generate an escrow keypair (prints the private key ONCE)",
		Long: "Generates an RSA keypair for E2E key escrow.\n\n" +
			"The PUBLIC half goes in " + e2e.EnvEscrowKey + " on the server.\n" +
			"The PRIVATE half is yours to keep and is never sent to, stored by, or\n" +
			"recoverable from filex. Put it somewhere you would put a root password.\n\n" +
			"Run this BEFORE the first boot of a new installation: escrow cannot be\n" +
			"turned on afterwards, because folders created without it carry no\n" +
			"escrow-wrapped key material and nothing can add it later.",
		RunE: func(cmd *cobra.Command, args []string) error {
			pub, priv, err := e2e.GenerateEscrowKeyPair(bits)
			if err != nil {
				return err
			}
			key, err := e2e.ParseEscrowPublicKey(pub)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if quiet {
				// Machine-readable: two lines, public then private.
				fmt.Fprintln(out, pub)
				fmt.Fprintln(out, priv)
				return nil
			}
			fmt.Fprintf(out, "escrow key id: %s  (%s, %d bits)\n\n", key.KID, e2e.EscrowAlg, key.Bits)
			fmt.Fprintln(out, "── PUBLIC half — put this on the server ──────────────────────────────")
			fmt.Fprintf(out, "%s=%s\n\n", e2e.EnvEscrowKey, pub)
			fmt.Fprintln(out, "── PRIVATE half — SAVE THIS NOW. It is shown once. ───────────────────")
			fmt.Fprintln(out, priv)
			fmt.Fprintln(out)
			fmt.Fprintln(out, "What the private key does: opens any E2E-encrypted folder created")
			fmt.Fprintln(out, "while this escrow key was configured, without the folder password.")
			fmt.Fprintln(out, "What it does not do: open folders created before escrow was enabled,")
			fmt.Fprintln(out, "or on an installation configured with a different escrow key.")
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Lose it and you lose the escrow path. filex has no copy — that is the")
			fmt.Fprintln(out, "point: a stolen filex database decrypts nothing.")
			if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
				fmt.Fprintln(os.Stderr, "filex: warning — the private key was written to a pipe or file, not a terminal.")
			}
			return nil
		},
	}
	c.Flags().IntVar(&bits, "bits", e2e.EscrowKeyBits, "RSA modulus size")
	c.Flags().BoolVar(&quiet, "quiet", false, "print only the two base64 keys (public, then private)")
	return c
}
