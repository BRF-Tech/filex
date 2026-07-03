package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/version"
)

// buildRoot mirrors main()'s tree construction so the test can drive it
// without invoking os.Exit. Keep this in sync with main().
func buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:     "filex",
		Short:   "filex — self-hosted file manager",
		Version: version.String(),
	}
	root.PersistentFlags().StringVar(&configPath, "config", "", "path to config.yaml")
	root.AddCommand(serveCmd(), migrateCmd(), adminCmd(), storageCmd())
	return root
}

// TestMain_RootCompiles verifies the command tree builds without panic.
func TestMain_RootCompiles(t *testing.T) {
	cmd := buildRoot()
	require.NotNil(t, cmd)
	require.Equal(t, "filex", cmd.Use)
}

// TestMain_VersionFlag — running `filex --version` writes the version
// string to stdout and returns successfully.
func TestMain_VersionFlag(t *testing.T) {
	cmd := buildRoot()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--version"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "filex")
}

// TestMain_HelpDoesNotError — `filex --help` should print and return nil.
func TestMain_HelpDoesNotError(t *testing.T) {
	cmd := buildRoot()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.Execute())
	out := buf.String()
	assert.True(t, strings.Contains(out, "serve") || strings.Contains(out, "migrate"),
		"help output should list subcommands, got %q", out)
}

// TestMain_KnownSubcommandsRegistered ensures each subcommand surface is
// hooked up so `filex <foo>` doesn't silently no-op.
func TestMain_KnownSubcommandsRegistered(t *testing.T) {
	cmd := buildRoot()
	want := []string{"serve", "migrate", "admin", "storage"}
	have := map[string]bool{}
	for _, sub := range cmd.Commands() {
		have[sub.Name()] = true
	}
	for _, name := range want {
		assert.True(t, have[name], "subcommand %q must be registered", name)
	}
}
