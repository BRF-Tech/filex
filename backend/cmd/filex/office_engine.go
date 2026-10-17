package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/brf-tech/filex/backend/internal/officeengine"
)

// officeEngineCmd is `filex office-engine`, the process an app's office
// engine runs in (internal/officeengine). filex starts it itself, with its
// configuration in the environment, and talks to it over stdin and stdout;
// it is hidden because there is nothing to do with it by hand.
func officeEngineCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "office-engine",
		Short:         "Run an app's office engine (started by filex itself)",
		Hidden:        true,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(officeengine.Main(os.Stdin, os.Stdout))
		},
	}
}
