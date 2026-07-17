package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

// TestClientCmd_TreeBuilds verifies the subcommand family registers.
func TestClientCmd_TreeBuilds(t *testing.T) {
	c := clientCmd()
	names := map[string]bool{}
	for _, sub := range c.Commands() {
		names[sub.Name()] = true
	}
	for _, want := range []string{"login", "ls", "upload", "download", "mkdir", "rm", "mv", "search", "share"} {
		assert.True(t, names[want], "missing subcommand %s", want)
	}
	// Persistent connection flags live on the parent.
	assert.NotNil(t, c.PersistentFlags().Lookup("url"))
	assert.NotNil(t, c.PersistentFlags().Lookup("token"))
	assert.NotNil(t, c.PersistentFlags().Lookup("json"))
}

// TestClientCmd_HelpRuns drives `filex client --help` through cobra.
func TestClientCmd_HelpRuns(t *testing.T) {
	c := clientCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"--help"})
	require.NoError(t, c.Execute())
	assert.Contains(t, out.String(), "adapter://")
}

func TestHumanSize(t *testing.T) {
	assert.Equal(t, "0 B", humanSize(0))
	assert.Equal(t, "512 B", humanSize(512))
	assert.Equal(t, "1.0 KB", humanSize(1024))
	assert.Equal(t, "1.2 MB", humanSize(1258291))
	assert.Equal(t, "2.0 GB", humanSize(2<<30))
}

func TestTruncateLine(t *testing.T) {
	assert.Equal(t, "a b c", truncateLine("a\n b\t\tc", 80), "whitespace collapses to one line")
	long := strings.Repeat("ç", 100)
	got := truncateLine(long, 10)
	assert.Equal(t, strings.Repeat("ç", 10)+"…", got, "rune-safe cut")
}

func TestFmtMillis(t *testing.T) {
	assert.Equal(t, "-", fmtMillis(0))
	assert.NotEqual(t, "-", fmtMillis(1752700000000))
}

// TestRenderListing spot-checks table alignment output.
func TestRenderListing(t *testing.T) {
	var buf bytes.Buffer
	renderListing(&buf, &cliclient.ListResult{
		Files: []cliclient.ListEntry{
			{Basename: "inbox", Type: "dir"},
			{Basename: "rapor.pdf", Type: "file", Size: 123456, LastModified: 1752700000000},
		},
	})
	out := buf.String()
	assert.Contains(t, out, "TYPE")
	assert.Contains(t, out, "inbox")
	assert.Contains(t, out, "120.6 KB")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	assert.Len(t, lines, 3, "header + 2 rows")
}
